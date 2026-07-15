// Package api implements `ofga api`: send a raw request to the OpenFGA API
// using the active profile's connection and authentication.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/output"
)

// Command is the `api` command.
type Command struct {
	cli *cli.CLI
	cmd *cobra.Command
}

// New builds the api command.
func New(cli *cli.CLI) *Command {
	c := &Command{cli: cli}
	c.cmd = &cobra.Command{
		Use:   "api <method> <path> [body]",
		Short: "Send a raw request to the OpenFGA API (uses the active profile's auth)",
		Long: "Send an arbitrary request to the OpenFGA API, reusing the active profile's " +
			"URL and authentication (token, client-credentials or private-key JWT).\n\n" +
			"The path is relative to the profile's API URL (e.g. /stores). A JSON body may " +
			"be passed as the third argument, or read from stdin by passing `-` as the body.",
		Example: "  ofga api GET /stores\n" +
			"  ofga api GET /stores/<id>/authorization-models\n" +
			`  ofga api POST /stores/<id>/check '{"tuple_key":{"user":"user:anne","relation":"viewer","object":"document:roadmap"}}'` + "\n" +
			`  echo '{"name":"demo"}' | ofga api POST /stores -`,
		Args: cobra.RangeArgs(2, 3),
		RunE: c.run,
	}
	return c
}

// Command returns the cobra command.
func (c *Command) Command() *cobra.Command { return c.cmd }

func (c *Command) run(cmd *cobra.Command, args []string) error {
	method := strings.ToUpper(args[0])
	path := args[1]

	body, err := requestBody(args, cmd.InOrStdin())
	if err != nil {
		return err
	}

	cl, err := c.cli.Client()
	if err != nil {
		return err
	}
	req, err := cl.NewRequest(cmd.Context(), method, path, body)
	if err != nil {
		return err
	}
	resp, err := cl.BareDo(req)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		// Non-2xx: the error carries the method/URL, status and API code+message.
		// Under --json, also emit the server error as a JSON object on stdout so
		// scripts can capture it (curl-like), in addition to the non-zero exit.
		if c.cli.JSON || c.cli.YAML {
			var er *openfga.ErrorResponse
			if errors.As(err, &er) {
				_ = output.Emit(cmd.OutOrStdout(), c.cli.YAML, map[string]string{"code": er.Code, "message": er.Message})
			}
		}
		return err
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return c.write(cmd.OutOrStdout(), data)
}

// requestBody resolves the JSON request body from the third argument, returning
// nil when there is none. A body argument of "-" reads the body from stdin
// (matching the tool's own `--file -` convention); stdin is never consumed
// otherwise, so non-body methods (GET/DELETE/HEAD) don't block on an open pipe.
func requestBody(args []string, stdin io.Reader) (any, error) {
	var raw []byte
	if len(args) == 3 {
		if args[2] == "-" {
			b, err := io.ReadAll(stdin)
			if err != nil {
				return nil, err
			}
			raw = b
		} else {
			raw = []byte(args[2])
		}
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("request body is not valid JSON")
	}
	return json.RawMessage(raw), nil
}

// write emits the response body: raw under --json (for piping), converted to
// YAML under --output yaml, pretty-printed JSON otherwise.
func (c *Command) write(w io.Writer, data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if c.cli.YAML {
		var generic any
		if err := json.Unmarshal(data, &generic); err != nil {
			// Not valid JSON (or empty/non-JSON body) — fall through and write
			// the raw bytes rather than failing the whole command over a
			// best-effort format conversion.
			_, err := w.Write(data)
			return err
		}
		return output.YAML(w, generic)
	}
	if !c.cli.JSON {
		var buf bytes.Buffer
		if json.Indent(&buf, data, "", "  ") == nil {
			data = buf.Bytes()
		}
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		_, err := io.WriteString(w, "\n")
		return err
	}
	return nil
}
