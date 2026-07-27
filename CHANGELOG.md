# Changelog

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
