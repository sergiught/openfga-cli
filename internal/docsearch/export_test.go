package docsearch

import (
	"net/http"
	"time"
)

// NewForTest points a Client at a test server.
func NewForTest(base string, hc *http.Client) *Client { return newWithBaseURL(base, hc) }

// HTTPTimeout is the timeout of the Client's HTTP client.
func (c *Client) HTTPTimeout() time.Duration { return c.http.Timeout }
