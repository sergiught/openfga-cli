# Auth0 event samples

`samples/*.json` are example payloads for the 21 Auth0 Events event types, one
file per type, named after the type. They were derived from the JSON Schemas
published at <https://auth0.com/docs/events> and trimmed to the fields a mapping
actually addresses: `data.object`, `data.previous_object` for `*.updated`, and
`data.context.tenant`.

They exist so `ofga mapping init` can preview a rule against a realistic event
without network access. They are illustrative, not normative — consult the Auth0
documentation for the authoritative schema.

To refresh a sample, replace the file and keep the CloudEvents envelope keys
(`specversion`, `type`, `source`, `id`, `time`, `data`, `a0tenant`, `a0stream`)
in that order. `catalog_test.go` enforces that every catalog entry has a file,
every file is valid JSON with a matching `type`, and no orphan files exist.
