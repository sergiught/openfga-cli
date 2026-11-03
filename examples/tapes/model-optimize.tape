# examples/tapes/model-optimize.tape — `ofga model optimize` findings, diff and
# before/after benchmark on the embedded server (no Docker, no demo stack needed).
# Regenerate: make demo && make gifs && make demo-down
Output examples/model-optimize.gif

Require ofga

Set Shell "bash"
Set FontFamily "MesloLGM Nerd Font Mono"
Set FontSize 20
Set Width 1200
Set Height 920
Set Padding 20
Set TypingSpeed 60ms
Set Theme "Catppuccin Mocha"

# --- hidden setup: isolate config only; model optimize never talks to a server ---
Hide
Type "export XDG_CONFIG_HOME=$(mktemp -d)" Enter
Type "clear" Enter
Show

# --- visible demo ---
Sleep 1s
Type "ofga model optimize examples/model-optimize/roles.fga" Sleep 400ms Enter
Sleep 4s
Type "clear" Enter
Type "ofga model optimize examples/model-optimize/hierarchy.fga --bench --iterations 20 --tier safe" Sleep 400ms Enter
Sleep 6s
