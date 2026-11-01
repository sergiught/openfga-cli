package optimize

import openfgav1 "github.com/openfga/api/proto/openfga/v1"

// Report is the full outcome of analysing a model.
type Report struct {
	// Findings lists safe findings (in Rewrite.Applied order), then caller,
	// then tuple findings; the latter two are sorted by type, relation, ID.
	Findings []Finding
	// Rewrite is nil when no safe findings were applied.
	Rewrite *RewriteResult
}

// Analyze validates p, applies the safe rewrites and reports advisory
// findings computed on the rewritten model.
func Analyze(p *openfgav1.AuthorizationModel) (*Report, error) {
	m, err := NewModel(p)
	if err != nil {
		return nil, err
	}
	rw, err := Rewrite(m)
	if err != nil {
		return nil, err
	}
	rep := &Report{}
	target := m
	if len(rw.Applied) > 0 {
		rep.Rewrite = rw
		rep.Findings = append(rep.Findings, rw.Applied...)
		target = rw.After
	}
	adv, err := advise(target)
	if err != nil {
		return nil, err
	}
	rep.Findings = append(rep.Findings, adv...)
	return rep, nil
}
