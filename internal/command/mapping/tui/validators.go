package tui

import (
	"errors"
	"strings"
	"unicode"
)

// Field validators for the forms here, run on tab-off and on submit by
// internal/ui/field.
//
// Each is lenient on an empty value — required-ness is enforced by mapping.Lint,
// which reports it in the preview pane — so navigating an empty field never
// nags; only a non-empty, malformed value is flagged. They check shape only.
// Whether a type, relation or condition actually exists is a question for the
// model, and Lint answers it against the loaded index.

// stripTemplates replaces every `{{ … }}` with a single placeholder so the shape
// checks can look at the literal text around a template without tripping over
// what is inside one: an expression may legitimately contain a colon, a comma or
// a brace, none of which mean there what they mean outside.
func stripTemplates(s string) string {
	var b strings.Builder
	for {
		open := strings.Index(s, "{{")
		if open < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:open])
		b.WriteString("T")
		end := strings.Index(s[open:], "}}")
		if end < 0 {
			return b.String()
		}
		s = s[open+end+2:]
	}
}

// vTemplate reports an unbalanced `{{ }}`. Worth catching early: a template
// missing its closing braces is not a syntax error anywhere the user can see
// it, it just silently yields the literal text at evaluation time.
func vTemplate(s string) error {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch {
		case strings.HasPrefix(s[i:], "{{"):
			depth++
			i++
		case strings.HasPrefix(s[i:], "}}"):
			depth--
			if depth < 0 {
				return errors.New("`}}` with no matching `{{`")
			}
			i++
		}
	}
	if depth > 0 {
		return errors.New("unclosed `{{` — add `}}`")
	}
	return nil
}

// wholeTemplate reports whether s is nothing but one template. It has no
// literal text to check a shape in: that is decided at evaluation time. It tests
// the original rather than the stripped form: stripTemplates marks each template
// with a "T" but does not record which "T"s it wrote, so a literal value of "TTT"
// is indistinguishable from three placeholders once stripped.
func wholeTemplate(s string) bool {
	t := strings.TrimSpace(s)
	return strings.HasPrefix(t, "{{") && strings.HasSuffix(t, "}}") && strings.Count(t, "{{") == 1
}

// splitRef checks the type:id shape shared by objects and users, returning the
// literal id half so the caller can rule on what is allowed to appear in it. A
// value that is one template supplies the whole reference, and has no id half.
func splitRef(s string) (id string, err error) {
	if err := vTemplate(s); err != nil {
		return "", err
	}
	if wholeTemplate(s) {
		return "", nil
	}
	typ, id, ok := strings.Cut(stripTemplates(s), ":")
	if !ok || strings.TrimSpace(typ) == "" || strings.TrimSpace(id) == "" {
		return "", errors.New("must be type:id")
	}
	return id, nil
}

// vObjectRef validates an object template. An object is always one concrete
// thing, so neither a wildcard nor a userset belongs here.
func vObjectRef(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	id, err := splitRef(s)
	if err != nil {
		return err
	}
	if strings.Contains(id, "*") || strings.Contains(id, "#") {
		return errors.New("must be a single object: no * or #")
	}
	return nil
}

// vUserRef validates a user template, which unlike an object may also be a
// wildcard (`user:*`) or a userset (`team:x#member`).
func vUserRef(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	_, err := splitRef(s)
	return err
}

// vFilterObject checks a tuple filter's object. Unlike a filter's user and
// relation, which may be blank to match anything, mapper requires the object to
// carry at least a type prefix ("organization:") and rejects a blank one with
// "must be set to at least an object type prefix". Blank is still allowed here —
// required-ness belongs to Lint — but a value that is present must have the colon.
func vFilterObject(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	stripped := stripTemplates(s)
	if strings.Contains(stripped, ":") {
		return nil
	}
	if wholeTemplate(s) {
		return nil
	}
	return errors.New("needs a type prefix like organization: before the id")
}

// vIdent validates a name the mapping refers to elsewhere by hand — a variable,
// an iterator alias, a condition. A name with a space or a dot in it parses as
// something else wherever it is used.
func vIdent(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for i, r := range s {
		if r == '_' || unicode.IsLetter(r) || (i > 0 && unicode.IsDigit(r)) {
			continue
		}
		return errors.New("must be letters, digits and _, not starting with a digit")
	}
	return nil
}

// vContextPairs validates the condition context's one-line form. Each pair is
// cut on its first raw = exactly as splitContext cuts it, so the key checked is
// the key committed; a key is a condition parameter, so it is an identifier.
func vContextPairs(s string) error { return contextPairs(s, "key=template") }

// contextPairs is vContextPairs, naming the pair's shape in its error.
func contextPairs(s, shape string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	if err := vTemplate(s); err != nil {
		return err
	}
	for _, part := range splitOutsideTemplates(s, ',') {
		if strings.TrimSpace(part) == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			return errors.New("must be " + shape + ", comma separated")
		}
		if err := vIdent(k); err != nil {
			return errors.New("each key " + err.Error())
		}
	}
	return nil
}

// A test's expected tuple is checked by these instead: it is not a template
// but the tuple FGA stores, written out.

// errTemplateInExpectation is why an expected tuple takes no `{{`: mapper
// compares it as it is, so a template there is literal text no evaluated
// tuple carries, and the test could never pass.
var errTemplateInExpectation = errors.New("an expectation is a literal value, not a template")

// vLiteral fails a value holding a template, then defers to check.
func vLiteral(check func(string) error) func(string) error {
	return func(s string) error {
		if strings.Contains(s, "{{") {
			return errTemplateInExpectation
		}
		if check != nil {
			return check(s)
		}
		return nil
	}
}

// vExpectedContext validates an expected tuple's context: key=value pairs,
// each value literal.
func vExpectedContext(s string) error {
	if strings.Contains(s, "{{") {
		return errTemplateInExpectation
	}
	return contextPairs(s, "key=value")
}

// vRequiredTuplePart fails an empty value, then defers to check. Lint enforces
// required-ness for rules, but nothing checks a test's expected tuple before
// mapper, which refuses the whole file when one misses a part.
func vRequiredTuplePart(check func(string) error) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errors.New("required")
		}
		if check != nil {
			return check(s)
		}
		return nil
	}
}

// splitOutsideTemplates splits s on sep wherever sep is not inside a `{{ }}`,
// pairing each `{{` with the next `}}` the way stripTemplates does. An
// expression inside a template may hold the separator — `{{ join(x, ",") }}` —
// and splitting there cuts the template in half. The validator and the commit
// both split with this, so the form can never accept a value the document then
// truncates.
func splitOutsideTemplates(s string, sep byte) []string {
	var out []string
	start, inside := 0, false
	for i := 0; i < len(s); i++ {
		switch {
		case !inside && strings.HasPrefix(s[i:], "{{"):
			inside = true
			i++
		case inside && strings.HasPrefix(s[i:], "}}"):
			inside = false
			i++
		case !inside && s[i] == sep:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
