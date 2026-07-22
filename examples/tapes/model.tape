# examples/tapes/model.tape — model write/latest/graph command demo
# Regenerate: make demo && make gifs && make demo-down
Output examples/model.gif

Require ofga

Set Shell "bash"
Set FontFamily "MesloLGM Nerd Font Mono"
Set FontSize 20
Set Width 1200
Set Height 700
Set Padding 20
Set TypingSpeed 60ms
Set Theme "Catppuccin Mocha"

# --- hidden setup: isolate config + point at the `make demo` stack ---
Hide
Source "examples/tapes/_setup.tape"
Type "ofga stores create demo-model --use" Enter
Sleep 2s
Type "clear" Enter
Show

# --- visible demo ---
Sleep 1s
Type "ofga model write --file examples/model.fga" Sleep 400ms Enter
Sleep 2s
Type "ofga model latest" Sleep 400ms Enter
Sleep 2s
Type "ofga model graph" Sleep 400ms Enter
Sleep 3s
