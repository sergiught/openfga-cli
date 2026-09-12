package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestGenerateIncludesModuleLicenseText(t *testing.T) {
	var buf bytes.Buffer

	err := generate(&buf, []module{{
		Path:    "example.com/mit",
		Version: "v1.0.0",
		Dir:     "testdata/mit-module",
	}})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"example.com/mit",
		"v1.0.0",
		"MIT",
		"Copyright (c) 2014 Bob Matcuk",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("bundle is missing %q", want)
		}
	}
}

// gopkg.in/yaml.v3 and go.yaml.in/yaml/v3 carry one LICENSE covering two
// licenses at once. Reporting a single ID for them would understate what the
// bundle has to attribute, so both must survive into the output.
func TestGenerateReportsEveryLicenseInADualLicensedFile(t *testing.T) {
	var buf bytes.Buffer

	err := generate(&buf, []module{{
		Path:    "example.com/dual",
		Version: "v1.0.0",
		Dir:     "testdata/dual-module",
	}})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// Asserted against the rendered "License:" header rather than the raw
	// output: the fixture's own text says "MIT" four times, so a bare
	// substring check would pass even if detection returned nothing at all.
	if want := "License: Apache-2.0, MIT\n"; !strings.Contains(buf.String(), want) {
		t.Errorf("bundle does not report both licenses; want a line %q", want)
	}
}

// The gate exists so a copyleft dependency cannot reach a release unnoticed.
// Emitting a bundle that quietly lists one would defeat the point.
func TestGenerateRejectsALicenseOutsideTheAllowlist(t *testing.T) {
	var buf bytes.Buffer

	err := generate(&buf, []module{{
		Path:    "example.com/disallowed",
		Version: "v1.0.0",
		Dir:     "testdata/disallowed-module",
	}})
	if err == nil {
		t.Fatal("generate succeeded on a CC-BY-SA-4.0 module; want an error")
	}
	if !strings.Contains(err.Error(), "CC-BY-SA-4.0") {
		t.Errorf("error %q does not name the offending license", err)
	}
}

// Real modules spell it LICENSE, LICENSE.txt, LICENSE.md, LICENCE or COPYING.
// Looking only for LICENSE would fail the build on perfectly compliant deps.
func TestGenerateFindsLicensesUnderAlternateFilenames(t *testing.T) {
	var buf bytes.Buffer

	err := generate(&buf, []module{{
		Path:    "example.com/copying",
		Version: "v1.0.0",
		Dir:     "testdata/copying-module",
	}})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if want := "License: BSD-3-Clause\n"; !strings.Contains(buf.String(), want) {
		t.Errorf("bundle does not report the COPYING file; want a line %q", want)
	}
}

// A module with no license text at all cannot be attributed, so it must stop
// the build rather than ship with a silent gap in the bundle.
func TestGenerateRejectsAModuleWithNoLicenseFile(t *testing.T) {
	var buf bytes.Buffer

	err := generate(&buf, []module{{
		Path:    "example.com/nolicense",
		Version: "v1.0.0",
		Dir:     "testdata/nolicense-module",
	}})
	if err == nil {
		t.Fatal("generate succeeded on a module with no license file; want an error")
	}
	if !strings.Contains(err.Error(), "example.com/nolicense") {
		t.Errorf("error %q does not name the offending module", err)
	}
}

// Apache-2.0 section 4(d) requires a derivative work to carry the attribution
// notices of the modules it redistributes. Nine of ofga's dependencies ship a
// NOTICE, so shipping the LICENSE alone would leave the bundle non-compliant.
func TestGenerateIncludesNoticeFileContents(t *testing.T) {
	var buf bytes.Buffer

	err := generate(&buf, []module{{
		Path:    "example.com/notice",
		Version: "v1.0.0",
		Dir:     "testdata/notice-module",
	}})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if want := "This product includes software developed at"; !strings.Contains(buf.String(), want) {
		t.Errorf("bundle does not carry the NOTICE contents; want %q", want)
	}
}

// go-digest's LICENSE.docs is CC-BY-SA-4.0 but covers only that repo's prose,
// which ofga does not vendor. The carve-out is recorded per module rather than
// by loosening the allowlist, so the next CC-BY-SA dependency still fails.
func TestGenerateAllowsARecordedExceptionAndStatesWhy(t *testing.T) {
	var buf bytes.Buffer

	err := generate(&buf, []module{{
		Path:    "github.com/opencontainers/go-digest",
		Version: "v1.0.0",
		Dir:     "testdata/disallowed-module",
	}})
	if err != nil {
		t.Fatalf("generate rejected a recorded exception: %v", err)
	}

	if want := "Exception:"; !strings.Contains(buf.String(), want) {
		t.Errorf("bundle does not record why the exception was granted; want a %q line", want)
	}
}

// go list emits one line per package, so a module contributing several packages
// repeats. The main module appears too and must not attribute itself.
func TestParseModulesDeduplicatesAndDropsTheMainModule(t *testing.T) {
	const out = `github.com/sergiught/openfga-cli
example.com/a	v1.0.0	/mod/a
example.com/a	v1.0.0	/mod/a
example.com/b	v2.0.0	/mod/b
`

	got, err := parseModules(out, "github.com/sergiught/openfga-cli")
	if err != nil {
		t.Fatalf("parseModules: %v", err)
	}

	want := []module{
		{Path: "example.com/a", Version: "v1.0.0", Dir: "/mod/a"},
		{Path: "example.com/b", Version: "v2.0.0", Dir: "/mod/b"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
