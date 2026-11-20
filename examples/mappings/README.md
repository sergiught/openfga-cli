# Auth0 mapping example

`auth0.yaml` is an `ofga mappings` file that turns Auth0 organization events into OpenFGA tuples: four rules (member added and removed, role assigned and removed; the two "added" and "assigned" rules also answer the `snapshot.*` events that `mappings backfill` and `mappings drift` replay) and the tests saved with them. `model.fga` is the small authorization model those tuples are written against, and `events.jsonl` is a short stream of Auth0 events to run through the mapping. The guide [Syncing events into tuples](https://sergiught.github.io/openfga-cli/guide/mappings/) walks through all three; run its commands from the repo root, for example `ofga mappings test examples/mappings/auth0.yaml`.

[`../mappings-full`](../mappings-full) is a second example with one rule for each of the six snapshot types, to try `mappings backfill` and `mappings drift` against a tenant of your own.
