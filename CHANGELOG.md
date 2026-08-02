# Changelog

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
