# examples/tapes/model-test.tape — `ofga model test` with coverage against the
# hermetic embedded server (no Docker, no demo stack needed).
# Regenerate: make demo && make gifs && make demo-down
Output examples/model-test.gif

Require ofga

Set Shell "bash"
Set FontFamily "MesloLGM Nerd Font Mono"
Set FontSize 20
Set Width 1200
Set Height 920
Set Padding 20
Set TypingSpeed 60ms
Set Theme "Catppuccin Mocha"

# --- hidden setup: isolate config only; model test uses an in-process server ---
Hide
Type "export XDG_CONFIG_HOME=$(mktemp -d)" Enter
Type "clear" Enter
Show

# --- visible demo ---
Sleep 1s
Type "ofga model test examples/model-tests --coverage" Sleep 400ms Enter
Sleep 4s
