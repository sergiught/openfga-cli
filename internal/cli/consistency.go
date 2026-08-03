package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/clierr"
)

// consistencyValues are the read-consistency preferences the API accepts, in
// the order --help lists them. Every read command registers its flag through
// this file so the accepted set is spelled out exactly once.
var consistencyValues = []openfga.ConsistencyPreference{
	openfga.ConsistencyHigherConsistency,
	openfga.ConsistencyMinimizeLatency,
	openfga.ConsistencyUnspecified,
}

// consistencyNames lists the accepted values for help and error text.
func consistencyNames() string {
	return strings.Join(ConsistencyValues(), ", ")
}

// RegisterConsistencyFlag adds --consistency to cmd, storing the raw value in
// target for ConsistencyOption to resolve once the flag has been parsed. It
// also registers the completion for the flag's closed set of values, so every
// command that offers --consistency completes it the same way.
func RegisterConsistencyFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVar(target, "consistency", "", fmt.Sprintf(
		"read consistency: %s (default %s)", consistencyNames(), openfga.ConsistencyHigherConsistency))
	_ = cmd.RegisterFlagCompletionFunc("consistency",
		cobra.FixedCompletions(ConsistencyValues(), cobra.ShellCompDirectiveNoFileComp))
}

// ConsistencyValues lists the accepted --consistency values.
func ConsistencyValues() []string {
	names := make([]string, 0, len(consistencyValues))
	for _, v := range consistencyValues {
		names = append(names, string(v))
	}
	return names
}

// ConsistencyOption turns a --consistency value into the request option that
// carries it. An unset flag yields no option, so the client default
// (HIGHER_CONSISTENCY) applies; an explicit UNSPECIFIED is sent as such, which
// hands the choice back to the server.
func ConsistencyOption(v string) ([]openfga.RequestOption, error) {
	if strings.TrimSpace(v) == "" {
		return nil, nil
	}
	for _, want := range consistencyValues {
		if strings.EqualFold(v, string(want)) {
			return []openfga.RequestOption{openfga.WithConsistency(want)}, nil
		}
	}
	return nil, clierr.WithCode(clierr.CodeUsage,
		fmt.Errorf("--consistency must be one of %s (got %q)", consistencyNames(), v))
}
