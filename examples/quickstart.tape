# examples/quickstart.tape — CLI quickstart demo for the ofga CLI.
# Regenerate: make demo && make gifs && make demo-down
Output examples/quickstart.gif

Require ofga

Set Shell "bash"
Set FontFamily "MesloLGM Nerd Font Mono"
Set FontSize 20
Set Width 2400
Set Height 1400
Set Padding 20
Set TypingSpeed 60ms
Set Theme "Catppuccin Mocha"

# --- hidden setup: isolate config + point at the `make demo` stack ---
Hide
Type "export XDG_CONFIG_HOME=$(mktemp -d)" Enter
Type "cd examples" Enter
Type "ofga profiles add local --api-url http://localhost:8080 && ofga profiles use local" Enter
Type "ofga profiles set auth_method client_credentials" Enter
Type "ofga profiles set client_id demo" Enter
Type "printf 'demo-secret' | ofga profiles set client_secret --value-stdin" Enter
Type "ofga profiles set token_url http://localhost:3000/oauth/token" Enter
Type "ofga profiles set audience https://api.openfga.local/" Enter
Type "clear" Enter
Show

# --- visible demo ---
Sleep 1s
Type "ofga store create demo --use" Sleep 400ms Enter
Sleep 2s
Type "ofga model write --file model.json" Sleep 400ms Enter
Sleep 2s
Type "ofga tuple write user:anne writer repo:openfga/openfga" Sleep 400ms Enter
Sleep 2s
Type "ofga query check user:anne writer repo:openfga/openfga" Sleep 400ms Enter
Sleep 3s
