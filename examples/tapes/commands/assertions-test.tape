# examples/tapes/commands/assertions-test.tape — `ofga assertions test` site demo
# Regenerate: make demo && make recordings && make demo-down
Output docs/site/public/recordings/assertions-test.webm

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
Type "ofga stores delete $(ofga stores list --plain | awk '/demo-assertions-test/{print $1}') --force 2>/dev/null; true" Enter
Type "ofga stores create demo-assertions-test --use" Enter
Type "ofga model write --file examples/model.fga" Enter
Type "ofga tuples write user:anne writer repo:acme/backend" Enter
Type `printf '[{"tuple_key":{"user":"user:anne","relation":"writer","object":"repo:acme/backend"},"expectation":true},{"tuple_key":{"user":"user:bob","relation":"admin","object":"repo:acme/backend"},"expectation":false}]' > /tmp/demo-assertions.json` Enter
Type "ofga assertions write --file /tmp/demo-assertions.json --force" Enter
Type "clear" Enter
Show

Sleep 1s
Type "ofga assertions test" Sleep 400ms Enter
Sleep 3s
