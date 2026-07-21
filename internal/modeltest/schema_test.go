package modeltest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateAcceptsGoodManifest(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "docs", "ofga.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	jsonData, err := yamlToJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(docManifest, jsonData); err != nil {
		t.Fatalf("good manifest rejected: %v", err)
	}
}

func TestValidateRejectsManifestMissingVersion(t *testing.T) {
	bad := []byte(`{"model":"./m.fga"}`)
	if err := validate(docManifest, bad); err == nil {
		t.Fatal("manifest without version must be rejected")
	}
}

func TestValidateRejectsCheckMissingRequiredFields(t *testing.T) {
	// A check with no user/object hits the engine with empty strings and a check
	// with no assertions is a silent no-op; the schema now requires all three.
	cases := map[string]string{
		"missing user":       `{"tests":[{"name":"t","check":[{"object":"doc:1","assertions":{"viewer":true}}]}]}`,
		"missing object":     `{"tests":[{"name":"t","check":[{"user":"user:anne","assertions":{"viewer":true}}]}]}`,
		"missing assertions": `{"tests":[{"name":"t","check":[{"user":"user:anne","object":"doc:1"}]}]}`,
	}

	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validate(docTestFile, []byte(doc)); err == nil {
				t.Fatalf("%s: check case must be rejected", name)
			}
		})
	}

	good := `{"tests":[{"name":"t","check":[{"user":"user:anne","object":"doc:1","assertions":{"viewer":true}}]}]}`
	if err := validate(docTestFile, []byte(good)); err != nil {
		t.Fatalf("complete check case rejected: %v", err)
	}
}

func TestValidateRejectsVacuousTests(t *testing.T) {
	for name, doc := range map[string]string{
		"name only":   `{"tests":[{"name":"looks-green"}]}`,
		"empty check": `{"tests":[{"name":"looks-green","check":[]}]}`,
		"empty name":  `{"tests":[{"name":"","check":[{"user":"user:a","object":"doc:1","assertions":{"viewer":true}}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validate(docTestFile, []byte(doc)); err == nil {
				t.Fatal("vacuous test must be rejected")
			}
		})
	}
}

func TestValidateRejectsListCasesMissingRequiredFields(t *testing.T) {
	// list_objects/list_users cases carry the same hazard as check cases: a
	// missing user/type/object hits the engine with empty strings and a missing
	// assertions block is a silent no-op. All three fields are now required.
	cases := map[string]string{
		"list_objects missing user":       `{"tests":[{"name":"t","list_objects":[{"type":"doc","assertions":{"viewer":["doc:1"]}}]}]}`,
		"list_objects missing type":       `{"tests":[{"name":"t","list_objects":[{"user":"user:anne","assertions":{"viewer":["doc:1"]}}]}]}`,
		"list_objects missing assertions": `{"tests":[{"name":"t","list_objects":[{"user":"user:anne","type":"doc"}]}]}`,
		"list_users missing object":       `{"tests":[{"name":"t","list_users":[{"user_filter":[{"type":"user"}],"assertions":{"viewer":{"users":["user:anne"]}}}]}]}`,
		"list_users missing user_filter":  `{"tests":[{"name":"t","list_users":[{"object":"doc:1","assertions":{"viewer":{"users":["user:anne"]}}}]}]}`,
		"list_users missing assertions":   `{"tests":[{"name":"t","list_users":[{"object":"doc:1","user_filter":[{"type":"user"}]}]}]}`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validate(docTestFile, []byte(doc)); err == nil {
				t.Fatalf("%s: case must be rejected", name)
			}
		})
	}

	goodLO := `{"tests":[{"name":"t","list_objects":[{"user":"user:anne","type":"doc","assertions":{"viewer":["doc:1"]}}]}]}`
	if err := validate(docTestFile, []byte(goodLO)); err != nil {
		t.Fatalf("complete list_objects case rejected: %v", err)
	}
	goodLU := `{"tests":[{"name":"t","list_users":[{"object":"doc:1","user_filter":[{"type":"user"}],"assertions":{"viewer":{"users":["user:anne"]}}}]}]}`
	if err := validate(docTestFile, []byte(goodLU)); err != nil {
		t.Fatalf("complete list_users case rejected: %v", err)
	}
}

func TestSchemaRejectsBothSingularAndPluralSubject(t *testing.T) {
	// user/users and object/objects are mutually exclusive; setting both was
	// previously accepted by the schema and only failed at runtime.
	bothUsers := `{"tests":[{"name":"t","check":[{"user":"user:a","users":["user:b"],"object":"doc:1","assertions":{"viewer":true}}]}]}`
	if err := validate(docTestFile, []byte(bothUsers)); err == nil {
		t.Fatal("check case with both user and users must be rejected")
	}
	bothObjects := `{"tests":[{"name":"t","check":[{"user":"user:a","object":"doc:1","objects":["doc:2"],"assertions":{"viewer":true}}]}]}`
	if err := validate(docTestFile, []byte(bothObjects)); err == nil {
		t.Fatal("check case with both object and objects must be rejected")
	}
	// The exclusive-or forms must still each be accepted.
	okSingular := `{"tests":[{"name":"t","check":[{"user":"user:a","object":"doc:1","assertions":{"viewer":true}}]}]}`
	if err := validate(docTestFile, []byte(okSingular)); err != nil {
		t.Fatalf("singular user/object should be accepted: %v", err)
	}
	okPlural := `{"tests":[{"name":"t","check":[{"users":["user:a"],"objects":["doc:1"],"assertions":{"viewer":true}}]}]}`
	if err := validate(docTestFile, []byte(okPlural)); err != nil {
		t.Fatalf("plural users/objects should be accepted: %v", err)
	}
}

func TestSchemaRejectsEmptyUserFilter(t *testing.T) {
	doc := `{"tests":[{"name":"t","list_users":[{"object":"doc:1","user_filter":[{}],"assertions":{"viewer":{"users":["user:a"]}}}]}]}`
	if err := validate(docTestFile, []byte(doc)); err == nil {
		t.Fatal("empty user_filter entry (no type) must be rejected")
	}
}

func TestValidateTestFileAcceptsGoodRejectsBad(t *testing.T) {
	good := "tests:\n  - name: t\n    check:\n      - {user: user:a, object: doc:1, assertions: {viewer: true}}\n"
	if err := ValidateTestFile([]byte(good)); err != nil {
		t.Fatalf("good test file rejected: %v", err)
	}

	missingTests := "fixtures: [x]\n"
	if err := ValidateTestFile([]byte(missingTests)); err == nil {
		t.Fatal("test file missing tests: must be rejected")
	}

	missingUser := "tests:\n  - name: t\n    check:\n      - {object: doc:1, assertions: {viewer: true}}\n"
	if err := ValidateTestFile([]byte(missingUser)); err == nil {
		t.Fatal("check case missing user must be rejected")
	}

	malformed := "tests: [\n"
	if err := ValidateTestFile([]byte(malformed)); err == nil {
		t.Fatal("malformed YAML must be rejected")
	}
}

func TestAllTestdataValidatesAgainstSchema(t *testing.T) {
	err := filepath.Walk("testdata", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		switch {
		case info.Name() == "ofga.yaml":
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			jsonData, jsonErr := yamlToJSON(data)
			if jsonErr != nil {
				t.Fatal(jsonErr)
			}
			if validateErr := validate(docManifest, jsonData); validateErr != nil {
				t.Errorf("%s: manifest rejected: %v", path, validateErr)
			}
		case strings.HasSuffix(info.Name(), ".test.yaml"):
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			jsonData, jsonErr := yamlToJSON(data)
			if jsonErr != nil {
				t.Fatal(jsonErr)
			}
			if validateErr := validate(docTestFile, jsonData); validateErr != nil {
				t.Errorf("%s: test file rejected: %v", path, validateErr)
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
