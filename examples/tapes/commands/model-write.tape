# examples/tapes/commands/model-write.tape — `ofga model write` site demo
# Regenerate: make demo && make recordings && make demo-down
Output docs/site/public/recordings/model-write.webm

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
Type "ofga stores delete $(ofga stores list --plain | awk '/demo-model-write/{print $1}') --force 2>/dev/null; true" Enter
Type "ofga stores create demo-model-write --use" Enter
Type "clear" Enter
Show

Sleep 1s
Type "ofga model write --file examples/model.fga" Sleep 400ms Enter
Sleep 3s
