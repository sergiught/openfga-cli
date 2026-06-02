// Package client constructs a configured go-openfga client from resolved config.
package client

import (
	"fmt"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/config"
)

// New builds an *openfga.Client from a resolved configuration. The store and
// authorization-model IDs are registered as client defaults so per-call
// overrides remain optional.
func New(r config.Resolved) (*openfga.Client, error) {
	if r.APIURL == "" {
		return nil, fmt.Errorf("no API URL configured: set one with --api-url, OPENFGA_API_URL, or `ofga context set`")
	}

	opts := []openfga.Option{
		openfga.WithUserAgent("ofga-cli"),
	}
	if r.StoreID != "" {
		opts = append(opts, openfga.WithStoreID(r.StoreID))
	}
	if r.ModelID != "" {
		opts = append(opts, openfga.WithAuthorizationModelID(r.ModelID))
	}
	if r.APIToken != "" {
		opts = append(opts, openfga.WithAPIToken(r.APIToken))
	}

	c, err := openfga.NewClient(r.APIURL, opts...)
	if err != nil {
		return nil, fmt.Errorf("create openfga client: %w", err)
	}
	return c, nil
}
