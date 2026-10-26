# examples/tapes/commands/mappings-apply.tape — `ofga mappings apply` site demo
# Regenerate: make demo && make recordings && make demo-down
Output docs/site/public/recordings/mappings-apply.webm

Require ofga

Set Shell "bash"
Set FontFamily "MesloLGM Nerd Font Mono"
Set FontSize 20
# 1400x900: wide enough for the plan lines; the tuples tape uses 1200.
Set Width 1400
Set Height 900
Set Padding 20
Set TypingSpeed 60ms
Set Theme "Catppuccin Mocha"

Hide
Source "examples/tapes/_setup.tape"
Type "ofga stores delete $(ofga stores list --plain | awk '/demo-mappings/{print $1}') --force 2>/dev/null; true" Enter
Type "ofga stores create demo-mappings --use" Enter
Type "ofga model write --file examples/mappings/model.fga" Enter
Type "clear" Enter
Show

Sleep 1s
Type "ofga mappings apply examples/mappings/auth0.yaml --events examples/mappings/events.jsonl" Sleep 400ms Enter
Sleep 3s
Type "y" Sleep 400ms Enter
Sleep 2s
Type "ofga tuples read" Sleep 400ms Enter
Sleep 3s
