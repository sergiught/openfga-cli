#!/usr/bin/env bash
# Seed the local OpenFGA (started by compose.yaml) with a small demo dataset:
# three stores (dev, staging, prod), one authorization model each, and 100
# tuples + 100 assertions per store. Auth is via auth0-mock's client_credentials
# (OpenFGA runs in oidc mode), so this mints a token first.
#
# Requires: curl, bash. No jq needed.
set -euo pipefail

API_URL="${API_URL:-http://localhost:8080}"
TOKEN_URL="${TOKEN_URL:-http://localhost:3000/oauth/token}"
AUDIENCE="${AUDIENCE:-https://api.openfga.local/}"
CLIENT_ID="${CLIENT_ID:-demo}"
CLIENT_SECRET="${CLIENT_SECRET:-demo-secret}"

STORES=(dev staging prod)
COUNT="${COUNT:-100}"

# json_get extracts the first "<key>":"<value>" string from stdin.
json_get() { grep -o "\"$1\":\"[^\"]*\"" | head -1 | sed 's/.*":"\(.*\)"/\1/'; }

echo "› minting a client_credentials token from auth0-mock"
TOKEN=$(curl -sf -X POST "$TOKEN_URL" \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d "grant_type=client_credentials&client_id=${CLIENT_ID}&client_secret=${CLIENT_SECRET}&audience=${AUDIENCE}" \
  | json_get access_token)
[ -n "$TOKEN" ] || { echo "failed to obtain a token" >&2; exit 1; }
auth=(-H "Authorization: Bearer $TOKEN")

read -r -d '' MODEL <<'JSON' || true
{
  "schema_version": "1.1",
  "type_definitions": [
    { "type": "user" },
    { "type": "document",
      "relations": {
        "owner":  { "this": {} },
        "editor": { "union": { "child": [ { "this": {} }, { "computedUserset": { "relation": "owner" } } ] } },
        "viewer": { "union": { "child": [ { "this": {} }, { "computedUserset": { "relation": "editor" } } ] } }
      },
      "metadata": { "relations": {
        "owner":  { "directly_related_user_types": [ { "type": "user" } ] },
        "editor": { "directly_related_user_types": [ { "type": "user" } ] },
        "viewer": { "directly_related_user_types": [ { "type": "user" } ] }
      } }
    }
  ]
}
JSON

for name in "${STORES[@]}"; do
  echo "› store '$name'"
  store_id=$(curl -sf "${auth[@]}" -X POST "$API_URL/stores" \
    -H 'Content-Type: application/json' -d "{\"name\":\"$name\"}" | json_get id)
  echo "  id: $store_id"

  model_id=$(curl -sf "${auth[@]}" -X POST "$API_URL/stores/$store_id/authorization-models" \
    -H 'Content-Type: application/json' -d "$MODEL" | json_get authorization_model_id)
  echo "  model: $model_id"

  # Write tuples in chunks of 50 (a /write transaction caps at 100) and collect
  # matching assertions (user:userN viewer document:docN).
  write_chunk() {
    [ -n "$1" ] || return 0
    curl -sf "${auth[@]}" -X POST "$API_URL/stores/$store_id/write" \
      -H 'Content-Type: application/json' -d "{\"writes\":{\"tuple_keys\":[$1]}}" >/dev/null
  }
  chunk="" ; n=0 ; assertions=""
  for i in $(seq 1 "$COUNT"); do
    key="{\"user\":\"user:user$i\",\"relation\":\"viewer\",\"object\":\"document:doc$i\"}"
    chunk="$chunk${chunk:+,}$key"
    assertions="$assertions${assertions:+,}{\"tuple_key\":$key,\"expectation\":true}"
    n=$((n + 1))
    if [ "$n" -ge 50 ]; then write_chunk "$chunk"; chunk="" ; n=0; fi
  done
  write_chunk "$chunk"
  echo "  wrote $COUNT tuples"

  curl -sf "${auth[@]}" -X PUT "$API_URL/stores/$store_id/assertions/$model_id" \
    -H 'Content-Type: application/json' -d "{\"assertions\":[$assertions]}" >/dev/null
  echo "  wrote $COUNT assertions"
done

echo "✓ demo seeded: ${STORES[*]} (each: 1 model, $COUNT tuples, $COUNT assertions)"
