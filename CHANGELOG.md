# Changelog

## [0.7.2](https://github.com/atqamz/hand/compare/v0.7.1...v0.7.2) (2026-08-29)


### Bug Fixes

* **npm:** read npm 12's array-shaped npm view document ([#512](https://github.com/atqamz/hand/issues/512)) ([b8d22bb](https://github.com/atqamz/hand/commit/b8d22bb48f1e74de192cb275a5bd5d919070c66c)), closes [#511](https://github.com/atqamz/hand/issues/511)

## [0.7.1](https://github.com/atqamz/hand/compare/v0.7.0...v0.7.1) (2026-08-29)


### Bug Fixes

* **npm:** retry post-publish verification instead of failing a first-ever publish ([#507](https://github.com/atqamz/hand/issues/507)) ([2a9a8e3](https://github.com/atqamz/hand/commit/2a9a8e379538513fd54fbf4e58dcf08015ccda88))

## [0.7.0](https://github.com/atqamz/hand/compare/v0.6.0...v0.7.0) (2026-08-29)


### Features

* **cmd:** record a PR delivered to a sibling repository with hand pr --cross-repo ([#467](https://github.com/atqamz/hand/issues/467)) ([e1cb96f](https://github.com/atqamz/hand/commit/e1cb96f415ecbf5d54e85b2b54e1f5c30a4510ff))
* **completion:** carry project identity on completion records ([#400](https://github.com/atqamz/hand/issues/400)) ([c7b3f5f](https://github.com/atqamz/hand/commit/c7b3f5f76e31037ad693ee4cb2b73c3a7b7a24bf))
* **harness:** add worker-only Antigravity support ([#351](https://github.com/atqamz/hand/issues/351)) ([b424e89](https://github.com/atqamz/hand/commit/b424e89538e3adfd88bb203f154680bdfaedfad1))
* **harness:** catalogue codex's usage-limit signature ([#450](https://github.com/atqamz/hand/issues/450)) ([1edfc35](https://github.com/atqamz/hand/commit/1edfc35c5c3db6c8bfcda4ef3763b2efc66c6f4c))
* **herdr:** add Fleet-scoped session lifecycle management ([#386](https://github.com/atqamz/hand/issues/386)) ([70c75ff](https://github.com/atqamz/hand/commit/70c75ff3b5723b48481ed74247a4e4914d914833))
* **launch:** replace shell-assembled worker launches ([#349](https://github.com/atqamz/hand/issues/349)) ([e08071b](https://github.com/atqamz/hand/commit/e08071b3b7e19be1716b8f5fb7187594ed1f6d64))
* **npm:** publish stable releases to npm behind an operator-approval gate ([#489](https://github.com/atqamz/hand/issues/489)) ([7af7221](https://github.com/atqamz/hand/commit/7af7221bfb47f77765a35cf72ec6a50decd5f26d))
* **orientation:** make supervisor orientation stateless and Fleet-scoped ([#350](https://github.com/atqamz/hand/issues/350)) ([ebce096](https://github.com/atqamz/hand/commit/ebce0968ae76f565d026096c77b87a0edb03131f))
* **project:** add local Git project adoption and creation ([#334](https://github.com/atqamz/hand/issues/334)) ([42883fe](https://github.com/atqamz/hand/commit/42883fe270cf7b54350e20af79c8dca25ec23299))
* **project:** add project mode updates and renaming ([#383](https://github.com/atqamz/hand/issues/383)) ([1c7d9d5](https://github.com/atqamz/hand/commit/1c7d9d52b2984632fe4a05c9d395140306f6ddf2))
* **runtime:** add fleet identity and registry isolation ([#341](https://github.com/atqamz/hand/issues/341)) ([a3cdd3d](https://github.com/atqamz/hand/commit/a3cdd3db8a1df8d3852c18a44056e63f013f1d46))
* **selfupdate:** add stable bootstrap adoption flow ([#352](https://github.com/atqamz/hand/issues/352)) ([26b243c](https://github.com/atqamz/hand/commit/26b243cc017e0066a3833590a400f660919bc523))
* **store:** give projects a durable surrogate identity ([#399](https://github.com/atqamz/hand/issues/399)) ([9661902](https://github.com/atqamz/hand/commit/96619026414ae0edabbfc2452a463e7988f801da))
* **supervision:** autonomously re-enter supported Supervisor Harnesses without operator prompts ([#356](https://github.com/atqamz/hand/issues/356)) ([fbbdeed](https://github.com/atqamz/hand/commit/fbbdeedca24791eb623b040c8290943a0dad464b))
* **toolchain:** add private pinned core runtime ([#342](https://github.com/atqamz/hand/issues/342)) ([749d1cc](https://github.com/atqamz/hand/commit/749d1ccf9c52f7825d5608ca635722fd57c6b1a2))


### Bug Fixes

* accept idle rendered composers ([#374](https://github.com/atqamz/hand/issues/374)) ([25ed6cd](https://github.com/atqamz/hand/commit/25ed6cd017471d0b5c4e0937b06e42a0acd0c6f6))
* **attention:** surface a not-submitted send to the supervisor ([#447](https://github.com/atqamz/hand/issues/447)) ([8bb29b3](https://github.com/atqamz/hand/commit/8bb29b32324baf176b11d0d78480b944b84b45e7))
* centralize secondhand infrastructure root ([2ee29db](https://github.com/atqamz/hand/commit/2ee29db532ef7ee0a2afc0edb0588c4a53229e4d))
* **doctor:** report a Treehouse lease held by a Fleet absent from the registry ([#462](https://github.com/atqamz/hand/issues/462)) ([4af1909](https://github.com/atqamz/hand/commit/4af19092ead27ec8f5c3fff82d8f18904e544d06))
* expose exact Fleet Herdr session state ([e5880d0](https://github.com/atqamz/hand/commit/e5880d055e7529ca687c4688fb6f6dc35d2a05fb))
* **harness:** append the report channel to grok's and pi's brief file ([#453](https://github.com/atqamz/hand/issues/453)) ([d5968f1](https://github.com/atqamz/hand/commit/d5968f1e0a94cb5fdac35eb9aaef0a654a3959a8))
* **harness:** state ship/scout delivery authorization at launch ([#449](https://github.com/atqamz/hand/issues/449)) ([9f7f23b](https://github.com/atqamz/hand/commit/9f7f23b0c5b779d9aa65656bca2877fa8478dabf))
* **holds:** report a blocked hold once its blocker goes terminal ([#452](https://github.com/atqamz/hand/issues/452)) ([4d6f74c](https://github.com/atqamz/hand/commit/4d6f74c29db2c6c3465654ddfeab7cec3e19d036))
* **home:** refuse HAND_HOME and cwd naming different fleet homes ([#469](https://github.com/atqamz/hand/issues/469)) ([8e551e0](https://github.com/atqamz/hand/commit/8e551e09f2967eca5e4cb96fb22e63501f7b288c))
* **init:** adopt hand's own source tree, and refuse an untagged suite by name ([#438](https://github.com/atqamz/hand/issues/438)) ([fc9ee9e](https://github.com/atqamz/hand/commit/fc9ee9e3c4dbee6be83a8b0db23048129f2d2008))
* keep help independent of fleet registry ([#381](https://github.com/atqamz/hand/issues/381)) ([d837a88](https://github.com/atqamz/hand/commit/d837a889cd296b0516a6e89b8a87567a9b8ea550))
* **merge:** converge on a PR merged outside hand instead of refusing ([#461](https://github.com/atqamz/hand/issues/461)) ([d3c5958](https://github.com/atqamz/hand/commit/d3c59584db6e06578c34cad11b2e8ea333090325))
* name checkout in trust refusal ([#493](https://github.com/atqamz/hand/issues/493)) ([3fedb88](https://github.com/atqamz/hand/commit/3fedb88cd845c0a6a2e629461681e4b87531f51c))
* **orient:** corroborate and latch parked before treating it as actionable ([#494](https://github.com/atqamz/hand/issues/494)) ([5ecb1f2](https://github.com/atqamz/hand/commit/5ecb1f2b200d31ccb760db257c598b647b05634a))
* **paths:** render resolved paths pasteably instead of %q ([#475](https://github.com/atqamz/hand/issues/475)) ([28cd355](https://github.com/atqamz/hand/commit/28cd355d5b2eb1c819f31bc3632a09e989814b06))
* **project:** preserve unrelated completion records during rename rollback ([#396](https://github.com/atqamz/hand/issues/396)) ([d706b7c](https://github.com/atqamz/hand/commit/d706b7c8d4768997fe3bc8218b5a3da4f26c1f6f))
* **promote:** refuse to launch a ship attempt against an unrevised brief ([#456](https://github.com/atqamz/hand/issues/456)) ([0d039ab](https://github.com/atqamz/hand/commit/0d039ab1cb52fb131414848691c2b5445387ad0b))
* **reconcile:** guard every teardown release behind one releasing check ([#485](https://github.com/atqamz/hand/issues/485)) ([da24388](https://github.com/atqamz/hand/commit/da24388e20ed28dc45ee0818b68b92a8156dc7e1))
* refresh mechanical plan base before dispatch ([#429](https://github.com/atqamz/hand/issues/429)) ([53e59f5](https://github.com/atqamz/hand/commit/53e59f56f497deb1da354d93620cffadf44f669e))
* **registry:** make test isolation unforgettable, add prune, refuse managed-tree homes ([#454](https://github.com/atqamz/hand/issues/454)) ([111eef2](https://github.com/atqamz/hand/commit/111eef2c1ba5f11553cee0adc8eb1002a2b49c78))
* **registry:** reconcile stale same-home Fleet claims ([#385](https://github.com/atqamz/hand/issues/385)) ([d9a613d](https://github.com/atqamz/hand/commit/d9a613d36d92c034c23cebe57f89c774971b02b1))
* **registry:** scope ambiguity classification to affected Fleets ([#382](https://github.com/atqamz/hand/issues/382)) ([79a2cf4](https://github.com/atqamz/hand/commit/79a2cf49a3a91171308b25181f62c12ba2498db2))
* **runtime:** close the worktree release gate on evidence ([#457](https://github.com/atqamz/hand/issues/457)) ([37956ca](https://github.com/atqamz/hand/commit/37956ca1f81fee8f273504c801e7599d43da076f))
* **runtime:** diagnose a git bundle with no https helper ([#463](https://github.com/atqamz/hand/issues/463)) ([a0067a7](https://github.com/atqamz/hand/commit/a0067a780bd104c0c32710df39226c8cb474b13d))
* **runtime:** give the managed git a real, empty template directory ([#468](https://github.com/atqamz/hand/issues/468)) ([f1b460a](https://github.com/atqamz/hand/commit/f1b460a08b561513123b5ae595fc1ce9e2d1a3b8))
* **runtime:** use recorded PR after detached HEAD ([#390](https://github.com/atqamz/hand/issues/390)) ([44c7189](https://github.com/atqamz/hand/commit/44c7189157d6e6e13b51fa5aed240453a5294622))
* scope treehouse pools to fleet homes ([#404](https://github.com/atqamz/hand/issues/404)) ([175c162](https://github.com/atqamz/hand/commit/175c162af7f8e12fdc5328def6d119f367e26d25))
* **steering:** anchor Tab confirmation on the composer's last block ([#495](https://github.com/atqamz/hand/issues/495)) ([1686ef1](https://github.com/atqamz/hand/commit/1686ef1364a935f5febe78258842276d3d407e4c))
* **steering:** chunk PaneSendText writes below the measured stall boundary ([#480](https://github.com/atqamz/hand/issues/480)) ([c4da6cc](https://github.com/atqamz/hand/commit/c4da6cc6499ed6cd9e30b9f8d6d107f3351caa54))
* **steering:** confirm Enter submission by positive composer evidence, not a claim ([#476](https://github.com/atqamz/hand/issues/476)) ([fd7e32f](https://github.com/atqamz/hand/commit/fd7e32f0292e280e20e12435a8294a3e0abe0417))
* **steering:** send Tab, not Enter, to a busy codex pane ([#466](https://github.com/atqamz/hand/issues/466)) ([1dc67d7](https://github.com/atqamz/hand/commit/1dc67d77d8170725f47e0261400a01de5b87f102))
* **supervision:** fail closed on unavailable runtime identity ([#384](https://github.com/atqamz/hand/issues/384)) ([4da2106](https://github.com/atqamz/hand/commit/4da2106ee404a7ad070289eee39d2e75bf49590b))
* **test:** make test pass reliably on a clean checkout under load ([#370](https://github.com/atqamz/hand/issues/370)) ([b4304db](https://github.com/atqamz/hand/commit/b4304db579de9e557290da1cb5106ac82943323a))
* **watcher:** confirm parked workers with live pane activity ([#366](https://github.com/atqamz/hand/issues/366)) ([16e0faf](https://github.com/atqamz/hand/commit/16e0faf46c8a0ab60418dadba87e7ca40d6eb01a))
* **watcher:** name the recorded lock holder in a contended refusal ([#451](https://github.com/atqamz/hand/issues/451)) ([3334151](https://github.com/atqamz/hand/commit/33341519d6829fb7e5a82a0a8c8975ba657c3202))
* **watcher:** stop failing an arm when a task is torn down mid-probe ([#465](https://github.com/atqamz/hand/issues/465)) ([60b98d6](https://github.com/atqamz/hand/commit/60b98d6d6b23368147c28d555533060ce02ef522))
* **watcher:** stop re-firing attention for delivered tasks ([#367](https://github.com/atqamz/hand/issues/367)) ([28c51cb](https://github.com/atqamz/hand/commit/28c51cb6810d40a9a1d9732dd1396a5b4b380ddb))
* **worktree:** enforce pool slot integrity ([#496](https://github.com/atqamz/hand/issues/496)) ([9585b47](https://github.com/atqamz/hand/commit/9585b477f84b33823fb64045da2f12592ca1444d))
* **worktree:** put the pool outside every fleet home ([#444](https://github.com/atqamz/hand/issues/444)) ([8f2881b](https://github.com/atqamz/hand/commit/8f2881b9a5628fa583a6f311829490434ec2772b))

## [0.6.0](https://github.com/atqamz/hand/compare/v0.5.0...v0.6.0) (2026-08-20)


### Features

* add checksum-verifying Hand installers ([#280](https://github.com/atqamz/hand/issues/280)) ([2d1e876](https://github.com/atqamz/hand/commit/2d1e87608bed03ab71f4465451f24835617513f8))
* add cross-platform Secondhand bootstrap ([#297](https://github.com/atqamz/hand/issues/297)) ([a3b36dc](https://github.com/atqamz/hand/commit/a3b36dcf676a51250a9d01ac82a027fe1f449098))
* add Windows PowerShell bootstrap ([#298](https://github.com/atqamz/hand/issues/298)) ([530bb83](https://github.com/atqamz/hand/commit/530bb83a8dc4c3c394d328ae344b315d57ca8b0b))
* **cmd:** add doctor readiness contract ([#296](https://github.com/atqamz/hand/issues/296)) ([758a3af](https://github.com/atqamz/hand/commit/758a3afaab5514c3c276fb01b0d69f5e871a6e79))
* **cmd:** add explicit acknowledgement for task status ([#281](https://github.com/atqamz/hand/issues/281)) ([f9c743d](https://github.com/atqamz/hand/commit/f9c743d75e0a03f7fef019bb867e83f60033f5ed))
* **doctor:** expand hand doctor diagnostic coverage ([e038b71](https://github.com/atqamz/hand/commit/e038b717d5198653af5d34b8a69989272458a180))
* **init:** make hand init the canonical fleet-reconciler contract ([690edde](https://github.com/atqamz/hand/commit/690eddea6b6af935b2361a65c070d023f424455e))
* **packaging:** add Homebrew, npm, deb, rpm, WinGet, and AUR package surfaces ([#277](https://github.com/atqamz/hand/issues/277)) ([be08bd0](https://github.com/atqamz/hand/commit/be08bd0eca716705c395d5681ce13b8052446456))
* **skill:** bundle first-party secondhand Agent Skill ([e7bc210](https://github.com/atqamz/hand/commit/e7bc2107d9b1791e413112f0ba440354f7334d39))
* **update:** hand update hands off fleet reconciliation to the new binary ([8ab2bf5](https://github.com/atqamz/hand/commit/8ab2bf5ff3abce247e6b137344745c6b15f1eafe))


### Bug Fixes

* **cmd:** preserve PR observation provenance ([#278](https://github.com/atqamz/hand/issues/278)) ([72dd288](https://github.com/atqamz/hand/commit/72dd2888946f8ffdcfb6404f60a831c280517fc2))
* **cmd:** preserve unknown report observations ([#279](https://github.com/atqamz/hand/issues/279)) ([4cfd1f3](https://github.com/atqamz/hand/commit/4cfd1f3e93bc20898067c2d567b14f9124af0249))
* **runtime:** detect PRs by durable branch name across a detached HEAD ([#285](https://github.com/atqamz/hand/issues/285)) ([6015e10](https://github.com/atqamz/hand/commit/6015e10875edc97d414268244e27208241adb057))
* **runtime:** label pane-derived holds as inferred ([#282](https://github.com/atqamz/hand/issues/282)) ([cea038d](https://github.com/atqamz/hand/commit/cea038dd112996232e92c1e5da288499fc730a86))
* **runtime:** observe attempt liveness from Herdr agent_status in reconcile ([#275](https://github.com/atqamz/hand/issues/275)) ([110d6af](https://github.com/atqamz/hand/commit/110d6af3793dd3cc2ff5fa4e8b7d722866e21e51))

## [0.5.0](https://github.com/atqamz/hand/compare/v0.4.0...v0.5.0) (2026-08-20)


### Features

* **brief:** make worker briefs execution-ready ([#227](https://github.com/atqamz/hand/issues/227)) ([48fdfe1](https://github.com/atqamz/hand/commit/48fdfe17bc64cea31f0f48be78607d28ec36bfff))
* **cmd:** surface deterministic session next action ([#238](https://github.com/atqamz/hand/issues/238)) ([d4b8714](https://github.com/atqamz/hand/commit/d4b87149ffef298b02eea73c05cd62b8511faaf2))
* **doctor:** report project gate and routing health ([#249](https://github.com/atqamz/hand/issues/249)) ([a563b36](https://github.com/atqamz/hand/commit/a563b367350088c6ea7e98c2de736235f2d1a9bc))
* **runtime:** add deterministic reconciliation ([#229](https://github.com/atqamz/hand/issues/229)) ([bc5c219](https://github.com/atqamz/hand/commit/bc5c21956f592a41076a6c25fd7bdb7f5e83ccaf))
* **runtime:** give every reconcile repair diagnosis a reachable treatment ([#258](https://github.com/atqamz/hand/issues/258)) ([3029818](https://github.com/atqamz/hand/commit/30298186004b8073742daabc39c0cbc51a543024))
* **selfupdate:** embed distribution identity in build metadata ([#271](https://github.com/atqamz/hand/issues/271)) ([05a81cc](https://github.com/atqamz/hand/commit/05a81cc1a11b76162da4320c9494d196779c4ada))
* **store:** split durable tasks from execution attempts ([#219](https://github.com/atqamz/hand/issues/219)) ([c9772bd](https://github.com/atqamz/hand/commit/c9772bd65511a0c3a049b7dc5ff7ac42cb78e888))
* **update:** refuse to mutate a package-manager-owned build ([#273](https://github.com/atqamz/hand/issues/273)) ([ee3b184](https://github.com/atqamz/hand/commit/ee3b184b08e13f7f3f08aed71f36960dc6d5ad4e))


### Bug Fixes

* **gate:** distinguish an unobserved gate run from an absent one ([#264](https://github.com/atqamz/hand/issues/264)) ([5d63cfa](https://github.com/atqamz/hand/commit/5d63cfa573f6992c0a9abd3c2936406eb6545bd3))
* **ghutil:** observe pull requests as found, absent, or unknown ([#256](https://github.com/atqamz/hand/issues/256)) ([f054c41](https://github.com/atqamz/hand/commit/f054c4141b8e5dd041354bca6db194dcf0d4c289))
* **project:** preserve operator-owned pull request metadata ([#260](https://github.com/atqamz/hand/issues/260)) ([0eb6dc2](https://github.com/atqamz/hand/commit/0eb6dc25afb92011a516256397b47b68cbea46a6))
* **runtime:** clear a resource repair code an attestation already resolved ([#272](https://github.com/atqamz/hand/issues/272)) ([b7e4139](https://github.com/atqamz/hand/commit/b7e41390715872a4e1a618cfd1b0cb2b37587a88))
* **runtime:** converge terminal attempt lifecycle and gate worktree return on commit safety ([#251](https://github.com/atqamz/hand/issues/251)) ([0513224](https://github.com/atqamz/hand/commit/051322413880b2ac31b3b058bebe02510ca329f0))
* **runtime:** reach pull request head evidence when remote-tracking refs cannot prove durability ([#257](https://github.com/atqamz/hand/issues/257)) ([d36591c](https://github.com/atqamz/hand/commit/d36591c90fb922d1a7f9e1b11cce20ca83f7fde6))
* **runtime:** recover unobservable worktree ownership ([#248](https://github.com/atqamz/hand/issues/248)) ([cb93eb3](https://github.com/atqamz/hand/commit/cb93eb3e19a60a9e5d43dc80c4954aee8274d1f4))
* **selfupdate:** reconcile update notice cache ([#237](https://github.com/atqamz/hand/issues/237)) ([2e8d493](https://github.com/atqamz/hand/commit/2e8d493d9803f6dc4939516a94a5eb0af8bf2bf3))
* **steering:** make terminal sends crash-safe ([#230](https://github.com/atqamz/hand/issues/230)) ([af7e118](https://github.com/atqamz/hand/commit/af7e118d6a236f5d55b727c2644e876fdde9b46e))
* **watcher:** harden takeover and cancellation lifecycle ([#231](https://github.com/atqamz/hand/issues/231)) ([be189b6](https://github.com/atqamz/hand/commit/be189b62f9008f4fec9f39399744c99a97dd2923))
* **watcher:** stop teardown from reporting a torn-down worker's success as a failure ([#261](https://github.com/atqamz/hand/issues/261)) ([2486b20](https://github.com/atqamz/hand/commit/2486b20d2ccb9ec67cec1dedddbdba1d3f7fb1fc))
* **watcher:** suppress false stale events for terminal or delivered tasks ([#236](https://github.com/atqamz/hand/issues/236)) ([152ab71](https://github.com/atqamz/hand/commit/152ab71c7167463605239ddfe89438f281b76cd6))
* **watcher:** wake on a fleet condition that was already actionable when the watch armed ([#262](https://github.com/atqamz/hand/issues/262)) ([8adb5f1](https://github.com/atqamz/hand/commit/8adb5f1352c583e5e1e25f37a83247694da292a8))

## [0.4.0](https://github.com/atqamz/hand/compare/v0.3.0...v0.4.0) (2026-08-13)


### Features

* add Windows portability ([#203](https://github.com/atqamz/hand/issues/203)) ([998b2c3](https://github.com/atqamz/hand/commit/998b2c30fc14a07c3262307ba2a9cb692796fceb))
* establish hand project identity ([#179](https://github.com/atqamz/hand/issues/179)) ([d689778](https://github.com/atqamz/hand/commit/d689778475cb643a7329b13a5d941712c17b3cb0))
* **project:** repoint a registered project at a renamed repo ([#209](https://github.com/atqamz/hand/issues/209)) ([b843340](https://github.com/atqamz/hand/commit/b8433409a69d11abb22d98b3e4c361fff7050264))
* **selfupdate:** add rolling edge release channel ([#212](https://github.com/atqamz/hand/issues/212)) ([911135e](https://github.com/atqamz/hand/commit/911135e12ab99572cbab055a2c22916c717ffbdb))


### Bug Fixes

* **notify:** resolve POSIX sh on PATH with an actionable missing-sh error ([#211](https://github.com/atqamz/hand/issues/211)) ([95c48a5](https://github.com/atqamz/hand/commit/95c48a51f9991f695d38a1d9d1c1c445ea2448cb))
* restore README onboarding guidance ([#185](https://github.com/atqamz/hand/issues/185)) ([fe0b70f](https://github.com/atqamz/hand/commit/fe0b70f9719ea8ad081b586e12f237bba23c47ac))

## [0.3.0](https://github.com/atqamz/secondhand/compare/v0.2.0...v0.3.0) (2026-08-09)


### Features

* **session:** add supervisor session bootstrap ([#173](https://github.com/atqamz/secondhand/issues/173)) ([6b51061](https://github.com/atqamz/secondhand/commit/6b5106104faeb0d967d9911b965e94ab9a4901eb))

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
