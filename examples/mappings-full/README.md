# Auth0 mapping example: every snapshot type

`auth0.yaml` has one rule for each of the six things `ofga mappings backfill` and `ofga mappings drift` can read from Auth0: connections, users, organizations, and each organization's connections, members and member roles. Every rule answers both the live event and the snapshot of the same thing, so one rule serves the event stream, the backfill and the drift check. `model.fga` is the authorization model its tuples are written against. The smaller example in [`../mappings`](../mappings) is the one the guide [Syncing events into tuples](https://sergiught.github.io/openfga-cli/guide/mappings/) walks through; this one is for trying the snapshot commands against a tenant of your own.

| Rule | Also answers | Writes |
| --- | --- | --- |
| `connection.created` | `snapshot.connection` | the connection's tenant, and one tuple per application it is enabled for |
| `user.created` | `snapshot.user` | the user as a member of the tenant, and as assignee of a plan when `app_metadata.plan` is set |
| `organization.created` | `snapshot.organization` | the organization's tenant |
| `organization.connection.added` | `snapshot.organization.connection` | the connection on the organization, when it is enabled |
| `organization.member.added` | `snapshot.organization.member` | the member on the organization, and a membership object for roles to hang from |
| `organization.member.role.assigned` | `snapshot.organization.member.role` | the member's role in the organization |

## Try it

You need a reachable OpenFGA server and a machine-to-machine application authorized for the Auth0 Management API with `read:connections`, `read:users`, `read:organizations`, `read:organization_connections`, `read:organization_members` and `read:organization_member_roles`. Run these from the repo root:

```bash
# Check the file and run its tests; neither needs a server or Auth0
ofga mappings validate examples/mappings-full/auth0.yaml --model-file examples/mappings-full/model.fga
ofga mappings test examples/mappings-full/auth0.yaml

# A local OpenFGA, a profile, and a store with the model
docker run --rm -d -p 8080:8080 openfga/openfga run
ofga profiles add local --api-url http://localhost:8080 --use
ofga stores create mappings-full-demo --use
ofga model write --file examples/mappings-full/model.fga

# The Auth0 application
export AUTH0_DOMAIN=example.us.auth0.com AUTH0_CLIENT_ID=... AUTH0_CLIENT_SECRET=...

# See what Auth0's state maps to, write it, then confirm nothing differs
ofga mappings backfill examples/mappings-full/auth0.yaml --provider auth0 --dry-run
ofga mappings backfill examples/mappings-full/auth0.yaml --provider auth0
ofga mappings drift    examples/mappings-full/auth0.yaml --provider auth0
```

`validate` prints `6 rules, 12 tests; checked against examples/mappings-full/model.fga (8 types)`, and the last command ends with `no drift: N tuples match Auth0`.

To see drift, add a tuple Auth0 does not back and run the check again:

```bash
ofga tuples write user:ghost member tenant:example
ofga mappings drift examples/mappings-full/auth0.yaml --provider auth0 --dry-run   # 1 stale tuple, exit 3
ofga mappings drift examples/mappings-full/auth0.yaml --provider auth0 --prune     # asks, then deletes it
```

Read the warning under [Things to know before you run it](https://sergiught.github.io/openfga-cli/guide/mappings/#things-to-know-before-you-run-it) before using `--prune` on a store that anything else writes to. From 1,000 users up the read asks Auth0 for a user export job, waits for it and downloads it; this needs no extra scope.
