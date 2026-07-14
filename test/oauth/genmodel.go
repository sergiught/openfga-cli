//go:build ignore

// Regenerate model.json from model.fga (the GitHub sample DSL):
//   go run test/oauth/genmodel.go test/oauth/model.fga test/oauth/model.json
package main

import (
	"os"

	"github.com/openfga/language/pkg/go/transformer"
)

func main() {
	dsl, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	js, err := transformer.TransformDSLToJSON(string(dsl))
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(os.Args[2], []byte(js+"\n"), 0o644); err != nil {
		panic(err)
	}
}
