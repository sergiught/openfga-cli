# Configuration

`ofga` stores configuration in the platform config directory (the XDG config
directory on Linux and the user Preferences directory on macOS). Find the
exact file with:

```bash
ofga config path
```

### Profiles

A **profile** bundles an API URL, an optional store and model ID, and auth settings. Switch between environments with `--profile`/`-p` or the `OPENFGA_PROFILE` env var.

```bash
ofga profiles add prod --api-url https://fga.example.com \
  --auth-method api_token --token-stdin < token.txt
ofga profiles use prod
ofga profiles show                # resolved active config (secrets masked)
ofga --profile staging stores list
```

The file is intentionally straightforward TOML:

```toml
version = 1
active_profile = "local"
theme = "aurora"

[profiles.local]
api_url = "http://localhost:8080"

[profiles.local.auth]
method = "none"
```

Prefer `ofga profiles add`, `set`, `unset`, and `remove` over editing the file:
they preserve atomic writes and keep managed credentials synchronized with the
OS keyring. If another process changes the file while `ofga` is running, the
save is rejected rather than overwriting the newer configuration.

### Precedence

Connection values are resolved in increasing order of precedence:

**profile → environment variables → command-line flags (including secret-file flags)**

Authentication overrides are method-aware: client secrets apply only to
`client_credentials`, private keys apply only to `private_key_jwt`, and an
explicit API-token file selects API-token authentication for that process.

### Environment variables

| Variable | Purpose |
| --- | --- |
| `OPENFGA_API_URL` | API URL (alias: `FGA_API_URL`) |
| `OPENFGA_STORE_ID` | Active store ID (alias: `FGA_STORE_ID`) |
| `OPENFGA_MODEL_ID` | Authorization model ID (aliases: `OPENFGA_AUTHORIZATION_MODEL_ID`, `FGA_MODEL_ID`, `FGA_AUTHORIZATION_MODEL_ID`) |
| `OPENFGA_API_TOKEN` | API bearer token compatibility fallback; prefer `--auth-token-file` (alias: `FGA_API_TOKEN`) |
| `OPENFGA_CLIENT_ID` | OAuth2 client ID for `client_credentials` (alias: `FGA_CLIENT_ID`) |
| `OPENFGA_CLIENT_SECRET` | OAuth2 secret compatibility fallback; prefer `--auth-client-secret-file` (alias: `FGA_CLIENT_SECRET`) |
| `OPENFGA_TOKEN_URL` | OAuth2 token endpoint for `client_credentials` (alias: `FGA_TOKEN_URL`) |
| `OPENFGA_API_AUDIENCE` | OAuth2 audience for `client_credentials` (alias: `FGA_API_AUDIENCE`) |
| `OPENFGA_SCOPES` | OAuth2 scopes for `client_credentials` (alias: `FGA_SCOPES`) |
| `OPENFGA_KEY_FILE` | Path to the PEM signing key; applies to a `private_key_jwt` profile (alias: `FGA_KEY_FILE`) |
| `OPENFGA_PROFILE` | Profile to use (alias: `FGA_PROFILE`) |
| `OPENFGA_CONFIG` | Path to the config file (overridden by the `--config` flag) |
| `OPENFGA_ICONS` | Icon mode: `nerdfont` (default), `unicode`, or `off` |
| `OPENFGA_REDUCED_MOTION` | Suppress TUI animations (alias: `OFGA_REDUCED_MOTION`) |
| `NO_COLOR` | Disable colored output |
| `CLICOLOR_FORCE` | Force colored output even when piped or redirected |
| `FORCE_COLOR` | Force colored output even when piped or redirected (equivalent to `CLICOLOR_FORCE`) |

`FGA_*` aliases are accepted for compatibility with the official CLI.

