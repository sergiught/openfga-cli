package mapping

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/modeltest"
)

// modelFlags selects the authorization model a mapping is checked against.
type modelFlags struct {
	file      string
	fromStore bool
}

func (f *modelFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.file, "model-file", "", "check against an authorization model file (.fga or .json)")
	cmd.Flags().BoolVar(&f.fromStore, "model-from-store", false, "check against the active profile's store model (--model-id, or the latest)")
}

// load returns nil, "" when no model was asked for.
func (c *Command) loadModelIndex(ctx context.Context, f modelFlags) (*mapping.ModelIndex, string, error) {
	switch {
	case f.file != "" && f.fromStore:
		return nil, "", clierr.WithCode(clierr.CodeUsage, errors.New("--model-file and --model-from-store are mutually exclusive"))
	case strings.HasSuffix(f.file, ".mod"):
		return nil, "", errors.New("modular models (fga.mod) are not supported yet; pass the model's .fga or .json file")
	case f.file != "":
		raw, err := os.ReadFile(f.file)
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", fmt.Errorf("model file %s not found", f.file)
		}
		if err != nil {
			return nil, "", err
		}
		loaded, err := modeltest.LoadModelBytes(raw)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", f.file, err)
		}
		return mapping.IndexModel(loaded.SDK), f.file, nil
	case f.fromStore:
		model, err := c.storeModel(ctx)
		if err != nil {
			return nil, "", err
		}
		return mapping.IndexModel(model), "store model " + model.ID, nil
	}
	return nil, "", nil
}

// storeModel reads the configured store's model: --model-id or the profile's
// model when set, else the latest.
func (c *Command) storeModel(ctx context.Context) (*openfga.AuthorizationModel, error) {
	cl, r, err := c.cli.ClientWithStore()
	if err != nil {
		return nil, err
	}
	if r.ModelID != "" {
		return cl.AuthorizationModels.Get(ctx, r.ModelID)
	}
	return cl.AuthorizationModels.ReadLatest(ctx)
}
