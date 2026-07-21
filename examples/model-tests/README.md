# `ofga model test` showcase workspace

A self-contained, runnable workspace that exercises the core authorization
semantics of `ofga model test`. Everything runs in-process against a hermetic
embedded OpenFGA server — no store, profile, or network required.

```bash
ofga model test examples/model-tests      # from the repo root
```

## What it demonstrates

The model (`model.fga`) is a realistic document-management authorization model
that deliberately uses every OpenFGA rewrite form, so coverage reports and
failure explanations have something to show:

| Rewrite form | Where |
| --- | --- |
| Direct assignment `[user]` | `folder.owner`, `document.owner` |
| Conditioned direct type `[user with non_expired_grant]` | `document.viewer` |
| Union `a or b` | `folder.viewer`, `document.editor`, `document.viewer` |
| Computed userset | `owner` referenced by `editor`/`viewer` |
| Tuple-to-userset (TTU) `x from parent` | folder + document inheritance |
| Exclusion `but not` | `document.can_delete` |
| ABAC condition | `non_expired_grant(current_time, grant_expiry)` |

## Layout

```
examples/model-tests/
├── ofga.yaml                     # manifest: model + fixtures + test globs
├── model.fga                     # the authorization model (OpenFGA 1.1 DSL)
├── fixtures/
│   ├── org.yaml                  # organization membership / admins
│   └── documents.yaml            # folder + document graph
└── tests/
    ├── inheritance.test.yaml     # union / computed / TTU + list_objects / list_users
    ├── exclusion.test.yaml       # the `but not` exclusion, both outcomes
    └── conditions.test.yaml      # ABAC condition (true + false) + inline / contextual tuples
```

### The world the fixtures build

```
organization:acme    members: anne, carol   admin: anne

folder:root          owner: anne   viewer: erin
  └── folder:eng      owner: bob    viewer: dave        (parent: root)

document:roadmap     owner: bob    viewer: frank*       (parent: eng)
document:budget      owner: carol  suspended for Acme members
document:memo        owner: anne

* frank is a viewer only while his non_expired_grant is valid.
```

Because `folder:eng`'s parent is `folder:root` and `document:roadmap`'s parent
is `folder:eng`, access flows all the way down: `anne` (root editor) becomes an
editor of the roadmap, and `erin`/`dave` (folder viewers) become roadmap
viewers — all resolved through TTU, never granted directly on the document.

## Commands (with real output)

The `(…s)` in the summary line is the run's wall-clock duration — it'll differ
slightly on your machine; everything else below is verbatim.

### Run everything

```console
$ ofga model test examples/model-tests
● 13/13 test(s) passed (0.03s)
```

### Coverage

`--coverage` enables a coverage report tracking per-type rewrite-branch
coverage (grant-based). This workspace covers the whole model:

```console
$ ofga model test examples/model-tests --coverage
● 13/13 test(s) passed (0.03s)

coverage:
TYPE           COVERED   TOTAL   PERCENT
document       13        13      100%
folder         8         8       100%
organization   2         2       100%
total          23        23      100%

  document.can_delete  2/2
  document.editor  3/3
  document.org_suspended  1/1
  document.owner  1/1
  document.parent  1/1
  document.viewer  5/5
  folder.editor  3/3
  folder.owner  1/1
  folder.parent  1/1
  folder.viewer  3/3
  organization.admin  1/1
  organization.member  1/1
coverage is grant-based (a rewrite branch counts covered only when a check
assertion showed that specific arm granting; each ABAC condition counts its true
and false outcomes separately; non-empty list_objects/list_users results credit
at relation granularity) over the manifest model.
```

Every relation is listed now (not just missed ones); a relation with any
uncovered branches gets a trailing `MISSED: <labels>`. Add `--coverage-detail`
to also list each branch under the relation, marked `✓` (covered) or `○`
(missed):

```console
$ ofga model test examples/model-tests --coverage --coverage-detail
● 13/13 test(s) passed (0.03s)

coverage:
TYPE           COVERED   TOTAL   PERCENT
document       13        13      100%
folder         8         8       100%
organization   2         2       100%
total          23        23      100%

  document.can_delete  2/2
    ✓ computed:owner
    ✓ but-not:org_suspended
  document.editor  3/3
    ✓ direct:user
    ✓ computed:owner
    ✓ ttu:parent/editor
  document.org_suspended  1/1
    ✓ direct:organization#member
  document.owner  1/1
    ✓ direct:user
  document.parent  1/1
    ✓ direct:folder
  document.viewer  5/5
    ✓ direct:user
    ✓ condition:non_expired_grant=true
    ✓ condition:non_expired_grant=false
    ✓ computed:editor
    ✓ ttu:parent/viewer
  folder.editor  3/3
    ✓ direct:user
    ✓ computed:owner
    ✓ ttu:parent/editor
  folder.owner  1/1
    ✓ direct:user
  folder.parent  1/1
    ✓ direct:folder
  folder.viewer  3/3
    ✓ direct:user
    ✓ computed:owner
    ✓ ttu:parent/viewer
  organization.admin  1/1
    ✓ direct:user
  organization.member  1/1
    ✓ direct:user
coverage is grant-based (a rewrite branch counts covered only when a check
assertion showed that specific arm granting; each ABAC condition counts its true
and false outcomes separately; non-empty list_objects/list_users results credit
at relation granularity) over the manifest model.
```

The `structural-relations` test in `inheritance.test.yaml` exists purely to
pull branch coverage to 100%: it asserts the `parent` links and organization
`member`/`admin` directly. Delete it and branch coverage drops to 82.6%, with
`○` markers and `MISSED: ...` lines on the now-uncovered branches — a good
illustration of exactly what the coverage report surfaces (relations only
ever reached through a TTU userset still count their own uncovered branch):

```console
$ ofga model test examples/model-tests --coverage --coverage-detail
● 12/12 test(s) passed (0.03s)

coverage:
TYPE           COVERED   TOTAL   PERCENT
document       12        13      92.3%
folder         7         8       87.5%
organization   0         2       0%
total          19        23      82.6%

  ...
  document.parent  0/1   MISSED: direct:folder
    ○ direct:folder
  ...
  folder.parent  0/1   MISSED: direct:folder
    ○ direct:folder
  ...
  organization.admin  0/1   MISSED: direct:user
    ○ direct:user
  organization.member  0/1   MISSED: direct:user
    ○ direct:user
```

### Filter tests

`--run` globs over `<relative-file>/<test-name>` (without `.test.yaml`):

```console
$ ofga model test examples/model-tests --run 'conditions/*'
● 3/3 test(s) passed (0.01s)
```

### CI report

```console
$ ofga model test examples/model-tests --report junit --report-file results.xml
● 13/13 test(s) passed (0.02s)
```

Writes a JUnit XML file to `results.xml` (`--report json` also supported).
Omit `--report-file` to print the report to the terminal instead.

### Explore results in the playground

`--playground` runs the suite as usual and then, on a TTY, boots the fixtures
onto an ephemeral server and opens the interactive playground against a
failing test's world (or the first test's, if everything passes) so you can
drill into every result. The seeded connection is shown under a clearly
labeled ephemeral profile (`✦ model-test (seeded)`) — your real profiles stay
listed and switchable, and nothing about the seeded run touches your config.
With `--no-tui` (or no TTY, as here) it prints a note and skips the TUI:

```console
$ ofga model test examples/model-tests --playground
● 13/13 test(s) passed (0.02s)
note: --playground needs an interactive terminal and human output; ignoring it
```

## What failure looks like

Every committed assertion passes. To see the explanation machinery, flip one
assertion to the wrong value — e.g. claim `carol` can view the roadmap
(`inheritance.test.yaml`, `document-inherits-from-folder`) — and the run
prints the full resolution tree plus a nearest-miss suggestion:

```console
$ ofga model test examples/model-tests --run 'inheritance/document-inherits-from-folder'
● 1/1 test(s) failed (0.01s)

inheritance/document-inherits-from-folder
  ✗ check user:carol viewer document:roadmap
expected: true    got: false
document:roadmap#viewer [false] — union
├─ document:roadmap#viewer [user:frank] [false]
├─ document:roadmap#editor [false]
│  └─ document:roadmap#editor [false] — union
│     ├─ document:roadmap#editor [false]
│     ├─ document:roadmap#owner [false]
│     │  └─ document:roadmap#owner [user:bob] [false]
│     └─ document:roadmap#parent → folder:eng#editor [false]
│        └─ folder:eng#editor [false] — union
│           ├─ folder:eng#editor [false]
│           ├─ folder:eng#owner [false]
│           │  └─ folder:eng#owner [user:bob] [false]
│           └─ folder:eng#parent → folder:root#editor [false]
│              └─ folder:root#editor [false] — union
│                 ├─ folder:root#editor [false]
│                 ├─ folder:root#owner [false]
│                 │  └─ folder:root#owner [user:anne] [false]
│                 └─ folder:root#parent [false]
└─ document:roadmap#parent → folder:eng#viewer [false]
   └─ folder:eng#viewer [false] — union
      ├─ folder:eng#viewer [user:dave] [false]
      ├─ folder:eng#owner [false]
      │  └─ folder:eng#owner [user:bob] [false]
      └─ folder:eng#parent → folder:root#viewer [false]
         └─ folder:root#viewer [false] — union
            ├─ folder:root#viewer [user:erin] [false]
            ├─ folder:root#owner [false]
            │  └─ folder:root#owner [user:anne] [false]
            └─ folder:root#parent [false]
nearest miss: a tuple (user:carol, viewer, document:roadmap) would grant it
```

The exit code is `3` when a test fails (`0` when all pass). Revert the
flipped assertion and the suite is green again.
