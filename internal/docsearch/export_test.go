package docsearch

import "net/http"

// NewForTest points a Client at a test server.
func NewForTest(base string, hc *http.Client) *Client { return newWithBaseURL(base, hc) }
