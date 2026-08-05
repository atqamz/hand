# Changelog

## [0.2.0](https://github.com/atqamz/secondhand/compare/v0.1.4...v0.2.0) (2026-08-05)


### Features

* **cmd:** move worker setup out of init ([#164](https://github.com/atqamz/secondhand/issues/164)) ([48327af](https://github.com/atqamz/secondhand/commit/48327afae04409931e81c1413435aaaa4d2f1bef))
* **cmd:** render every command as a TOON document with aggregates, --fields and next steps ([#155](https://github.com/atqamz/secondhand/issues/155)) ([a69e405](https://github.com/atqamz/secondhand/commit/a69e405278b54d292c5b4c3d7f024d7a030e2f1a))
* **watcher:** resume a worker whose harness stopped on a usage limit ([#154](https://github.com/atqamz/secondhand/issues/154)) ([ab2db52](https://github.com/atqamz/secondhand/commit/ab2db5296633bcda28745884eaf4802521fd6f7f))


### Bug Fixes

* **cmd:** accept a completed scout deliverable on a ship-recorded task ([#148](https://github.com/atqamz/secondhand/issues/148)) ([120f92e](https://github.com/atqamz/secondhand/commit/120f92e5d638a85c078ee029b8b7f6ce6cc570bf))
* **cmd:** fold case in repo slug comparisons for PR detection ([#147](https://github.com/atqamz/secondhand/issues/147)) ([8db4770](https://github.com/atqamz/secondhand/commit/8db4770b219ba0a69678a78404ecce259fc181ac))
* **cmd:** resolve promote's tier after task and gate checks ([#157](https://github.com/atqamz/secondhand/issues/157)) ([fa212bf](https://github.com/atqamz/secondhand/commit/fa212bf0f8f57f2be7fe0f7d4cd10aaf937e9e99))
* detect an aborted worktree return and keep a registered project syncable ([#158](https://github.com/atqamz/secondhand/issues/158)) ([46415fb](https://github.com/atqamz/secondhand/commit/46415fbc511b5f29c405a6eaf5909eae77483ddb))
* **harness:** launch Codex as an interactive worker ([#165](https://github.com/atqamz/secondhand/issues/165)) ([e257e2f](https://github.com/atqamz/secondhand/commit/e257e2f78cda4b5f94a5529edf3f272f4fdd2b3d))
* **harness:** warn when a harness cannot carry a declared model or prompt ([#152](https://github.com/atqamz/secondhand/issues/152)) ([d6ce20a](https://github.com/atqamz/secondhand/commit/d6ce20a9b1918a64a0213ee8cfe44351c7d99932))
* **release:** allow pre-1.0 minor bumps ([#168](https://github.com/atqamz/secondhand/issues/168)) ([8dbddc6](https://github.com/atqamz/secondhand/commit/8dbddc6bc2b6ee031fde9cd0174f7858d4eccf40))
* **state:** discard the report offset when a same-length rewrite invalidates it ([#150](https://github.com/atqamz/secondhand/issues/150)) ([c2fc402](https://github.com/atqamz/secondhand/commit/c2fc402d6c20d37b75269e34f5f1551a53928264))

## [0.1.4](https://github.com/atqamz/secondhand/compare/v0.1.3...v0.1.4) (2026-08-04)


### Features

* **cmd:** detect gate-opened PRs on a project's declared upstream ([#142](https://github.com/atqamz/secondhand/issues/142)) ([63bfe31](https://github.com/atqamz/secondhand/commit/63bfe31c0de80b901a2f687381ef10b996dcb44a))
* **cmd:** make hand watch a singleton and flag unread terminal reports ([#138](https://github.com/atqamz/secondhand/issues/138)) ([4eb6932](https://github.com/atqamz/secondhand/commit/4eb6932d5c138a1105fa55e43992cc4f0999d47a))
* **cmd:** record delivered-not-landed work and accept a PR on a project's declared upstream ([#135](https://github.com/atqamz/secondhand/issues/135)) ([9bccfb1](https://github.com/atqamz/secondhand/commit/9bccfb17bd2f2ee1c381ae9e22c8b6f83555bc93))
* **cmd:** seed operator context, learnings and backlog archive files ([#133](https://github.com/atqamz/secondhand/issues/133)) ([95e22b3](https://github.com/atqamz/secondhand/commit/95e22b398e9996cbe9bf205c51836c941d986576))
* deliver the operator-decision rule to workers and add hand doctor ([341b151](https://github.com/atqamz/secondhand/commit/341b151fd984a63616872754ac0829f11363984d))
* **store:** gate schema changes on PRAGMA user_version ([#115](https://github.com/atqamz/secondhand/issues/115)) ([5c6f237](https://github.com/atqamz/secondhand/commit/5c6f23764acd8cecca4392532739d7b906259366))
* **watcher:** notify a supervisory agent when no session is watching ([#131](https://github.com/atqamz/secondhand/issues/131)) ([d135901](https://github.com/atqamz/secondhand/commit/d1359010412cbd543f699804516d59f1f2d8f9dc))


### Bug Fixes

* **cmd:** create the data directory update seeds into and report every seed failure ([#139](https://github.com/atqamz/secondhand/issues/139)) ([f6f58cc](https://github.com/atqamz/secondhand/commit/f6f58cc9de15fd87c529914b34349b09b1fa3500))
* **cmd:** print HAND_HOME mismatch warning as an absolute path ([#126](https://github.com/atqamz/secondhand/issues/126)) ([d045762](https://github.com/atqamz/secondhand/commit/d045762bb2dc2f7b26098e9f84da7310487c215b))
* **herdr:** close the workspace herdr created when workspace create returns an unusable response ([#112](https://github.com/atqamz/secondhand/issues/112)) ([21fb575](https://github.com/atqamz/secondhand/commit/21fb5756e87c5ad5db88f026f07a2c658d6ebea6))
* **herdr:** sanitize inherited harness-identity env on pane creation and document holds in the fleet-home template ([#130](https://github.com/atqamz/secondhand/issues/130)) ([78f94dc](https://github.com/atqamz/secondhand/commit/78f94dca1f43626f2274ee4a4e8f74ee133191ea))
* make hand send survive a busy composer and stop herdr lookups matching foreign state ([a285913](https://github.com/atqamz/secondhand/commit/a285913c74e3e3a4d284719cdbc269ecfa86e267))
* **state:** tolerate a report channel rewritten in place ([#144](https://github.com/atqamz/secondhand/issues/144)) ([aaa112f](https://github.com/atqamz/secondhand/commit/aaa112ff0c9e078f273b989420d220a912bb4186))
* **watcher:** anchor parked on durable pane-start and fired-for stamps ([#143](https://github.com/atqamz/secondhand/issues/143)) ([ddb57dc](https://github.com/atqamz/secondhand/commit/ddb57dc364aa6f1b765200fa21f488867407348a))
* **worktree:** key the collision guard on the treehouse lease id ([#132](https://github.com/atqamz/secondhand/issues/132)) ([56c52b3](https://github.com/atqamz/secondhand/commit/56c52b3845cfb87fe093b9e4d50eec706de326e3))

## [0.1.3](https://github.com/atqamz/secondhand/compare/v0.1.2...v0.1.3) (2026-08-02)


### Features

* **cmd:** resolve a fleet home from HAND_HOME or an ancestor walk instead of the working directory ([#104](https://github.com/atqamz/secondhand/issues/104)) ([a4e0369](https://github.com/atqamz/secondhand/commit/a4e0369f048822d34458a48835493b3794475ef3))
* **cmd:** write a durable completion record before teardown removes task state ([#101](https://github.com/atqamz/secondhand/issues/101)) ([5cbcd23](https://github.com/atqamz/secondhand/commit/5cbcd23c29dc16d75963cb0b285b171b745a5990))
* **state:** back machine state with sqlite and index the prose corpus ([#107](https://github.com/atqamz/secondhand/issues/107)) ([8f44164](https://github.com/atqamz/secondhand/commit/8f44164d7ba51ded96ce7ac943ce05f3fbdadeb4))

## [0.1.2](https://github.com/atqamz/secondhand/compare/v0.1.1...v0.1.2) (2026-08-02)


### Features

* **cmd:** honor a brief's declared model and effort on spawn and promote ([#59](https://github.com/atqamz/secondhand/issues/59)) ([017fffa](https://github.com/atqamz/secondhand/commit/017fffa7c9ba1b3da788a1c5c85b4846e6a645f2))
* **cmd:** name what is dirty on teardown refusal and allow already-landed dirt ([#93](https://github.com/atqamz/secondhand/issues/93)) ([bcaee85](https://github.com/atqamz/secondhand/commit/bcaee8558d484b8dba8a20631f9547cae1ddb0de))
* **cmd:** refuse to dispatch into an uninitialized no-mistakes gate ([#94](https://github.com/atqamz/secondhand/issues/94)) ([d4cd130](https://github.com/atqamz/secondhand/commit/d4cd130e000813b1b1c0a67097bd36831e20ffaa))
* **cmd:** surface PR merge state in hand status ([6a768aa](https://github.com/atqamz/secondhand/commit/6a768aa4bf7e430a51acaf278efba5497bbd27cf)), closes [#51](https://github.com/atqamz/secondhand/issues/51) [#58](https://github.com/atqamz/secondhand/issues/58)
* **watch:** add hand watch --until-event so the watcher can wake its orchestrator ([c14bf27](https://github.com/atqamz/secondhand/commit/c14bf278004586feaed991be2624b3963a2d4537))


### Bug Fixes

* **cmd:** cap report rendering in hand status ([#91](https://github.com/atqamz/secondhand/issues/91)) ([4d2294a](https://github.com/atqamz/secondhand/commit/4d2294a523bfbc4623acf667efb6ddf4c260c58a))
* **cmd:** detect gate-opened PRs by branch head ref ([#76](https://github.com/atqamz/secondhand/issues/76)) ([8fddac4](https://github.com/atqamz/secondhand/commit/8fddac43128f524994ab146f96abce38de421ac1))
* **cmd:** reuse a new workspace's root tab instead of orphaning it ([#72](https://github.com/atqamz/secondhand/issues/72)) ([81e6427](https://github.com/atqamz/secondhand/commit/81e6427efb049de49b85ca71a59c4b13ac5c9a48))
* **ghutil:** resolve multi-PR head refs by preference tier instead of an arbitrary pick ([4149241](https://github.com/atqamz/secondhand/commit/4149241f9f153e6f98ec0c340beeafc38b3fe863))

## [0.1.1](https://github.com/atqamz/secondhand/compare/v0.1.0...v0.1.1) (2026-07-27)


### Features

* **cmd:** add worker report channel with hand pr and dashboard reconciliation ([#38](https://github.com/atqamz/secondhand/issues/38)) ([7fd9e3e](https://github.com/atqamz/secondhand/commit/7fd9e3e47cab2a45d4f58186fb1a9d9d78036362))


### Bug Fixes

* **cmd:** confirm workers clear first-run dialogs before reporting spawn success ([#37](https://github.com/atqamz/secondhand/issues/37)) ([e3bbcb3](https://github.com/atqamz/secondhand/commit/e3bbcb3a62bc0a86ed6b7df329038971c1691b68))
* **cmd:** parse subprocess stdout only and pad CLI table columns with tabwriter ([#23](https://github.com/atqamz/secondhand/issues/23)) ([649fa02](https://github.com/atqamz/secondhand/commit/649fa02643ae77b8491acba3a2c22409be0eb58a))
* launch workers interactively and stop leaking herdr workspaces ([#29](https://github.com/atqamz/secondhand/issues/29)) ([170dd98](https://github.com/atqamz/secondhand/commit/170dd988c86a89cc4907a0cf56e6b8f2080f2f6a))

## 0.1.0 (2026-07-26)


### Features

* bootstrap secondhand CLI scaffold ([#1](https://github.com/atqamz/secondhand/issues/1)) ([598bf73](https://github.com/atqamz/secondhand/commit/598bf737476ae0f80ed5f998b05206ac0723ae6b))
* **cmd:** add hand update self-update and startup version notice ([#11](https://github.com/atqamz/secondhand/issues/11)) ([832ca37](https://github.com/atqamz/secondhand/commit/832ca372ed70d7ac84752c8345f445dc90d3cbce))
* **cmd:** add merge, promote, notify, and project sync commands ([#10](https://github.com/atqamz/secondhand/issues/10)) ([8fde5fa](https://github.com/atqamz/secondhand/commit/8fde5fac9f92988953b1d904727cd8bad119743d))
* **cmd:** add task lifecycle commands ([#7](https://github.com/atqamz/secondhand/issues/7)) ([68ebd68](https://github.com/atqamz/secondhand/commit/68ebd6840f551c06f74933a52f797b1802466fbd))
* **cmd:** add watch command and dashboard maintenance ([#9](https://github.com/atqamz/secondhand/issues/9)) ([9c27f45](https://github.com/atqamz/secondhand/commit/9c27f45a63879bf92b6977313d2c46131df9dd9a))
* **cmd:** add workspace initialization and project management ([#2](https://github.com/atqamz/secondhand/issues/2)) ([0e445d4](https://github.com/atqamz/secondhand/commit/0e445d4922d9f63d2bda8361c3bf2e98f324326f))
* **cmd:** refresh AGENTS.md and report release notes on hand update ([#16](https://github.com/atqamz/secondhand/issues/16)) ([727080f](https://github.com/atqamz/secondhand/commit/727080f9c581fc9753ae331d173e472bfec63f1c))
* set initial release version to 0.1.0 ([#5](https://github.com/atqamz/secondhand/issues/5)) ([2bfc925](https://github.com/atqamz/secondhand/commit/2bfc92597b7aa27b3143155b9788bd9678c2719b))


### Bug Fixes

* **cmd:** enforce exit-code contract and split watch diagnostics to stderr ([#12](https://github.com/atqamz/secondhand/issues/12)) ([93a8691](https://github.com/atqamz/secondhand/commit/93a8691d93834a5e59d132d2932d28a9267089eb))
* make the nix flake usable for builds and dev shells ([#14](https://github.com/atqamz/secondhand/issues/14)) ([d4c5275](https://github.com/atqamz/secondhand/commit/d4c5275fc914b38fca8541f6a33e235c8934e045))
