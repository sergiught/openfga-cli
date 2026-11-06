package sandbox

import (
	"fmt"
	"os"

	gap "github.com/muesli/go-app-paths"
)

// DBEnv overrides the saved playground database location.
const DBEnv = "OPENFGA_PLAYGROUND_DB"

// DefaultPath returns where the playground database is saved: $OPENFGA_PLAYGROUND_DB
// if set, else playground.db in the platform's per-user data directory.
func DefaultPath() (string, error) {
	if v := os.Getenv(DBEnv); v != "" {
		return v, nil
	}
	p, err := gap.NewScope(gap.User, "ofga").DataPath("playground.db")
	if err != nil {
		return "", fmt.Errorf("resolve playground data path: %w", err)
	}
	return p, nil
}
