# examples/tapes/commands/model-test.tape — `ofga model test` site demo
# Regenerate: make demo && make recordings && make demo-down
Output docs/site/public/recordings/model-test.webm

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
Type "export XDG_CONFIG_HOME=$(mktemp -d)" Enter
Type "clear" Enter
Show

Sleep 1s
Type "ofga model test examples/model-tests --coverage" Sleep 400ms Enter
Sleep 5s
