package mapping

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseEventsAcceptsObjectArrayAndJSONL(t *testing.T) {
	for name, in := range map[string]string{
		"object": `{"type":"a"}`,
		"array":  "[{\"type\":\"a\"},\n {\"type\":\"b\"}]",
		"jsonl":  "{\"type\":\"a\"}\n\n{\"type\":\"b\"}\n",
		"mixed":  "[{\"type\":\"a\"}]\n{\"type\":\"b\"}",
	} {
		t.Run(name, func(t *testing.T) {
			evs, err := ParseEvents(strings.NewReader(in), "e.json")
			if err != nil || len(evs) == 0 || evs[0]["type"] != "a" {
				t.Fatalf("evs=%v err=%v", evs, err)
			}
		})
	}
}

func TestParseEventsErrors(t *testing.T) {
	cases := map[string]string{
		"":                        "no events in e.json",
		"  \n\n":                  "no events in e.json",
		"[]":                      "no events in e.json",
		"{\"type\":\"a\"}\n{bad}": "e.json:2:",
		"{\"type\":\"a\"}\n42\n":  "e.json:2:1: event 2 is not a JSON object",
	}
	for in, want := range cases {
		_, err := ParseEvents(strings.NewReader(in), "e.json")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", in, err, want)
		}
	}
}

// A bad object deep in a JSONL stream must be reported at its own line, not
// line 1 and not the line of the object before it.
func TestParseEventsReportsLineOfBadObject(t *testing.T) {
	cases := map[string]string{
		"{\"type\":\"a\"}\n{bad}":                   "e.json:2:",
		"{\"type\":\"a\"}\n{\"type\":\"b\"}\n{bad}": "e.json:3:",
		"{\"type\":\"a\"}\n\n\n42":                  "e.json:4:1: event 2 is not a JSON object",
	}
	for in, want := range cases {
		_, err := ParseEvents(strings.NewReader(in), "e.json")
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%q: err = %v, want prefix %q", in, err, want)
		}
	}
}

func TestParseEventsSkipsALeadingBOM(t *testing.T) {
	evs, err := ParseEvents(strings.NewReader("\xef\xbb\xbf{\"type\":\"a\"}\n{\"type\":\"b\"}\n"), "e.jsonl")
	if err != nil || len(evs) != 2 || evs[0]["type"] != "a" {
		t.Fatalf("events = %v err = %v", evs, err)
	}
}

func TestParseEventsKeepsLargeIntegersExact(t *testing.T) {
	evs, err := ParseEvents(strings.NewReader(`{"id": 1234567890123456789, "n": 5, "d": {"k": [7]}}`), "e.json")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := evs[0]["id"].(int); !ok || got != 1234567890123456789 {
		t.Fatalf("id = %#v, want int", evs[0]["id"])
	}
	if got, ok := evs[0]["d"].(map[string]any)["k"].([]any)[0].(int); !ok || got != 7 {
		t.Fatalf("nested = %#v, want int", evs[0]["d"])
	}
	d := &Document{Rules: []Rule{{
		Name: "r", When: `input.n > 3`,
		Tuples: []Tuple{{User: "user:a", Relation: "member", Object: "org:{{ input.id }}"}},
	}}}
	p := Evaluate(context.Background(), d, evs[0])
	if !p.OK() || len(p.Tuples) != 1 || p.Tuples[0].Object != "org:1234567890123456789" {
		t.Fatalf("preview = %+v err=%v", p.Tuples, p.EvalErr)
	}
}

func TestParseEventsKeepsFractions(t *testing.T) {
	evs, err := ParseEvents(strings.NewReader(`{"x": 1.5, "y": 1e3}`), "e.json")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := evs[0]["x"].(float64); !ok || v != 1.5 {
		t.Fatalf("x = %#v", evs[0]["x"])
	}
	if _, ok := evs[0]["y"].(float64); !ok {
		t.Fatalf("y = %#v, want float64", evs[0]["y"])
	}
}

func TestParseEventsNamesLineAndColumn(t *testing.T) {
	_, err := ParseEvents(strings.NewReader("{\"a\":1}\n{\"b\":2}\n{\"c\"}\n"), "events.jsonl")
	if err == nil || !strings.HasPrefix(err.Error(), "events.jsonl:3:5:") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseEventsNamesANonObject(t *testing.T) {
	_, err := ParseEvents(strings.NewReader(`["x"]`), "e.json")
	if err == nil || err.Error() != "e.json:1:2: event 1 is not a JSON object" {
		t.Fatalf("err = %v", err)
	}
}

func TestDecodeEventNamesWhatItGot(t *testing.T) {
	for in, want := range map[string]string{
		`[1]`:   "an event must be a JSON object, not an array",
		`"x"`:   "an event must be a JSON object, not a string",
		`42`:    "an event must be a JSON object, not a number",
		`true`:  "an event must be a JSON object, not true or false",
		`false`: "an event must be a JSON object, not true or false",
		`null`:  "an event must be a JSON object, not null",
	} {
		_, err := DecodeEvent([]byte(in))
		if err == nil || err.Error() != want {
			t.Errorf("%s: err = %v, want %q", in, err, want)
		}
	}
}

func TestDecodeEventRefusesASecondValue(t *testing.T) {
	_, err := DecodeEvent([]byte("{\"a\":1}\n  {\"b\":2}"))
	if err == nil || err.Error() != "line 2, column 3: paste one event; this is a second one" {
		t.Fatalf("err = %v", err)
	}
}

func TestDecodeEventReportsLineAndColumn(t *testing.T) {
	_, err := DecodeEvent([]byte("{\n  \"a\" 1\n}"))
	if err == nil || !strings.HasPrefix(err.Error(), "line 2, column 7:") {
		t.Fatalf("err = %v", err)
	}
	ev, err := DecodeEvent([]byte(`{"id": 9007199254740993}`))
	if err != nil || ev["id"] != 9007199254740993 {
		t.Fatalf("ev = %#v err = %v", ev, err)
	}
}

func TestDecodeEventEmptyIsErrNoEvent(t *testing.T) {
	for _, in := range []string{"", "  \n\t "} {
		if _, err := DecodeEvent([]byte(in)); !errors.Is(err, ErrNoEvent) {
			t.Errorf("%q: err = %v", in, err)
		}
	}
}

func TestParseEventsNamesTheLineOfATruncatedEvent(t *testing.T) {
	_, err := ParseEvents(strings.NewReader("{\"a\":1}\n\n{\"b\":\n"), "s")
	if err == nil || !strings.HasPrefix(err.Error(), "s:3:") {
		t.Fatalf("err = %v", err)
	}
}

func TestDecodeEventNamesTheLineATruncatedPasteStopsOn(t *testing.T) {
	_, err := DecodeEvent([]byte("{\n\"a\":\n"))
	if err == nil || !strings.HasPrefix(err.Error(), "line 2, column 4:") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseEventsRefusesAnOutOfRangeNumber(t *testing.T) {
	for in, want := range map[string]string{
		"{\"a\":1}\n{\"b\": {\"n\": 1e999}}": "e.jsonl:2:13: number 1e999 is out of range",
		"[{\"a\":1}, {\"n\": [1, -1e999]}]":  "e.jsonl:1:21: number -1e999 is out of range",
		// The first in the file, whatever order a map holds them in.
		"{\"z\": 1e999, \"a\": 2e999}": "e.jsonl:1:7: number 1e999 is out of range",
	} {
		_, err := ParseEvents(strings.NewReader(in), "e.jsonl")
		if err == nil || err.Error() != want {
			t.Errorf("%q: err = %v, want %q", in, err, want)
		}
	}
	_, err := DecodeEvent([]byte("{\n  \"n\": 1E400}"))
	if want := "line 2, column 8: number 1E400 is out of range"; err == nil || err.Error() != want {
		t.Errorf("DecodeEvent: err = %v, want %q", err, want)
	}
	// A number too small to hold is zero, as a float64 rounds it.
	evs, err := ParseEvents(strings.NewReader(`{"n": 1e-999}`), "e.jsonl")
	if err != nil || evs[0]["n"] != 0.0 {
		t.Fatalf("1e-999: evs = %v, err = %v", evs, err)
	}
}
