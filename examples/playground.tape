# examples/playground.tape — TUI hero demo for the ofga playground.
# Regenerate: make demo && make gifs && make demo-down
Output examples/playground.gif

Require ofga

Set Shell "bash"
Set FontSize 20
Set Width 1200
Set Height 800
Set Padding 20
Set TypingSpeed 80ms
Set Theme "Catppuccin Mocha"

# --- hidden setup: isolate config + point at the `make demo` stack ---
Hide
Type "export XDG_CONFIG_HOME=$(mktemp -d)" Enter
Type "ofga profiles add local --api-url http://localhost:8080 && ofga profiles use local" Enter
Type "ofga profiles set auth_method client_credentials" Enter
Type "ofga profiles set client_id demo" Enter
Type "printf 'demo-secret' | ofga profiles set client_secret --value-stdin" Enter
Type "ofga profiles set token_url http://localhost:3000/oauth/token" Enter
Type "ofga profiles set audience https://api.openfga.local/" Enter
Type "clear" Enter
Show

# --- launch the TUI and walk the sections ---
Type "ofga" Enter
Sleep 2s

# Stores: descend, pick the first store, back out
Type "2" Sleep 1200ms
Enter Sleep 1500ms
Enter Sleep 1500ms
Escape Sleep 1s

# Model
Type "3" Sleep 1200ms
Enter Sleep 2s
Escape Sleep 1s

# Tuples
Type "4" Sleep 1200ms
Enter Sleep 2s
Escape Sleep 1s

# API Logs — live request/response
Type "8" Sleep 1200ms
Enter Sleep 3s
Escape Sleep 800ms

# quit cleanly
Ctrl+C
Sleep 1s
