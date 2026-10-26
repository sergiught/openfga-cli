package profiles

import "github.com/sergiught/openfga-cli/internal/outputschema"

// OutputTypes maps each profiles command's full path to what it prints under
// --json. All are hand-written: they print maps, and show and
// cleanup-credentials vary by mode.
func OutputTypes() map[string]any {
	h := outputschema.Handwritten{}
	return map[string]any{
		"ofga profiles list":                h,
		"ofga profiles current":             h,
		"ofga profiles show":                h,
		"ofga profiles use":                 h,
		"ofga profiles set":                 h,
		"ofga profiles unset":               h,
		"ofga profiles add":                 h,
		"ofga profiles remove":              h,
		"ofga profiles cleanup-credentials": h,
	}
}
