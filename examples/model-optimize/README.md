# `ofga model optimize` examples

Three small models, each built to trigger a different part of the optimizer.
Everything runs locally: the analysis never talks to a server, and `--bench`
uses an in-process embedded OpenFGA.

```bash
ofga model optimize examples/model-optimize/roles.fga       # from the repo root
```

| Model | What it shows |
| --- | --- |
| `roles.fga` | A role hierarchy with three redundant rewrites: S1 (union), S2 (intersection), S3 (duplicate) |
| `hierarchy.fga` | A folder/document tree with a redundant `from parent` operand (S4) and recursion advice (T1) |
| `exclusion.fga` | A `but not` that subtracts a recursive relation, which disables every safe rewrite (plus C2 and T1 advice) |

See the [guide](https://sergiught.github.io/openfga-cli/guide/model-optimizer/)
for what each rule means and why "safe" is a strong word.

## Commands (with real output)

### Redundant roles

`roles.fga` says `maintainer` includes `owner`, then repeats `owner` next to
`maintainer` twice and lists `contributor` twice:

```console
$ ofga model optimize examples/model-optimize/roles.fga
Safe rewrites (3)
  project#contributor  [S1]  [user] or maintainer or owner → [user] or maintainer
    reason: operand is already included by another operand of the union
    proof: project#owner ⊆ project#maintainer
  project#can_release  [S2]  owner and maintainer → owner
    reason: intersection operand is already implied by the other
    proof: project#owner ⊆ project#maintainer
  project#can_read  [S3]  contributor or contributor → contributor
    reason: duplicate union operand
    proof: operands are syntactically identical

Diff
--- examples/model-optimize/roles.fga
+++ examples/model-optimize/roles.fga (optimized)
@@ -5,8 +5,8 @@
 
 type project
   relations
-    define can_read: contributor or contributor
-    define can_release: owner and maintainer
-    define contributor: [user] or maintainer or owner
+    define can_read: contributor
+    define can_release: owner
+    define contributor: [user] or maintainer
     define maintainer: [user] or owner
     define owner: [user]
```

Each finding carries its proof. The diff is against the normalized model
(relations sorted), which is also what `--write` and `--output-file` produce.

### A redundant `from parent`

In `hierarchy.fga`, `document#viewer` lists both `viewer from parent` and
`editor from parent`, but `document#editor` already includes `editor from parent`, so the operand adds nothing:

```console
$ ofga model optimize examples/model-optimize/hierarchy.fga
● rewrite reformats the model: comments are dropped and relations are sorted
Safe rewrites (1)
  document#viewer  [S4]  [user] or editor or viewer from parent or editor from parent → [user] or editor or viewer from parent
    reason: tuple-to-userset operand is already included by another operand of the union
    proof: document#editor from parent ⊆ document#editor

Tuple recommendations (2)
  folder#editor  [T1]
    reason: Relation introduces recursion: each nesting level in the tuple data costs one more datastore read and one more dispatch; keep nesting shallow.
    weights: user=inf
  folder#viewer  [T1]
    reason: Relation introduces recursion: each nesting level in the tuple data costs one more datastore read and one more dispatch; keep nesting shallow.
    weights: user=inf

Diff
--- examples/model-optimize/hierarchy.fga
+++ examples/model-optimize/hierarchy.fga (optimized)
@@ -5,14 +5,14 @@
 
 type folder
   relations
-    define parent: [folder]
+    define editor: [user] or owner or editor from parent
     define owner: [user]
-    define editor: [user] or owner or editor from parent
-    define viewer: [user] or editor or viewer from parent
-
-type document
-  relations
-    define parent: [folder]
-    define owner: [user]
-    define editor: [user] or owner or editor from parent
+    define parent: [folder]
+    define viewer: [user] or editor or viewer from parent
+
+type document
+  relations
+    define editor: [user] or owner or editor from parent
+    define owner: [user]
+    define parent: [folder]
-    define viewer: [user] or editor or viewer from parent or editor from parent
+    define viewer: [user] or editor or viewer from parent
```

The T1 lines are advice, never applied: the folder tree is recursive by
design, and the tool only points out that each nesting level costs a read and a dispatch.

### Prove it with a benchmark

`--bench` runs the original and the rewritten model on two embedded servers
with the same synthesized tuples and the same Checks. Numbers differ by
machine and run; the structure does not:

```console
$ ofga model optimize examples/model-optimize/hierarchy.fga --bench --iterations 20 --tier safe
Safe rewrites (1)
  ...

Benchmark · synthesis · 230 tuples · 36 checks · 20 runs

  Deny reads       153 → 147   -4%
  Deny dispatches   22 →  21   -5%
  Answers          all 36 unchanged

Deny checks whose cost changed (1)
  CHECK            READS         DISPATCHES       P50 ms
  document#viewer  77 → 71  -8%  12 → 11     -8%  1.18 → 1.29

  23 deny checks unchanged · 12 allow checks not compared
  (allow checks stop early, so their read counts are noise)
  in-process, indicative only
```

`Answers all 36 unchanged` is the line that matters: if any Check answered
differently on the rewritten model, the command would exit `3` and write
nothing.

### When nothing is safe

`exclusion.fga` has the same redundant `owner` next to `editor` as
`roles.fga`, but `can_view` subtracts `blocked`, and `blocked` is recursive.
OpenFGA's cycle handling under exclusion depends on the strategy it picks for
the whole model, so no rewrite anywhere can be proven safe:

```console
$ ofga model optimize examples/model-optimize/exclusion.fga
● safe rewrites disabled: folder#can_view subtracts a recursive relation; OpenFGA's cycle handling there depends on evaluation strategy, so no rewrite can be proven safe
Caller recommendations (1)
  folder#can_view  [C2]
    reason: Exclusion present: the subtracted side is always evaluated, so a Check on this relation costs an extra datastore read, and an extra dispatch when the subtracted side is not a direct relation.
    weights: user=inf

Tuple recommendations (1)
  folder#blocked  [T1]
    reason: Relation introduces recursion: each nesting level in the tuple data costs one more datastore read and one more dispatch; keep nesting shallow.
    weights: user=inf
● no safe rewrites; 2 recommendations
```

The notices go to stderr; the findings go to stdout. Make `blocked` a plain
`[user]` and the `viewer` rewrite comes back.

### Write the result

```console
$ ofga model optimize examples/model-optimize/roles.fga --output-file optimized.fga
...
● wrote rewritten model to optimized.fga
```

`--write` overwrites the input file instead. Both reformat the file and drop
comments (the command warns on stderr), so review the diff first. Use
`--output-file -` to print the rewritten model to stdout and nothing else. Nothing is written when there is no safe
rewrite or when a benchmark finds a changed answer.

### Machine-readable

```bash
ofga model optimize examples/model-optimize/hierarchy.fga --json | jq '[.findings[].id]'
```

prints `["S4","T1","T1"]`. Add `--bench` and the `bench` field carries the
per-check numbers; see [Output schemas](https://sergiught.github.io/openfga-cli/guide/output-schemas/).
