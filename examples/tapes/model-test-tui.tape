# examples/tapes/model-test-tui.tape — explore model-test results in the
# playground TUI. `ofga model test --playground` runs the suite against the
# hermetic embedded server, then opens the playground on the Tests tab seeded
# with the workspace's world so you can drill into every result.
# Regenerate: make demo && make gifs && make demo-down
Output examples/model-test-tui.gif

Require ofga

Set Shell "bash"
Set FontFamily "MesloLGM Nerd Font Mono"
Set FontSize 20
Set Width 2400
Set Height 1600
Set Padding 20
Set TypingSpeed 80ms
Set Theme "Catppuccin Mocha"

# --- hidden setup: isolate config only; the embedded server needs no stack ---
Hide
Type "export XDG_CONFIG_HOME=$(mktemp -d)" Enter
Type "clear" Enter
Show

# --- run the suite and open the playground on the Tests tab ---
Sleep 1s
Type "ofga model test examples/model-tests --playground" Sleep 400ms Enter
Sleep 5s

# Explore the Tests tab: move through the test tree and drill into a result,
# then run the suite in the TUI (r) and show the coverage report (c).
Enter Sleep 1400ms
Down Sleep 900ms
Down Sleep 900ms
Enter Sleep 1800ms
Type "r" Sleep 3500ms
Type "c" Sleep 4000ms
