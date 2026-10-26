// Package docsearch is a client for the openfga.dev documentation: the
// Mintlify MCP search endpoint and the markdown rendering of each docs page.
package docsearch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/sergiught/openfga-cli/internal/clierr"
)

// DefaultBaseURL is the docs site the client talks to.
const DefaultBaseURL = "https://openfga.dev"

const (
	defaultTimeout = 30 * time.Second
	maxBody        = 8 << 20 // 8 MiB
	docsHost       = "openfga.dev"
)

// Client searches and fetches the OpenFGA docs.
type Client struct {
	baseURL string
	http    *http.Client
}

// Result is one docs search hit.
type Result struct {
	Title       string `json:"title" yaml:"title"`
	URL         string `json:"url" yaml:"url"`
	MarkdownURL string `json:"markdown_url" yaml:"markdown_url"` // URL + ".md"
	Page        string `json:"page" yaml:"page"`
	Snippet     string `json:"snippet" yaml:"snippet"`
}

// Page is a docs page rendered as markdown.
type Page struct {
	URL      string `json:"url" yaml:"url"`
	Markdown string `json:"markdown" yaml:"markdown"`
}

// New returns a Client for openfga.dev. A zero timeout means 30s.
func New(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return newWithBaseURL(DefaultBaseURL, &http.Client{Timeout: timeout})
}

// NewWithBaseURL returns a Client for a different base URL. It exists so
// other packages' tests can point a Client at an httptest server; production
// code uses New.
func NewWithBaseURL(base string, hc *http.Client) *Client { return newWithBaseURL(base, hc) }

func newWithBaseURL(base string, hc *http.Client) *Client {
	base = strings.TrimRight(base, "/")
	// Never follow a redirect off the base host: it could carry the MCP
	// session id (or the request) to another site.
	bu, _ := url.Parse(base)
	cp := *hc
	cp.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if bu == nil || req.URL.Scheme != bu.Scheme || req.URL.Host != bu.Host {
			return fmt.Errorf("refusing redirect to %s", req.URL.Redacted())
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &Client{baseURL: base, http: &cp}
}

// unreachable reports a transport failure. It deliberately has no Unwrap so
// clierr.Friendly does not attach the OpenFGA-server URL hint.
type unreachable struct{ cause error }

func (u *unreachable) Error() string { return "cannot reach openfga.dev: " + u.cause.Error() }

// FriendlyAsIs tells clierr.Friendly to print the message without a hint.
func (u *unreachable) FriendlyAsIs() {}

func networkErr(cause error) error {
	return clierr.WithKind(clierr.KindNetwork, clierr.WithCode(clierr.CodeNetwork, &unreachable{cause}))
}

func statusErr(status string, code int) error {
	err := fmt.Errorf("openfga.dev returned %s", status)
	if code >= 500 {
		return clierr.WithKind(clierr.KindServer, err)
	}
	return clierr.WithKind(clierr.KindError, err)
}

// readBody reads at most maxBody bytes and fails if the body is larger.
func readBody(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxBody {
		return nil, errors.New("response larger than 8 MiB")
	}
	return b, nil
}

// PageURL canonicalizes ref to https://openfga.dev/docs/<path>. It accepts a
// bare docs path, a /docs/ path, or a full openfga.dev URL, and drops any .md
// suffix, fragment and query.
func PageURL(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("empty docs page reference")
	}
	u, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("invalid docs page reference %q: %w", ref, err)
	}
	p := u.Path
	switch {
	case u.Scheme != "" || u.Host != "" || u.User != nil || strings.HasPrefix(ref, "//"):
		if u.Scheme != "https" {
			return "", fmt.Errorf("docs page must be an https URL on %s, got %q", docsHost, ref)
		}
		if u.User != nil {
			return "", fmt.Errorf("docs page URL must not carry credentials: %q", ref)
		}
		if !strings.EqualFold(u.Hostname(), docsHost) || u.Port() != "" {
			return "", fmt.Errorf("docs page must be on %s, got %q", docsHost, ref)
		}
	default:
		p = "/" + strings.TrimPrefix(p, "/")
		if !strings.HasPrefix(p, "/docs/") && p != "/docs" {
			p = "/docs" + p
		}
	}
	if !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("invalid docs page path %q", ref)
	}
	// A decoded ? or # (or a backslash) would change meaning once the path is
	// put back into a request URL, so refuse them rather than guess.
	if strings.ContainsAny(p, "?#\\") {
		return "", fmt.Errorf("docs page path must not contain '?', '#' or '\\': %q", ref)
	}
	p = path.Clean(p)
	p = strings.TrimSuffix(p, ".md")
	if !strings.HasPrefix(p, "/docs/") || len(p) == len("/docs/") {
		return "", fmt.Errorf("docs page must be under https://%s/docs/, got %q", docsHost, ref)
	}
	return "https://" + docsHost + p, nil
}

// Page fetches the markdown rendering of a docs page, verbatim.
func (c *Client) Page(ctx context.Context, ref string) (Page, error) {
	canonical, err := PageURL(ref)
	if err != nil {
		return Page{}, clierr.WithKind(clierr.KindUsage, err)
	}
	// Re-derive the fetch URL from the validated path so only /docs/... is ever requested.
	cu, _ := url.Parse(canonical)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+cu.Path+".md", nil)
	if err != nil {
		return Page{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Page{}, networkErr(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Page{}, statusErr(resp.Status, resp.StatusCode)
	}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mt != "text/markdown" && mt != "text/plain" {
		return Page{}, clierr.WithKind(clierr.KindError,
			fmt.Errorf("openfga.dev returned %q instead of markdown for %s", mt, canonical))
	}
	b, err := readBody(resp.Body)
	if err != nil {
		return Page{}, networkErr(err)
	}
	return Page{URL: canonical, Markdown: string(b)}, nil
}
