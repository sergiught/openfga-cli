# examples/tapes/commands/tuples-write.tape — `ofga tuples write` site demo
# Regenerate: make demo && make recordings && make demo-down
Output docs/site/public/recordings/tuples-write.webm

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
Type "ofga stores delete $(ofga stores list --plain | awk '/demo-tuples-write/{print $1}') --force 2>/dev/null; true" Enter
Type "ofga stores create demo-tuples-write --use" Enter
Type "ofga model write --file examples/model.fga" Enter
Type "clear" Enter
Show

Sleep 1s
Type "ofga tuples write user:anne writer repo:acme/backend" Sleep 400ms Enter
Sleep 2s
Type "ofga tuples read" Sleep 400ms Enter
Sleep 3s
