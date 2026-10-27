package docs

import "github.com/sergiught/openfga-cli/internal/docsearch"

// OutputTypes maps each docs command's full path to what it prints under
// --json.
func OutputTypes() map[string]any {
	return map[string]any{
		"ofga docs search": []docsearch.Result{},
		"ofga docs show":   docsearch.Page{},
	}
}
