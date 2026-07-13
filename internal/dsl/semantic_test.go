package dsl

import "testing"

func TestUndefinedTypeFlagsOnlyUnknownTypeReference(t *testing.T) {
	// user & document are declared; grp is not. member (after '#'), c (after
	// 'with'), and owner/parent (RHS, not in brackets) are NOT type references.
	src := "model\n  schema 1.1\ntype user\ntype document\n  relations\n" +
		"    define viewer: [user, grp#member with c] or owner from parent\n" +
		"condition c(x: string) {\n  x == \"y\"\n}\n"
	diags := UndefinedTypeDiagnostics(src)
	if len(diags) != 1 {
		t.Fatalf("expected exactly 1 diagnostic (grp), got %d: %+v", len(diags), diags)
	}
	if diags[0].Msg != `undefined type "grp"` {
		t.Fatalf("expected msg `undefined type \"grp\"`, got %q", diags[0].Msg)
	}
}

func TestUndefinedTypeAllowsDeclared(t *testing.T) {
	src := "type user\ntype doc\n  relations\n    define v: [user]\n"
	if d := UndefinedTypeDiagnostics(src); len(d) != 0 {
		t.Fatalf("expected no diagnostics, got %+v", d)
	}
}

func TestUndefinedTypePosition(t *testing.T) {
	// [nope] is on line index 2 (0-based).
	src := "type doc\n  relations\n    define v: [nope]\n"
	diags := UndefinedTypeDiagnostics(src)
	if len(diags) != 1 || diags[0].Line != 2 {
		t.Fatalf("expected 1 diagnostic on line 2, got %+v", diags)
	}
}

func TestUndefinedTypeEmpty(t *testing.T) {
	if d := UndefinedTypeDiagnostics(""); d != nil {
		t.Fatalf("expected nil, got %+v", d)
	}
}

func TestUndefinedTypeIgnoresConditionBracketExpressions(t *testing.T) {
	// A CEL list literal inside a condition body uses the same bracket tokens as
	// a type restriction, but its identifiers are variables, not types.
	src := "model\n  schema 1.1\ntype user\n  relations\n    define v: [user]\n" +
		"condition c(x: string, foo: string, bar: string) {\n  x in [foo, bar]\n}\n"
	if d := UndefinedTypeDiagnostics(src); len(d) != 0 {
		t.Fatalf("expected no diagnostics (foo/bar are CEL vars, not types), got %+v", d)
	}
}
