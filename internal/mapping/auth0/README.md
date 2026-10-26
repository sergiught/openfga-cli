# Auth0 event samples

`samples/*.json` are example payloads for the 21 Auth0 Events event types, one
file per type, named after the type. They were derived from the JSON Schemas
published at <https://auth0.com/docs/events> and trimmed to the fields a mapping
actually addresses: `data.object`, `data.previous_object` for `*.updated`, and
the envelope's `a0tenant`, which is where a recipe reads the tenant from.
`data.context.tenant` is also in the samples, but Auth0 makes `data.context`
optional and `a0tenant` required, so no recipe depends on it.

They exist so the `ofga mappings` workspace can preview a rule against a realistic event
without network access. They are illustrative, not normative — consult the Auth0
documentation for the authoritative schema.

In the `*.updated` samples, `data.previous_object` differs from `data.object` in
exactly one field, except `organization.connection.updated`. A recipe for an
update event is gated on something having changed, so a sample whose two halves
are identical would preview as nothing at all — the screen would say the recipe
produces no tuple, which is true of that payload and false of the recipe.
`organization.connection.updated` is the exception because its recipe is gated
on the value of `is_enabled`, not on a change, so its two halves are equal and
it still previews.

## The model

`model.fga` is one authorization model covering every event here, embedded and
returned by `Model()`. Each recipe also declares the slice of it that recipe
needs, which is what the recipe screen shows; this file is their union, in a
form you can write to disk. `recipes_test.go` lints every recipe against it,
checks it satisfies every declared requirement, and rejects a relation no recipe
uses.

To refresh a sample, replace the file and keep the CloudEvents envelope keys
(`specversion`, `type`, `source`, `id`, `time`, `data`, `a0tenant`, `a0stream`)
in that order. `catalog_test.go` enforces that every catalog entry has a file,
every file is valid JSON with a matching `type`, and no orphan files exist.
