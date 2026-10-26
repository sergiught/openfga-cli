package configcmd

import "github.com/sergiught/openfga-cli/internal/outputschema"

// OutputTypes maps the config-related commands' full paths to what they print
// under --json. All are hand-written: they print maps, and theme varies by
// mode.
func OutputTypes() map[string]any {
	h := outputschema.Handwritten{}
	return map[string]any{
		"ofga config path": h,
		"ofga theme":       h,
		"ofga init":        h,
	}
}
