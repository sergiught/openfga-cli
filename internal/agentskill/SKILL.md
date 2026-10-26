---
name: ofga
description: Use when driving OpenFGA stores, authorization models, relationship tuples and permission queries from the shell with the ofga CLI, or when testing a model with `ofga model test`.
---

# Driving OpenFGA with ofga

`ofga` is a CLI for OpenFGA. Every command can be scripted: it can print JSON,
it never has to prompt, and its failures are machine-readable. Read this once,
then use `ofga <command> --help` for the details of a command.

## 1. Run in agent mode

Agent mode gives compact one-line JSON, turns prompts and color off, and drops
success chatter. It turns on by itself when `CLAUDECODE=1` is set. Otherwise
ask for it with `--agent` or `OPENFGA_AGENT_MODE=1`.

If you cannot use agent mode, pass `--json --no-input` on every call. Never
start the TUI: a bare `ofga` and `ofga playground` refuse to run in agent mode.

```bash
ofga stores list --agent
ofga query check user:anne viewer document:roadmap --json --no-input
```

Pick the store and model with `--store-id` and `--model-id` (or a `--profile`).
Fields in the JSON are stable; a command's schema is linked from the catalog
(see 3).

## 2. Exit codes and error codes

| Exit | Meaning |
| --- | --- |
| 0 | success |
| 1 | error |
| 2 | usage (bad command, flag or arguments) |
| 3 | `model test`, `assertions test` or a coverage gate failed |
| 4 | the server could not be reached |
| 130 | canceled |

With `--json`, `-o json|jsonl|yaml` or agent mode, a failure writes exactly one
line of JSON to stderr:

```json
{"error":{"code":"not_found","message":"...","status":404,"api_code":"...","hint":"...","details":[]}}
```

Branch on `code`, never on `message`. `status`, `api_code` and `hint` appear
only when there is one; `details` is always an array. The codes are `usage`,
`confirmation_required`, `validation`, `already_exists`, `unauthenticated`,
`forbidden`, `not_found`, `rate_limited`, `server`, `network`, `test_failed`,
`partial_failure`, `canceled` and `error`. Read `hint` before retrying.

## 3. Discover commands

Do not guess command names or flags. Ask the binary:

```bash
ofga commands --json
```

It lists every command with its path, flags, whether it mutates state, whether
it needs a store or model, and the URL of its output JSON Schema. Filter it
with `jq`, for example `ofga commands --json | jq '.commands[] | select(.path | contains("tuples"))'`.

## 4. Dry-run before writing

Preview every write first. `--dry-run` (or `-n`) shows what would happen and
changes nothing:

```bash
ofga tuples write user:anne viewer document:roadmap --dry-run
ofga tuples write --file tuples.json --dry-run
ofga model write --file model.fga --dry-run
ofga stores create --dry-run
```

Read the preview, then repeat the same command without the flag.

## 5. Destructive commands need --force

Commands that delete data prompt for confirmation. You cannot answer a prompt,
so without `--force` they fail with the error code `confirmation_required`
(exit 1) and change nothing. That is a safety stop, not a bug.

```bash
ofga tuples delete user:anne viewer document:roadmap --dry-run
ofga tuples delete user:anne viewer document:roadmap --force
```

Add `--force` only after you have checked the target, and only when the user
asked for the deletion. `ofga stores delete` removes a store with all its
models and tuples, so confirm the store ID with the user first.

## 6. Re-run failures with --failed-file

Bulk `--file` writes and deletes can partly fail. The run ends with the error
code `partial_failure` (or `network` if the server went away). Always give bulk
runs a failed file:

```bash
ofga tuples write --file tuples.json --failed-file failed.json
ofga tuples write --file failed.json --failed-file failed-again.json
```

`failed.json` holds only the tuples that were rejected, in the input format,
ready to re-run. Fix the cause (see each tuple's error) before repeating.
`ofga tuples delete --file` takes the same flag.

## 7. Large reads: -o jsonl

`--json` prints one document, so a big read is held in memory and parsed as a
whole. For many rows use `-o jsonl`: one JSON value per line, streamed.

```bash
ofga tuples read --object document:roadmap -o jsonl --max-results 1000
ofga tuples read -o jsonl --output-file tuples.jsonl
```

Always bound reads with `--max-results`. The file written by `--output-file`
can be fed straight back to `ofga tuples write --file`.

## 8. Test a model with model test

Model tests live in a workspace: an `ofga.yaml` manifest and `*.test.yaml`
files. They run against an embedded OpenFGA server, so no store or profile is
needed and nothing outside the test run is touched.

```bash
ofga model test --json
ofga model test --run 'viewer*' --explain full
ofga model test --coverage --coverage-min 80
```

Exit 3 (error code `test_failed`) means a test failed or the coverage gate was
missed; the JSON lists each failure with expected and got values. Fix the model
or the test, then re-run. Use `ofga model test schema` for the file format.
For single questions against a real store use `ofga query check`,
`ofga query list-objects` and `ofga query batch-check --file checks.json`.

## 9. Look up OpenFGA itself

For questions about OpenFGA concepts, modeling or the DSL, search the official
docs instead of relying on memory:

```bash
ofga docs search "conditions" --max-results 3 --json
ofga docs show /docs/modeling/conditions
```

`docs search` returns titles and URLs; pass a path or URL to `docs show` to
read the page as markdown.
