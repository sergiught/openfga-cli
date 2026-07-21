# Command reference

Complete per-command reference for `ofga`, generated from the CLI's own `--help` output. Every command also accepts the global flags shown in `ofga --help` (`--profile`, `--store-id`, `--model-id`, `--output`, `--json`/`--yaml`/`--plain`, `--theme`, `--debug`, `--timeout`, `--no-input`, `--no-color`, `--quiet`, `--config`, `--api-url`, `--auth-*-file`, `--version`); they are omitted below to keep each entry focused on what's specific to that command.

## init

![ofga init — guided setup](../../examples/init.gif)

Set up a connection profile (guided). On a terminal it runs an interactive tour (API URL, auth, connection test, store/model picker); non-interactively it uses the flags and defaults, so it's safe in CI.

### init

- Synopsis: create or update a connection profile and make it active.
- Key flags: `--api-url` (default `http://localhost:8080`), `--token-stdin` (read the API token from stdin), `--store-id`, `--model-id`, `-f, --force` (overwrite an existing profile without prompting).
- Example:
  ```bash
  ofga init prod --api-url https://fga.example.com --token-stdin < token.txt
  ```

## stores

![ofga stores](../../examples/stores.gif)

Create, list, inspect and delete stores. (`store` is an alias for `stores`.)

### stores list

- Synopsis: list stores; by default all stores are returned (the CLI auto-pages).
- Key flags: `--max-results` (cap the total returned, 0 = unbounded), `--limit` (alias for `--max-results`).
- Example:
  ```bash
  ofga stores list
  ```

### stores create

- Synopsis: create a new store.
- Key flags: `--use` (save the new store ID to the active profile), `-n, --dry-run` (show what would be created without creating it).
- Example:
  ```bash
  ofga stores create my-store --use
  ```

### stores get

- Synopsis: show details of a store.
- Example:
  ```bash
  ofga stores get 01ARZ3NDEKTSV4RRFFQ69G5FAV
  ```

### stores delete

- Synopsis: delete a store.
- Key flags: `-f, --force` (skip the confirmation prompt), `-n, --dry-run` (show what would be deleted without deleting).
- Example:
  ```bash
  ofga stores delete 01ARZ3NDEKTSV4RRFFQ69G5FAV --force
  ```

## model

![ofga model](../../examples/model.gif)

Write, inspect and visualize authorization models.

### model write

- Synopsis: write a new authorization model from a JSON or `.fga` DSL file (format chosen by extension, or content-sniffed for stdin).
- Key flags: `-f, --file` (path to the model file, `-` for stdin), `-n, --dry-run` (validate without writing).
- Example:
  ```bash
  ofga model write --file model.fga
  ```

### model get

- Synopsis: show an authorization model as DSL.
- Example:
  ```bash
  ofga model get 01ARZ3NDEKTSV4RRFFQ69G5FAV
  ```

### model latest

- Synopsis: show the most recent authorization model as DSL.
- Example:
  ```bash
  ofga model latest
  ```

### model list

- Synopsis: list authorization models in the store.
- Key flags: `--max-results` (cap the total returned), `--limit` (alias for `--max-results`).
- Example:
  ```bash
  ofga model list
  ```

### model graph

- Synopsis: render an authorization model as a colored tree showing, per type and relation, directly-assignable types, implied relations, and inherited (tuple-to-userset) paths. With no argument, the latest model is used.
- Example:
  ```bash
  ofga model graph
  ```

### model test

- Synopsis: run authorization model tests declared by an `ofga.yaml` workspace, against an embedded OpenFGA server by default (or a real one with `--openfga-image`/`--server-addr`).
- Key flags: `-f, --file` (workspace manifest, directory, or single test file), `--model` (model file, overrides the manifest), `--tests` (test-file glob(s), repeatable), `--run` (glob to select tests), `--watch` (re-run on file change), `--coverage` / `--coverage-min` / `--coverage-diff` (branch coverage reporting and gating), `--report` (`junit`, `json`, or `github`) with `--report-file`, `--playground` (open the interactive playground on a failing test's seeded world), `--openfga-image` / `--server-addr` (run against a real server instead of the embedded one), `--fail-fast`, `--parallel`.
- Example:
  ```bash
  ofga model test --coverage --coverage-min 80
  ```

#### model test init

- Synopsis: scaffold a minimal, runnable `ofga.yaml` workspace (schema, model, fixture, passing test) into a directory (default: current directory).
- Key flags: `--force` (overwrite existing files).
- Example:
  ```bash
  ofga model test init ./ws
  ```

#### model test schema

- Synopsis: print the JSON Schema for the model-test workspace format (`ofga.yaml` and `*.test.yaml`).
- Example:
  ```bash
  ofga model test schema
  ```

## tuples

![ofga tuples](../../examples/tuples.gif)

Write, delete and read relationship tuples. (`tuple` is an alias for `tuples`.)

### tuples write

- Synopsis: write one relationship tuple, or many with `--file`.
- Key flags: `--user`, `--relation`, `--object` (alternatives to the positional args), `--file` (JSON file of tuples to write in bulk, `-` for stdin), `-n, --dry-run`.
- Example:
  ```bash
  ofga tuples write user:anne viewer document:roadmap
  ```

### tuples read

- Synopsis: read relationship tuples, optionally filtered; by default all matching tuples are returned (the CLI auto-pages).
- Key flags: `--user`, `--relation`, `--object` (all optional filters), `--max-results` (alias `--limit`), `--page-size` (per-request page size, not a total cap).
- Example:
  ```bash
  ofga tuples read --object document:roadmap
  ```

### tuples delete

- Synopsis: delete one relationship tuple, or many with `--file`.
- Key flags: `--user`, `--relation`, `--object` (alternatives to the positional args), `--file` (JSON file of tuples to delete in bulk, `-` for stdin), `-f, --force` (skip the confirmation prompt), `-n, --dry-run`.
- Example:
  ```bash
  ofga tuples delete user:anne viewer document:roadmap
  ```

### tuples changes

- Synopsis: show the tuple changelog (writes and deletes); by default all currently-available changes are returned (the CLI auto-pages).
- Key flags: `--type` (filter by object type), `--start-time` (only changes at/after an RFC3339 time), `--max-results` (alias `--limit`), `--page-size`.
- Example:
  ```bash
  ofga tuples changes --type document
  ```

## query

![ofga query](../../examples/query.gif)

Ask authorization questions. Positional argument order mirrors the OpenFGA API and differs per subcommand (`check` is user-first, `list-objects` is user-last, `list-users`/`expand` are object-first) — use the named flags (`--user`/`--relation`/`--object`) if the order is easy to mix up.

### query check

- Synopsis: check whether a user has a relation on an object.
- Key flags: `--user`, `--relation`, `--object` (alternatives to the positional args), `--contextual-tuple` (repeatable, `user,relation,object`), `--context` (JSON condition context).
- Example:
  ```bash
  ofga query check user:anne viewer document:roadmap
  ```

### query batch-check

- Synopsis: run several checks in one request.
- Key flags: `--check` (repeatable, `user,relation,object`).
- Example:
  ```bash
  ofga query batch-check --check user:anne,viewer,doc:1 --check user:bob,editor,doc:1
  ```

### query expand

- Synopsis: expand the userset tree that grants a relation (JSON output).
- Key flags: `--relation`, `--object` (alternatives to the positional args).
- Example:
  ```bash
  ofga query expand viewer document:roadmap
  ```

### query list-objects

- Synopsis: list objects of a type a user has a relation with.
- Key flags: `--type`, `--relation`, `--user` (alternatives to the positional args), `--contextual-tuple` (repeatable), `--context`.
- Example:
  ```bash
  ofga query list-objects document viewer user:anne
  ```

### query list-users

- Synopsis: list users that have a relation on an object.
- Key flags: `--object`, `--relation` (alternatives to the positional args), `--type` (repeatable, user type filter, optionally `type#relation`), `--contextual-tuple` (repeatable), `--context`.
- Example:
  ```bash
  ofga query list-users document:roadmap viewer --type user
  ```

## assertions

![ofga assertions](../../examples/assertions.gif)

Read, write and run a model's assertion test-suite.

### assertions read

- Synopsis: read the assertions for a model (default: latest).
- Example:
  ```bash
  ofga assertions read
  ```

### assertions write

- Synopsis: replace the assertions for a model (the active `--model-id`, or latest) from a JSON file — an array of assertions, or `{"assertions": [...]}`, each `{"tuple_key":{"user","relation","object"},"expectation":true}`.
- Key flags: `-f, --file` (assertions JSON file, `-` for stdin), `--force` (replace without prompting), `-n, --dry-run`.
- Example:
  ```bash
  ofga assertions write --file assertions.json
  ```

### assertions test

- Synopsis: read the stored assertions for a model and verify each one with a live check, comparing the result to the expectation.
- Example:
  ```bash
  ofga assertions test
  ```

## profiles

![ofga profiles](../../examples/profiles.gif)

Manage named connection profiles. Each profile stores an API URL, optional store and authorization-model IDs, and optional authentication settings.

### profiles add

- Synopsis: create a new profile. Auth method defaults to bearer token when `--token*` is given, otherwise none; for OAuth pass `--auth-method client_credentials` or `private_key_jwt` with their fields.
- Key flags: `--api-url` (default `http://localhost:8080`), `--auth-method` (`none | api_token | client_credentials | private_key_jwt`), `--token-stdin`/`--token-file`, `--client-id`, `--client-secret-stdin`/`--client-secret-file`, `--token-url`, `--audience` (client_credentials), `--api-audience` (private_key_jwt), `--scopes`, `--key-file`, `--key-id`, `--signing-method`, `--store-id`, `--model-id`, `--use` (switch to this profile after creating).
- Example:
  ```bash
  ofga profiles add dev --api-url http://localhost:8080 --use
  ```

### profiles list

- Synopsis: list all profiles.
- Example:
  ```bash
  ofga profiles list
  ```

### profiles current

- Synopsis: show the active profile name.
- Example:
  ```bash
  ofga profiles current
  ```

### profiles use

- Synopsis: switch the active profile.
- Example:
  ```bash
  ofga profiles use prod
  ```

### profiles show

- Synopsis: show a profile's resolved values (token masked). With no argument, shows the fully resolved active configuration after env/flag overrides.
- Example:
  ```bash
  ofga profiles show prod
  ```

### profiles set

- Synopsis: set a field on a profile. Settable keys — connection: `api_url`, `store_id`, `model_id`; auth: `auth_method`, `token`, `client_id`, `client_secret`, `token_url`, `audience`, `api_audience`, `key_file`, `private_key`, `signing_method`, `key_id`, `scopes`. For secrets (`token`, `client_secret`, `private_key`) prefer `--value-file` or `--value-stdin` so the value never appears in `ps` output or shell history; those are stored in the OS keyring.
- Key flags: `--value-file`, `--value-stdin`.
- Example:
  ```bash
  ofga profiles set client_secret --value-file ./secret
  ```

### profiles unset

- Synopsis: clear a field on a profile. Unsetting `auth` clears the entire auth method, `token_url`, `audience` and any keyring-stored secrets, not just the method; prompts for confirmation unless `--force` is given.
- Key flags: `-f, --force` (skip the confirmation prompt when clearing auth).
- Example:
  ```bash
  ofga profiles unset store_id
  ```

### profiles remove

- Synopsis: delete a profile.
- Key flags: `-f, --force` (skip the confirmation prompt).
- Example:
  ```bash
  ofga profiles remove old --force
  ```

### profiles cleanup-credentials

- Synopsis: retry pending OS-keyring cleanup, or purge all `ofga` secrets from the keyring (including orphans from deleted profiles) with `--purge`.
- Key flags: `--purge`, `-f, --force` (skip the confirmation prompt with `--purge`).
- Example:
  ```bash
  ofga profiles cleanup-credentials --purge
  ```

## api

![ofga api](../../examples/api.gif)

Send a raw request to the OpenFGA API, reusing the active profile's URL and authentication. The path is relative to the profile's API URL; a JSON body may be passed as the third argument, or read from stdin with `-`. This is an expert escape hatch — mutating methods are sent directly, with no confirmation or dry-run support.

### api

- Synopsis: `ofga api <method> <path> [body]`.
- Example:
  ```bash
  ofga api GET /stores
  ```

## config

Inspect ofga's configuration. Profile metadata lives in a TOML file (mode 0600); tokens, client secrets and private keys are stored separately in the OS keyring. Currently has a single subcommand.

### config path

- Synopsis: print the path to the config file.
- Example:
  ```bash
  ofga config path
  ```

## theme

Show or set the color theme. With no argument, lists available themes and marks the current one; with a name, sets and saves the global theme.

### theme

- Synopsis: `ofga theme [name]`. Available themes: `aurora`, `catppuccin`, `charm`, `dracula`, `gruvbox`, `nord`, `tokyonight`, `mono`.
- Example:
  ```bash
  ofga theme dracula
  ```

## completion

Generate the autocompletion script for the specified shell.

### completion bash / zsh / fish / powershell

- Synopsis: generate the autocompletion script for that shell.
- Key flags: `--no-descriptions` (disable completion descriptions).
- Example:
  ```bash
  source <(ofga completion bash)
  ```

## version

Print version and build information.

### version

- Synopsis: `ofga version`.
- Example:
  ```bash
  ofga version
  ```

## playground

![the ofga playground TUI](../../examples/playground.gif)

Launch the interactive playground.

### playground

- Synopsis: `ofga playground`.
- Example:
  ```bash
  ofga playground
  ```
