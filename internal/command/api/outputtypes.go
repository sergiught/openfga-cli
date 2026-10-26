package api

import "github.com/sergiught/openfga-cli/internal/outputschema"

// OutputTypes maps api's full path to what it prints under --json: the
// server's response, passed through unchanged, so any JSON value.
func OutputTypes() map[string]any {
	return map[string]any{"ofga api": outputschema.Handwritten{}}
}
