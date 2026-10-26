package tuple

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/output"
)

// maxReportedFailures caps the per-tuple failure lines printed in human mode;
// the full list is in --json/--yaml output and in --failed-file.
const maxReportedFailures = 10

// bulkOpts are the knobs the bulk --file write and delete paths share.
type bulkOpts struct {
	format      bulkFormat
	maxPerChunk int
	maxParallel int
	onDuplicate string // write only
	onMissing   string // delete only
	failedFile  string
}

// bulkFailure is one rejected tuple with the reason, as it appears in
// --json/--yaml output.
type bulkFailure struct {
	Tuple  openfga.TupleKey `json:"tuple"  yaml:"tuple"`
	Reason string           `json:"reason" yaml:"reason"`
}

// validate rejects out-of-range throughput and conflict-mode flags as usage
// errors, before any file is read or any request is issued.
func (o bulkOpts) validate(del bool) error {
	if o.maxPerChunk < 1 {
		return clierr.WithCode(clierr.CodeUsage,
			fmt.Errorf("--max-tuples-per-write must be at least 1 (got %d)", o.maxPerChunk))
	}
	if o.maxParallel < 1 {
		return clierr.WithCode(clierr.CodeUsage,
			fmt.Errorf("--max-parallel-requests must be at least 1 (got %d)", o.maxParallel))
	}
	if del {
		switch openfga.OnMissing(o.onMissing) {
		case openfga.OnMissingError, openfga.OnMissingIgnore:
			return nil
		default:
			return clierr.WithCode(clierr.CodeUsage,
				fmt.Errorf("--on-missing must be one of error, ignore (got %q)", o.onMissing))
		}
	}
	switch openfga.OnDuplicate(o.onDuplicate) {
	case openfga.OnDuplicateError, openfga.OnDuplicateIgnore:
		return nil
	default:
		return clierr.WithCode(clierr.CodeUsage,
			fmt.Errorf("--on-duplicate must be one of error, ignore (got %q)", o.onDuplicate))
	}
}

// requestOptions maps the CLI flags onto the SDK's bulk request options. The
// "error" conflict modes are deliberately not sent: that is already the server
// default, so an unflagged invocation keeps producing exactly the request body
// it produced before --on-duplicate/--on-missing existed (and keeps working
// against servers older than OpenFGA 1.10, which reject the field).
func (o bulkOpts) requestOptions(del bool) []openfga.RequestOption {
	opts := []openfga.RequestOption{
		openfga.WithMaxPerChunk(o.maxPerChunk),
		openfga.WithMaxParallel(o.maxParallel),
	}
	if del && openfga.OnMissing(o.onMissing) == openfga.OnMissingIgnore {
		opts = append(opts, openfga.WithOnMissing(openfga.OnMissingIgnore))
	}
	if !del && openfga.OnDuplicate(o.onDuplicate) == openfga.OnDuplicateIgnore {
		opts = append(opts, openfga.WithOnDuplicate(openfga.OnDuplicateIgnore))
	}
	return opts
}

// runBulk writes (or deletes, when del is true) keys through the SDK's
// non-transactional bulk helpers, collecting per-tuple failures instead of
// aborting on the first one. It reports the outcome in whichever output mode is
// active and returns a non-nil error when any tuple failed, so the process exit
// code is non-zero.
func runBulk(cmd *cobra.Command, c *cli.CLI, cl *openfga.Client, keys []openfga.TupleKey, del bool, o bulkOpts) error {
	verb, noun := "wrote", "written"
	if del {
		verb, noun = "deleted", "deleted"
	}
	succeeded, failures, connErr := bulkApply(cmd, cl, keys, del, o, verb)

	failedKeys := make([]openfga.TupleKey, 0, len(failures))
	for _, f := range failures {
		failedKeys = append(failedKeys, f.Tuple)
	}
	// Written unconditionally, so a clean re-run truncates the file left behind
	// by the previous one: a stale list of failures that have since been fixed
	// is worse than an empty file.
	if o.failedFile != "" {
		if err := writeFailedFile(o.failedFile, failedKeys, o.format); err != nil {
			output.Errorf(cmd.ErrOrStderr(), "could not write --failed-file %s: %v", o.failedFile, err)
		}
	}

	machine := c.JSON || c.YAML
	emitErr := emitBulkResult(cmd, c, noun, len(succeeded), keys, succeeded, failures)
	if len(failures) == 0 {
		if machine || output.Plain {
			return emitErr
		}
		output.Successf(cmd.ErrOrStderr(), "%s %d tuple(s)", verb, len(succeeded))
		return nil
	}

	summary := fmt.Sprintf("%s %d, failed %d of %d tuple(s)", verb, len(succeeded), len(failures), len(keys))
	if ctxErr := cmd.Context().Err(); ctxErr != nil {
		// Interrupted: main's cancellation handler prints "canceled" plus this
		// detail, so printing a second summary here would only duplicate it.
		return clierr.WithPartialResult(fmt.Errorf("%s: %w", summary, ctxErr))
	}
	if !machine && !output.Plain {
		reportFailures(cmd, summary, failures, o, connErr)
	}
	// A transport failure keeps CodeNetwork: `ofga tuples write --file` is a
	// scripting entry point, and "the server was unreachable" is a retry, while
	// "the server rejected these tuples" is not.
	code, kind := clierr.CodeError, clierr.KindPartialFailure
	if connErr != nil {
		code, kind = clierr.CodeNetwork, clierr.KindNetwork
	}
	hint := "pass --failed-file <path> to save the failed tuples for a re-run"
	if o.failedFile != "" {
		hint = "re-run the failed tuples with --file " + o.failedFile
	}
	// The summary and the per-tuple reasons (or the machine payload) have
	// already reached the user, so the error is Silent: main honors the exit
	// code without printing a second, redundant message. It still carries a
	// PartialResult so a cancellation racing this return keeps its reporting.
	return clierr.WithPartialResult(clierr.WithHint(hint, clierr.WithKind(kind, clierr.Silent(code, summary))))
}

// bulkApply feeds keys to the SDK a window at a time and splits the per-tuple
// results into successes and failures. The SDK chunks and parallelizes
// internally but exposes no progress hook, so keys are handed over in windows
// of maxPerChunk*maxParallel — the most work it can have in flight — and
// progress is reported between windows. With the defaults (100 per chunk, one
// request at a time) a window is exactly one chunk.
//
// The third return value is the first transport failure seen, if any: it stops
// the run (a server that is down will not be up for the next window) and marks
// the outcome as a network failure rather than a tuple rejection.
func bulkApply(cmd *cobra.Command, cl *openfga.Client, keys []openfga.TupleKey, del bool, o bulkOpts, verb string) ([]openfga.TupleKey, []bulkFailure, error) {
	opts := o.requestOptions(del)
	budget := maxIsolationRequests
	succeeded := make([]openfga.TupleKey, 0, len(keys))
	failures := make([]bulkFailure, 0)

	window := o.maxPerChunk * o.maxParallel
	if window < 1 || window > len(keys) {
		window = len(keys)
	}
	for start := 0; start < len(keys); start += window {
		end := min(start+window, len(keys))
		var res *openfga.WriteTuplesResponse
		var err error
		if del {
			res, err = cl.Tuples.DeleteTuples(cmd.Context(), keys[start:end], opts...)
		} else {
			res, err = cl.Tuples.WriteTuples(cmd.Context(), keys[start:end], opts...)
		}
		if err != nil {
			// The SDK only returns a top-level error when no request could be
			// issued at all (e.g. no store resolved); nothing later would fare
			// better, so record the remainder as failed and stop.
			failures = append(failures, notAttempted(keys[start:], failureReason(err))...)
			if clierr.IsConnErr(err) {
				return succeeded, failures, err
			}
			return succeeded, failures, nil
		}
		results := res.Writes
		if del {
			results = res.Deletes
		}
		var connErr error
		// suspects are the tuples a 400 rejected, grouped by the chunk the SDK
		// put them in (it chunks in input order, one result per key).
		suspects := map[int][]int{}
		for i, r := range results {
			if r.Status == openfga.WriteStatusFailure {
				// The SDK returns one result per key, in input order, and chunks
				// in input order (go-openfga tuples.go bulkWrite), so result i
				// belongs to chunk i/maxPerChunk.
				if o.maxPerChunk > 1 && isTupleRejection(r.Err) {
					suspects[i/o.maxPerChunk] = append(suspects[i/o.maxPerChunk], i)
				} else {
					failures = append(failures, bulkFailure{Tuple: r.TupleKey, Reason: failureReason(r.Err)})
				}
				if connErr == nil && clierr.IsConnErr(r.Err) {
					connErr = r.Err
				}
				continue
			}
			succeeded = append(succeeded, r.TupleKey)
		}
		for chunk := 0; chunk*o.maxPerChunk < len(results); chunk++ {
			idx := suspects[chunk]
			if len(idx) == 0 {
				continue
			}
			// A lone suspect needs no bisection. When another chunk of this
			// window lost the server, bisecting would only send more requests
			// into the outage, so the chunk keeps its own rejection.
			if len(idx) == 1 || connErr != nil {
				for _, i := range idx {
					failures = append(failures, bulkFailure{Tuple: results[i].TupleKey, Reason: failureReason(results[i].Err)})
				}
				continue
			}
			ks := make([]openfga.TupleKey, len(idx))
			for j, i := range idx {
				ks[j] = results[i].TupleKey
			}
			ok, bad, cerr := isolateRejected(cmd, cl, ks, del, o, &budget, failureReason(results[idx[0]].Err))
			succeeded = append(succeeded, ok...)
			failures = append(failures, bad...)
			if cerr != nil {
				// Suspects in later chunks of this window were taken out of
				// the result loop above, so account for them here.
				for later := chunk + 1; later*o.maxPerChunk < len(results); later++ {
					for _, i := range suspects[later] {
						failures = append(failures, notAttempted([]openfga.TupleKey{results[i].TupleKey}, reasonUnreachable)...)
					}
				}
				return succeeded, append(failures, notAttempted(keys[end:], reasonUnreachable)...), cerr
			}
		}
		if connErr != nil {
			// The server is unreachable, not merely unhappy with these tuples.
			// Hammering it with the remaining windows would only repeat the
			// failure, so stop and account for what was never attempted.
			failures = append(failures, notAttempted(keys[end:], reasonUnreachable)...)
			return succeeded, failures, connErr
		}
		output.Progressf(cmd.ErrOrStderr(), "%s %d/%d tuples", verb, len(succeeded), len(keys))
	}
	return succeeded, failures, nil
}

const reasonUnreachable = "not attempted: stopped after the server became unreachable"

// isTupleRejection reports whether err is a 400 whose API code says a tuple in
// the request was bad. Other 400s (for example authorization_model_not_found)
// are request-level problems that every half of a bisection would hit too, so
// bisecting them would burn the whole budget for nothing; they keep the
// chunk-wide marking.
func isTupleRejection(err error) bool {
	var verr *openfga.ValidationError
	if !errors.As(err, &verr) {
		return false
	}
	return verr.Code == "write_failed_due_to_invalid_input" || verr.Code == openfga.CodeValidationError
}

// maxIsolationRequests bounds the extra requests spent, across a whole run,
// pinning a rejected chunk down to the tuples the server actually refused. It
// is a var so tests can lower it.
var maxIsolationRequests = 1000

// isolateRejected bisects keys, which a chunk request rejected with a 400, to
// find which tuples the server actually refused. A rejected write is
// transactional (nothing was applied), so resending halves is safe. Requests
// are sequential and each half goes out as a single request. chunkReason is
// the rejection of the whole chunk, kept for tuples the budget could not
// settle. A connection error stops the bisection and is returned.
func isolateRejected(cmd *cobra.Command, cl *openfga.Client, keys []openfga.TupleKey, del bool, o bulkOpts, budget *int, chunkReason string) (ok []openfga.TupleKey, failed []bulkFailure, connErr error) {
	if len(keys) == 1 {
		return nil, []bulkFailure{{Tuple: keys[0], Reason: chunkReason}}, nil
	}
	mid := len(keys) / 2
	halves := [][]openfga.TupleKey{keys[:mid], keys[mid:]}
	for h, half := range halves {
		if *budget <= 0 {
			for _, rest := range halves[h:] {
				failed = append(failed, notAttempted(rest, "unisolated: "+chunkReason)...)
			}
			return ok, failed, nil
		}
		*budget--
		opts := o
		opts.maxPerChunk, opts.maxParallel = len(half), 1
		var res *openfga.WriteTuplesResponse
		var err error
		if del {
			res, err = cl.Tuples.DeleteTuples(cmd.Context(), half, opts.requestOptions(del)...)
		} else {
			res, err = cl.Tuples.WriteTuples(cmd.Context(), half, opts.requestOptions(del)...)
		}
		var halfErr error
		if err != nil {
			halfErr = err
		} else {
			results := res.Writes
			if del {
				results = res.Deletes
			}
			for _, r := range results {
				if r.Status == openfga.WriteStatusFailure {
					halfErr = r.Err
					break
				}
			}
		}
		switch {
		case halfErr == nil:
			ok = append(ok, half...)
		case isTupleRejection(halfErr):
			o2, f2, c2 := isolateRejected(cmd, cl, half, del, o, budget, failureReason(halfErr))
			ok, failed = append(ok, o2...), append(failed, f2...)
			if c2 != nil {
				for _, rest := range halves[h+1:] {
					failed = append(failed, notAttempted(rest, reasonUnreachable)...)
				}
				return ok, failed, c2
			}
		case clierr.IsConnErr(halfErr):
			failed = append(failed, notAttempted(half, reasonUnreachable)...)
			for _, rest := range halves[h+1:] {
				failed = append(failed, notAttempted(rest, reasonUnreachable)...)
			}
			return ok, failed, halfErr
		default:
			failed = append(failed, notAttempted(half, failureReason(halfErr))...)
		}
	}
	return ok, failed, nil
}

// notAttempted records keys that never reached the server, so the failure count
// and --failed-file still account for every input tuple.
func notAttempted(keys []openfga.TupleKey, reason string) []bulkFailure {
	out := make([]bulkFailure, 0, len(keys))
	for _, k := range keys {
		out = append(out, bulkFailure{Tuple: k, Reason: reason})
	}
	return out
}

// failureReason renders a per-tuple reason. clierr.Friendly's multi-line hints
// are deliberately skipped for connection failures: the same three lines
// repeated for every rejected tuple buries the summary. reportFailures prints
// that hint once instead.
func failureReason(err error) string {
	if clierr.IsConnErr(err) {
		return err.Error()
	}
	return clierr.Friendly(err)
}

// reportFailures prints the human summary followed by the rejected tuples and
// their reasons, capped so a large import does not bury the summary.
func reportFailures(cmd *cobra.Command, summary string, failures []bulkFailure, o bulkOpts, connErr error) {
	w := cmd.ErrOrStderr()
	output.Errorf(w, "%s", summary)
	for i, f := range failures {
		if i == maxReportedFailures {
			output.Hintf(w, "… and %d more", len(failures)-i)
			break
		}
		output.Hintf(w, "%s: %s", fga.FormatTuple(f.Tuple), f.Reason)
	}
	switch {
	case connErr != nil:
		// Printed once, not once per tuple, and instead of the chunk hint:
		// nothing about the chunk size is what went wrong here.
		output.Hintf(w, "%s", clierr.Friendly(connErr))
	default:
		if n := countUnisolated(failures); n > 0 {
			output.Hintf(w, "%d tuple(s) could not be isolated within the retry budget; re-run the failed tuples with --max-tuples-per-write 1", n)
		}
	}
	if o.failedFile != "" {
		output.Hintf(w, "the failed tuples were written to %s and can be re-run with --file", o.failedFile)
	}
}

func countUnisolated(failures []bulkFailure) int {
	n := 0
	for _, f := range failures {
		if strings.HasPrefix(f.Reason, "unisolated: ") {
			n++
		}
	}
	return n
}

// emitBulkResult renders the machine-readable bulk outcome. The legacy
// written/deleted, total and complete fields are kept so existing scripts still
// work; successful and failed are added alongside them.
func emitBulkResult(cmd *cobra.Command, c *cli.CLI, noun string, done int, keys, succeeded []openfga.TupleKey, failures []bulkFailure) error {
	if c.JSON || c.YAML {
		return output.Emit(cmd.OutOrStdout(), c.YAML, map[string]any{
			noun:         done,
			"total":      len(keys),
			"complete":   len(failures) == 0,
			"successful": succeeded,
			"failed":     failures,
		})
	}
	if output.Plain {
		// A clean run stays the single row it has always been; total/complete
		// are the legacy partial-failure rows and only appear when the run was
		// in fact partial.
		rows := [][2]string{{noun, fmt.Sprint(done)}}
		if len(failures) > 0 {
			rows = append(rows,
				[2]string{"total", fmt.Sprint(len(keys))},
				[2]string{"complete", "false"},
			)
		}
		return output.KeyValues(cmd.OutOrStdout(), rows)
	}
	return nil
}

// writeFailedFile writes the rejected tuples in the same format the input used,
// with the reasons stripped, so the file can be fed straight back to --file
// once the cause is fixed.
func writeFailedFile(path string, keys []openfga.TupleKey, format bulkFormat) error {
	var buf bytes.Buffer
	if err := encodeTupleFile(&buf, keys, format); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o600)
}
