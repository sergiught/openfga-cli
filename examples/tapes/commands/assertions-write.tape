# examples/tapes/commands/assertions-write.tape — `ofga assertions write` site demo
# Regenerate: make demo && make recordings && make demo-down
Output docs/site/public/recordings/assertions-write.webm

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
Type "ofga stores delete $(ofga stores list --plain | awk '/demo-assertions-write/{print $1}') --force 2>/dev/null; true" Enter
Type "ofga stores create demo-assertions-write --use" Enter
Type "ofga model write --file examples/model.fga" Enter
Type "ofga tuples write user:anne writer repo:acme/backend" Enter
Type `printf '[{"tuple_key":{"user":"user:anne","relation":"writer","object":"repo:acme/backend"},"expectation":true},{"tuple_key":{"user":"user:bob","relation":"admin","object":"repo:acme/backend"},"expectation":false}]' > /tmp/demo-assertions.json` Enter
Type "clear" Enter
Show

Sleep 1s
Type "ofga assertions write --file /tmp/demo-assertions.json --force" Sleep 400ms Enter
Sleep 3s
