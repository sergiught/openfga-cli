# examples/tapes/playground-optimize.tape — the playground's Model-tab optimizer:
# benchmark the model (B), review the safe rewrites (o), tick one, and write it (w, y).
# Regenerate: make demo && make gifs && make demo-down
Output examples/playground-optimize.gif
Output docs/site/public/recordings/playground-optimize.webm

Require ofga

Set Shell "bash"
Set FontFamily "MesloLGM Nerd Font Mono"
Set FontSize 20
Set Width 2000
Set Height 1300
Set Padding 20
Set TypingSpeed 80ms
Set Theme "Catppuccin Mocha"

# --- hidden setup: isolate config, point at the `make demo` stack, and seed a
# fresh store with the folder/document hierarchy example (one safe rewrite plus
# two advisories). The benchmark synthesizes its own data, so no tuples. ---
Hide
Type "export XDG_CONFIG_HOME=$(mktemp -d)" Enter
Type "ofga profiles add local --api-url http://localhost:8080 && ofga profiles use local" Enter
Type "ofga profiles set auth_method client_credentials" Enter
Type "ofga profiles set client_id demo" Enter
Type "printf 'demo-secret' | ofga profiles set client_secret --value-stdin" Enter
Type "ofga profiles set token_url http://localhost:3000/oauth/token" Enter
Type "ofga profiles set audience https://api.openfga.local/" Enter
Type "ofga store create optimize-demo --use" Enter Sleep 1s
Type "ofga model write --file examples/model-optimize/hierarchy.fga" Enter Sleep 1s
Type "clear" Enter
Show

# --- launch the TUI on the fresh store and open the Model tab ---
Type "ofga" Enter
Sleep 2s
Type "1" Sleep 600ms
Down Sleep 600ms
Down Sleep 1s
Enter Sleep 1200ms

# Benchmark the model on synthesized data, then step through the check rows.
Type "B" Sleep 3s
Down Sleep 1s
Down Sleep 1500ms
Escape Sleep 1s

# Review the safe rewrites: open an advisory to see its cost, then tick the rewrite.
Type "o" Sleep 2s
Down Sleep 1500ms
Down Sleep 2s
Up Sleep 700ms
Up Sleep 700ms
Space Sleep 1500ms

# Write: the dialog shows the diff, checks answers, then writes a new model version.
Type "w" Sleep 4s
Type "y" Sleep 3s

# quit cleanly
Ctrl+C
Sleep 1s
