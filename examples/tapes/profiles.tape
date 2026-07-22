# examples/tapes/profiles.tape — profile management (`profiles add/use/show/list`) demo
# Regenerate: make demo && make gifs && make demo-down
Output examples/profiles.gif

Require ofga

Set Shell "bash"
Set FontFamily "MesloLGM Nerd Font Mono"
Set FontSize 20
Set Width 1200
Set Height 700
Set Padding 20
Set TypingSpeed 60ms
Set Theme "Catppuccin Mocha"

# --- hidden setup: isolate config and seed an existing `local` profile (via
# _setup.tape) so `profiles list` has more than one row. The demo stack isn't
# needed for what's shown here — this tape is about profile management, not
# connecting to a server. ---
Hide
Source "examples/tapes/_setup.tape"
Type "clear" Enter
Show

# --- visible demo ---
Sleep 1s
Type "printf 'demo-token-not-a-real-secret' | ofga profiles add staging --api-url https://fga.example.com --auth-method api_token --token-stdin" Sleep 400ms Enter
Sleep 1.5s
Type "ofga profiles use staging" Sleep 400ms Enter
Sleep 1.5s
Type "ofga profiles show" Sleep 400ms Enter
Sleep 1.5s
Type "ofga profiles list" Sleep 400ms Enter
Sleep 3s
