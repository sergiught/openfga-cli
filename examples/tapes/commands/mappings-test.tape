# examples/tapes/commands/mappings-test.tape — `ofga mappings test` site demo
# Regenerate: make demo && make recordings && make demo-down
Output docs/site/public/recordings/mappings-test.webm

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
Type "ofga mappings test examples/mappings/auth0.yaml" Sleep 400ms Enter
Sleep 3s
