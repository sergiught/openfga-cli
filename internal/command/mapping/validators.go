package mapping

import (
	"errors"
	"strings"
	"unicode"
)

// Field validators for the wizard's forms, run on tab-off and on submit by
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

// splitRef checks the type:id shape shared by objects and users, returning the
// literal id half so the caller can rule on what is allowed to appear in it.
func splitRef(s string) (id string, err error) {
	if err := vTemplate(s); err != nil {
		return "", err
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
	// A value that is nothing but one template has no literal text to check the
	// colon in at all — its shape is decided at evaluation time, not here. Test
	// the original rather than the stripped form: stripTemplates marks each
	// template with a "T" but does not record which "T"s it wrote, so a literal
	// value of "TTT" is indistinguishable from three placeholders once stripped.
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "{{") && strings.HasSuffix(t, "}}") && strings.Count(t, "{{") == 1 {
		return nil
	}
	return errors.New("needs a type prefix like organization: before the id")
}

// vTupleAction and vFilterAction cover the two action vocabularies: a tuple is
// written or deleted, a filter patches or deletes what a rule already owns.
func vTupleAction(s string) error { return vOneOf(s, "write", "delete") }

func vFilterAction(s string) error { return vOneOf(s, "patch", "delete") }

func vOneOf(s string, allowed ...string) error {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "{{") {
		return nil
	}
	for _, a := range allowed {
		if s == a {
			return nil
		}
	}
	return errors.New("must be " + strings.Join(allowed, " or "))
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

// vContextPairs validates the condition context's one-line form.
func vContextPairs(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	if err := vTemplate(s); err != nil {
		return err
	}
	for _, part := range strings.Split(stripTemplates(s), ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			return errors.New("must be key=template, comma separated")
		}
	}
	return nil
}
