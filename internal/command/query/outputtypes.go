package query

import "github.com/sergiught/go-openfga/openfga"

// OutputTypes maps each query command's full path to what it prints under
// --json. expand prints the userset tree as the server returned it.
func OutputTypes() map[string]any {
	return map[string]any{
		"ofga query check":          openfga.CheckResponse{},
		"ofga query batch-check":    openfga.BatchCheckResponse{},
		"ofga query expand":         map[string]any{},
		"ofga query list-objects":   []string{},
		"ofga query list-relations": []string{},
		"ofga query list-users":     []openfga.User{},
	}
}
