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
			"  list-relations <user> <object>            (user first)\n" +
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
		c.listRelationsCmd(),
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
		Example: `  ofga query check --user user:anne --relation viewer --object document:roadmap
  ofga query check user:anne viewer document:roadmap`,
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
			cx, err := parseContext(contextJSON)
			if err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			ct, err := parseContextualTuples(ctxTuples)
			if err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			cl, _, err := c.cli.ClientWithStore()
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
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "allowed\t%t\n", res.Allowed)
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
		// A partial per-item failure is reported by an already-emitted table plus
		// a non-nil error for the exit code (see batchCheckErr); cobra's default
		// "Error: ..." + usage dump on a non-nil RunE error would duplicate that
		// and pollute --json/--plain stdout. Silence both here rather than
		// relying on the root command's settings, since this command is also
		// exercised standalone in tests.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(checks) == 0 {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("provide at least one --check user,relation,object"))
			}
			items := make([]openfga.BatchCheckItem, 0, len(checks))
			labels := make([]string, 0, len(checks))
			for i, raw := range checks {
				parts := strings.Split(raw, ",")
				if len(parts) != 3 {
					return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--check %q must be user,relation,object", raw))
				}
				key, err := fga.ParseTuple(
					strings.TrimSpace(parts[0]),
					strings.TrimSpace(parts[1]),
					strings.TrimSpace(parts[2]),
				)
				if err != nil {
					return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--check %q: %w", raw, err))
				}
				id := fmt.Sprintf("c%d", i)
				items = append(items, openfga.BatchCheckItem{
					TupleKey:      openfga.CheckRequestTupleKey{User: key.User, Relation: key.Relation, Object: key.Object},
					CorrelationID: id,
				})
				labels = append(labels, fmt.Sprintf("%s %s %s", key.User, key.Relation, key.Object))
			}
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			// BatchCheckAll chunks Checks into requests of at most the server's
			// 50-item /batch-check cap and merges the results; BatchCheck alone
			// would send everything as one request and 400 past 50 items.
			res, callErr := cl.Relationships.BatchCheckAll(cmd.Context(), &openfga.BatchCheckRequest{Checks: items})
			if res == nil {
				return callErr
			}
			// Results are keyed by correlation ID in a map, so this resolves each
			// item by its own ID rather than trusting map iteration order.
			outcomes := make([]batchOutcome, len(items))
			failed := 0
			for i, item := range items {
				outcomes[i] = resolveBatchOutcome(res, item.CorrelationID)
				if outcomes[i].isError {
					failed++
				}
			}
			if c.cli.JSON || c.cli.YAML {
				if err := output.Emit(cmd.OutOrStdout(), c.cli.YAML, res); err != nil {
					return err
				}
				return batchCheckErr(callErr, failed, len(items))
			}
			for i, o := range outcomes {
				if output.Plain {
					word := allowedWord(o.allowed)
					if o.isError {
						word = "error"
					}
					if err := writePlainBatchResult(cmd.OutOrStdout(), word, labels[i], o.detail); err != nil {
						return err
					}
				} else if err := writeBatchResult(cmd.OutOrStdout(), o, labels[i]); err != nil {
					return err
				}
			}
			return batchCheckErr(callErr, failed, len(items))
		},
	}
	cmd.Flags().StringArrayVar(&checks, "check", nil, "a check as user,relation,object (repeatable)")
	_ = cmd.MarkFlagRequired("check")
	return cmd
}

func (c *Command) expandCmd() *cobra.Command {
	var fRel, fObj string
	cmd := &cobra.Command{
		Use:   "expand [relation] [object]",
		Short: "Expand the userset tree that grants a relation (JSON)",
		Example: "  ofga query expand --relation viewer --object document:roadmap\n" +
			"  ofga query expand viewer document:roadmap",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			values, err := resolveArgs(args, []string{fRel, fObj}, []string{"relation", "object"})
			if err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			relation, object := values[0], values[1]
			if err := fga.ValidateObjectRef(object); err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			req := &openfga.ExpandRequest{TupleKey: openfga.CheckRequestTupleKey{Relation: relation, Object: object}}
			res, err := cl.Relationships.Expand(cmd.Context(), req)
			if err != nil {
				return err
			}
			// --plain and -o table both render the userset tree as an indented
			// text outline (expand has no tabular form); --json and -o yaml emit
			// the structured tree via output.Emit.
			if (output.Plain || c.cli.Output == "table") && !c.cli.JSON && !c.cli.YAML {
				return writeTreePlain(cmd.OutOrStdout(), res.Tree, 0)
			}
			return output.Emit(cmd.OutOrStdout(), c.cli.YAML, res.Tree)
		},
	}
	cmd.Flags().StringVar(&fRel, "relation", "", "relation (alternative to the positional arg)")
	cmd.Flags().StringVar(&fObj, "object", "", "object (alternative to the positional arg)")
	return cmd
}

func (c *Command) listRelationsCmd() *cobra.Command {
	var (
		contextJSON string
		ctxTuples   []string
		relations   []string
		fUser, fObj string
	)
	cmd := &cobra.Command{
		Use:     "list-relations [user] [object]",
		Aliases: []string{"relations"},
		Short:   "List the relations a user has on an object",
		Example: "  ofga query list-relations user:anne document:roadmap\n" +
			"  ofga query list-relations --user user:anne --object document:roadmap\n" +
			"  ofga query list-relations user:anne document:roadmap --relation viewer --relation editor",
		Long: "List the relations a user has on an object. Without --relation, every relation " +
			"defined on the object's type in the latest authorization model is tested; " +
			"--relation narrows that to the ones you name (repeatable).\n\n" +
			"This issues one batch-check per chunk of relations, so it needs OpenFGA >= 1.8.0.",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			values, err := resolveArgs(args, []string{fUser, fObj}, []string{"user", "object"})
			if err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			user, object := values[0], values[1]
			if err := fga.ValidateUserRef(user); err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			if err := fga.ValidateObjectRef(object); err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			if dup, ok := firstDuplicate(relations); ok {
				return clierr.WithCode(clierr.CodeUsage, fmt.Errorf("--relation %q given more than once", dup))
			}
			cx, err := parseContext(contextJSON)
			if err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			ct, err := parseContextualTuples(ctxTuples)
			if err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			candidates := relations
			if len(candidates) == 0 {
				// No --relation: the candidate set is whatever the object's type
				// declares, which only the model knows.
				m, err := cl.AuthorizationModels.ReadLatest(cmd.Context())
				if err != nil {
					return err
				}
				candidates, err = fga.ParseModel(m).RelationsForObject(object)
				if err != nil {
					return clierr.WithCode(clierr.CodeUsage, err)
				}
			}
			allowed, err := cl.Relationships.ListRelations(cmd.Context(), &openfga.ListRelationsRequest{
				User:             user,
				Object:           object,
				Relations:        candidates,
				Context:          cx,
				ContextualTuples: ct,
			})
			if err != nil {
				return err
			}
			if c.cli.JSON || c.cli.YAML {
				return output.Emit(cmd.OutOrStdout(), c.cli.YAML, allowed)
			}
			if len(allowed) == 0 {
				output.Infof(cmd.ErrOrStderr(), "no relations")
				return nil
			}
			for _, r := range allowed {
				safe := output.SanitizeField(r)
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
	cmd.Flags().StringVar(&fUser, "user", "", "user (alternative to the positional arg)")
	cmd.Flags().StringVar(&fObj, "object", "", "object (alternative to the positional arg)")
	cmd.Flags().StringArrayVar(&relations, "relation", nil,
		"relation to test (repeatable; default: every relation on the object's type)")
	cmd.Flags().StringVar(&contextJSON, "context", "", "JSON object of condition context")
	cmd.Flags().StringArrayVar(&ctxTuples, "contextual-tuple", nil, "contextual tuple as user,relation,object (repeatable)")
	return cmd
}

// firstDuplicate reports the first value that appears twice. The SDK rejects a
// duplicate relation, but as a runtime error; catching it here keeps a typo in
// repeated --relation flags a usage error.
func firstDuplicate(vals []string) (string, bool) {
	seen := make(map[string]struct{}, len(vals))
	for _, v := range vals {
		if _, ok := seen[v]; ok {
			return v, true
		}
		seen[v] = struct{}{}
	}
	return "", false
}

func (c *Command) listObjectsCmd() *cobra.Command {
	var (
		contextJSON           string
		ctxTuples             []string
		objectType, rel, user string
	)
	cmd := &cobra.Command{
		Use:     "list-objects [type] [relation] [user]",
		Aliases: []string{"objects"},
		Short:   "List objects of a type a user has a relation with",
		Example: "  ofga query list-objects --type document --relation viewer --user user:anne\n" +
			"  ofga query list-objects document viewer user:anne",
		Args: cobra.MaximumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			values, err := resolveArgs(args,
				[]string{objectType, rel, user},
				[]string{"type", "relation", "user"})
			if err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			if err := fga.ValidateUserRef(values[2]); err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			cx, err := parseContext(contextJSON)
			if err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			ct, err := parseContextualTuples(ctxTuples)
			if err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			req := &openfga.ListObjectsRequest{Type: values[0], Relation: values[1], User: values[2], Context: cx, ContextualTuples: ct}
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
	cmd.Flags().StringArrayVar(&ctxTuples, "contextual-tuple", nil, "contextual tuple as user,relation,object (repeatable)")
	cmd.Flags().StringVar(&objectType, "type", "", "object type")
	cmd.Flags().StringVar(&rel, "relation", "", "relation")
	cmd.Flags().StringVar(&user, "user", "", "user")
	return cmd
}

func (c *Command) listUsersCmd() *cobra.Command {
	var (
		contextJSON string
		ctxTuples   []string
		userTypes   []string
		object, rel string
	)
	cmd := &cobra.Command{
		Use:     "list-users [object] [relation] --type <user-type>",
		Aliases: []string{"users"},
		Short:   "List users that have a relation on an object",
		Example: "  ofga query list-users --object document:roadmap --relation viewer --type user\n" +
			"  ofga query list-users document:roadmap viewer --type user",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			values, err := resolveArgs(args,
				[]string{object, rel},
				[]string{"object", "relation"})
			if err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			if err := fga.ValidateObjectRef(values[0]); err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			cx, err := parseContext(contextJSON)
			if err != nil {
				return clierr.WithCode(clierr.CodeUsage, err)
			}
			ct, err := parseContextualTuples(ctxTuples)
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
				Object:           openfga.FGAObjectRelation{Object: values[0]},
				Relation:         values[1],
				UserFilters:      filters,
				Context:          cx,
				ContextualTuples: ct,
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
	cmd.Flags().StringVar(&contextJSON, "context", "", "JSON object of condition context")
	cmd.Flags().StringArrayVar(&ctxTuples, "contextual-tuple", nil, "contextual tuple as user,relation,object (repeatable)")
	_ = cmd.MarkFlagRequired("type")
	return cmd
}

func writePlainBatchResult(w io.Writer, word, label, detail string) error {
	_, err := fmt.Fprintf(w, "%s\t%s\t%s\n", word, output.PlainField(label), output.PlainField(detail))
	return err
}

// batchOutcome is one --check item resolved against a BatchCheckResponse: a
// correlation ID either has a per-item Error, is missing from the response
// map entirely (a failed chunk), or carries an Allowed verdict.
type batchOutcome struct {
	allowed bool
	isError bool
	detail  string
}

// resolveBatchOutcome looks up id in res.Result. A missing correlation ID or a
// non-empty per-item Error both count as an error, never as "denied" — a
// typo'd relation must not look like a real authorization denial.
func resolveBatchOutcome(res *openfga.BatchCheckResponse, id string) batchOutcome {
	r, ok := res.Result[id]
	switch {
	case !ok:
		return batchOutcome{isError: true, detail: "no result returned for this check"}
	case len(r.Error) > 0:
		return batchOutcome{isError: true, detail: batchCheckErrorDetail(r.Error)}
	default:
		return batchOutcome{allowed: r.Allowed}
	}
}

// batchCheckErrorDetail extracts a human-readable message from a per-item
// BatchCheckSingleResult.Error, falling back to its raw JSON when the server
// didn't send a "message" field.
func batchCheckErrorDetail(e map[string]any) string {
	if msg, ok := e["message"].(string); ok && msg != "" {
		return msg
	}
	b, err := json.Marshal(e)
	if err != nil {
		return "check failed"
	}
	return string(b)
}

// writeBatchResult renders one check's outcome for human/table output: an
// ALLOWED/DENIED badge, or a distinct ERROR badge (with detail) so a per-item
// failure never reads as a denial.
func writeBatchResult(w io.Writer, o batchOutcome, label string) error {
	badge := style.Allowed(o.allowed)
	if o.isError {
		badge = style.Failure.Render(style.IconCross + " ERROR")
	}
	line := fmt.Sprintf("%s  %s", badge, style.Faint.Render(label))
	if o.detail != "" {
		line += style.Faint.Render(" (" + output.SanitizeField(o.detail) + ")")
	}
	_, err := fmt.Fprintln(w, line)
	return err
}

// batchCheckErr picks the command's exit error: nil on full success, or an
// error naming how many of the total checks failed (wrapping callErr, the
// BatchCheckAll transport error, if BatchCheckAll itself returned one).
// Mirrors the plain-error partial-failure convention used by the tuple
// write/delete batch helpers (writeInBatches): a real per-item failure is a
// generic runtime error (clierr.CodeError, the default for an unwrapped
// error), not clierr.CodeTestFailed — that code is reserved for `model
// test`/`assertions test`'s "ran fine, but the expectation didn't match"
// outcome, which per-item batch-check errors are not.
func batchCheckErr(callErr error, failed, total int) error {
	if failed == 0 {
		return callErr
	}
	if callErr != nil {
		return fmt.Errorf("%d of %d check(s) failed: %w", failed, total, callErr)
	}
	return fmt.Errorf("%d of %d check(s) failed", failed, total)
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
