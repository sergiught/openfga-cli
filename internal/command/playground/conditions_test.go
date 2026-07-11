package playground

import "testing"

func TestParseContextJSON(t *testing.T) {
	m, err := parseContextJSON(`{"a":1,"b":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if m["a"] != float64(1) || m["b"] != "x" {
		t.Fatalf("parsed = %v", m)
	}
	if got, _ := parseContextJSON("   "); got != nil {
		t.Error("empty context should parse to nil")
	}
	if _, err := parseContextJSON("not json"); err == nil {
		t.Error("invalid JSON should error")
	}
}

func TestParseContextualTuples(t *testing.T) {
	ts, err := parseContextualTuples("user:anne member team:eng; user:bob viewer document:1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 {
		t.Fatalf("got %d tuples, want 2", len(ts))
	}
	if ts[0].User != "user:anne" || ts[0].Relation != "member" || ts[0].Object != "team:eng" {
		t.Errorf("tuple[0] = %+v", ts[0])
	}
	if _, err := parseContextualTuples("only two fields"); err == nil {
		t.Error("a non user/relation/object tuple should error")
	}
	if got, _ := parseContextualTuples(""); got != nil {
		t.Error("empty should parse to nil")
	}
	// Round-trip through the formatter.
	if s := formatContextualTuples(ts); s != "user:anne member team:eng; user:bob viewer document:1" {
		t.Errorf("round-trip = %q", s)
	}
}

func TestParseCondition(t *testing.T) {
	c, err := parseCondition("non_expired_grant", `{"grant_duration":"10m"}`)
	if err != nil || c == nil {
		t.Fatalf("c=%v err=%v", c, err)
	}
	if c.Name != "non_expired_grant" || c.Context["grant_duration"] != "10m" {
		t.Errorf("condition = %+v", c)
	}
	if got, _ := parseCondition("", `{"x":1}`); got != nil {
		t.Error("empty condition name should yield nil (unconditioned tuple)")
	}
}
