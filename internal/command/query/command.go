// Package query implements `ofga query`: the read-side authorization
// questions — check, batch-check, expand, list-objects and list-users.
package query

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/output"
	"github.com/sergiught/openfga-cli/internal/style"
)

// Command is the `query` command group.
type Command struct {
	cli *cli.CLI
	cmd *cobra.Command
}

// New builds the query command group.
func New(cli *cli.CLI) *Command {
	c := &Command{cli: cli}
	c.cmd = &cobra.Command{
		Use:     "query",
		Aliases: []string{"q"},
		RunE:    cli.GroupRunE,
		Short:   "Ask authorization questions",
		Long: "Ask authorization questions.\n\n" +
			"Positional argument order mirrors the OpenFGA API for each call, so it differs " +
			"between subcommands:\n" +
			"  check        <user> <relation> <object>   (user first)\n" +
			"  list-objects <type> <relation> <user>     (user last)\n" +
			"  list-users   <object> <relation>          (object first, --type for the user filter)\n" +
			"  expand       <relation> <object>\n" +
			"Use the named flags (--user/--relation/--object) where available if the order is easy to mix up.",
	}
	c.RegisterSubCommands()
	return c
}

// Command returns the cobra command.
func (c *Command) Command() *cobra.Command { return c.cmd }

// RegisterSubCommands wires the query sub-commands.
func (c *Command) RegisterSubCommands() {
	c.cmd.AddCommand(
		c.checkCmd(),
		c.batchCheckCmd(),
		c.expandCmd(),
		c.listObjectsCmd(),
		c.listUsersCmd(),
	)
}

// parseContext parses a JSON object string into a map, or nil if empty.
func parseContext(s string) (map[string]any, error) {
	return fga.ParseJSONObject("--context", s)
}

func resolveArgs(args, flags, names []string) ([]string, error) {
	values := append([]string(nil), flags...)
	rest := args
	for i := range values {
		if values[i] == "" && len(rest) > 0 {
			values[i], rest = rest[0], rest[1:]
		}
	}
	if len(rest) > 0 {
		return nil, fmt.Errorf("too many arguments: %v", rest)
	}
	var missing []string
	for i, value := range values {
		if value == "" {
			missing = append(missing, "--"+names[i])
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("provide %s as arguments or named flags", strings.Join(missing, ", "))
	}
	return values, nil
}

// parseContextualTuples parses repeated "user,relation,object" values. Each
// triple is validated through fga.ParseTuple, the same check the TUI applies,
// so malformed contextual tuples are rejected consistently.
func parseContextualTuples(vals []string) (*openfga.ContextualTupleKeys, error) {
	if len(vals) == 0 {
		return nil, nil
	}
	keys := make([]openfga.TupleKey, 0, len(vals))
	for _, v := range vals {
		parts := strings.Split(v, ",")
		if len(parts) != 3 {
			return nil, fmt.Errorf("contextual tuple %q must be user,relation,object", v)
		}
		key, err := fga.ParseTuple(parts[0], parts[1], parts[2])
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return &openfga.ContextualTupleKeys{TupleKeys: keys}, nil
}

func (c *Command) checkCmd() *cobra.Command {
	var (
		contextJSON       string
		ctxTuples         []string
		fUser, fRel, fObj string
	)
	cmd := &cobra.Command{
		Use:   "check [user] [relation] [object]",
		Short: "Check whether a user has a relation on an object",
		Example: `  ofga query check user:anne viewer document:roadmap
  ofga query check --user user:anne --relation viewer --object document:roadmap`,
		Args: cobra.MaximumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			user, relation, object, err := fga.Triple(args, fUser, fRel, fObj)
			if err != nil {
				// Missing/incomplete arguments are a usage error (exit 2).
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			// Validate the triple locally (same check as `tuples write`) so a
			// swapped/malformed argument gives a friendly hint instead of a raw
			// server 400.
			if _, err := fga.ParseTuple(user, relation, object); err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			cx, err := parseContext(contextJSON)
			if err != nil {
				return err
			}
			ct, err := parseContextualTuples(ctxTuples)
			if err != nil {
				return err
			}
			req := &openfga.CheckRequest{
				TupleKey:         openfga.CheckRequestTupleKey{User: user, Relation: relation, Object: object},
				Context:          cx,
				ContextualTuples: ct,
			}
			res, err := cl.Relationships.Check(cmd.Context(), req)
			if err != nil {
				return err
			}
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, res)
			}
			if output.Plain {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), allowedWord(res.Allowed))
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n",
				style.Allowed(res.Allowed),
				style.Faint.Render(fmt.Sprintf("%s %s %s", user, relation, object)))
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&fUser, "user", "", "user (alternative to the positional arg)")
	f.StringVar(&fRel, "relation", "", "relation (alternative to the positional arg)")
	f.StringVar(&fObj, "object", "", "object (alternative to the positional arg)")
	f.StringVar(&contextJSON, "context", "", "JSON object of condition context")
	f.StringArrayVar(&ctxTuples, "contextual-tuple", nil, "contextual tuple as user,relation,object (repeatable)")
	return cmd
}

func (c *Command) batchCheckCmd() *cobra.Command {
	var checks []string
	cmd := &cobra.Command{
		Use:     "batch-check --check user,relation,object [...]",
		Short:   "Run several checks in one request",
		Example: "  ofga query batch-check --check user:anne,viewer,doc:1 --check user:bob,editor,doc:1",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(checks) == 0 {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("provide at least one --check user,relation,object"))
			}
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			items := make([]openfga.BatchCheckItem, 0, len(checks))
			labels := make([]string, 0, len(checks))
			for i, raw := range checks {
				parts := strings.Split(raw, ",")
				if len(parts) != 3 {
					return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--check %q must be user,relation,object", raw))
				}
				id := fmt.Sprintf("c%d", i)
				items = append(items, openfga.BatchCheckItem{
					TupleKey:      openfga.CheckRequestTupleKey{User: strings.TrimSpace(parts[0]), Relation: strings.TrimSpace(parts[1]), Object: strings.TrimSpace(parts[2])},
					CorrelationID: id,
				})
				labels = append(labels, fmt.Sprintf("%s %s %s", strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])))
			}
			res, err := cl.Relationships.BatchCheck(cmd.Context(), &openfga.BatchCheckRequest{Checks: items})
			if err != nil {
				return err
			}
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, res)
			}
			for i, item := range items {
				r := res.Result[item.CorrelationID]
				if output.Plain {
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", allowedWord(r.Allowed), labels[i]); err != nil {
						return err
					}
				} else {
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n", style.Allowed(r.Allowed), style.Faint.Render(labels[i])); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&checks, "check", nil, "a check as user,relation,object (repeatable)")
	_ = cmd.MarkFlagRequired("check")
	return cmd
}

func (c *Command) expandCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "expand <relation> <object>",
		Short:   "Expand the userset tree that grants a relation (JSON)",
		Example: "  ofga query expand viewer document:roadmap",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			req := &openfga.ExpandRequest{TupleKey: openfga.CheckRequestTupleKey{Relation: args[0], Object: args[1]}}
			res, err := cl.Relationships.Expand(cmd.Context(), req)
			if err != nil {
				return err
			}
			// --plain renders the userset tree as an indented text outline;
			// otherwise (default, --json, and --output yaml) it is emitted
			// structured via output.Emit.
			if output.Plain && !c.cli.JSON && !c.cli.YAML {
				return writeTreePlain(cmd.OutOrStdout(), res.Tree, 0)
			}
			return output.Emit(cmd.OutOrStdout(), c.cli.YAML, res.Tree)
		},
	}
}

func (c *Command) listObjectsCmd() *cobra.Command {
	var (
		contextJSON           string
		objectType, rel, user string
	)
	cmd := &cobra.Command{
		Use:     "list-objects [type] [relation] [user]",
		Aliases: []string{"objects"},
		Short:   "List objects of a type a user has a relation with",
		Example: "  ofga query list-objects document viewer user:anne\n" +
			"  ofga query list-objects --type document --relation viewer --user user:anne",
		Args: cobra.MaximumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			values, err := resolveArgs(args,
				[]string{objectType, rel, user},
				[]string{"type", "relation", "user"})
			if err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			cx, err := parseContext(contextJSON)
			if err != nil {
				return err
			}
			req := &openfga.ListObjectsRequest{Type: values[0], Relation: values[1], User: values[2], Context: cx}
			res, err := cl.Relationships.ListObjects(cmd.Context(), req)
			if err != nil {
				return err
			}
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, res.Objects)
			}
			if len(res.Objects) == 0 {
				output.Infof(cmd.ErrOrStderr(), "no objects")
				return nil
			}
			for _, o := range res.Objects {
				safe := output.SanitizeField(o)
				if output.Plain {
					if _, err := fmt.Fprintln(cmd.OutOrStdout(), safe); err != nil {
						return err
					}
				} else {
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", style.Bullet(), safe); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&contextJSON, "context", "", "JSON object of condition context")
	cmd.Flags().StringVar(&objectType, "type", "", "object type")
	cmd.Flags().StringVar(&rel, "relation", "", "relation")
	cmd.Flags().StringVar(&user, "user", "", "user")
	return cmd
}

func (c *Command) listUsersCmd() *cobra.Command {
	var (
		userTypes   []string
		object, rel string
	)
	cmd := &cobra.Command{
		Use:     "list-users [object] [relation] --type <user-type>",
		Aliases: []string{"users"},
		Short:   "List users that have a relation on an object",
		Example: "  ofga query list-users document:roadmap viewer --type user\n" +
			"  ofga query list-users --object document:roadmap --relation viewer --type user",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			values, err := resolveArgs(args,
				[]string{object, rel},
				[]string{"object", "relation"})
			if err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			filters := make([]openfga.UserTypeFilter, 0, len(userTypes))
			for _, t := range userTypes {
				if i := strings.Index(t, "#"); i >= 0 {
					filters = append(filters, openfga.UserTypeFilter{Type: t[:i], Relation: t[i+1:]})
				} else {
					filters = append(filters, openfga.UserTypeFilter{Type: t})
				}
			}
			req := &openfga.ListUsersRequest{
				Object:      openfga.FGAObjectRelation{Object: values[0]},
				Relation:    values[1],
				UserFilters: filters,
			}
			res, err := cl.Relationships.ListUsers(cmd.Context(), req)
			if err != nil {
				return err
			}
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, res.Users)
			}
			if len(res.Users) == 0 {
				output.Infof(cmd.ErrOrStderr(), "no users")
				return nil
			}
			for _, u := range res.Users {
				safe := output.SanitizeField(formatUser(u))
				if output.Plain {
					if _, err := fmt.Fprintln(cmd.OutOrStdout(), safe); err != nil {
						return err
					}
				} else {
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", style.Bullet(), safe); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&userTypes, "type", nil, "user type filter, optionally type#relation (repeatable)")
	cmd.Flags().StringVar(&object, "object", "", "object")
	cmd.Flags().StringVar(&rel, "relation", "", "relation")
	_ = cmd.MarkFlagRequired("type")
	return cmd
}

// writeTreePlain renders an untyped expand tree (map[string]any) as an indented
// text outline, so `expand --plain` produces a readable tree instead of JSON.
func writeTreePlain(w io.Writer, v any, indent int) error {
	pad := strings.Repeat("  ", indent)
	switch val := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			switch child := val[k].(type) {
			case map[string]any, []any:
				if _, err := fmt.Fprintf(w, "%s%s\n", pad, output.SanitizeField(k)); err != nil {
					return err
				}
				if err := writeTreePlain(w, child, indent+1); err != nil {
					return err
				}
			default:
				if _, err := fmt.Fprintf(w, "%s%s: %s\n", pad,
					output.SanitizeField(k), output.SanitizeField(fmt.Sprint(child))); err != nil {
					return err
				}
			}
		}
	case []any:
		for _, item := range val {
			if err := writeTreePlain(w, item, indent); err != nil {
				return err
			}
		}
	default:
		_, err := fmt.Fprintf(w, "%s%s\n", pad, output.SanitizeField(fmt.Sprint(val)))
		return err
	}
	return nil
}

func allowedWord(ok bool) string {
	if ok {
		return "allowed"
	}
	return "denied"
}

// formatUser renders one entry of a ListUsers response, describing a concrete
// object, a userset, or a type-bound wildcard.
func formatUser(u openfga.User) string {
	switch {
	case u.Object != nil:
		return fmt.Sprintf("%s:%s", u.Object.Type, u.Object.ID)
	case u.Userset != nil:
		return fmt.Sprintf("%s:%s#%s", u.Userset.Type, u.Userset.ID, u.Userset.Relation)
	case u.Wildcard != nil:
		return u.Wildcard.Type + ":*"
	default:
		b, _ := json.Marshal(u)
		return string(b)
	}
}
