# examples/tapes/commands/completion-bash.tape — `ofga completion bash` site demo
# Regenerate: make demo && make recordings && make demo-down
Output docs/site/public/recordings/completion-bash.webm

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
Type "source <(ofga completion bash)" Sleep 400ms Enter
Sleep 1s
Type "ofga stor" Sleep 500ms Tab
Sleep 3s
