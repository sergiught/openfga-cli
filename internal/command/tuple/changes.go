package tuple

import (
	"context"
	"fmt"
	"os"

	"github.com/sergiught/go-openfga/openfga"
)

// readChanges pages the changelog and returns both the changes and the token to
// resume from.
//
// The SDK's ChangesAll iterator does the same paging, but it consumes the
// continuation token internally and never surfaces the final one, so a caller
// polling for new changes had no way to say "carry on from where I stopped" and
// had to re-read the changelog from the beginning (or guess with --start-time,
// which is not a cursor and can straddle equal timestamps). Paging here keeps
// the last token.
//
// Termination matches the SDK's: stop when the server returns no changes, or
// when it hands back the token it was given, which is how OpenFGA signals that
// the reader has caught up.
func readChanges(
	ctx context.Context,
	cl *openfga.Client,
	opts *openfga.ReadChangesOptions,
	maxResults int,
) ([]openfga.TupleChange, string, error) {
	o := *opts
	var changes []openfga.TupleChange

	for {
		sent := o.ContinuationToken
		page, err := cl.Tuples.ReadChanges(ctx, &o)
		if err != nil {
			return nil, "", err
		}
		for _, ch := range page.Changes {
			changes = append(changes, ch)
			if maxResults > 0 && len(changes) >= maxResults {
				// Stopping mid-page: the page's token covers changes that were
				// not returned, so resuming from it would skip them. The caller
				// re-reads from the token that produced this page instead.
				return changes, sent, nil
			}
		}
		o.ContinuationToken = page.ContinuationToken
		if len(page.Changes) == 0 || page.ContinuationToken == "" || page.ContinuationToken == sent {
			return changes, page.ContinuationToken, nil
		}
	}
}

// writeTokenFile records the resume token for the next run. An empty token
// still truncates the file: a stale token left behind by a previous run would
// silently resume from the wrong place.
func writeTokenFile(path, token string) error {
	if path == "" {
		return nil
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("write --token-file %s: %w", path, err)
	}
	return nil
}
