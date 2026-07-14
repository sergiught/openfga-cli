# OAuth flow testing (OpenFGA + auth0-mock)

A self-contained stack for exercising the CLI's OAuth authentication against a
real, token-validating OpenFGA — and for watching the playground's **API Logs**
tab redact the bearer token and skip the token-endpoint request.

- **OpenFGA** on `http://localhost:8080`, running in `oidc` mode so it actually
  validates the JWTs it receives.
- **[auth0-mock](https://github.com/sergiught/auth0-mock)** on
  `http://localhost:3000`, minting real RS256 tokens and serving OIDC discovery
  + JWKS. It doesn't validate client credentials (it's a mock), so any
  `client_id`/`client_secret` or signing key is accepted — but the token it
  returns is genuinely signed and validated by OpenFGA.

## Quick start

```bash
make demo        # from the repo root (or `cd test/oauth && make demo`)
```

This starts the stack and seeds **three stores — `dev`, `staging`, `prod`** —
each with one authorization model, 100 tuples, and 100 assertions, then prints
the CLI profile setup. Tear it down with `make demo-down`.

The sections below are the manual equivalents.

## 1. Start the stack

```bash
cd test/oauth
docker compose up -d
```

`docker compose ps` should show `openfga` as `healthy` and `auth0-mock` as `Up`.
Seed the demo data (three stores, a model + 100 tuples + 100 assertions each)
with `make seed` or `./seed.sh`.

Quick sanity check that auth is enforced:

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/stores   # -> 401
```

## 2. client_credentials

Use an isolated config dir so this never touches your real `ofga` config:

```bash
export XDG_CONFIG_HOME=$(mktemp -d)

ofga profiles add local --api-url http://localhost:8080
ofga profiles use local
ofga profiles set auth_method client_credentials
ofga profiles set client_id demo
printf 'supersecret' | ofga profiles set client_secret --value-stdin
ofga profiles set token_url http://localhost:3000/oauth/token
ofga profiles set audience https://api.openfga.local/   # must match OPENFGA_AUTHN_OIDC_AUDIENCE

ofga stores create demo-store   # succeeds => the client_credentials token was accepted
ofga stores list
```

If the token were invalid (wrong audience/issuer/signature) OpenFGA would answer
`401` and the CLI would surface an authentication error.

## 3. private_key_jwt

```bash
export XDG_CONFIG_HOME=$(mktemp -d)
openssl genrsa -out /tmp/ofga-pkjwt.pem 2048

ofga profiles add pk --api-url http://localhost:8080
ofga profiles use pk
ofga profiles set auth_method private_key_jwt
ofga profiles set client_id demo
ofga profiles set token_url http://localhost:3000/oauth/token
ofga profiles set key_file /tmp/ofga-pkjwt.pem
ofga profiles set signing_method RS256
ofga profiles set api_audience https://api.openfga.local/   # aud of the access token (OpenFGA)
ofga profiles set audience http://localhost:3000/            # aud of the signed client assertion

ofga stores list   # succeeds => the signed-JWT client assertion was accepted
```

> auth0-mock does not verify the client assertion's signature (it's a mock), so
> any key works here. Against a real IdP (e.g. Keycloak) you register the public
> key on the client.

## 4. See it in the playground's API Logs

```bash
ofga   # launches the TUI with the active profile
```

Create a store / run a Check, then open the **API Logs** tab (press `8`). You
should see:

- the OpenFGA API calls, with `Authorization: Bearer ***redacted***`;
- **no** `oauth/token` row — the token fetch goes to a different host
  (`localhost:3000`) than the API (`localhost:8080`) and is deliberately not
  captured, so the `access_token` and `client_secret` never land in the log.

## How the issuer wiring works

auth0-mock stamps the token's `iss` (and its discovery/JWKS URLs) from
`ISSUER_URL`, regardless of the request host. The compose sets it to
`http://host.docker.internal:3000/` so:

- **OpenFGA** (in the container) validates `iss` against
  `OPENFGA_AUTHN_OIDC_ISSUER` and fetches JWKS from
  `host.docker.internal:3000` via the host gateway;
- **your CLI** (on the host) fetches the token from `localhost:3000` — it
  doesn't care about `iss`, so the two URLs pointing at the same server is fine.

## Skip validation

To only look at the API Logs redaction (no server-side validation), comment out
the `OPENFGA_AUTHN_*` block in `compose.yaml` and `docker compose up -d` again —
OpenFGA then accepts any request, but the CLI still performs the token fetch.

## Tear down

```bash
docker compose down
```
