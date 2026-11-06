package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/oklog/ulid/v2"
	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/optimize"
	"github.com/sergiught/openfga-cli/internal/readlimit"
)

// diffSide is one resolved, validated side of `model diff`.
type diffSide struct {
	Label string // "model.fga", "stdin", "store latest <id>", "store model <id>"
	File  string // set only for file sides (the path as given)
	Model *optimize.Model
}

// resolveDiffArgs maps one or two arguments to the (old, new) pair: a single
// argument is compared against the store's latest model.
func resolveDiffArgs(args []string) (old, new string, err error) {
	if len(args) == 1 {
		return "store", args[0], nil
	}
	if args[0] == "-" && args[1] == "-" {
		return "", "", clierr.WithCode(clierr.CodeUsage, errors.New("only one side can be read from stdin"))
	}
	return args[0], args[1], nil
}

// loadDiffSide resolves one argument (stdin, file, "store" or a model ID) into
// a validated model. side is "old" or "new" and prefixes every error.
func (c *Command) loadDiffSide(ctx context.Context, stdin io.Reader, side, arg string) (diffSide, error) {
	s, err := c.resolveDiffSide(ctx, stdin, arg)
	if err != nil {
		return diffSide{}, fmt.Errorf("%s model: %w", side, err)
	}
	return s, nil
}

func (c *Command) resolveDiffSide(ctx context.Context, stdin io.Reader, arg string) (diffSide, error) {
	if arg == "-" {
		data, err := readlimit.All(stdin, readlimit.Document, "model from stdin")
		if err != nil {
			return diffSide{}, err
		}
		if len(bytes.TrimSpace(data)) == 0 {
			return diffSide{}, usageHint("pipe a model in, or pass a model file", errors.New("model from stdin is empty"))
		}
		return diffModelFromBytes("stdin", "", data)
	}
	if err := modeltest.RejectModular(arg); err != nil {
		return diffSide{}, clierr.WithCode(clierr.CodeUsage, err)
	}
	if fi, err := os.Stat(arg); err == nil && !fi.IsDir() {
		data, err := readlimit.File(arg, readlimit.Document, "model file")
		if err != nil {
			return diffSide{}, err
		}
		if len(bytes.TrimSpace(data)) == 0 {
			return diffSide{}, usageHint("pass a non-empty .fga or .json model file", fmt.Errorf("model file is empty: %s", arg))
		}
		return diffModelFromBytes(arg, arg, data)
	}
	switch {
	case arg == "store":
		m, err := c.latestStoreModel(ctx)
		if err != nil {
			return diffSide{}, err
		}
		return diffModelFromSDK("store latest "+m.ID, m)
	case isModelID(arg):
		cl, _, err := c.cli.ClientWithStore()
		if err != nil {
			return diffSide{}, err
		}
		m, err := cl.AuthorizationModels.Get(ctx, arg)
		if err != nil {
			return diffSide{}, err
		}
		return diffModelFromSDK("store model "+m.ID, m)
	}
	return diffSide{}, clierr.WithCode(clierr.CodeUsage, fmt.Errorf(`%s: not a file, "store", or a model ID`, arg))
}

// latestStoreModel reads the store's latest model, ignoring any pinned model
// ID (pass the ID to diff against that model).
func (c *Command) latestStoreModel(ctx context.Context) (*openfga.AuthorizationModel, error) {
	cl, r, err := c.cli.ClientWithStore()
	if err != nil {
		return nil, err
	}
	page, err := cl.AuthorizationModels.List(ctx, &openfga.ListModelsOptions{PageSize: 1})
	if err != nil {
		return nil, err
	}
	if len(page.AuthorizationModels) == 0 {
		return nil, fmt.Errorf("store %s has no authorization model", r.StoreID)
	}
	return &page.AuthorizationModels[0], nil
}

func isModelID(s string) bool {
	_, err := ulid.ParseStrict(s)
	return err == nil
}

func diffModelFromSDK(label string, m *openfga.AuthorizationModel) (diffSide, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return diffSide{}, err
	}
	return diffModelFromBytes(label, "", data)
}

func diffModelFromBytes(label, file string, data []byte) (diffSide, error) {
	lm, err := modeltest.LoadModelBytes(data)
	if err != nil {
		return diffSide{}, clierr.WithCode(clierr.CodeUsage, err)
	}
	m, err := optimize.NewModel(lm.Proto)
	if err != nil {
		if errors.Is(err, optimize.ErrInvalidModel) {
			return diffSide{}, clierr.WithCode(clierr.CodeUsage, err)
		}
		return diffSide{}, err
	}
	return diffSide{Label: label, File: file, Model: m}, nil
}
