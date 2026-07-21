# The interactive TUI

![The ofga playground TUI](../../examples/playground.gif)

Run `ofga` with no arguments to launch the interactive playground. It's a keyboard- **and mouse**-driven cockpit for the whole OpenFGA surface.

The playground uses the same resolved profile as CLI commands: `ofga --profile
staging playground` opens staging without changing the saved default. Profile,
store, and model switches made inside the playground are reflected immediately
in its footer and subsequent actions.

**Sections** (switch with `tab`, the number keys `1`–`9`, `ctrl+k` for the command palette, or **click a tab**): Profiles · Stores · Model · Tuples · Changes · Tuple Queries · Assertions · API Logs · Tests.

**Highlights**

- 🎨 **Model graph** — the authorization model rendered as a colored tree of types, relations, and inherited (tuple-to-userset) paths.
- 🔎 **Query + resolution tree** — run `check`/`list-objects`/`list-users`/`list-relations` and expand *why* a decision was made.
- ✍️ **Inline editing** — add/delete tuples, edit assertions, and edit the model DSL, with **inline validation** as you type.
- 🖱 **Full mouse support** — wheel-scroll the graph and lists, click tabs and list rows, click the footer keycaps as buttons, and click outside a dialog to dismiss it.
- 🎭 **Themes** — `aurora`, `catppuccin`, `charm`, `dracula`, `gruvbox`, `nord`, `tokyonight`, and a `mono` (NO_COLOR-friendly) theme.

**Keys:** press `?` at any time for the full, context-aware keybinding overlay.

> The TUI only launches on an interactive terminal. In a pipe or CI, bare `ofga` prints help instead of hanging.

## Keybinding reference

The `?` overlay renders this same list, filtered to the global keys plus whatever section you're currently in.

| Key | Context | Action |
| --- | --- | --- |
| `tab` / `→` `↓` / `j` / `l` | Sidebar (tab bar) | Move to the next tab |
| `shift+tab` / `←` `↑` / `k` / `h` | Sidebar (tab bar) | Move to the previous tab |
| `1`–`9` | Global | Jump straight to a section (1–5 rerun history instead, in Tuple Queries); `9` is Tests |
| `enter` | Sidebar (tab bar) | Enter the focused panel |
| `esc` | Panel | Return focus to the sidebar tabs (closes the resolution tree first if it's open) |
| `ctrl+k` | Global | Open the command palette |
| `?` | Global | Toggle the keybinding help overlay |
| `?` / `esc` / `q` / `enter` | Help overlay | Close the overlay |
| `q` | Sidebar (tab bar) | Quit |
| `ctrl+c` | Global | Quit immediately (always active, even inside overlays and forms) |
| `↑` `↓` | Profiles | Move selection |
| `/` | Profiles | Filter |
| `enter` | Profiles | Switch to the selected profile |
| `a` | Profiles | Add a profile |
| `e` | Profiles | Edit the selected profile |
| `d` | Profiles | Delete the selected profile |
| `↑` `↓` | Stores | Move selection |
| `/` | Stores | Filter |
| `enter` | Stores | Select the store |
| `a` | Stores | Add a store |
| `d` | Stores | Delete the selected store |
| `r` | Stores | Reload |
| `↑` `↓` / `k` `j` | Model | Scroll the graph |
| `←` `→` / `h` `l` | Model | Pan the graph |
| `pgup`/`pgdn`, `b`/`f`, `space` | Model | Page the graph |
| `g`/`home`, `G`/`end` | Model | Jump to top/bottom |
| `v` | Model | Toggle weighted-graph / diagram view |
| `e` | Model | Edit the model DSL |
| `m` | Model | Switch model |
| `r` | Model | Reload |
| `↑` `↓` | Tuples | Move selection |
| `/` | Tuples | Filter |
| `a` | Tuples | Add a tuple |
| `d` | Tuples | Delete the selected tuple |
| `r` | Tuples | Reload |
| `v` | Tuples, Changes, Assertions | Toggle the compact, full-width list view |
| `↑` `↓` | Changes | Move selection |
| `/` | Changes | Filter |
| `r` | Changes | Reload |
| `i` / `enter` | Tuple Queries | Edit the query |
| `tab` / `shift+tab` | Tuple Queries | Cycle the query mode forward/back |
| `m` | Tuple Queries | Browse query modes without entering the form |
| `1`–`5` | Tuple Queries | Rerun a query from recent history |
| `r` | Tuple Queries | Show the resolution tree for the last check |
| `ctrl+r` | Tuple Queries (while editing) | Jump straight to the resolution tree without leaving edit mode first |
| `r` / `esc` | Tuple Queries → resolution tree | Close the resolution tree |
| `p` | Tuple Queries → resolution tree | Toggle full tree / ACL-path-only view |
| `←` `→` / `h` `l` | Tuple Queries → resolution tree | Scroll horizontally |
| `↑` `↓` | Tuple Queries → resolution tree | Move within the tree |
| `↑` `↓` | Assertions | Move selection |
| `/` | Assertions | Filter |
| `enter` | Assertions | Run the assertion and open its resolution |
| `a` | Assertions | Add an assertion |
| `e` | Assertions | Edit the selected assertion |
| `d` | Assertions | Delete the selected assertion |
| `t` | Assertions | Run all assertions |
| `r` | Assertions | Reload |
| `↑` `↓` | API Logs | Select a request |
| `tab` / `shift+tab` | API Logs | Cycle the detail sub-section (Req/Resp headers/body) |
| `j` `k` | API Logs | Scroll the detail section |
| `pgup`/`pgdn`, `b`/`f`, `space` | API Logs | Page the detail section |
| `←` `→` | API Logs | Scroll the URL |
| `c` | API Logs | Toggle readable / compact bodies |
| `x` | API Logs | Clear the log |
| `wheel` | API Logs | Scroll the list or the detail pane |
| `↑` `↓` / `j` `k` | Tests | Move selection |
| `enter` / `space` | Tests | Expand a file / show the test explanation |
| `n` | Tests | New test file |
| `e` | Tests | Edit the test file |
| `d` | Tests | Delete the test file |
| `r` | Tests | Run the suite |
| `R` | Tests | Run the selected file |
| `c` | Tests | Toggle the coverage report |
| `v` | Tests | Toggle the explanation panel |
| `esc` | Tests | Back to the sidebar |
| `ctrl+s` | Model → Edit DSL | Apply the model |
| `esc` | Model → Edit DSL | Cancel (confirms first if the buffer has unsaved edits) |
| `esc` | Add/edit forms (tuple, assertion, profile, store) | Cancel and close the form |
| `wheel` | Model, Tuple Queries → resolution tree, Tests, list sections | Scroll the graph, resolution tree, tree/coverage/detail pane, or move the list selection |
| click a tab | Global (sidebar) | Jump to that section |
| click a footer keycap | Global | Invoke that key's action, as a button |
| click a list row | List sections (Profiles, Stores, Tuples, Changes, Assertions) | Select that row |
| click a query mode chip | Tuple Queries | Switch query mode |
| click the resolution header | Tuple Queries → resolution tree | Toggle full tree / ACL path, or close the tree (mirrors `p` and `r`/`esc`) |
| click a detail tab | API Logs | Switch the Req/Resp headers/body sub-section |
| click a tree row | Tests | Select that file or test |
| click outside the dialog | Global (any open dialog) | Dismiss it, same as `esc` |
