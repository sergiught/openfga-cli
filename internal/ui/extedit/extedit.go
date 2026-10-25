// Package extedit resolves the user's terminal editor and builds the command
// that opens a file in it, so every TUI that hands a file to $EDITOR picks the
// same editor and parses the variable the same way.
package extedit

import (
	"os"
	"os/exec"
	"strings"
)

// splitCommandLine splits a command string into words, honoring single and
// double quotes and backslash escapes (shell-style), so a $EDITOR whose binary
// path contains spaces still parses correctly when quoted, e.g.
// `"/Applications/Sublime Text.app/Contents/SharedSupport/bin/subl" -w`.
// Unquoted whitespace still separates words, matching shell semantics.
func splitCommandLine(s string) []string {
	var (
		words   []string
		cur     strings.Builder
		inWord  bool
		quote   rune // 0, '\'' or '"'
		escaped bool
	)
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			inWord = true
			escaped = false
		case r == '\\' && quote != '\'':
			escaped = true
			inWord = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	flush()
	return words
}

// Command resolves the user's terminal editor for opening a file:
// $VISUAL, then $EDITOR, then the first of vim/vi/nano found on PATH. It
// returns the resolved binary and any leading arguments (the file path is
// appended by the caller). The binary is "" when nothing suitable is found, so
// the caller can surface a status hint rather than launching an empty command.
func Command() (string, []string) {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			fields := splitCommandLine(v)
			if len(fields) == 0 {
				continue
			}
			return fields[0], fields[1:]
		}
	}
	for _, cand := range []string{"vim", "vi", "nano"} {
		if _, err := exec.LookPath(cand); err == nil {
			return cand, nil
		}
	}
	return "", nil
}
