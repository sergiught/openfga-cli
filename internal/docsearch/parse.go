package docsearch

import (
	"strings"

	"github.com/sergiught/openfga-cli/internal/clierr"
)

const errFormat = "unexpected response from the openfga.dev docs search; the format may have changed"

// parseResult turns one text content item
// ("Title: …\nLink: …\nPage: …\nContent: …") into a Result.
func parseResult(text string) (Result, bool) {
	var r Result
	var hasTitle, hasLink bool
	rest := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
	for rest != "" {
		line, tail, _ := strings.Cut(rest, "\n")
		switch {
		case strings.HasPrefix(line, "Title:"):
			r.Title, hasTitle = strings.TrimSpace(strings.TrimPrefix(line, "Title:")), true
		case strings.HasPrefix(line, "Link:"):
			r.URL, hasLink = strings.TrimSpace(strings.TrimPrefix(line, "Link:")), true
		case strings.HasPrefix(line, "Page:"):
			r.Page = strings.TrimSpace(strings.TrimPrefix(line, "Page:"))
		case strings.HasPrefix(line, "Content:"):
			// Content runs to the end of the item.
			content := strings.TrimPrefix(line, "Content:")
			if tail != "" {
				content += "\n" + tail
			}
			r.Snippet = strings.TrimSpace(content)
			tail = ""
		}
		rest = tail
	}
	if !hasTitle || !hasLink || r.URL == "" {
		return Result{}, false
	}
	// The link may carry a #fragment or ?query; the markdown lives at the
	// canonical page URL. A link outside the docs has no markdown rendering.
	if canonical, err := PageURL(r.URL); err == nil {
		r.MarkdownURL = canonical + ".md"
	}
	return r, true
}

func parseResults(texts []string) ([]Result, error) {
	out := make([]Result, 0, len(texts))
	for _, t := range texts {
		r, ok := parseResult(t)
		if !ok {
			return nil, clierr.WithKind(clierr.KindError, errorString(errFormat))
		}
		out = append(out, r)
	}
	return out, nil
}

type errorString string

func (e errorString) Error() string { return string(e) }
