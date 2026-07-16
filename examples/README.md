# Example recordings

Demo GIFs for the `ofga` CLI and TUI, generated with
[charmbracelet/vhs](https://github.com/charmbracelet/vhs).

| GIF | Shows |
| --- | --- |
| `playground.gif` | The interactive `ofga` playground TUI: browsing stores, model, tuples, and live API logs. |
| `quickstart.gif` | A CLI flow: create a store, write a model, write a tuple, and check access. |

## Regenerating

Requires [`vhs`](https://github.com/charmbracelet/vhs) and its runtime
dependencies (`ttyd`, `ffmpeg`) on your `PATH`.

````bash
make demo        # bring up + seed the local OpenFGA + auth0-mock stack
make gifs        # record both tapes into examples/*.gif
make demo-down   # tear the stack down
````

`make gifs` builds `bin/ofga`, checks the demo stack is reachable on
`localhost:8080`, then runs `vhs` on each tape in `tapes/`. The tapes isolate
`XDG_CONFIG_HOME` to a temp dir so recording never touches your real config.

The recordings write the shared model from `model.fga` (the `.fga` DSL, which
`ofga model write` transforms to JSON automatically).
