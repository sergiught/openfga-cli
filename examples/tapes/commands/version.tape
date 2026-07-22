# examples/tapes/commands/version.tape — `ofga version` site demo
# Regenerate: make demo && make recordings && make demo-down
Output docs/site/public/recordings/version.webm

Require ofga

Set Shell "bash"
Set FontFamily "MesloLGM Nerd Font Mono"
Set FontSize 20
Set Width 1200
Set Height 700
Set Padding 20
Set TypingSpeed 60ms
Set Theme "Catppuccin Mocha"

Sleep 1s
Type "ofga version" Sleep 400ms Enter
Sleep 3s
