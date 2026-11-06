# examples/tapes/commands/playground.tape — `ofga playground` site demo
# Sandbox tour: no server needed. Regenerate: make recordings (or vhs this file with ./bin on PATH)
Output docs/site/public/recordings/playground.webm

Require ofga

Set Shell "bash"
Set FontFamily "MesloLGM Nerd Font Mono"
Set FontSize 20
Set Width 1200
Set Height 700
Set Padding 20
Set TypingSpeed 60ms
Set Theme "Catppuccin Mocha"

# Isolate: never touch the real config or the real playground database.
# (Env must come AFTER the Set block, or vhs 0.12.1 drops the height to 600px.)
Env OPENFGA_AGENT_MODE "0"
Env OPENFGA_CONFIG "/tmp/ofga-tape-playground/config.toml"
Env OPENFGA_PLAYGROUND_DB "/tmp/ofga-tape-playground/playground.db"

Sleep 1s
Type "ofga playground" Sleep 400ms Enter
Sleep 4s

# Stores: seven samples are preloaded. Open github.
Type "2" Sleep 1500ms
Enter Sleep 800ms
Enter Sleep 1500ms

# Tuple Queries: is charles an admin of the repo, through team membership?
Type "6" Sleep 1200ms
Type "user:charles" Sleep 300ms
Enter Sleep 300ms
Type "admin" Sleep 300ms
Enter Sleep 300ms
Type "repo:openfga/openfga" Sleep 300ms
Enter Sleep 300ms
Enter Sleep 2500ms
Escape Sleep 800ms

# Tests: the sample's own test suite.
Type "9" Sleep 1800ms
Type "r" Sleep 3500ms
Escape Sleep 800ms

# Browsing and checking change nothing, so quitting asks nothing.
Type "q"
Sleep 2s
