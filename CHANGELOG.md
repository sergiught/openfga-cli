# Changelog

## [0.265.1](https://github.com/sergiught/openfga-cli/compare/v0.265.0...v0.265.1) (2026-07-18)


### Bug fixes

* **release:** sign checksums with cosign v3 bundle format ([#26](https://github.com/sergiught/openfga-cli/issues/26)) ([1833685](https://github.com/sergiught/openfga-cli/commit/1833685b9f95a759d6adebea21ed6d1ad4172af3))

## [0.265.0](https://github.com/sergiught/openfga-cli/compare/v0.264.0...v0.265.0) (2026-07-18)


### Features

* **config:** advise when env credentials are ignored or incomplete ([#17](https://github.com/sergiught/openfga-cli/issues/17)) ([ceedd3e](https://github.com/sergiught/openfga-cli/commit/ceedd3ef41432e9e65689d828f8e455152d79ad4))
* **fga:** weighted-graph resolution-cost heatmap on model graph views ([#23](https://github.com/sergiught/openfga-cli/issues/23)) ([a4e22de](https://github.com/sergiught/openfga-cli/commit/a4e22de19b1b279edd5c75e5219f5785479f5077))
* **query:** what-if flags on list-objects/list-users; fix exit codes and output ([#15](https://github.com/sergiught/openfga-cli/issues/15)) ([32da485](https://github.com/sergiught/openfga-cli/commit/32da485b3f0e1ec4bfdae3be13cb5e8042a9aba4))
* **tui:** warn when unauthorized to manage stores; fix filter and pin issues ([#18](https://github.com/sergiught/openfga-cli/issues/18)) ([fe34bf2](https://github.com/sergiught/openfga-cli/commit/fe34bf2df0043c2b8f4069c63fa181507fa2ff39))
* **tui:** weighted-graph view of the authorization model (toggle with v) ([#24](https://github.com/sergiught/openfga-cli/issues/24)) ([2d92c73](https://github.com/sergiught/openfga-cli/commit/2d92c73c156b8460aef71af2f4a2da50e8a4bc7d))


### Bug fixes

* **clierr:** diagnose OAuth token-endpoint failures correctly ([#14](https://github.com/sergiught/openfga-cli/issues/14)) ([cdc1dd2](https://github.com/sergiught/openfga-cli/commit/cdc1dd2557b1de6fe7746a53160ec533112be299))
* **cli:** restore unknown-command diagnosis and reject an unknown --theme ([#13](https://github.com/sergiught/openfga-cli/issues/13)) ([96df988](https://github.com/sergiught/openfga-cli/commit/96df98831380751d44ae13a417c3c687af38da67))
* **config:** explain a locked OS keyring instead of leaking the D-Bus error ([#25](https://github.com/sergiught/openfga-cli/issues/25)) ([57f301f](https://github.com/sergiught/openfga-cli/commit/57f301f157874c0e6059e813bebaa4adfe5d2c44))
* **output:** consistent --plain timestamps, glyphs, and count footers ([#16](https://github.com/sergiught/openfga-cli/issues/16)) ([fb9025e](https://github.com/sergiught/openfga-cli/commit/fb9025eb0fc6422a5349649273baa9e0bae4cc81))


### Documentation

* document the playground subcommand and cleanup-credentials --purge ([#19](https://github.com/sergiught/openfga-cli/issues/19)) ([cebc9c9](https://github.com/sergiught/openfga-cli/commit/cebc9c9bc50ac49fe2fe2aaf89c61379154b9dcf))

## [0.264.0](https://github.com/sergiught/openfga-cli/compare/v0.263.1...v0.264.0) (2026-07-17)


### Features

* **profiles:** pin store_id and model_id on a profile from the TUI form and init ([#11](https://github.com/sergiught/openfga-cli/issues/11)) ([c5e4413](https://github.com/sergiught/openfga-cli/commit/c5e441303bae0199f9cc47ab0db8363764269439))

## [0.263.1](https://github.com/sergiught/openfga-cli/compare/v0.263.0...v0.263.1) (2026-07-17)


### Bug fixes

* **release:** use .Env for the Homebrew tap token ([b251d5b](https://github.com/sergiught/openfga-cli/commit/b251d5b46bddea1f5d775793134f033be0567f32))

## 0.263.0 (2026-07-17)


### ⚠ BREAKING CHANGES

* **cli:** --store and --model are no longer accepted; use --store-id and --model-id instead.

### Features

* **apilog:** add Entry type and ring-buffer Recorder ([9bc7082](https://github.com/sergiught/openfga-cli/commit/9bc70828fcce920f65700a6326ec65ad96659e71))
* **apilog:** capture bodies, redact auth, cap size in Transport ([a1ca36b](https://github.com/sergiught/openfga-cli/commit/a1ca36bbc099d1a9062b0fd1d9f67f62930e1abf))
* **cli:** add `api` raw-request command; drop --api-url/--token; rename store→stores, tuple→tuples ([46a9d95](https://github.com/sergiught/openfga-cli/commit/46a9d95582b40e9e6507a378f8131bbc97398f82))
* **cli:** consistent commands, groups error on typos, -o/--output ([74746a8](https://github.com/sergiught/openfga-cli/commit/74746a8d8251f4555f99537316119e7b8bee1cd7))
* **client:** add WithCapture option to record requests ([5c48527](https://github.com/sergiught/openfga-cli/commit/5c485271e10dd7f7322bc401c2a23c927e3b44e2))
* **client:** default to higher consistency for reads ([c8f9d9b](https://github.com/sergiught/openfga-cli/commit/c8f9d9b6bcf86564724a0c32e4ec9cc42fb1d9b8))
* **client:** load the private_key_jwt PEM from keyring or key_file ([a4a969a](https://github.com/sergiught/openfga-cli/commit/a4a969a666c671bf828e20ff035e018ced67b302))
* **cli:** make bare ofga the sole TUI entry point; gradient help banner ([7c77882](https://github.com/sergiught/openfga-cli/commit/7c778823f90322abcd937e9da5fbe34d6fa35f9e))
* **cli:** render help usage/examples as raised terminal blocks with a $ prompt ([dd53aa2](https://github.com/sergiught/openfga-cli/commit/dd53aa2491e42d447ceb6837d2bfc5f98db83659))
* **cli:** standardize store and model ID flags ([df7204a](https://github.com/sergiught/openfga-cli/commit/df7204a87f7c3389b19ee5dae3c93b7c43e500db))
* **cli:** stderr for status lines, bulk --file tuple writes ([b34fa56](https://github.com/sergiught/openfga-cli/commit/b34fa566b11b2a7e16ae2ebb5dc43900e315e0f1))
* **cli:** styled --help (chip section headers, aligned columns) in theme colors ([c7c36b6](https://github.com/sergiught/openfga-cli/commit/c7c36b684f916d2dff237ce4c787e1ae9121cac4))
* **cli:** version subcommand, TTY guard, theme command, query/tuple flags, examples ([2cf4811](https://github.com/sergiught/openfga-cli/commit/2cf4811ae213c5c44ebdd886d268588fed9f6acd))
* **completion:** dynamic shell completion for profiles, stores and models ([35f02e4](https://github.com/sergiught/openfga-cli/commit/35f02e49c0a1c1e9cc98802bdc03f457e4f93433))
* **config,client:** profile auth methods; rename `context`→`profiles` ([7a9df1e](https://github.com/sergiught/openfga-cli/commit/7a9df1ec6b1d849c85f26dfdb6ed10cad5bb0334))
* **config:** add keyring secret helpers and private_key field ([124084a](https://github.com/sergiught/openfga-cli/commit/124084ad366100f19fddc8a72e420bb7d08915bc))
* **config:** discoverability, env parity, validation and safer loading ([4e74c99](https://github.com/sergiught/openfga-cli/commit/4e74c9900dfd8a7a11cf2b5a01d3db657c3837a6))
* **config:** env secrets, config override, friendly auth, safer set ([77266cf](https://github.com/sergiught/openfga-cli/commit/77266cf05922298196c176c07ac865a32aec16cd))
* **config:** resolve secrets from the keyring; clean up on remove ([e13fdc0](https://github.com/sergiught/openfga-cli/commit/e13fdc0365c6e1b231b909ead5646eea2da55d2e))
* **config:** store secrets in the OS keyring on save ([83c0948](https://github.com/sergiught/openfga-cli/commit/83c0948d3011efe9598c24fac521d6ba1e53cbb0))
* Crush-style block wordmark hero on splash + compact sidebar logo; Aurora-themed forms; splash dismisses on keypress ([6a9d561](https://github.com/sergiught/openfga-cli/commit/6a9d5616eddbc0c3a9c5d4ac9b935d27da0f8f6c))
* **dsl:** ANTLR-lexer syntax highlighting for DSL text ([0243f36](https://github.com/sergiught/openfga-cli/commit/0243f36fbed01694c8e0c184252df592ff2f535b))
* **dsl:** per-rune Cells primitive; rebuild Highlight on it ([1a1dc8b](https://github.com/sergiught/openfga-cli/commit/1a1dc8b2a4391d3b3899f94f2ed01ced9d18e848))
* **dsl:** role-based syntax colors (type/relation names, dimmed comments) ([f02d019](https://github.com/sergiught/openfga-cli/commit/f02d01937d8b7d701b1fd3d040e8e2fec99441f5))
* **dsl:** syntax-error diagnostics via openfga/language parser ([cd7c234](https://github.com/sergiught/openfga-cli/commit/cd7c23446f073e95e0c8967c6ed9d525d428c373))
* **dsl:** undefined-type diagnostics ([25eb125](https://github.com/sergiught/openfga-cli/commit/25eb125f47084f402c0c24bf88e0f95745798db5))
* **dx:** harden secrets and fix CLI/output/TUI correctness bugs ([8604bf5](https://github.com/sergiught/openfga-cli/commit/8604bf5645abdaf5df870b314bb723dbb55ffff0))
* **errors:** humanize network failures, add typed exit codes and a request timeout ([1f1a318](https://github.com/sergiught/openfga-cli/commit/1f1a3187cf3339702cc8f5e6954410480a9132ae))
* **errors:** show usage on misuse, validate tuples early, add fix-it hints ([1a27086](https://github.com/sergiught/openfga-cli/commit/1a27086857393031cdc2d82860f2aaf12fa95810))
* **fga:** bundle converging graph edges onto shared trunks; lift diagram row-width cap ([0150d37](https://github.com/sergiught/openfga-cli/commit/0150d378da9e2708d203b8dcd277b5a07477bf5f))
* **field:** add a toggle field; Enter advances between fields ([d235af6](https://github.com/sergiught/openfga-cli/commit/d235af60541694e74d7433645b24faa97c1b20c8))
* **field:** highlight the focused form row; flatten the dialog ([8c7df98](https://github.com/sergiught/openfga-cli/commit/8c7df98c2c28bc64039c494ea8620147ed3d7f3b))
* **install:** add verified release installer ([9d9e029](https://github.com/sergiught/openfga-cli/commit/9d9e029251c2ed16babd16d11572b2443ee815e3))
* **logo:** kerned 4-row slab wordmark that fits every sidebar width ([83a2ae5](https://github.com/sergiught/openfga-cli/commit/83a2ae5376b77893a802d416e1b5d60884e9f344))
* **logo:** real OpenFGA mark as embedded-bitmap half-block art ([49d56d4](https://github.com/sergiught/openfga-cli/commit/49d56d48574e0d7410e845a26d8197d70753b84d))
* **logo:** stacked OPENFGA block wordmark renderer with gradient + shimmer ([210af2b](https://github.com/sergiught/openfga-cli/commit/210af2b4ff8787d63228e68fb2def61b604db170))
* **model:** accept .fga DSL input in model write ([3d0c0b3](https://github.com/sergiught/openfga-cli/commit/3d0c0b3e0828ccf3f900e6e4f66579eabb8464cb))
* **onboarding:** add `ofga init` and OAuth flags on `profiles add` ([7079036](https://github.com/sergiught/openfga-cli/commit/7079036b5b0ca15d6fd15cfe468947390b849208))
* **output:** add --yaml output alias ([229faa9](https://github.com/sergiught/openfga-cli/commit/229faa9f0d027b4ef2e35198039bfe2ba1a37f65))
* **output:** add -o yaml output format ([6b15ca6](https://github.com/sergiught/openfga-cli/commit/6b15ca6f9e2f30df2e37287c413c14d1208a09dd))
* **output:** consistent JSON contract for mutations, queries and version ([419b2ab](https://github.com/sergiught/openfga-cli/commit/419b2abf049c86d9878e940648f0efb382aa6e8a))
* **output:** themed CLI output — status dots, framed tables ([3c73f53](https://github.com/sergiught/openfga-cli/commit/3c73f539d5984db7c2974c106a707b1fe3825188))
* **playground:** ABAC conditions & contextual tuples; per-section footer counts ([ce8e291](https://github.com/sergiught/openfga-cli/commit/ce8e2912f2de43edaf0474e426a754f43764f229))
* **playground:** add API Logs keybindings ([b61c453](https://github.com/sergiught/openfga-cli/commit/b61c453f6034665a1cc869cdc461eec683ef28da))
* **playground:** add list-relations query mode ([a91a8c8](https://github.com/sergiught/openfga-cli/commit/a91a8c82ae389e731df9fee47169798443ddf1d5))
* **playground:** add the collapsed "ACL path" view to the resolution ([7ea8ba5](https://github.com/sergiught/openfga-cli/commit/7ea8ba5fc1dddf2699aca18f407a2e858f2df8de))
* **playground:** always show assertion count badge (0 included); autoload assertions ([60bc442](https://github.com/sergiught/openfga-cli/commit/60bc442f85e22d03ea2f5f58712bf0f2e830c0e6))
* **playground:** ambient gradient drift on wordmark and active pill ([6ab1571](https://github.com/sergiught/openfga-cli/commit/6ab157114dc727f39ae560139f407c08aeb4af0e))
* **playground:** API Logs footer/help keys, mouse-wheel scroll, aligned URL header ([9ed31d8](https://github.com/sergiught/openfga-cli/commit/9ed31d8bcd37f6068edb6eacd7f7f2c93fba0917))
* **playground:** API URL header, path-only detail, j/k body scroll, request count badge ([dcca528](https://github.com/sergiught/openfga-cli/commit/dcca52837c02a3952292dc262f9a42ee6efce29f))
* **playground:** arrow-key section navigation; graph pans via shift+arrows and hjkl ([ce26e4e](https://github.com/sergiught/openfga-cli/commit/ce26e4e6c91f92f7bba9793b1602082045d242d4))
* **playground:** assertions master-detail split with condition/context detail ([f47d03e](https://github.com/sergiught/openfga-cli/commit/f47d03e9cd1ff603d556e0680468db5ea962af3d))
* **playground:** Check resolution tree (Expand) in the Query panel ([b95f645](https://github.com/sergiught/openfga-cli/commit/b95f6455938f43f23c50516d93fd6b7459794cd7))
* **playground:** concrete placeholder examples for context, contextual tuples & condition ([157c22b](https://github.com/sergiught/openfga-cli/commit/157c22b74dae683c20342bd4f7fc9775339d2ee1))
* **playground:** ctrl+r opens a check's resolution from the query editor ([b35f704](https://github.com/sergiught/openfga-cli/commit/b35f70445b6db86678f36517dc3857033bdb3146))
* **playground:** delete a store from the Stores tab (with confirmation) ([159d78f](https://github.com/sergiught/openfga-cli/commit/159d78fedaf20fa82d4645c53b107c6b25e6c504))
* **playground:** descriptive footer key hints (n add, / filter, t run all, …) ([7fea154](https://github.com/sergiught/openfga-cli/commit/7fea154e02ac3609ae2765e8fc786c36983fe4bb))
* **playground:** DSL model editor — author and apply authorization models in the TUI ([21ef2bf](https://github.com/sergiught/openfga-cli/commit/21ef2bf39aae893de51f48b5a8bdfe4a9af74fca))
* **playground:** flag undefined types live when the DSL parses ([4c98347](https://github.com/sergiught/openfga-cli/commit/4c983477571f87002e5178ec882e59b5555e63de))
* **playground:** flat query sections, de-boxed previews, inline empty states, brand version ([5a5c83f](https://github.com/sergiught/openfga-cli/commit/5a5c83fd71ff86d8a6016dc13399ce6794a442aa))
* **playground:** gate query context/contextual tuples behind a toggle ([6d4c810](https://github.com/sergiught/openfga-cli/commit/6d4c810aa0f7ddd28308406cd524f8519293bceb))
* **playground:** highlight the granting path; open it from assertions ([221b327](https://github.com/sergiught/openfga-cli/commit/221b3279dcaf031cc51faea5966c39fe9cd04a9a))
* **playground:** label Stores add shortcut "add" (was "new") ([ca0f530](https://github.com/sergiught/openfga-cli/commit/ca0f53051a1ce0895079007685c7c43f33213b12))
* **playground:** make API log detail tabs clickable ([7e71a3b](https://github.com/sergiught/openfga-cli/commit/7e71a3b903c9df6c0445dede4a490c4fc048c590))
* **playground:** manage assertions (add / edit / delete / run) ([0462c53](https://github.com/sergiught/openfga-cli/commit/0462c531929bbb16ae6c13a274008f62491e6acc))
* **playground:** master-detail previews for stores, tuples, and changes ([1086307](https://github.com/sergiught/openfga-cli/commit/10863078b0de807554f434d32226082eee4be456))
* **playground:** migrate to bubbletea v2 + bespoke field forms; drop huh ([6300f61](https://github.com/sergiught/openfga-cli/commit/6300f61c6f986f2d341b64eb5aec02010ce43bdb))
* **playground:** minimal ctrl+k command palette overlay ([55ffff9](https://github.com/sergiught/openfga-cli/commit/55ffff9e48e07e10fc7d9bc19df086a194d1818c))
* **playground:** pin query input to a bottom rounded box with mode chip ([e26dc21](https://github.com/sergiught/openfga-cli/commit/e26dc214964416cb51789779ff3b2b7c34e7421e))
* **playground:** profiles tab, config auto-persist, auth forms, resolution view ([c929e34](https://github.com/sergiught/openfga-cli/commit/c929e34805636ac9b565064b224143fedf759587))
* **playground:** query dashboard with verdict card and rerunnable history strip ([fe37283](https://github.com/sergiught/openfga-cli/commit/fe372831558170208db57817b4acbd9159eb64a8))
* **playground:** recompute DSL diagnostics on every editor edit ([565a3cd](https://github.com/sergiught/openfga-cli/commit/565a3cd33833d48213538f32e3d3e28540e20f0d))
* **playground:** register API Logs section and wire recorder ([759daf0](https://github.com/sergiught/openfga-cli/commit/759daf04cba0145c09babb420eb478a3f97e604a))
* **playground:** remove the Settings theme switcher; Aurora is the fixed theme ([9c89f01](https://github.com/sergiught/openfga-cli/commit/9c89f011985f1e795af1f4ec72fc52e81988497d))
* **playground:** render API Logs list detail panes ([e009d33](https://github.com/sergiught/openfga-cli/commit/e009d3353495e03f681303758051cb6b0949ea8f))
* **playground:** render forms and model picker as centered overlays ([52e0302](https://github.com/sergiught/openfga-cli/commit/52e0302fb3b6400e1df515e4938999cf37db0779))
* **playground:** render through the sidebar shell + splash screen ([a822434](https://github.com/sergiught/openfga-cli/commit/a822434192dcec7d090c4f058febdde69f1646ad))
* **playground:** resolve tuple-to-userset branches in the granting path ([0e65f5d](https://github.com/sergiught/openfga-cli/commit/0e65f5d9355b9261d6cc71d900f9cdcbfe5a2a4a))
* **playground:** scroll API Logs detail/URL and style the status line ([7701117](https://github.com/sergiught/openfga-cli/commit/7701117130fea1f448af851d54f04e2dcdfdd518))
* **playground:** sidebar count badges for Profiles, Stores and Assertions ([fbf0880](https://github.com/sergiught/openfga-cli/commit/fbf0880c55027b2274a48be8f09aac668f8ed3d1))
* **playground:** single syntax-colored DSL editor pane; drop split preview ([60a394b](https://github.com/sergiught/openfga-cli/commit/60a394b2715f078e6b84e7ac1a04fa45cc416b74))
* **playground:** single-line list formatting in compact mode ([90a5f84](https://github.com/sergiught/openfga-cli/commit/90a5f84431deaf7ca93f92a19d2619fb680887df))
* **playground:** soft-wrap the API Logs detail body ([96d399b](https://github.com/sergiught/openfga-cli/commit/96d399b9e635ec84ca1a62dfef4609b330719d4d))
* **playground:** splash gradient shimmer with spring rise; self-stopping ticker ([a1ac30f](https://github.com/sergiught/openfga-cli/commit/a1ac30f31d5cf4188abdbad3e9dc954bef6191d7))
* **playground:** splashless spring entrance replaces the gate screen ([3c83fc2](https://github.com/sergiught/openfga-cli/commit/3c83fc26d239f5059d6730533af6d1b8a57d954f))
* **playground:** split DSL editor with live highlight preview and diagnostics ([8d8bce1](https://github.com/sergiught/openfga-cli/commit/8d8bce1f1c0256cd56762749ef7699489a6a645e))
* **playground:** tabbed API Logs detail, wider list, no latency column ([73013cb](https://github.com/sergiught/openfga-cli/commit/73013cb15723c8480221c07f1ada86ebf39a6186))
* **playground:** tidy API Logs detail spacing, scrolling, and list rows ([e65bdc2](https://github.com/sergiught/openfga-cli/commit/e65bdc2b857f366395d95085b9b4eacbef772b66))
* **playground:** toast chips + breathing connection dot ([7ba1e1f](https://github.com/sergiught/openfga-cli/commit/7ba1e1f3f2ca71411c0c2b9069ca71a3b7ad6902))
* **playground:** two-frame materialize transition between sections ([1ee419a](https://github.com/sergiught/openfga-cli/commit/1ee419a6c378c5a3a44d1a62664ed09690196969))
* **playground:** use 'a' to add on Profiles and Stores (was 'n') ([84d2f07](https://github.com/sergiught/openfga-cli/commit/84d2f077ea635d0655bd6c2912039ff5bff26df3))
* **playground:** v toggles compact full-width list view ([f7a6a00](https://github.com/sergiught/openfga-cli/commit/f7a6a0051dae9f5a3e21f2c26b8e2270ae65241a))
* **profiles:** add cleanup-credentials --purge to wipe orphaned keyring entries (AUTH-10) ([d1cf026](https://github.com/sergiught/openfga-cli/commit/d1cf026bd614067a36b057c6bd98baecdf68eacb))
* **profiles:** add private_key secret, stored in the keyring ([1bba1f9](https://github.com/sergiught/openfga-cli/commit/1bba1f92e258f8a014b14ad1bdb9934aedf59397))
* **safety:** confirm destructive commands and add --dry-run ([4465276](https://github.com/sergiught/openfga-cli/commit/44652768e9659a4a9ed00597dd6af88b993c4c3f))
* **shell:** block wordmark in sidebar, centered empty states, cleaner query (no double border), fix nav background marks ([6208663](https://github.com/sergiught/openfga-cli/commit/6208663da9f5a686bd8b7e0c959a5fe9126fb372))
* **shell:** centered dialog box and ANSI-aware overlay compositor ([026963e](https://github.com/sergiught/openfga-cli/commit/026963e583d96f9be2bdc3893a98a49a9e83c42e))
* **shell:** filled title bar + gradient underline, powerline chips, violet dialogs, magenta selection, drift/entrance plumbing ([31da1a6](https://github.com/sergiught/openfga-cli/commit/31da1a661f4325513255d216a65509bb45c313f8))
* **shell:** flat canvas — header-rule main pane, hatch-banded brand block, quiet status, no panel fills ([41c6ee2](https://github.com/sergiught/openfga-cli/commit/41c6ee2d6a8966648cc0b02eaaed5dc0bc3cb7a3))
* **shell:** frame main pane in a rounded panel on a cohesive base; fix list row width ([d6ebf0a](https://github.com/sergiught/openfga-cli/commit/d6ebf0ac9607458781e02ac909b3cdf37274e752))
* **shell:** master-detail focus model for the playground ([726e1ac](https://github.com/sergiught/openfga-cli/commit/726e1ace125a301992a43385ed026dbd719d4189))
* **shell:** nav glyphs + gradient active pill; segmented status bar with keycaps ([602ec74](https://github.com/sergiught/openfga-cli/commit/602ec742d6e9059fd9ec4d57ef4639983086f666))
* **shell:** painted surfaces via lipgloss v2 canvas; dialog scrim+shadow; toast slot ([01986d4](https://github.com/sergiught/openfga-cli/commit/01986d42ac81d73ad2738d0d64060d6f07f1a1bb))
* **shell:** render the real OpenFGA mark in the sidebar; rebrand to OpenFGA CLI ([bc6b15a](https://github.com/sergiught/openfga-cli/commit/bc6b15a77d156911f65db225f606b247dfd57b32))
* **shell:** sidebar + main + status bar layout with collapse ([740366e](https://github.com/sergiught/openfga-cli/commit/740366e1f0db967a343df4433c221f9e084b1b4d))
* **shell:** stacked OPENFGA wordmark in the sidebar; retire the bitmap mark; tagline brand line ([22ded0a](https://github.com/sergiught/openfga-cli/commit/22ded0a9fc4f8c2ae5f153683fdb0366585ab939))
* **style:** add per-rune Gradient renderer for the wordmark ([8cc8b81](https://github.com/sergiught/openfga-cli/commit/8cc8b813c6fbfd5c6c3c45707c97e829c6e946b2))
* **style:** add status-dot helpers ([223aad2](https://github.com/sergiught/openfga-cli/commit/223aad27b67eebe3899493da08c592933f5d9093))
* **style:** chip, keycap, and gradient-pill helpers ([045daf0](https://github.com/sergiught/openfga-cli/commit/045daf0211d3bac8c6e5f5a55393b4917fa59b6e))
* **style:** SectionHeader primitive; dim keycaps; retire GradientUnderline ([62723bd](https://github.com/sergiught/openfga-cli/commit/62723bd394efd061496d05ba31ef1e8117db3675))
* **theme:** add Aurora theme derived from OpenFGA brand gradient, make it default ([4d3df0b](https://github.com/sergiught/openfga-cli/commit/4d3df0bf59eb3b9f20cacce2aea658d69341d316))
* **theme:** migrate theme+style to lipgloss v2 color.Color; add surface tiers ([93b65a3](https://github.com/sergiught/openfga-cli/commit/93b65a331a73fb688e7694417e1e7f1aa365e8a9))
* **theme:** violet/magenta accent family, deeper surface contrast, phased gradient helpers ([32e256c](https://github.com/sergiught/openfga-cli/commit/32e256c0cb41ed01d76f5d1bf8c611026dc43398))
* **tui:** add a ? keybinding overlay ([e4de055](https://github.com/sergiught/openfga-cli/commit/e4de05594f69e5f18fb637a08e695aa4adbeb50a))
* **tui:** add a focus caret under the mono theme ([532d130](https://github.com/sergiught/openfga-cli/commit/532d130cc5acefa2e520c7d89627341b4ad7e5fd))
* **tui:** breadcrumb sub-modes and a compact tab strip when collapsed ([373e833](https://github.com/sergiught/openfga-cli/commit/373e8330950efccc3ae14864421e6fd83bee7f93))
* **tui:** confirm every delete through one modal, highlight the subject ([b8abeef](https://github.com/sergiught/openfga-cli/commit/b8abeef7b275c749741dcd0fd5f1e6bf4dbe1bb0))
* **tui:** ctrl+s submits forms; unify form errors into one modal ([f110b59](https://github.com/sergiught/openfga-cli/commit/f110b59498dc9156a3fb43cce4a9a5e69fff200c))
* **tui:** first-run CTAs, safer confirms, loading + cap feedback ([6ad0a75](https://github.com/sergiught/openfga-cli/commit/6ad0a75a9946ff3e0c5142ed1f1d059e2299988e))
* **tui:** flag truncated query output instead of dropping it silently ([210abc8](https://github.com/sergiught/openfga-cli/commit/210abc8e9d3a0e6fdeb620eabe9f90d5f9481433))
* **tui:** inline field validation on blur ([8cd1dff](https://github.com/sergiught/openfga-cli/commit/8cd1dffef6eaff510ab083d745feab5778681305))
* **tui:** mouse support — wheel scroll and click-to-navigate ([3f9dbc9](https://github.com/sergiught/openfga-cli/commit/3f9dbc969cbd15d11912826854e717362e570088))
* **tui:** richer mouse — buttons, list rows, chips, list scroll, click-away ([11236e3](https://github.com/sergiught/openfga-cli/commit/11236e3efb9c8c63fc293bcc17137872ec03f414))
* **tui:** show a loading spinner in empty stores/tuples/changes panes ([027ec37](https://github.com/sergiught/openfga-cli/commit/027ec37f4f41709ba513b8047205e55de0008526))
* **tui:** stack toasts, wrap text, linger longer on errors ([b84dfcd](https://github.com/sergiught/openfga-cli/commit/b84dfcd5b2cd31358b7639965ffa9ce3430de967))
* **ui/list:** add compact single-line mode to list wrapper ([cd3c43f](https://github.com/sergiught/openfga-cli/commit/cd3c43f18ae62f4afd72ecdab67b0a7ef5b25475))
* **ui:** bespoke themed field/form component to replace huh ([a04af8b](https://github.com/sergiught/openfga-cli/commit/a04af8b6b8a0cfc7e422b2cbddc7935ae4840204))
* **ui:** cycle-select form field + FocusIndex; profile nav icon; profile status chip ([5296979](https://github.com/sergiught/openfga-cli/commit/5296979eaf7e00929b907f6e37068ccb273371d4))
* **ui:** icon capability rungs (nerdfont/unicode/off) with config key ([3bcdf20](https://github.com/sergiught/openfga-cli/commit/3bcdf2060bf545edfae7822c010bacde8deb8617))
* **ui:** migrate layout/list/fga to charm.land v2 packages ([e3b1b9e](https://github.com/sergiught/openfga-cli/commit/e3b1b9e52e9ba818a52512328f2d986067120818))


### Bug fixes

* **apilog:** capture only OpenFGA API host so OAuth token fetches never leak ([deab462](https://github.com/sergiught/openfga-cli/commit/deab462e5a4d66719ac69b3bb30d093586e8a4ee))
* **apilog:** redact secret fields in captured request/response bodies ([f8e53ff](https://github.com/sergiught/openfga-cli/commit/f8e53ffdc2771c4c4c2c33672987940cae6dca57))
* **api:** read stdin only for a `-` body; expose error bodies as JSON ([87ed884](https://github.com/sergiught/openfga-cli/commit/87ed88466d6559abc12d1d29bb48be51216efa53))
* **auth:** isolate credentials and harden config persistence ([8d858b9](https://github.com/sergiught/openfga-cli/commit/8d858b9f64c0ab86b264026780ff492033e1fc34))
* **ci:** give the test job an OS keyring for secret-store e2e tests ([b98bf21](https://github.com/sergiught/openfga-cli/commit/b98bf210094efb8b8a69dfbbe7d56e175979d43a))
* classify bad-input errors as usage (exit 2) (OUT-22, OUT-23, CLI-25) ([af7afc9](https://github.com/sergiught/openfga-cli/commit/af7afc9d1fbab5fc32777a927ba11c03d2d4b791))
* **cli:** correct exit codes and JSON shape for scripting/CI ([94d9503](https://github.com/sergiught/openfga-cli/commit/94d95037c235f40b7f2dc6291ba469ceddf92f71))
* **cli:** correct exit codes, typo suggestions, forced color and icons ([cdcbb27](https://github.com/sergiught/openfga-cli/commit/cdcbb271e2abbf5c24f729488ca88d35ad48d51b))
* **cli:** diagnose a mistyped global flag before a subcommand path (CLI-27) ([bf24765](https://github.com/sergiught/openfga-cli/commit/bf247653e47e7bb3c2a20ad5e6ec787cec146ecc))
* **cli:** dry-run JSON, usage exit codes and friendly validation for commands ([c428f6d](https://github.com/sergiught/openfga-cli/commit/c428f6de98c8a45115c0a3fc19059cf1d441a755))
* **client:** warn on cleartext credentials sent to the OAuth token endpoint ([de29463](https://github.com/sergiught/openfga-cli/commit/de294638f08c5b1c61e555023b21912d51856355))
* **client:** warn on plaintext-http credentials and key/method mismatch ([60b6783](https://github.com/sergiught/openfga-cli/commit/60b6783b47851f2fdd60e1bc47021a357e4822f7))
* **clierr:** point an unreachable token endpoint at token_url, not api_url (OUT-24) ([a231bd2](https://github.com/sergiught/openfga-cli/commit/a231bd23d4ef297be93d8699d69ba517c9fef954))
* **cli:** harden automation, output, and I/O ([6d8936e](https://github.com/sergiught/openfga-cli/commit/6d8936ec1b5641a529b03f5e55ee15cbb18dd6fb))
* **cli:** honor FORCE_COLOR on the early config-load error path (CFG-27) ([19f2f0d](https://github.com/sergiught/openfga-cli/commit/19f2f0dd4cfc1665900f3f2367a9fc22bfe29ebb))
* **cli:** indent the root help banner to align with the sections ([fceddfd](https://github.com/sergiught/openfga-cli/commit/fceddfd86a3497ae485c3b82deef30ac8de9b658))
* **cli:** keep single-file go run working ([53fcced](https://github.com/sergiught/openfga-cli/commit/53fcced6bff3a53b849a56795be88e5b33b20818))
* **cli:** match crush help layout — bold headers + raised usage/examples block ([71ae88a](https://github.com/sergiught/openfga-cli/commit/71ae88aef425153c427b977e6dfa0b862cfdceb1))
* **cli:** resolve version banner, exit 130 on interrupt, colorize errors safely ([80f86d8](https://github.com/sergiught/openfga-cli/commit/80f86d86b4e9ddc671921d2b93ade5fba4755c4f))
* **cli:** stabilize scripting and automation contracts ([d7cb17d](https://github.com/sergiught/openfga-cli/commit/d7cb17d079157b0ea394e6d180734433997ce477))
* **config:** honor FORCE_COLOR/NO_COLOR conventions and fix icons warning ([7087fab](https://github.com/sergiught/openfga-cli/commit/7087fab17106323d0d739c430462aa614c051953))
* **config:** let env vars bypass the keyring in Resolve ([5372c6c](https://github.com/sergiught/openfga-cli/commit/5372c6c6b063179455937bf88b2c68fcfcbb995b))
* **config:** refuse to clobber an unreadable config; surface load errors ([f3c71d0](https://github.com/sergiught/openfga-cli/commit/f3c71d02c3dfb8af8cf7d57a36198409f88b3ed8))
* **dsl:** don't flag CEL identifiers in condition bodies as undefined types ([b47f25f](https://github.com/sergiught/openfga-cli/commit/b47f25fc4113c6df7d92b40806e55d81b6f2c43a))
* **icons:** correct Model glyph to U+E725; pin exact nav codepoints in test ([3887d62](https://github.com/sergiught/openfga-cli/commit/3887d625666716983e8d25aa166e12b26f250945))
* **icons:** move nav glyphs to Nerd-Font-v2-safe ranges; add powerline caps ([322b89e](https://github.com/sergiught/openfga-cli/commit/322b89e6d76e885362ef651455b3016657cd5e43))
* **list:** mint selection accent instead of off-theme pink ([8d2d3bb](https://github.com/sergiught/openfga-cli/commit/8d2d3bb6a08115d2fca16c8accd677295007d317))
* **logo:** keep smooth downsampling; cull sub-15% alpha bleed in the renderer ([78d0157](https://github.com/sergiught/openfga-cli/commit/78d0157b1800e2ef26c11fd07c18d9a40057b10b))
* **model:** report unknown subcommand, not unknown flag, when a typo carries a flag (CLI-26) ([4ce3e3c](https://github.com/sergiught/openfga-cli/commit/4ce3e3cfe0c92450103a00cd3c6308b1a0747d6b))
* **output:** honor --plain/-o table for model get and version (OUT-25, CLI-30) ([e0b8c01](https://github.com/sergiught/openfga-cli/commit/e0b8c01a22ea2b4d5a3dc28a81b77e67ca40812a))
* **output:** profile-aware writers restore NO_COLOR/pipe byte parity for classic CLI ([805f4fd](https://github.com/sergiught/openfga-cli/commit/805f4fdc0cb86b98ee7011aeda078a2d23ca0549))
* **output:** resolve build version in `version --json` ([b8bada2](https://github.com/sergiught/openfga-cli/commit/b8bada25333937e34d6a0777cbb99990e186f2f0))
* **output:** treat a closed pipe on -o yaml as a clean short read (OUT-21) ([096dce2](https://github.com/sergiught/openfga-cli/commit/096dce22fe4ab4cb8d3bb2cb2f67d227ecc1f025))
* **playground:** advertise the / filter with a hint and placeholder ([64dda9f](https://github.com/sergiught/openfga-cli/commit/64dda9f695ea992cfc59a890586b343b31e20b1a))
* **playground:** align API log status column ([96e1034](https://github.com/sergiught/openfga-cli/commit/96e1034d277a67ef479f2c3d3f4bef40d9238068))
* **playground:** API log label honors OPENFGA_PROFILE/FGA_PROFILE (CFG-26) ([73073d0](https://github.com/sergiught/openfga-cli/commit/73073d0f887a46ac22503b15ae77485eb53fa077))
* **playground:** blank line between the assertion form's last field and its hint ([2a3f7f2](https://github.com/sergiught/openfga-cli/commit/2a3f7f21c586ad488a99d3a01d69148c46b0e26f))
* **playground:** cap query dashboard and sidebar height so the status bar survives short terminals ([1e9f009](https://github.com/sergiught/openfga-cli/commit/1e9f0092c49dbb5406e809fdb2492714740f2adc))
* **playground:** consistent tab keycap hint across section states ([7e91118](https://github.com/sergiught/openfga-cli/commit/7e91118b462f28496a9c3f851b40ab468c1bb197))
* **playground:** consistent, persistent query errors; capitalize context labels ([0f76a04](https://github.com/sergiught/openfga-cli/commit/0f76a04276fc8d2dd9730b4b5d186698327e7585))
* **playground:** don't strand the Model view on a stale pinned model_id (TUI-33) ([b1e9de1](https://github.com/sergiught/openfga-cli/commit/b1e9de136256f2d4429e467b33516afe3317be24))
* **playground:** follow the just-added row after a write (TUI-34) ([d89cec3](https://github.com/sergiught/openfga-cli/commit/d89cec3e13d2e0fcaeb85b833d5ffbc74b74a5a2))
* **playground:** gate verdict flash on badge results; skip empty preview header ([f0bc61c](https://github.com/sergiught/openfga-cli/commit/f0bc61c36881e0228c992111d4cc7a166dcea83d))
* **playground:** honest assertion toast, working palette filter and hint fixes ([289165e](https://github.com/sergiught/openfga-cli/commit/289165eef8eaa429c83a3abc1fe2a6e40d8c3963))
* **playground:** inline query context/contextual tuples (drop unreachable `c` key) ([ef07db6](https://github.com/sergiught/openfga-cli/commit/ef07db6fb1fff3266fb85393bd6c260833b73150))
* **playground:** keep assertion filter clean in compact mode ([f6583ec](https://github.com/sergiught/openfga-cli/commit/f6583ecdb2b8bc9833a18f653cc2b904bc033144))
* **playground:** keep cursor in view after resize; document cursor-past-width ([ce1ef8f](https://github.com/sergiught/openfga-cli/commit/ce1ef8f04ad8dea324922b9559dada0389476013))
* **playground:** keep the query editable after running it ([9532bf5](https://github.com/sergiught/openfga-cli/commit/9532bf54bbbcd7b1acad755e026b284ff64a3546))
* **playground:** let the boot-time size report start the entrance instead of killing it ([fbeb572](https://github.com/sergiught/openfga-cli/commit/fbeb5725c1151ca7135485bb59b9c83755e94b77))
* **playground:** preserve graph viewport scroll offsets across terminal resizes ([01e76bd](https://github.com/sergiught/openfga-cli/commit/01e76bde7b5776c8389b60c48ca3656880e89b66))
* **playground:** record query history mode from the completed query, not live UI state ([1aaecba](https://github.com/sergiught/openfga-cli/commit/1aaecbad51a971bc5769d556e23c294b67993ced))
* **playground:** scope API log origin label to the active profile ([fc59c6d](https://github.com/sergiught/openfga-cli/commit/fc59c6d1cb6efd34e2325147e60fe6060817246f))
* **playground:** show apply error once (footer, not also a toast) while editing ([72c52db](https://github.com/sergiught/openfga-cli/commit/72c52db5a0f5f43250b70ee76a94af764425691e))
* **playground:** size dialog content to the dialog so modal corners stay on screen ([f98aca9](https://github.com/sergiught/openfga-cli/commit/f98aca915a584c7d855c2dc4ffbfe2c5ed8bee48))
* **playground:** size query form to fit inside its box (no more clipped … column) ([32bda78](https://github.com/sergiught/openfga-cli/commit/32bda7853080d501d331936f4f9fed9f63d33655))
* **playground:** size section lists to the master-detail split width ([01f81ba](https://github.com/sergiught/openfga-cli/commit/01f81ba914a3f4015b991b998442c68f7a437523))
* **playground:** splash auto-dismisses when the shimmer completes ([997ff25](https://github.com/sergiught/openfga-cli/commit/997ff2550b5016a6365995d7886ae944a66bdbe2))
* **playground:** TUI correctness, secret handling and rerun ergonomics ([44b1a55](https://github.com/sergiught/openfga-cli/commit/44b1a5514f02510b28405759f025eb2268b55cc7))
* **playground:** wrap long editor error footer instead of truncating ([a4961a2](https://github.com/sergiught/openfga-cli/commit/a4961a2f3252fbc1c4fa4e07645514304485d1a1))
* **profiles:** show a masked private_key row for private_key_jwt (AUTH-9) ([575bc7d](https://github.com/sergiught/openfga-cli/commit/575bc7db7b35db3bde44c7cb9fbfce0c82e51553))
* **profiles:** usage exit codes + confirm the wide auth unset (CLI-28, CLI-29) ([10553e9](https://github.com/sergiught/openfga-cli/commit/10553e95088984d2b1e112e05f45b1d802f15503))
* **query:** flag parity for expand + swap-catching validation (CLI-23, CLI-24) ([ac9bea0](https://github.com/sergiught/openfga-cli/commit/ac9bea0653c4d56fe15d1936d145f1ab65c17bad))
* **shell:** align sidebar context (connection status) icon with the nav tab icons ([39f30bd](https://github.com/sergiught/openfga-cli/commit/39f30bd133151fdd1a30f382775f4a00f8d90955))
* **shell:** correct sidebar/main width math so regions fill the full width ([462c3ae](https://github.com/sergiught/openfga-cli/commit/462c3aeccf8314a3aea94bf304193a81361d8eb2))
* **shell:** drop painted panel backgrounds to eliminate bg-discontinuity artifacts; structure via borders ([a1b6f70](https://github.com/sergiught/openfga-cli/commit/a1b6f701c472efa7877144aa348f20e0c1434602))
* **shell:** drop sidebar tagline, show version only (no mid-word truncation) ([89d0540](https://github.com/sergiught/openfga-cli/commit/89d0540f59bfed9a2ddb9790b3d12cafad571a10))
* **shell:** make the modal panel fill cleanly to its border ([b252869](https://github.com/sergiught/openfga-cli/commit/b252869039bc0421742701bae4d06b4af0c72d96))
* **shell:** show badge on active nav item; test splash keypress dismissal ([ad4bcd3](https://github.com/sergiught/openfga-cli/commit/ad4bcd3d00afd1ddac4f18de63ba5407fbe6d698))
* **shell:** theme the modal dialog and form inputs ([1d6c980](https://github.com/sergiught/openfga-cli/commit/1d6c98095b4a69582d88aa4bce124bd8db0b7340))
* **shell:** truncate content and clamp frame to terminal size to prevent wrap-induced overflow ([8d826c8](https://github.com/sergiught/openfga-cli/commit/8d826c8e140ffcd11d2494e1a1c426e2bcaf4b7e))
* **style:** SectionHeader pads to exact width when the title fills it ([6b76e7f](https://github.com/sergiught/openfga-cli/commit/6b76e7fc9d6fec2b9e5023b34d2585596c3eb194))
* **test:** forward D-Bus session to the keyring e2e subprocess ([027aeaf](https://github.com/sergiught/openfga-cli/commit/027aeafe9b8e494e4cff7c1439e0f8ad4942b0ca))
* **toast:** cap toast text width so wide errors never overlap the sidebar ([82de1f0](https://github.com/sergiught/openfga-cli/commit/82de1f0469271d3d5c9fa8faa16e11a5591b8591))
* **tui:** error toasts auto-expire instead of sticking forever ([b97797d](https://github.com/sergiught/openfga-cli/commit/b97797d35e11f9219f7264a9f2b4fe25f6396a03))
* **tui:** fill toast background behind the icon separator and wrapped lines ([1c38a23](https://github.com/sergiught/openfga-cli/commit/1c38a2384f2804efa42865170a9caa7e4c39efb3))
* **tui:** harden playground state and interactions ([3a9d6ca](https://github.com/sergiught/openfga-cli/commit/3a9d6ca1f4f05aaee1726d01505981df23795257))
* **tui:** prevent stale state and unsafe rendering ([e4c77b1](https://github.com/sergiught/openfga-cli/commit/e4c77b1de9ebee82718b8be0e3688928808383da))
* **tui:** symmetric graph pan and accurate form key hints ([3f17030](https://github.com/sergiught/openfga-cli/commit/3f17030da314fb4142c30fbf7ebcbbe985d98433))


### Performance

* **tui:** stop the spinner tick loop when idle ([1dc38c5](https://github.com/sergiught/openfga-cli/commit/1dc38c56ae0cf80721a0be9922e65de1728a09a5))


### Refactors

* **config:** remove legacy api_token field ([7b076d1](https://github.com/sergiught/openfga-cli/commit/7b076d100c26c7d6157e787477dd1cbbf22f3955))
* **fga:** share context parsers, fix positional/flag Triple ([c844168](https://github.com/sergiught/openfga-cli/commit/c84416828e21bbcdf75580b717a0b58b46928439))
* **playground:** editorBody uses editorViewportRows ([5c4b56e](https://github.com/sergiught/openfga-cli/commit/5c4b56eba93333b4193fc669ade2fc60d1493c97))
* **playground:** guard diagnostics reparse; test multi-error ordering ([f126f4b](https://github.com/sergiught/openfga-cli/commit/f126f4ba0f7696a06a8ad7eaed1eb4ea8a5eea25))
* rename app package and App type to cli/CLI ([f983089](https://github.com/sergiught/openfga-cli/commit/f983089fbfa78c0ec835d2ca2e898734eed3d3cb))


### Tests

* add seeded OpenFGA + auth0-mock demo stack for OAuth flows ([3dafb13](https://github.com/sergiught/openfga-cli/commit/3dafb1358f7f12516989888de9dcfda678a22d40))
* **cli:** black-box e2e coverage for command behavior ([d33f7e6](https://github.com/sergiught/openfga-cli/commit/d33f7e6b10923321d901d05627999392c8e22b0a))
* **oauth:** seed the GitHub authorization model + varied tuples ([8e8833c](https://github.com/sergiught/openfga-cli/commit/8e8833c8fd08377e79af50079d38b27725fe6041))
* **playground:** assert compact toggle drops the detail card ([7f4bb09](https://github.com/sergiught/openfga-cli/commit/7f4bb094c9d7d9fe1030374ba3e4d9fae21beac7))
* **playground:** make verdict-flash regression test discriminate the badge gate ([3897f16](https://github.com/sergiught/openfga-cli/commit/3897f16db715e8938be68d5706edbefaae5ed86d))


### Documentation

* add README, MIT LICENSE, and CONTRIBUTING ([7c5ce21](https://github.com/sergiught/openfga-cli/commit/7c5ce21643834fc90b528113f2b2a7db9b85fa77))
* add repo banner ([47b1723](https://github.com/sergiught/openfga-cli/commit/47b1723ea25a94d573b4aebbdc612f8f0694bffd))
* clarify --store/--model vs --store-id/--model-id flag naming ([567805e](https://github.com/sergiught/openfga-cli/commit/567805e4af4cd01e93dd68bb932da390f371bd04))
* correct env vars, examples and contributing guide ([eb3dd5b](https://github.com/sergiught/openfga-cli/commit/eb3dd5bbeabd089efbd2998b7d39ee5a5cb742ce))
* document hardened CLI and release workflows ([e5e40ea](https://github.com/sergiught/openfga-cli/commit/e5e40ea6b9e249503d14596f9ed71b227325e4b7))
* document OAuth env vars and correct stale help/README copy ([50addd5](https://github.com/sergiught/openfga-cli/commit/50addd56f5df96d2d0305f1476fd102ad34e1eeb))
* document that model write accepts .fga DSL (DOC-12) ([283f0af](https://github.com/sergiught/openfga-cli/commit/283f0afa56b4b3d9d0b97fc3d506549def7c140b))
* embed playground and quickstart demo GIFs in README ([8baef23](https://github.com/sergiught/openfga-cli/commit/8baef239858228c4e944a3a596f1dd3e0df821a7))
* **examples:** add generated CLI and playground demo GIFs ([cd4adc5](https://github.com/sergiught/openfga-cli/commit/cd4adc59c4a0ca118fa29cc97b0ba0f9e2f644ae))
* **examples:** add VHS tapes and make gifs target for CLI/TUI demos ([6fe1887](https://github.com/sergiught/openfga-cli/commit/6fe18870e212ee4989bb86e86bf7a95a05da9ef3))
* **examples:** end hero demo with an allowed check and its resolution ([b812faf](https://github.com/sergiught/openfga-cli/commit/b812faf03452a39b562325c68243a86a7df12c2b))
* **examples:** render tab icons and enlarge demo GIFs ([ca4095b](https://github.com/sergiught/openfga-cli/commit/ca4095b7174ff7b3afff1efd51394a85e2aad55c))
* **examples:** richer playground demo and smaller quickstart GIF ([98b73ee](https://github.com/sergiught/openfga-cli/commit/98b73ee1f25516973020b885a4b8df34f112140d))
* **examples:** use .fga model and move tapes to examples/tapes ([ade2b42](https://github.com/sergiught/openfga-cli/commit/ade2b42a4caca71e337254c9d5b3131f6e93f939))
* **examples:** use ctrl+r for the resolution in the hero demo ([21ffc42](https://github.com/sergiught/openfga-cli/commit/21ffc42a2a2658fcce03247b6f22dd9548bc78ab))
* improve onboarding and operational guidance ([c5b6a68](https://github.com/sergiught/openfga-cli/commit/c5b6a68a5371476f64c964a6eaa2445dd0d9dd9f))
* **readme:** show model.fga DSL in quickstart and reorder the playground hero gif ([2d34198](https://github.com/sergiught/openfga-cli/commit/2d3419872641b4fb6b1b758f92eff393d50afc2c))
* **shell:** fix package doc — structure comes from headers, not borders ([6ea6ed0](https://github.com/sergiught/openfga-cli/commit/6ea6ed07c31ff191e7a7047e6a57ecf7bfd40a71))
* **shell:** refresh stale wordmark comment; reconcile spec shimmer-axis wording ([734f202](https://github.com/sergiught/openfga-cli/commit/734f202f933a67891cd9b4de2a4f67fa7c582a97))


### Build

* add Makefile and GitHub issue/PR templates ([d2631d8](https://github.com/sergiught/openfga-cli/commit/d2631d88d9f05e20f7165e17834cf537d2f952c1))
* add release pipeline, CI, and distribution ([4110409](https://github.com/sergiught/openfga-cli/commit/41104092b5a7b4ea7b53884cbcff6f020210f596))
* publish container image as ghcr.io/sergiught/ofga ([6dfd144](https://github.com/sergiught/openfga-cli/commit/6dfd14494af950dcc4bdd16b3e7811281ad62f89))
* require go 1.26.5 and derive Go version from go.mod in CI ([b26c33e](https://github.com/sergiught/openfga-cli/commit/b26c33ed23013c21aff616372e9bb1d63526e342))
* scaffold Homebrew tap and AUR packaging ([a79d387](https://github.com/sergiught/openfga-cli/commit/a79d387114c47e1a0dfc940732ddb078be5fb913))


### Chores

* release 0.263.0 ([f30b7de](https://github.com/sergiught/openfga-cli/commit/f30b7debf387d7763e936f8adcfced4a702d2427))
