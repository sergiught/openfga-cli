# examples/tapes/query.tape — query check/list-objects/list-users command demo
# Regenerate: make demo && make gifs && make demo-down
Output examples/query.gif

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
Type "ofga stores create demo-query --use" Enter
Sleep 2s
Type "ofga model write --file examples/model.fga" Enter
Sleep 2s
Type "ofga tuples write user:anne writer repo:acme/backend" Enter
Sleep 1s
Type "ofga tuples write user:bob reader repo:acme/backend" Enter
Sleep 1s
Type "clear" Enter
Show

# --- visible demo ---
Sleep 1s
Type "ofga query check user:anne writer repo:acme/backend" Sleep 400ms Enter
Sleep 2s
Type "ofga query list-objects repo writer user:anne" Sleep 400ms Enter
Sleep 2s
Type "ofga query list-users repo:acme/backend writer --type user" Sleep 400ms Enter
Sleep 2s
