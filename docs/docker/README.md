# ofga — a modern CLI & TUI for OpenFGA

`ofga` is an unofficial command-line interface and terminal UI for
[OpenFGA](https://openfga.dev). It talks to any OpenFGA server: manage stores,
author authorization models (including the `.fga` DSL), read and write tuples,
run checks and assertions, and explore it all in an interactive playground.

- **Source & docs:** https://github.com/sergiught/openfga-cli
- **Also on GitHub Container Registry:** `ghcr.io/sergiught/openfga-cli`

## Supported tags

- `X.Y.Z` — a specific release (e.g. `0.264.0`)
- `latest` — the most recent release

Images are multi-arch (`linux/amd64`, `linux/arm64`), run as a non-root user
(`ofga`, uid `10001`), and ship a Sigstore SBOM + signature you can verify with
`cosign`.

## Usage

```sh
# Print the version
docker run --rm docker.io/sergiught/openfga-cli version

# Run a CLI command against a server (JSON out for scripting)
docker run --rm \
  -e OPENFGA_API_URL=http://host.docker.internal:8080 \
  docker.io/sergiught/openfga-cli stores list --json

# Launch the interactive TUI (needs a TTY)
docker run --rm -it \
  -e OPENFGA_API_URL=http://host.docker.internal:8080 \
  docker.io/sergiught/openfga-cli
```

### Persisting configuration

`ofga` stores profiles under `$XDG_CONFIG_HOME`. Mount a volume and point it
there to keep profiles between runs:

```sh
docker run --rm -it \
  -e XDG_CONFIG_HOME=/config \
  -v ofga-config:/config \
  docker.io/sergiught/openfga-cli
```

Secrets are normally kept in the OS keyring, which isn't available in a
container; supply credentials via the documented environment variables instead
(e.g. `OPENFGA_API_TOKEN`, or `OPENFGA_CLIENT_ID` / `OPENFGA_CLIENT_SECRET` /
`OPENFGA_TOKEN_URL` for a client-credentials grant).

## Verifying an image

```sh
cosign verify docker.io/sergiught/openfga-cli:latest \
  --certificate-identity-regexp 'https://github.com/sergiught/openfga-cli/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## License

See [LICENSE](https://github.com/sergiught/openfga-cli/blob/main/LICENSE).
