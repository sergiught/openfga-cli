# examples/tapes/stores.tape — stores create/list command demo
# Regenerate: make demo && make gifs && make demo-down
Output examples/stores.gif

Require ofga

Set Shell "bash"
Set FontFamily "MesloLGM Nerd Font Mono"
Set FontSize 20
Set Width 1200
Set Height 700
Set Padding 20
Set TypingSpeed 60ms
Set Theme "Catppuccin Mocha"

# --- hidden setup: isolate config, point at the `make demo` stack, and drop
# any demo-* stores left by earlier recordings so the list stays readable ---
Hide
Source "examples/tapes/_setup.tape"
Type "for s in $(ofga stores list --plain | awk '/demo/{print $1}'); do ofga stores delete $s --force; done" Enter
Sleep 500ms
Type "clear" Enter
Show

# --- visible demo ---
Sleep 1s
Type "ofga stores create demo-stores --use" Sleep 400ms Enter
Sleep 2s
Type "ofga stores list" Sleep 400ms Enter
Sleep 3s
