package agentskill

import (
	"bytes"
	"strings"
	"testing"
)

func TestContentIsAFrontmatteredSkill(t *testing.T) {
	c := Content()
	if !bytes.HasPrefix(c, []byte("---\nname: ofga\ndescription: ")) {
		t.Fatalf("SKILL.md must open with name/description frontmatter, got:\n%.80s", c)
	}
	if strings.Contains(string(c), "OFGA_AGENT_MODE") {
		t.Error("SKILL.md mentions a non-existent OFGA_AGENT_MODE alias")
	}
}

func TestContentReturnsACopy(t *testing.T) {
	a := Content()
	a[0] = 'X'
	if Content()[0] == 'X' {
		t.Error("Content must return a copy")
	}
}
