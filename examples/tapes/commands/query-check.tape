# examples/tapes/commands/query-check.tape — `ofga query check` site demo
# Regenerate: make demo && make recordings && make demo-down
Output docs/site/public/recordings/query-check.webm

Require ofga

Set Shell "bash"
Set FontFamily "MesloLGM Nerd Font Mono"
Set FontSize 20
Set Width 1200
Set Height 700
Set Padding 20
Set TypingSpeed 60ms
Set Theme "Catppuccin Mocha"

Hide
Source "examples/tapes/_setup.tape"
Type "ofga stores delete $(ofga stores list --plain | awk '/demo-query-check/{print $1}') --force 2>/dev/null; true" Enter
Type "ofga stores create demo-query-check --use" Enter
Type "ofga model write --file examples/model.fga" Enter
Type "ofga tuples write user:anne writer repo:acme/backend" Enter
Type "ofga tuples write user:bob reader repo:acme/backend" Enter
Type "clear" Enter
Show

Sleep 1s
Type "ofga query check user:anne writer repo:acme/backend" Sleep 400ms Enter
Sleep 2s
Type "ofga query check user:bob admin repo:acme/backend" Sleep 400ms Enter
Sleep 3s
