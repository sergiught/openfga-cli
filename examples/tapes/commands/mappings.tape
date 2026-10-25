# examples/tapes/commands/mappings.tape — `ofga mappings` workspace site demo
# Regenerate: make demo && make recordings && make demo-down
#
# Runs on a temp copy of examples/mappings and never saves or applies: the
# Apply pane only plans. The window is larger than the other command tapes so
# the workspace shows the preview beside the Rules pane.
Output docs/site/public/recordings/mappings.webm

Require ofga

Set Shell "bash"
Set FontFamily "MesloLGM Nerd Font Mono"
Set FontSize 20
Set Width 2000
Set Height 1100
Set Padding 20
Set TypingSpeed 60ms
Set Theme "Catppuccin Mocha"

Hide
Source "examples/tapes/_setup.tape"
Type "ofga stores delete $(ofga stores list --plain | awk '/demo-mappings/{print $1}') --force 2>/dev/null; true" Enter
Type "ofga stores create demo-mappings --use" Enter
Type "ofga model write --file examples/mappings/model.fga" Enter
Type `cd "$(mktemp -d)" && cp "$OLDPWD"/examples/mappings/* .` Enter
Type "clear" Enter
Show

# Start screen: auth0.yaml is the first file found here.
Sleep 1s
Type "ofga mappings" Sleep 400ms Enter
Sleep 2s
Enter Sleep 2s

# The model dialog preselects model.fga, the file beside the mapping.
Enter Sleep 2s

# Rules: open the first rule; the preview shows the tuples its sample makes.
Enter Sleep 3s

# Check, then Tests.
Type "2" Sleep 2500ms
Type "3" Sleep 2500ms

# Apply: select every listed event, plan them, and stop at the plan.
Type "4" Sleep 2s
Space Sleep 400ms
Down Space Sleep 400ms
Down Space Sleep 400ms
Down Space Sleep 800ms
Type "p" Sleep 3500ms
Escape Sleep 1s
Type "q" Sleep 1500ms
