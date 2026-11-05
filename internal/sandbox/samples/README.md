# Sample stores

Source: https://github.com/openfga/sample-stores at commit
`6802b4d859d2773eea72c2315b3a9f027a2893a0`.

Licence: Apache-2.0 (see `LICENSE` and `NOTICE.txt`, copied from upstream).

These workspaces are generated. Do not hand-edit them; re-run the tool:

```sh
git clone https://github.com/openfga/sample-stores /tmp/sample-stores
git -C /tmp/sample-stores checkout 6802b4d859d2773eea72c2315b3a9f027a2893a0
go run ./tools/samplestores -src /tmp/sample-stores/stores \
  -out internal/sandbox/samples \
  -stores github,gdrive,slack,custom-roles,multitenant-rbac,temporal-access,expenses \
  -commit 6802b4d859d2773eea72c2315b3a9f027a2893a0
```
