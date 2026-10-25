package mapping

import (
	"time"

	"github.com/openfga/mapper"
)

// CompileSource compiles a mapping file's bytes with the limits and tracing
// every ofga surface uses, so `validate`, `test` and `apply` agree on what a
// file means. Evaluation gets mapper's default timeout, as in `fga`.
func CompileSource(src []byte) (*mapper.Mapping, mapper.Diagnostics, error) {
	return compileSource(src, mapper.DefaultTimeout)
}

// compileSource is CompileSource with the evaluation timeout given: the
// workspace's preview passes the shorter EvalTimeout.
func compileSource(src []byte, timeout time.Duration) (*mapper.Mapping, mapper.Diagnostics, error) {
	m, err := mapper.Compile(src,
		mapper.WithTrace(true),
		mapper.WithTimeout(timeout),
		mapper.WithMaxTuples(MaxTuples),
		mapper.WithMaxRules(MaxRules),
		mapper.WithMaxIteratorItems(MaxIteratorItems),
	)
	if err != nil {
		return nil, mapper.DiagnosticsFrom(err), err
	}
	return m, nil, nil
}
