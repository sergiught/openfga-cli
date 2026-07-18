// Package playground implements the interactive `ofga` TUI: a full-screen,
// Crush-style shell for exploring a store — picking models, browsing tuples,
// running live checks, and visualizing the authorization model as a graph.
// The model pane offers two views, toggled with `v`: the node-link diagram
// (types and relations) and the weighted graph (the fully-expanded resolution
// graph with per-terminal-type weights, in the style of model-visualizer).
// It is launched by the bare `ofga` command (there is no named subcommand).
package playground
