package mapping

import (
	"bytes"
	"strings"
	"testing"
)

func TestSnapshotHeaderCountsRateLimitWaits(t *testing.T) {
	res := fakeResult()
	var buf bytes.Buffer
	snapshotHeader(&buf, "Fake", res)
	if want := "Fake read  14:02:13 → 14:05:40 UTC  tenant.example\n"; !strings.HasPrefix(buf.String(), want) {
		t.Errorf("header = %q", buf.String())
	}

	for n, want := range map[int]string{1: "tenant.example  1 rate-limit wait\n", 3: "tenant.example  3 rate-limit waits\n"} {
		res.RateLimited = n
		buf.Reset()
		snapshotHeader(&buf, "Fake", res)
		if !strings.Contains(buf.String(), want) {
			t.Errorf("n = %d: header = %q, want it to contain %q", n, buf.String(), want)
		}
	}
}
