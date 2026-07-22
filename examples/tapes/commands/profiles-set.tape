# examples/tapes/commands/profiles-set.tape — `ofga profiles set` site demo
# Regenerate: make demo && make recordings && make demo-down
Output docs/site/public/recordings/profiles-set.webm

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
Type "clear" Enter
Show

Sleep 1s
Type "ofga profiles set store_id 01ARZ3NDEKTSV4RRFFQ69G5FAV" Sleep 400ms Enter
Sleep 2s
Type "ofga profiles show" Sleep 400ms Enter
Sleep 3s
