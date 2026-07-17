# examples/playground.tape — TUI hero demo for the ofga playground.
# Regenerate: make demo && make gifs && make demo-down
Output examples/playground.gif

Require ofga

Set Shell "bash"
Set FontFamily "MesloLGM Nerd Font Mono"
Set FontSize 20
Set Width 2400
Set Height 1600
Set Padding 20
Set TypingSpeed 80ms
Set Theme "Catppuccin Mocha"

# --- hidden setup: isolate config, point at the `make demo` stack, and seed a
# fresh store (model + a few tuples, zero assertions) so the add flows below
# have headroom (the seeded dev/staging/prod stores are already at the 100
# assertion limit). ---
Hide
Type "export XDG_CONFIG_HOME=$(mktemp -d)" Enter
Type "ofga profiles add local --api-url http://localhost:8080 && ofga profiles use local" Enter
Type "ofga profiles set auth_method client_credentials" Enter
Type "ofga profiles set client_id demo" Enter
Type "printf 'demo-secret' | ofga profiles set client_secret --value-stdin" Enter
Type "ofga profiles set token_url http://localhost:3000/oauth/token" Enter
Type "ofga profiles set audience https://api.openfga.local/" Enter
Type "ofga store create playground --use" Enter Sleep 1s
Type "ofga model write --file examples/model.fga" Enter Sleep 1s
Type "ofga tuple write user:anne admin repo:acme/backend" Enter Sleep 400ms
Type "ofga tuple write user:bob writer repo:acme/backend" Enter Sleep 400ms
Type "ofga tuple write user:carol reader repo:acme/frontend" Enter Sleep 400ms
Type "ofga tuple write user:dave maintainer repo:acme/frontend" Enter Sleep 400ms
Type "ofga tuple write user:erin triager repo:acme/backend" Enter Sleep 800ms
Type "clear" Enter
Show

# --- launch the TUI (starts on the fresh 'playground' store) ---
Type "ofga" Enter
Sleep 2s

# Walk the tabs top to bottom, pausing at each to show what it does.
# Profiles and Stores just pass through — nothing to demo there.
Type "1" Sleep 1200ms
Down Sleep 1200ms

# Model: descend into the panel and scroll the colored relation graph.
Down Sleep 1s
Enter Sleep 1s
Down Sleep 700ms
Down Sleep 700ms
Down Sleep 900ms
Escape Sleep 1s

# Tuples: add one.
Down Sleep 1200ms
Type "a" Sleep 1s
Type "user:vhs" Sleep 300ms
Enter Sleep 300ms
Type "writer" Sleep 300ms
Enter Sleep 300ms
Type "repo:acme/vhs-demo" Sleep 300ms
Enter Sleep 300ms
Enter Sleep 300ms
Enter Sleep 1800ms
Escape Sleep 1s

# Changes: the write we just made shows at the top of the changelog.
Down Sleep 1200ms
Enter Sleep 1800ms
Escape Sleep 1s

# Tuple Queries: run a check that's allowed, then open its resolution.
Down Sleep 1200ms
Enter Sleep 800ms
Type "user:vhs" Sleep 300ms
Enter Sleep 300ms
Type "reader" Sleep 300ms
Enter Sleep 300ms
Type "repo:acme/vhs-demo" Sleep 300ms
Enter Sleep 300ms
Enter Sleep 1800ms
Ctrl+R Sleep 2500ms
Escape Sleep 600ms
Escape Sleep 1s

# Assertions: add one.
Down Sleep 1200ms
Type "a" Sleep 1s
Type "user:vhs" Sleep 300ms
Enter Sleep 300ms
Type "reader" Sleep 300ms
Enter Sleep 300ms
Type "repo:acme/vhs-demo" Sleep 300ms
Enter Sleep 300ms
Enter Sleep 300ms
Enter Sleep 300ms
Enter Sleep 1800ms
Escape Sleep 1s

# API Logs: scroll through a request and its response.
Down Sleep 1200ms
Enter Sleep 1s
Tab Sleep 900ms
Type "j" Sleep 400ms
Type "j" Sleep 600ms
Tab Sleep 900ms
Tab Sleep 900ms
Type "j" Sleep 400ms
Type "j" Sleep 900ms
Escape Sleep 800ms

# quit cleanly
Ctrl+C
Sleep 1s
