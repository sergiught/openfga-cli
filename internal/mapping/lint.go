package mapping

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Problem is one thing wrong with a document. Blocking problems stop a save;
// warnings are advisory — the model is a snapshot and a mapping may legitimately
// target types a newer model will add.
type Problem struct {
	Rule    int
	Section string
	// Item is the 1-based tuple or filter in Section the problem is about,
	// counting the iterator's tuples after the rule's own; 0 when it is about
	// the rule or the section as a whole.
	Item    int
	Field   string
	Message string
	Warning bool
}

// Blocking filters ps down to the problems that must be fixed before saving.
func Blocking(ps []Problem) []Problem {
	var out []Problem
	for _, p := range ps {
		if !p.Warning {
			out = append(out, p)
		}
	}
	return out
}

// Warnings filters ps down to the advisory ones — every check that compared the
// document against a model, and CheckSamples' sample that makes no test. They
// are the complement of Blocking.
func Warnings(ps []Problem) []Problem {
	var out []Problem
	for _, p := range ps {
		if p.Warning {
			out = append(out, p)
		}
	}
	return out
}

// Lint checks d for the mistakes mapper cannot catch: structurally incomplete
// rules the workspace allows mid-edit, and identifiers that compile but do not
// exist in ix. A nil ix disables the model checks entirely.
func Lint(d *Document, ix *ModelIndex) []Problem {
	var ps []Problem
	if len(d.Rules) == 0 {
		return append(ps, Problem{Rule: -1, Section: "rule", Message: "a mapping needs at least one rule"})
	}

	seen := map[string]bool{}
	for i, r := range d.Rules {
		switch {
		case strings.TrimSpace(r.Name) == "":
			ps = append(ps, Problem{Rule: i, Section: "rule", Field: "name", Message: "every rule needs a name"})
		case seen[r.Name]:
			ps = append(ps, Problem{Rule: i, Section: "rule", Field: "name",
				Message: fmt.Sprintf("duplicate rule name %q", r.Name)})
		default:
			seen[r.Name] = true
		}

		ps = append(ps, lintTrigger(i, r)...)
		ps = append(ps, lintTuples(i, r, ix)...)
		ps = append(ps, lintVariables(i, r)...)
		ps = append(ps, lintIterator(i, r)...)
		ps = append(ps, lintFilters(i, r, ix)...)
	}
	return ps
}

// variablesRef finds an access to the variables scope in a rule-level when:
// variables.x or variables[...], outside a string literal, and not a field of
// something else such as input.variables. mapper rejects either form at
// compile time because the when runs before the variables do.
var variablesRef = regexp.MustCompile(`(^|[^.\w])variables\s*[.\[]`)

// lintTrigger catches a rule-level when that reads a variable.
func lintTrigger(i int, r Rule) []Problem {
	if !variablesRef.MatchString(stripStrings(r.When)) {
		return nil
	}
	return []Problem{{Rule: i, Section: "trigger", Field: "when",
		Message: "a rule's when runs before its variables, so it can read only input; " +
			"move the check to a tuple's when, which sees both"}}
}

// stripStrings blanks the contents of quoted string literals in an expression,
// so a pattern looking for code does not match inside text.
func stripStrings(expr string) string {
	out := []rune(expr)
	var quote rune
	for i := 0; i < len(out); i++ {
		c := out[i]
		switch {
		case quote == 0 && (c == '"' || c == '\'' || c == '`'):
			quote = c
		case quote != 0 && c == '\\' && quote != '`':
			out[i] = ' '
			if i+1 < len(out) {
				i++
				out[i] = ' '
			}
		case quote != 0 && c == quote:
			quote = 0
		case quote != 0:
			out[i] = ' '
		}
	}
	return string(out)
}

func lintTuples(i int, r Rule, ix *ModelIndex) []Problem {
	var ps []Problem
	all := r.Tuples
	if r.Iterator != nil {
		all = append(append([]Tuple{}, all...), r.Iterator.Tuples...)
	}
	if len(all) == 0 {
		// A rule can do its whole job through tuple_filters — that is what the
		// deletion events map to — so only a rule that does nothing at all is
		// missing something.
		if len(r.Filters) > 0 {
			return ps
		}
		return append(ps, Problem{Rule: i, Section: "tuple",
			Message: "rule has no tuples yet"})
	}
	for j, t := range all {
		from := len(ps)
		for _, f := range []struct{ name, val string }{
			{"user", t.User}, {"relation", t.Relation}, {"object", t.Object},
		} {
			if strings.TrimSpace(f.val) == "" {
				ps = append(ps, Problem{Rule: i, Section: "tuple", Field: f.name,
					Message: fmt.Sprintf("tuple is missing its %s", f.name)})
			}
		}
		if r.Action != "" && t.Action != "" {
			ps = append(ps, Problem{Rule: i, Section: "tuple", Field: "action",
				Message: "a tuple action cannot be combined with a rule-level action"})
		}
		if t.Action == "" {
			t.Action = r.Action
		}
		ps = append(ps, lintAgainstModel(i, t, ix)...)
		setItem(ps[from:], j+1)
	}
	return ps
}

// MissingTypes is every type a literal tuple user or object names that ix does
// not define, sorted. Templated values are skipped, and so is an empty index,
// which defines nothing to compare against.
func MissingTypes(d *Document, ix *ModelIndex) []string {
	if ix.Empty() {
		return nil
	}
	missing := map[string]bool{}
	note := func(typ string, literal bool) {
		if base, _, _ := strings.Cut(typ, "#"); literal && base != "" && !contains(ix.TypeNames(), base) {
			missing[base] = true
		}
	}
	for _, r := range d.Rules {
		all := r.Tuples
		if r.Iterator != nil {
			all = append(append([]Tuple{}, all...), r.Iterator.Tuples...)
		}
		for _, t := range all {
			note(literalObjectType(t.Object))
			note(literalUserType(t.User))
		}
	}
	return slices.Sorted(maps.Keys(missing))
}

// lintAgainstModel checks the parts of a tuple that resolve statically. Anything
// containing `{{` is only known at evaluation time and is left alone.
func lintAgainstModel(i int, t Tuple, ix *ModelIndex) []Problem {
	if ix.Empty() {
		return nil
	}
	ps, relationKnown := lintTarget(i, "tuple", t.User, t.Relation, t.Object, ix)

	knownCondition := true
	if t.Condition != "" {
		params := ix.ConditionParams(t.Condition)
		if params == nil && !contains(ix.ConditionNames(), t.Condition) {
			knownCondition = false
			ps = append(ps, Problem{Rule: i, Section: "tuple", Field: "condition", Warning: true,
				Message: fmt.Sprintf("the model has no condition %q", t.Condition)})
		} else {
			// A parameter the tuple leaves out is not a problem: a Check
			// request can supply the rest of the context.
			for _, e := range t.Context {
				if !contains(params, e.Key) {
					ps = append(ps, Problem{Rule: i, Section: "tuple", Field: "context", Warning: true,
						Message: fmt.Sprintf("condition %q has no parameter %q", t.Condition, e.Key)})
				}
			}
		}
	}

	// The server takes a tuple only with a condition the model pairs with its
	// user type: [user with c] refuses user:1 without c, and [user] refuses it
	// with one. A user type the relation lacks was reported by lintTarget. A
	// delete names a tuple by user, relation and object alone, whatever
	// condition it was written with.
	if relationKnown && knownCondition && t.Action != "delete" && !strings.Contains(t.Condition, "{{") {
		typ, _ := literalObjectType(t.Object)
		if userTyp, ok := modelUserType(t.User); ok && contains(ix.UserTypesFor(typ, t.Relation), userTyp) {
			if conds := ix.UserConditionsFor(typ, t.Relation, userTyp); !contains(conds, t.Condition) {
				msg := fmt.Sprintf("relation %q on type %q does not accept user type %q with condition %q",
					t.Relation, typ, userTyp, t.Condition)
				if t.Condition == "" {
					msg = fmt.Sprintf("relation %q on type %q accepts user type %q only with condition %s",
						t.Relation, typ, userTyp, quoteJoin(conds, " or "))
				}
				ps = append(ps, Problem{Rule: i, Section: "tuple", Field: "condition", Warning: true, Message: msg})
			}
		}
	}
	return ps
}

// lintTarget checks a tuple's or filter's object type, relation and user type
// against the model, reporting whether the relation resolved (only then is the
// user type checked against what the relation accepts).
func lintTarget(i int, section, user, relation, object string, ix *ModelIndex) ([]Problem, bool) {
	var ps []Problem
	typ, literalType := literalObjectType(object)
	if literalType && typ != "" && !contains(ix.TypeNames(), typ) {
		ps = append(ps, Problem{Rule: i, Section: section, Field: "object", Warning: true,
			Message: fmt.Sprintf("the model has no type %q", typ)})
	}

	relationKnown := false
	if literalType && typ != "" && !strings.Contains(relation, "{{") && relation != "" {
		if contains(ix.TypeNames(), typ) {
			if contains(ix.RelationsFor(typ), relation) {
				relationKnown = true
			} else {
				ps = append(ps, Problem{Rule: i, Section: section, Field: "relation", Warning: true,
					Message: fmt.Sprintf("type %q has no relation %q", typ, relation)})
			}
		}
	}
	if relationKnown {
		// A relation the model indexed with no user types is computed from
		// others, so no tuple can name it. One it never indexed (a model
		// without metadata) says nothing either way.
		if userTypes, indexed := ix.UserTypes[typ+"#"+relation]; indexed && len(userTypes) == 0 {
			ps = append(ps, Problem{Rule: i, Section: section, Field: "relation", Warning: true,
				Message: fmt.Sprintf("relation %q on type %q accepts no user directly: the model computes it", relation, typ)})
		} else if userTyp, literalUser := modelUserType(user); literalUser {
			if len(userTypes) > 0 && !contains(userTypes, userTyp) {
				ps = append(ps, Problem{Rule: i, Section: section, Field: "user", Warning: true,
					Message: fmt.Sprintf("relation %q on type %q does not accept user type %q", relation, typ, userTyp)})
			}
		}
	}
	return ps, relationKnown
}

// lintFilterAgainstModel checks a tuple filter the way lintAgainstModel checks
// a tuple. A filter may leave the relation out, so its user type is then
// checked against the model's types instead. A filter naming something the
// model lacks reads nothing, so its rule silently changes nothing.
func lintFilterAgainstModel(i int, f TupleFilter, ix *ModelIndex) []Problem {
	if ix.Empty() {
		return nil
	}
	ps, relationKnown := lintTarget(i, "filters", f.User, f.Relation, f.Object, ix)
	if userTyp, literalUser := literalUserType(f.User); literalUser && !relationKnown {
		if base, _, _ := strings.Cut(userTyp, "#"); !contains(ix.TypeNames(), base) {
			ps = append(ps, Problem{Rule: i, Section: "filters", Field: "user", Warning: true,
				Message: fmt.Sprintf("the model has no type %q", base)})
		}
	}
	return ps
}

// literalObjectType extracts the type from `type:id`, reporting false when the
// type half is templated and therefore unknowable until evaluation.
func literalObjectType(object string) (string, bool) {
	colon := strings.IndexByte(object, ':')
	if colon <= 0 {
		return "", false
	}
	typ := object[:colon]
	if strings.Contains(typ, "{{") {
		return "", false
	}
	return typ, true
}

// modelUserType is literalUserType as the model names it. The model names a
// wildcard as "user:*", and accepts it only where it says so.
func modelUserType(user string) (string, bool) {
	userTyp, ok := literalUserType(user)
	if ok && user == userTyp+":*" {
		userTyp = user
	}
	return userTyp, ok
}

// literalUserType extracts the reference form a tuple's user contributes to a
// model check: "user" for "user:123", or "group#member" for a userset like
// "group:eng#member". Reports false when the type half is templated and
// therefore unknowable until evaluation.
func literalUserType(user string) (string, bool) {
	colon := strings.IndexByte(user, ':')
	if colon <= 0 {
		return "", false
	}
	typ := user[:colon]
	if strings.Contains(typ, "{{") {
		return "", false
	}
	rest := user[colon+1:]
	if hash := strings.IndexByte(rest, '#'); hash >= 0 {
		relation := rest[hash+1:]
		if strings.Contains(relation, "{{") {
			return "", false
		}
		return typ + "#" + relation, true
	}
	return typ, true
}

// identPattern is mapper's own rule for a variable name.
var identPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func lintVariables(i int, r Rule) []Problem {
	var ps []Problem
	seen := map[string]bool{}
	for _, v := range r.Variables {
		name := strings.TrimSpace(v.Name)
		switch {
		case name == "":
			ps = append(ps, Problem{Rule: i, Section: "variables", Field: "name",
				Message: "every variable needs a name"})
		case !identPattern.MatchString(name):
			ps = append(ps, Problem{Rule: i, Section: "variables", Field: "name",
				Message: fmt.Sprintf("variable %q: a name is ASCII letters, digits and _, not starting with a digit", name)})
		case seen[name]:
			ps = append(ps, Problem{Rule: i, Section: "variables", Field: "name",
				Message: fmt.Sprintf("two variables are named %q", name)})
		}
		seen[name] = true
		if strings.TrimSpace(v.Expr) == "" {
			ps = append(ps, Problem{Rule: i, Section: "variables", Field: "expression",
				Message: fmt.Sprintf("variable %q needs an expression", v.Name)})
		}
	}
	return ps
}

func lintIterator(i int, r Rule) []Problem {
	if r.Iterator == nil {
		return nil
	}
	var ps []Problem
	if strings.TrimSpace(r.Iterator.Source) == "" {
		ps = append(ps, Problem{Rule: i, Section: "iterator", Field: "source",
			Message: "the iterator needs a source expression"})
	}
	switch as := strings.TrimSpace(r.Iterator.As); as {
	case "":
		ps = append(ps, Problem{Rule: i, Section: "iterator", Field: "as",
			Message: "the iterator needs an alias"})
	case "input", "variables":
		ps = append(ps, Problem{Rule: i, Section: "iterator", Field: "as",
			Message: fmt.Sprintf("the alias %q would hide the event's own %s; pick another name", as, as)})
	}
	return ps
}

func lintFilters(i int, r Rule, ix *ModelIndex) []Problem {
	var ps []Problem
	for j, f := range r.Filters {
		from := len(ps)
		ps = append(ps, lintFilterAgainstModel(i, f, ix)...)
		if strings.TrimSpace(f.Object) == "" {
			ps = append(ps, Problem{Rule: i, Section: "filters", Field: "object",
				Message: "a tuple filter needs an object"})
		}
		// A filter with no action is a patch, and a patch is a reconciliation:
		// mapper diffs the tuples the rule produced against the ones the filter
		// matches. A rule that produces none has an empty desired state, which
		// mapper refuses outright rather than read as "delete everything",
		// failing the whole event. The mistake is invisible
		// until the first event arrives, so it is caught here instead.
		if f.Action == "" && !r.WritesTuples() {
			ps = append(ps, Problem{Rule: i, Section: "filters", Field: "action",
				Message: "this filter patches, but the rule writes no tuples for it to " +
					"reconcile against — mapper fails the event. Add tuples, or make it a delete"})
		}
		// Three fixed values name one tuple, and a filter is for a range of
		// them. A type prefix ("document:") is still a range.
		if fixed(f.User) && fixed(f.Relation) && fixed(f.Object) && !isTypePrefix(f.Object) {
			ps = append(ps, Problem{Rule: i, Section: "filters",
				Message: "this filter names a single tuple, but a filter is for a range of them: " +
					"leave one field blank or templated, or use a tuple with action delete"})
		}
		setItem(ps[from:], j+1)
	}
	return append(ps, lintFilterActions(i, r)...)
}

// setItem marks ps as being about the item-th tuple or filter of their section.
func setItem(ps []Problem, item int) {
	for k := range ps {
		ps[k].Item = item
	}
}

// fixed reports whether a filter field is a concrete value rather than blank or
// templated.
func fixed(s string) bool { return s != "" && !strings.Contains(s, "{{") }

// lintFilterActions checks how a rule's filters and tuple actions combine.
// Patch filters make the rule's tuples the desired state, so nothing in it may
// delete; delete filters remove everything they match, so a rule whose filters
// all delete may not want tuples at all.
func lintFilterActions(i int, r Rule) []Problem {
	if len(r.Filters) == 0 {
		return nil
	}
	patch := false
	for _, f := range r.Filters {
		if f.Action != "delete" {
			patch = true
		}
	}
	if !patch {
		if r.WritesTuples() {
			return []Problem{{Rule: i, Section: "filters", Field: "action",
				Message: "every filter deletes, which removes all it matches, so the rule's tuples " +
					"have nothing to do: remove the tuples, or make a filter a patch"}}
		}
		return nil
	}
	if r.Action == "delete" {
		return []Problem{{Rule: i, Section: "action",
			Message: "a patch filter keeps the rule's tuples as the desired state, so the rule cannot " +
				"delete them: set the action to write or per tuple, or make every filter a delete"}}
	}
	all := r.Tuples
	if r.Iterator != nil {
		all = append(append([]Tuple{}, all...), r.Iterator.Tuples...)
	}
	for _, t := range all {
		if t.Action == "delete" {
			return []Problem{{Rule: i, Section: "tuple", Field: "action",
				Message: "a patch filter keeps the rule's tuples as the desired state, so none of them " +
					"can delete: the patch already removes what the tuples leave out"}}
		}
	}
	return nil
}

// quoteJoin quotes each of ss and joins them with sep.
func quoteJoin(ss []string, sep string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = strconv.Quote(s)
	}
	return strings.Join(q, sep)
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
