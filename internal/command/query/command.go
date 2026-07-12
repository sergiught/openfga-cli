// Package query implements `ofga query`: the read-side authorization
// questions — check, batch-check, expand, list-objects and list-users.
package query

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/cli"
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
		Short:   "Ask authorization questions (check, expand, list)",
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
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("--context must be a JSON object: %w", err)
	}
	return m, nil
}

// parseContextualTuples parses repeated "user,relation,object" values.
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
		keys = append(keys, openfga.TupleKey{
			User:     strings.TrimSpace(parts[0]),
			Relation: strings.TrimSpace(parts[1]),
			Object:   strings.TrimSpace(parts[2]),
		})
	}
	return &openfga.ContextualTupleKeys{TupleKeys: keys}, nil
}

func (c *Command) checkCmd() *cobra.Command {
	var (
		contextJSON string
		ctxTuples   []string
	)
	cmd := &cobra.Command{
		Use:     "check <user> <relation> <object>",
		Short:   "Check whether a user has a relation on an object",
		Example: "  ofga query check user:anne viewer document:roadmap",
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
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
				TupleKey:         openfga.CheckRequestTupleKey{User: args[0], Relation: args[1], Object: args[2]},
				Context:          cx,
				ContextualTuples: ct,
			}
			res, err := cl.Relationships.Check(cmd.Context(), req)
			if err != nil {
				return err
			}
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), res)
			}
			if output.Plain {
				fmt.Fprintln(cmd.OutOrStdout(), allowedWord(res.Allowed))
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n",
				style.Allowed(res.Allowed),
				style.Faint.Render(fmt.Sprintf("%s %s %s", args[0], args[1], args[2])))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&contextJSON, "context", "", "JSON object of condition context")
	f.StringArrayVar(&ctxTuples, "contextual-tuple", nil, "contextual tuple as user,relation,object (repeatable)")
	return cmd
}

func (c *Command) batchCheckCmd() *cobra.Command {
	var checks []string
	cmd := &cobra.Command{
		Use:   "batch-check --check user,relation,object [...]",
		Short: "Run several checks in one request",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(checks) == 0 {
				return fmt.Errorf("provide at least one --check user,relation,object")
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
					return fmt.Errorf("--check %q must be user,relation,object", raw)
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
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), res)
			}
			for i, item := range items {
				r := res.Result[item.CorrelationID]
				if output.Plain {
					fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", allowedWord(r.Allowed), labels[i])
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n", style.Allowed(r.Allowed), style.Faint.Render(labels[i]))
				}
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&checks, "check", nil, "a check as user,relation,object (repeatable)")
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
			// The userset tree has no meaningful flat rendering; it is always
			// emitted as JSON, so --json/--plain are intentionally no-ops here.
			return output.JSON(cmd.OutOrStdout(), res.Tree)
		},
	}
}

func (c *Command) listObjectsCmd() *cobra.Command {
	var contextJSON string
	cmd := &cobra.Command{
		Use:     "list-objects <type> <relation> <user>",
		Aliases: []string{"objects"},
		Short:   "List objects of a type a user has a relation with",
		Example: "  ofga query list-objects document viewer user:anne",
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, _, err := c.cli.ClientWithStore()
			if err != nil {
				return err
			}
			cx, err := parseContext(contextJSON)
			if err != nil {
				return err
			}
			req := &openfga.ListObjectsRequest{Type: args[0], Relation: args[1], User: args[2], Context: cx}
			res, err := cl.Relationships.ListObjects(cmd.Context(), req)
			if err != nil {
				return err
			}
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), res.Objects)
			}
			if len(res.Objects) == 0 {
				output.Infof(cmd.OutOrStdout(), "no objects")
				return nil
			}
			for _, o := range res.Objects {
				if output.Plain {
					fmt.Fprintln(cmd.OutOrStdout(), o)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", style.Bullet(), o)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&contextJSON, "context", "", "JSON object of condition context")
	return cmd
}

func (c *Command) listUsersCmd() *cobra.Command {
	var userTypes []string
	cmd := &cobra.Command{
		Use:     "list-users <object> <relation> --type <user-type>",
		Aliases: []string{"users"},
		Short:   "List users that have a relation on an object",
		Example: "  ofga query list-users document:roadmap viewer --type user",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(userTypes) == 0 {
				return fmt.Errorf("at least one --type filter is required (e.g. --type user)")
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
				Object:      openfga.FGAObjectRelation{Object: args[0]},
				Relation:    args[1],
				UserFilters: filters,
			}
			res, err := cl.Relationships.ListUsers(cmd.Context(), req)
			if err != nil {
				return err
			}
			if c.cli.JSON {
				return output.JSON(cmd.OutOrStdout(), res.Users)
			}
			if len(res.Users) == 0 {
				output.Infof(cmd.OutOrStdout(), "no users")
				return nil
			}
			for _, u := range res.Users {
				if output.Plain {
					fmt.Fprintln(cmd.OutOrStdout(), formatUser(u))
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", style.Bullet(), formatUser(u))
				}
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&userTypes, "type", nil, "user type filter, optionally type#relation (repeatable)")
	return cmd
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
