# `ofga model diff` example

Two versions of a folder/document model. Diffing them runs locally and never
talks to a server.

```bash
ofga model diff examples/model-diff/old.fga examples/model-diff/new.fga   # from the repo root
```

`new.fga` makes four kinds of change:

| Change | Section |
| --- | --- |
| `document#viewer` no longer accepts `user:*` | Breaking |
| `document#viewer` now goes through the new `commenter` relation | Changed |
| a new `document#commenter` relation; `group#member` can be assigned to `document#editor` | Added |
| `document#editor` costs more to resolve for a user (1 → 2) | Cost |

```console
$ ofga model diff examples/model-diff/old.fga examples/model-diff/new.fga
old: examples/model-diff/old.fga
new: examples/model-diff/new.fga

Breaking (1)
  document#viewer: `user:*` no longer assignable to `document#viewer`

Changed (1)
  document#viewer: rewrite of `document#viewer` changed; access may differ (access analysis not done yet)

Added (2)
  document#commenter: relation `document#commenter` added
  document#editor: `group#member` now assignable to `document#editor`

Cost (1)
  document#editor: increased (user 1 → 2)

1 breaking, 1 changed, 2 added, 1 cost increase — breaking
● cost increased for 1 relation(s); pass --fail-on-cost to fail on this
$ echo $?
3
```

Exit code 3 means at least one breaking change, so a CI step fails. Add
`--fail-on-cost` to fail on cost increases too, or compare against the live
model with `ofga model diff store new.fga`. See the
[command reference](https://sergiught.github.io/openfga-cli/reference/model/diff/).
