# examples/tapes/assertions.tape — assertions write/test command demo
# Regenerate: make demo && make gifs && make demo-down
Output examples/assertions.gif

Require ofga

Set Shell "bash"
Set FontFamily "MesloLGM Nerd Font Mono"
Set FontSize 20
Set Width 1200
Set Height 700
Set Padding 20
Set TypingSpeed 60ms
Set Theme "Catppuccin Mocha"

# --- hidden setup: isolate config + point at the `make demo` stack ---
Hide
Source "examples/tapes/_setup.tape"
Type "ofga stores create demo-assertions --use" Enter
Sleep 2s
Type "ofga model write --file examples/model.fga" Enter
Sleep 2s
Type "ofga tuples write user:anne writer repo:acme/backend" Enter
Sleep 1s
Type `printf '[{"tuple_key":{"user":"user:anne","relation":"writer","object":"repo:acme/backend"},"expectation":true},{"tuple_key":{"user":"user:bob","relation":"admin","object":"repo:acme/backend"},"expectation":false}]' > assertions.json` Enter
Sleep 500ms
Type "clear" Enter
Show

# --- visible demo ---
Sleep 1s
Type "ofga assertions write --file assertions.json --force" Sleep 400ms Enter
Sleep 2s
Type "ofga assertions test" Sleep 400ms Enter
Sleep 2s
