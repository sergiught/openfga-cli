# examples/tapes/commands/profiles-use.tape — `ofga profiles use` site demo
# Regenerate: make demo && make recordings && make demo-down
Output docs/site/public/recordings/profiles-use.webm

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
Type "printf 'demo-token-not-a-real-secret' | ofga profiles add staging --api-url https://fga.example.com --auth-method api_token --token-stdin" Enter
Type "clear" Enter
Show

Sleep 1s
Type "ofga profiles use staging" Sleep 400ms Enter
Sleep 2s
Type "ofga profiles current" Sleep 400ms Enter
Sleep 3s
