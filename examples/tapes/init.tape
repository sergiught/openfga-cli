# examples/tapes/init.tape — guided `ofga init` first-run setup demo
# Regenerate: make demo && make gifs && make demo-down
Output examples/init.gif

Require ofga

Set Shell "bash"
Set FontFamily "MesloLGM Nerd Font Mono"
Set FontSize 20
Set Width 1200
Set Height 1050
Set Padding 20
Set TypingSpeed 60ms
Set Theme "Catppuccin Mocha"

# --- hidden setup: isolate config only. Unlike other tapes this does NOT
# Source _setup.tape — that would create an on-disk config and make `init`
# ask an overwrite-confirm before the guided tour even starts. `init` needs a
# fresh, empty config for its first-run tour. ---
Hide
Type "export XDG_CONFIG_HOME=$(mktemp -d)" Enter
Type "clear" Enter
Show

# --- visible demo ---
# `ofga init` runs an interactive bubbletea wizard whenever stdin/stdout are a
# real terminal — which they are under vhs — so this drives the guided tour's
# steps (Welcome, Server, Auth, Test, Store, Model, Review), not the plain
# flag-prompt fallback used in non-interactive shells. The keystrokes below:
# accept the default API URL, pick "OAuth2 client credentials" (2nd option in
# the auth-method picker) and fill in the demo stack's real creds so the
# connection test passes green, then skip store/model.
Sleep 500ms
Type "ofga init" Sleep 400ms Enter
Sleep 1.2s
Enter
Sleep 1s
Enter
Sleep 1s
Down
Sleep 300ms
Enter
Sleep 800ms
Type "demo" Sleep 300ms Enter
Sleep 500ms
Type "demo-secret" Sleep 300ms Enter
Sleep 500ms
Type "http://localhost:3000/oauth/token" Sleep 300ms Enter
Sleep 500ms
Type "https://api.openfga.local/" Sleep 300ms Enter
Sleep 500ms
Enter
Sleep 3s
Enter
Sleep 1.2s
Down Sleep 200ms
Down Sleep 200ms
Down Sleep 200ms
Down Sleep 200ms
Enter
Sleep 1.2s
Enter
Sleep 2.5s
