package readlimit

import (
	"strings"
	"testing"
)

func TestAllRejectsOversizedInput(t *testing.T) {
	if _, err := All(strings.NewReader("12345"), 4, "payload"); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("All() error = %v, want size limit", err)
	}
}

func TestAllAcceptsLimitExactly(t *testing.T) {
	got, err := All(strings.NewReader("1234"), 4, "payload")
	if err != nil || string(got) != "1234" {
		t.Fatalf("All() = %q, %v", got, err)
	}
}
