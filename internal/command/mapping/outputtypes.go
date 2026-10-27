package mapping

import "github.com/sergiught/openfga-cli/internal/modeltest"

// OutputTypes maps each mappings command's full path to what it prints under
// --json. mappings (the interactive workspace) prints no document.
func OutputTypes() map[string]any {
	return map[string]any{
		"ofga mappings apply":    applyResult{},
		"ofga mappings test":     modeltest.Results{},
		"ofga mappings validate": validateResult{},
	}
}
