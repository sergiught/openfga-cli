# Scripting & automation

`ofga` is built to compose:

- `--json` emits clean, machine-readable JSON (secrets omitted) for `jq`.
- `--yaml` (or `-o yaml` / `--output yaml`) emits the same structured data as YAML, for tools that prefer it (e.g. diffing against a YAML-based config).
- `--plain` emits unstyled TSV for both reads and mutations; embedded tabs,
  newlines, and control characters are normalized to spaces so every item
  remains one record. Key/value results are `key<TAB>value`, so
  `query check --plain` prints `allowed<TAB>true` or `allowed<TAB>false`
  (a denied check is still a successful query, so the exit code stays `0`).
- Meaningful **exit codes**: `0` success, `1` generic failure, `2` usage error, `3` failed `model test` (or coverage gate), `4` network error, `130` interrupted by Ctrl-C/SIGINT.
- First-class server mutation commands support `-n`/`--dry-run`; `ofga api` is an expert escape hatch.
- Destructive replacements and deletes prompt on a TTY and require `--force` when non-interactive, so scripts fail safe.
- Piped output drops colors and box-drawing automatically.
- `--timeout` bounds each HTTP request (30 seconds by default; `0` disables it).
- `--no-input` prevents prompts and disables the bare-command TUI for automation running under a pseudo-TTY.
- A downstream consumer that closes stdout early (for example `| head`) is
  treated as normal pipeline completion unless a command-specific failure must
  take precedence.
- `ofga api --plain` preserves the response body bytes rather than
  pretty-printing JSON, adding only a missing final newline.

Bulk tuple writes/deletes are sent in server-sized batches and are not
transactional across batches. If a later batch fails, JSON/YAML and plain
output report `written`/`deleted`, `total`, and `complete: false` before the
command exits non-zero. Automation should inspect the exit status and treat
the reported count as already committed.

> **Note:** `ofga tuples read`, `ofga tuples changes`, `ofga stores list`, and
> `ofga model list` auto-paginate and return **all** rows by default
> (`--page-size` only sets the per-request page size, not a total cap). Against
> a large store that can be a lot of output—cap it with `--max-results`
> (alias `--limit`), or pipe through `head` or `--json | jq`.

```bash
# Which documents can anne view?
ofga query list-objects document viewer user:anne --plain

# Fail a CI job if the assertion suite regresses
ofga assertions test || exit 1
```
