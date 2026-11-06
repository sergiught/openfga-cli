# examples/tapes/model-diff.tape — `ofga model diff` between two model files:
# breaking, changed, added and cost sections, plus the exit code CI gates on.
# File-to-file diffing never talks to a server, but `make gifs` still expects
# the demo stack: make demo && make gifs && make demo-down
Output examples/model-diff.gif
Output docs/site/public/recordings/model-diff.webm

Require ofga

Set Shell "bash"
Set FontFamily "MesloLGM Nerd Font Mono"
Set FontSize 20
Set Width 1460
Set Height 720
Set Padding 20
Set TypingSpeed 60ms
Set Theme "Catppuccin Mocha"

Env OPENFGA_AGENT_MODE "0"

# --- hidden setup: isolate config only ---
Hide
Type "export XDG_CONFIG_HOME=$(mktemp -d)" Enter
Type "clear" Enter
Show

# --- visible demo ---
Sleep 1s
Type "ofga model diff examples/model-diff/old.fga examples/model-diff/new.fga" Sleep 400ms Enter
Sleep 2s
Type "echo exit=$?" Sleep 300ms Enter
Sleep 5s
