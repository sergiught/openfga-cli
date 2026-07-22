# examples/tapes/api.tape — raw `ofga api` command demo
# Regenerate: make demo && make gifs && make demo-down
Output examples/api.gif
Output docs/site/public/recordings/api.webm

Require ofga

Set Shell "bash"
Set FontFamily "MesloLGM Nerd Font Mono"
Set FontSize 20
Set Width 1200
Set Height 700
Set Padding 20
Set TypingSpeed 60ms
Set Theme "Catppuccin Mocha"

# --- hidden setup: isolate config, drop any demo-* stores left by earlier
# recordings so `GET /stores` stays readable, and capture the seeded `dev`
# store id for the second call ---
Hide
Source "examples/tapes/_setup.tape"
Type "for s in $(ofga stores list --plain | awk '/demo/{print $1}'); do ofga stores delete $s --force; done" Enter
Sleep 500ms
Type "STORE=$(ofga stores list --plain | awk -v n=dev '$2==n{print $1}')" Enter
Sleep 500ms
Type "clear" Enter
Show

# --- visible demo ---
Sleep 1s
Type "ofga api GET /stores" Sleep 400ms Enter
Sleep 2s
Type `ofga api POST /stores/$STORE/check '{"tuple_key":{"user":"user:user1","relation":"member","object":"organization:org1"}}'` Sleep 400ms Enter
Sleep 3s
