// Package modeltest runs authorization-model tests declared by an ofga
// workspace (an ofga.yaml manifest and its *.test.yaml files).
//
// Tests run against a pluggable Engine: by default an in-process, embedded
// OpenFGA server (hermetic — no external dependency, real store, or profile),
// or a specific OpenFGA version in a Docker container, or an already-running
// server over gRPC. Each test gets its own fresh store. The package also
// computes grant-based branch coverage against the model and can render
// human/JUnit/JSON/GitHub reports.
package modeltest
