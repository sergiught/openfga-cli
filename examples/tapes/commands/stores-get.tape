# examples/tapes/commands/stores-get.tape — `ofga stores get` site demo
# Regenerate: make demo && make recordings && make demo-down
Output docs/site/public/recordings/stores-get.webm

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
Type "ofga stores delete $(ofga stores list --plain | awk '/demo-stores-get/{print $1}') --force 2>/dev/null; true" Enter
Type "ofga stores create demo-stores-get --use" Enter
Type "STORE=$(ofga stores list --plain | awk '/demo-stores-get/{print $1}')" Enter
Type "clear" Enter
Show

Sleep 1s
Type "ofga stores get $STORE" Sleep 400ms Enter
Sleep 3s
