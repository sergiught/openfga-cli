# examples/tapes/tuples.tape — tuples write/read/changes command demo
# Regenerate: make demo && make gifs && make demo-down
Output examples/tuples.gif

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
Type "ofga stores create demo-tuples --use" Enter
Sleep 2s
Type "ofga model write --file examples/model.fga" Enter
Sleep 2s
Type "clear" Enter
Show

# --- visible demo ---
Sleep 1s
Type "ofga tuples write user:anne writer repo:acme/backend" Sleep 400ms Enter
Sleep 2s
Type "ofga tuples read" Sleep 400ms Enter
Sleep 2s
Type "ofga tuples changes" Sleep 400ms Enter
Sleep 2s
