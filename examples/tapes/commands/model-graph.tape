# examples/tapes/commands/model-graph.tape — `ofga model graph` site demo
# Regenerate: make demo && make recordings && make demo-down
Output docs/site/public/recordings/model-graph.webm

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
Type "ofga stores delete $(ofga stores list --plain | awk '/demo-model-latest/{print $1}') --force 2>/dev/null; true" Enter
Type "ofga stores create demo-model-latest --use" Enter
Type "ofga model write --file examples/model.fga" Enter
Type "clear" Enter
Show

Sleep 1s
Type "ofga model graph" Sleep 400ms Enter
Sleep 5s
