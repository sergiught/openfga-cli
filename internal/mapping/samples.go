package mapping

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/openfga/mapper"
	"github.com/openfga/mapper/language"
)

// CheckTuple reports what is wrong with the shape of an evaluated tuple: an
// object that is not type:id, or a user that is not type:id, type:* or
// type:id#relation, with every part non-empty. mapper renders a template that
// resolves to "" or leaves out the type without complaint; FGA refuses the
// write.
func CheckTuple(t language.Tuple) error {
	if typ, id, ok := strings.Cut(t.Object, ":"); !ok || typ == "" || id == "" {
		return fmt.Errorf("object %q is not type:id", t.Object)
	}
	typ, id, ok := strings.Cut(t.User, ":")
	if !ok || typ == "" || id == "" {
		return fmt.Errorf("user %q is not type:id", t.User)
	}
	if base, rel, ok := strings.Cut(id, "#"); ok && (base == "" || rel == "") {
		return fmt.Errorf("user %q is not type:id#relation", t.User)
	}
	return nil
}

// CheckSamples evaluates every rule's sample through the whole document, the
// way BuildTests does, and reports what keeps each one from becoming a test: an
// evaluation error, or a tuple CheckTuple rejects, each blocking; or a warning
// that the sample evaluates cleanly to nothing at all. A problem is filed under
// the first rule carrying that sample. Rules without a sample are skipped, and
// a document that does not compile yields nothing here: its diagnostics are the
// compile's, not any sample's.
//
// It evaluates every sample, so a caller asks when it needs the answer — at
// save time — rather than after every keystroke.
func CheckSamples(ctx context.Context, d *Document) []Problem {
	var ps []Problem
	for _, i := range uniqueSamples(d) {
		_, why, compiled := judgeSample(ctx, d, i)
		if !compiled {
			return nil
		}
		ps = append(ps, why...)
	}
	return collapseRepeats(ps)
}

// collapseRepeats keeps the first of each identical problem and counts the
// rest on it: two samples that both run a rule filing the same warning on it
// are one thing to fix, not two.
func collapseRepeats(ps []Problem) []Problem {
	first := map[Problem]int{}
	count := map[Problem]int{}
	var out []Problem
	for _, p := range ps {
		if _, ok := first[p]; !ok {
			first[p] = len(out)
			out = append(out, p)
		}
		count[p]++
	}
	for p, i := range first {
		if n := count[p]; n > 1 {
			out[i].Message = fmt.Sprintf("%s (%d samples)", p.Message, n)
		}
	}
	return out
}

// SamplesFromTests gives each rule without a sample the input of the first
// test, in file order, whose evaluation matches that rule, labelled with the
// test's name. Rules are matched by position in mapper's trace (one entry per
// rule, in order, until an error), never by name, which may repeat or be empty.
// A document that does not compile gets none. It sets Sample directly: picking
// an event through the workspace would also rename the rule.
func SamplesFromTests(ctx context.Context, d *Document) {
	for _, tc := range d.Tests {
		if tc.Input == nil {
			continue
		}
		p := Evaluate(ctx, d, tc.Input)
		if !p.Evaluated {
			return
		}
		for i, rt := range p.Rules {
			if r := &d.Rules[i]; rt.Status == mapper.RuleMatched && (r.Sample == nil || len(r.Sample.Event) == 0) {
				r.Sample = &Sample{Label: "test " + tc.Name, Event: maps.Clone(tc.Input)}
			}
		}
	}
}

// judgeSample is the one verdict on the sample of rule i, the first rule
// carrying it: the test it makes, or the problems saying why it makes none,
// each filed under the rule that caused it. A test can come with warnings too,
// such as a write no filter covers. CheckSamples reports the why and BuildTests
// keeps the test, so the two cannot disagree about a sample. compiled is false
// when the document does not compile, and then there is neither.
//
// A sample that evaluates cleanly to nothing makes no test, since a test that
// expects nothing asserts nothing. That is worth a warning when the rule writes
// tuples or filters: what it writes missed its own sample, through a when that
// does not match it, say. A rule that writes neither is explain-only and
// produces nothing on purpose.
func judgeSample(ctx context.Context, d *Document, i int) (tc *TestCase, why []Problem, compiled bool) {
	r := d.Rules[i]
	p := Evaluate(ctx, d, r.Sample.Event)
	if !p.Evaluated {
		return nil, nil, false
	}
	if faults := sampleFaults(p); len(faults) > 0 {
		for _, f := range faults {
			at, msg := i, "the sample makes no test: "+f.msg
			if f.rule >= 0 && f.rule != i {
				at = f.rule
				msg = fmt.Sprintf("the sample of %s makes %s %s, so it makes no test: %s",
					ruleLabel(d, i), ruleLabel(d, at), f.verb, f.msg)
			}
			why = append(why, Problem{Rule: at, Section: "sample", Message: msg})
		}
		return nil, why, true
	}
	if len(p.Tuples) == 0 && len(p.Filters) == 0 {
		if !r.WritesTuples() && len(r.Filters) == 0 {
			return nil, nil, true
		}
		return nil, []Problem{{Rule: i, Section: "sample", Warning: true,
			Message: "the sample makes no test: no rule produces anything for it"}}, true
	}

	built := testFrom(r.Name, r.Sample.Event, p)
	_, opRule := emitters(p)
	for k, op := range p.Filters {
		// A rule's write that none of its own filters matches is never cleaned
		// up by a later patch: it stays in the store whatever the rule decides.
		at := i
		if opRule[k] >= 0 {
			at = opRule[k]
		}
		for _, t := range uncoveredWrites(op.Tuples, op.Filters) {
			why = append(why, Problem{Rule: at, Section: "filters", Warning: true,
				Message: fmt.Sprintf("the sample writes %s %s %s, which no filter of the rule "+
					"matches, so a later patch never removes it", t.User, t.Relation, t.Object)})
		}
	}
	return &built, why, true
}

// ruleLabel names rule i in a sentence: by name when it has one, else by its
// 1-based position, as the save dialog does.
func ruleLabel(d *Document, i int) string {
	if name := d.Rules[i].Name; name != "" {
		return fmt.Sprintf("rule %q", name)
	}
	return fmt.Sprintf("rule %d", i+1)
}

// emitters maps what p produced back to the rule that produced it: the rule
// index of each of p.Tuples and of each of p.Filters, -1 where unknown. mapper's
// trace has one entry per rule, in order, so a matched rule without filters took
// its EmittedN tuples off p.Tuples next, and a matched rule with filters made
// the next filter operation. EmittedN counts before mapper drops duplicate
// tuples, so when the counts no longer add up to p.Tuples the offsets are lost
// and no direct tuple is attributed.
func emitters(p Preview) (direct, ops []int) {
	direct, ops = make([]int, len(p.Tuples)), make([]int, len(p.Filters))
	for i := range direct {
		direct[i] = -1
	}
	for i := range ops {
		ops[i] = -1
	}
	total := 0
	for _, rt := range p.Rules {
		if rt.Status == mapper.RuleMatched && rt.FilterN == 0 {
			total += rt.EmittedN
		}
	}
	trusted := total == len(p.Tuples)
	next, op := 0, 0
	for i, rt := range p.Rules {
		if rt.Status != mapper.RuleMatched {
			continue
		}
		if rt.FilterN > 0 {
			if op < len(ops) {
				ops[op] = i
			}
			op++
			continue
		}
		for j := 0; trusted && j < rt.EmittedN; j++ {
			direct[next+j] = i
		}
		next += rt.EmittedN
	}
	return direct, ops
}

// testFrom is the test asserting exactly what p, the evaluation of event,
// produced. NewTest and judgeSample both build through it, so a test the user
// adds and one a save generates cannot disagree about the same event.
func testFrom(name string, event map[string]any, p Preview) TestCase {
	// mapper's runner compares against every tuple the event produced: the
	// direct ones and the desired state each filter operation carries.
	tc := TestCase{Name: name, Input: event, ExpectTuples: p.Tuples}
	for _, op := range p.Filters {
		tc.ExpectTuples = append(tc.ExpectTuples, op.Tuples...)
		tc.ExpectTupleFilters = append(tc.ExpectTupleFilters, op.Filters...)
	}
	// Pin filter coverage when it holds, so a later edit that leaves a write
	// outside every filter fails the test. mapper checks it across the whole
	// event, so a write from a rule without filters keeps it off.
	tc.AssertWritesCoveredByFilter = len(tc.ExpectTupleFilters) > 0 &&
		len(uncoveredWrites(tc.ExpectTuples, tc.ExpectTupleFilters)) == 0
	return tc
}

// uniqueSamples is the index of the first rule carrying each distinct sample,
// in document order. Rules without a sample are skipped. CheckSamples and
// BuildTests both walk it, so they always agree on which samples exist.
func uniqueSamples(d *Document) []int {
	var out []int
	seen := map[string]bool{}
	for i, r := range d.Rules {
		if r.Sample == nil || len(r.Sample.Event) == 0 {
			continue
		}
		key, err := SampleKey(r.Sample.Event)
		if err != nil || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, i)
	}
	return out
}

// sampleFault is one thing stopping an evaluated sample becoming a test. rule
// is the rule at fault, -1 when that is unknown, and verb says what it did, for
// the message that names a rule other than the sample's own.
type sampleFault struct {
	rule int
	verb string
	msg  string
}

// sampleFaults lists, once each, what stops one evaluated sample becoming a
// test: mapper's evaluation errors, then every tuple CheckTuple rejects. An
// error belongs to the rule mapper stopped at; a bad tuple, to the rule that
// emitted it.
func sampleFaults(p Preview) []sampleFault {
	var out []sampleFault
	seen := map[sampleFault]bool{}
	add := func(rule int, verb, msg string) {
		f := sampleFault{rule, verb, msg}
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	errored := -1
	for i, rt := range p.Rules {
		if rt.Status == mapper.RuleErrored {
			errored = i
		}
	}
	for _, d := range p.Diagnostics {
		msg, _, _ := strings.Cut(d.Message, "\n")
		add(errored, "fail", msg)
	}
	if p.EvalErr != nil {
		add(errored, "fail", p.EvalErr.Error())
	}
	direct, ops := emitters(p)
	for k, t := range p.Tuples {
		if err := CheckTuple(t); err != nil {
			add(direct[k], "write a bad tuple", err.Error())
		}
	}
	for k, op := range p.Filters {
		for _, t := range op.Tuples {
			if err := CheckTuple(t); err != nil {
				add(ops[k], "write a bad tuple", err.Error())
			}
		}
	}
	return out
}
