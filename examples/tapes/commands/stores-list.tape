# examples/tapes/commands/stores-list.tape — `ofga stores list` site demo
# Regenerate: make demo && make recordings && make demo-down
Output docs/site/public/recordings/stores-list.webm

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
Type "ofga stores delete $(ofga stores list --plain | awk '/demo-stores-list/{print $1}') --force 2>/dev/null; true" Enter
Type "ofga stores create demo-stores-list --use" Enter
Type "clear" Enter
Show

Sleep 1s
Type "ofga stores list" Sleep 400ms Enter
Sleep 2s
Type "ofga stores list --plain" Sleep 400ms Enter
Sleep 3s
