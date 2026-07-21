# examples/tapes/_setup.tape — shared hidden setup, Source-able from other
# command tapes. Isolates config and points the CLI at the `make demo` stack.
#
# Contract for callers (the path after Source must be quoted):
#   Hide
#   Source "examples/tapes/_setup.tape"
#   Type "..."  # per-tape store/model/tuple seeding
#   Enter
#   Type "clear" Enter
#   Show
#   # visible commands
#
# This file must stay Source-able: no Output/Set/Require/Hide/Show directives
# here — the caller owns Hide/Show and everything Output/Set/Require related.
Type "export XDG_CONFIG_HOME=$(mktemp -d)" Enter
Type "ofga profiles add local --api-url http://localhost:8080 && ofga profiles use local" Enter
Type "ofga profiles set auth_method client_credentials" Enter
Type "ofga profiles set client_id demo" Enter
Type "printf 'demo-secret' | ofga profiles set client_secret --value-stdin" Enter
Type "ofga profiles set token_url http://localhost:3000/oauth/token" Enter
Type "ofga profiles set audience https://api.openfga.local/" Enter
