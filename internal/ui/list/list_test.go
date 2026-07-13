package list

import (
	"strings"
	"testing"
)

func TestSetCompactHidesDescriptionsButKeepsTitles(t *testing.T) {
	l := New()
	l.SetItems([]Item{
		{TitleText: "alpha", DescText: "first"},
		{TitleText: "beta", DescText: "second"},
	})
	l.SetSize(40, 10)

	normal := l.View()
	if !strings.Contains(normal, "first") || !strings.Contains(normal, "second") {
		t.Fatalf("normal view should show descriptions, got:\n%s", normal)
	}

	l.SetCompact(true)
	compact := l.View()
	if strings.Contains(compact, "first") || strings.Contains(compact, "second") {
		t.Fatalf("compact view should hide descriptions, got:\n%s", compact)
	}
	if !strings.Contains(compact, "alpha") || !strings.Contains(compact, "beta") {
		t.Fatalf("compact view should still show titles, got:\n%s", compact)
	}

	// Rows must actually collapse to one line each, not just have their
	// description text hidden while still occupying a blank second line.
	lines := strings.Split(compact, "\n")
	alphaLine := -1
	for i, ln := range lines {
		if strings.Contains(ln, "alpha") {
			alphaLine = i
			break
		}
	}
	if alphaLine == -1 || alphaLine+1 >= len(lines) || !strings.Contains(lines[alphaLine+1], "beta") {
		t.Fatalf("compact view should render beta on the line immediately after alpha with no blank line between rows, got:\n%s", compact)
	}

	l.SetCompact(false)
	restored := l.View()
	if !strings.Contains(restored, "first") {
		t.Fatalf("toggling compact off should restore descriptions, got:\n%s", restored)
	}
}
