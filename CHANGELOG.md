# Changelog

All notable changes to longue-vue are recorded here. Format loosely follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
— the REST and database contracts may still change incompatibly before
`v1.0.0`.

## [0.25.0](https://github.com/sthalbert/Longue-Vue/compare/v0.24.1...v0.25.0) (2026-05-22)


### Features

* **api,store:** denormalize parent names on entity list/get responses ([7e071b6](https://github.com/sthalbert/Longue-Vue/commit/7e071b656ffa6e4164e3e3620ee2599bd07a644d))
* denormalize parent names on list responses for scale (ADR-0027) ([4ced900](https://github.com/sthalbert/Longue-Vue/commit/4ced90039f509697db02bd3c6a9f9ddb20fd1028))
* **ui:** drop client-side parent indexes; render denormalized names from API ([6c8b6c4](https://github.com/sthalbert/Longue-Vue/commit/6c8b6c4ae846654f9ea89580e98e4b1149e7407c))


### Bug Fixes

* **impact,store:** satisfy gocritic/revive after parent-name denormalization ([3cd02a7](https://github.com/sthalbert/Longue-Vue/commit/3cd02a7e54b6aa20328e557628eab49e7c197ed6))

## [0.24.1](https://github.com/sthalbert/Longue-Vue/compare/v0.24.0...v0.24.1) (2026-05-22)


### Bug Fixes

* **mirrorresolve:** passthrough unparseable refs instead of erroring ([b0d815e](https://github.com/sthalbert/Longue-Vue/commit/b0d815e1084257756c27010bd3a6a210bba70498))
* **mirrorresolve:** passthrough unparseable refs instead of erroring ([049d3b1](https://github.com/sthalbert/Longue-Vue/commit/049d3b1a330346d53d9743a5f2ec53707a232eb5))

## [0.24.0](https://github.com/sthalbert/Longue-Vue/compare/v0.23.0...v0.24.0) (2026-05-21)


### Features

* **api:** composite key + credentials endpoint for image registries ([6054224](https://github.com/sthalbert/Longue-Vue/commit/6054224bbcb6543c6e92884d434afc80f2fdf399))
* **api:** mirror fields on image registry types ([e0c1469](https://github.com/sthalbert/Longue-Vue/commit/e0c14696403274d63b6b1eb9419ced76c32c5d80))
* **imageversions:** integrate mirror resolver into enricher ([ce9cbf4](https://github.com/sthalbert/Longue-Vue/commit/ce9cbf4b27c2f21f2bfd0d7974a3fb98cbeb4365))
* **imageversions:** mirrorresolve package skeleton ([8dae4c3](https://github.com/sthalbert/Longue-Vue/commit/8dae4c3526b42757c69d0f4fb24826725ae8d003))
* **imageversions:** wire mirror resolver in main ([1d7fbb2](https://github.com/sthalbert/Longue-Vue/commit/1d7fbb2c89de9b389011def423012892bde71fe0))
* mirror image source resolution ([2a31511](https://github.com/sthalbert/Longue-Vue/commit/2a315115eb43dbb221e394bc2eb51837fae8b75b))
* **mirrorresolve:** oci manifest-based origin resolver ([6ef51f7](https://github.com/sthalbert/Longue-Vue/commit/6ef51f73d0b27e7c526f9e356499b7af6d2c3923))
* **store:** composite key + mirror lookup for image registries ([97498ba](https://github.com/sthalbert/Longue-Vue/commit/97498bafd6efc7fe697d55bae34a7237f785e679))
* **store:** migration 00043 — mirror image registries ([cb89c7b](https://github.com/sthalbert/Longue-Vue/commit/cb89c7bb0ac27b3093573ac5609296a975deb7fa))
* **ui:** mirror registry support on admin page ([d916f16](https://github.com/sthalbert/Longue-Vue/commit/d916f16bc6fadf79558f65b4534ced67be79fa8a))


### Bug Fixes

* **api,imageversions:** close review gaps on mirror resolver ([f97cc96](https://github.com/sthalbert/Longue-Vue/commit/f97cc965976a888f2b9fde3be29e7f0971a08bb3))
* **api:** wire mirror credentials route, tighten upsert spec, regen ([2705c86](https://github.com/sthalbert/Longue-Vue/commit/2705c86138f569bac8737d3c6a18f592f0dab46a))
* **main:** remove duplicate mirror credentials route mount ([35be135](https://github.com/sthalbert/Longue-Vue/commit/35be135d078f33d2846d2a816dc4966fea066643))

## [0.23.0](https://github.com/sthalbert/Longue-Vue/compare/v0.22.0...v0.23.0) (2026-05-19)


### Features

* **store:** backfill cluster/namespace soft-delete orphans (00042) ([5b4dfa5](https://github.com/sthalbert/Longue-Vue/commit/5b4dfa57eeff9446ece86758272209992217823b))


### Bug Fixes

* **store:** auto-restore soft-deleted clusters on ensure ([2fc86f3](https://github.com/sthalbert/Longue-Vue/commit/2fc86f33229ac13443ef61f6be90e22421686975))
* **store:** cascade-delete cluster children + auto-restore in EnsureCluster ([b87dad9](https://github.com/sthalbert/Longue-Vue/commit/b87dad90ba7d7e21a5e489e50f2cedaf74c53351))
* **store:** hard-delete pod/svc/ing/pv/pvc orphans on cluster soft-delete ([4746584](https://github.com/sthalbert/Longue-Vue/commit/47465845b29f91f6c01b28796160899dfe8c0f4c))
* **store:** hard-delete pod/svc/ing/pvc orphans on namespace soft-delete ([baab4fd](https://github.com/sthalbert/Longue-Vue/commit/baab4fdbdca8b92e5ba3cede6dc00b7b9a457207))

## [0.22.0](https://github.com/sthalbert/Longue-Vue/compare/v0.21.0...v0.22.0) (2026-05-16)


### Features

* **api:** add HandleEolExtract for /v1/eol/extract ([602f34a](https://github.com/sthalbert/Longue-Vue/commit/602f34a8e9c4f70fb4bc17cb3c33930acfb2bc1c))
* **api:** add HandleSearchExtract for workloads/pods/virtual_machines ([c336e97](https://github.com/sthalbert/Longue-Vue/commit/c336e971373cc93b36f997799dbd93dfc8f73c81))
* **api:** add HandleSearchExtractZip for combined ZIP bundle ([eadd47f](https://github.com/sthalbert/Longue-Vue/commit/eadd47f702372e1f8888e040ed06f1d581dadd57))
* **api:** add slug + timestamp helpers for extract filenames ([aed61fb](https://github.com/sthalbert/Longue-Vue/commit/aed61fb9340111dfb42c112927bcb362b9a9f3d3))
* **api:** add streaming JSON-array writer for extracts ([0acf11a](https://github.com/sthalbert/Longue-Vue/commit/0acf11aa442702d6c3f3818176789fe83c2b86f1))
* **api:** add UTF-8-BOM CSV writer for extracts ([9f20bd7](https://github.com/sthalbert/Longue-Vue/commit/9f20bd77c824d442e96c977c34a9e7a57e5e87f8))
* **api:** add ZIP wrapper for Search 'extract all' bundle ([2eb8196](https://github.com/sthalbert/Longue-Vue/commit/2eb8196f0794345aea4dea07e0dd619af24bbbb3))
* **api:** allowlist /v1/search/extract* and /v1/eol/extract in audit middleware ([2446880](https://github.com/sthalbert/Longue-Vue/commit/2446880c719f55ad68b7f4ae04ef9c103ba14a10))
* bulk extract for Search and EOL Dashboard ([298ae6f](https://github.com/sthalbert/Longue-Vue/commit/298ae6f8ecea5e5e1b907dc2074e6bb084c6476f))
* **eolagg:** flatten EOL annotations across clusters, nodes, VMs ([7f0aa9b](https://github.com/sthalbert/Longue-Vue/commit/7f0aa9bd0350a4517cfd30c7ec5ba8644eb9db81))
* **eolagg:** scaffold Row struct and Flatten stub ([9bc902f](https://github.com/sthalbert/Longue-Vue/commit/9bc902f073638e3f3db848bc2949fac8925f0082))
* **metrics:** add longue_vue_extracts_total and extract_rows_total counters ([b0f0237](https://github.com/sthalbert/Longue-Vue/commit/b0f02376d15e77c2499e3f61e95608ed225ca333))
* **openapi:** document extract endpoints ([35b069f](https://github.com/sthalbert/Longue-Vue/commit/35b069f3dc718ae1a578606552e03782a3441d92))
* **server:** mount /v1/search/extract* and /v1/eol/extract routes ([28282ad](https://github.com/sthalbert/Longue-Vue/commit/28282ad3dd768f354ddef290e3c51508e4671f18))
* **ui:** add dismissible TruncationBanner component ([e5ae4df](https://github.com/sthalbert/Longue-Vue/commit/e5ae4dfe3e63d87a7e69231384f56b05868ee2fa))
* **ui:** add downloadExtract helper with truncation-header parsing ([de282ff](https://github.com/sthalbert/Longue-Vue/commit/de282ff4a6b6ace98e0e970c09a6e9cd0372286a))
* **ui:** add extract client functions in api.ts ([cff3176](https://github.com/sthalbert/Longue-Vue/commit/cff31763318ef4e5bc5e6d3a0a97ef55133f26bc))
* **ui:** add ExtractButton dropdown component ([377d2f5](https://github.com/sthalbert/Longue-Vue/commit/377d2f53ec243f7192ca3c23a25fd82557b116d8))
* **ui:** wire ExtractButton + zip extract into Search page ([d8df6ce](https://github.com/sthalbert/Longue-Vue/commit/d8df6ce0997e6718e4b64b733352d9789a114fcd))
* **ui:** wire ExtractButton into EOL Dashboard ([30673f7](https://github.com/sthalbert/Longue-Vue/commit/30673f74626750ad189fe6fb0f3f5186211dd907))


### Bug Fixes

* **ingestgw:** bump TestCertReloader_HotReload timeout and re-fire to reduce inotify flake ([7684b2f](https://github.com/sthalbert/Longue-Vue/commit/7684b2f682cf48b8eb1bcdd5a4d7fbcfbca09e9f))
* **lint:** apply gofumpt to audit.go, auth_handlers.go, server.go ([e30c3f5](https://github.com/sthalbert/Longue-Vue/commit/e30c3f5329e792fec0be944fde02f97a2a28da5d))
* **lint:** satisfy all golangci-lint checks on extract + eolagg code ([58fae2d](https://github.com/sthalbert/Longue-Vue/commit/58fae2d141c5bde4ac641cb5eabbbdccdbee6159))
* **openapi:** use anyOf for searchExtract body to avoid oneOf ambiguity ([a78cdbc](https://github.com/sthalbert/Longue-Vue/commit/a78cdbcb7104b46b306c6caeff4c49297146de9a))
* **ui:** copy EOL fixtures into ui/ tree so Dockerfile ui-build can resolve them ([1febed9](https://github.com/sthalbert/Longue-Vue/commit/1febed91a7f37025eaab8815533d93f045208991))

## [Unreleased]

### Added

- Bulk extract endpoints `/v1/search/extract`, `/v1/search/extract.zip`, `/v1/eol/extract` with CSV / JSON output and `LONGUE_VUE_EXTRACT_MAX_ROWS` (default 50 000) cap; every extract recorded in `audit_events`.
- "Extract" buttons on the Search page (per table + ZIP bundle) and EOL Dashboard.

## [0.21.0](https://github.com/sthalbert/Longue-Vue/compare/v0.20.0...v0.21.0) (2026-05-15)


### Features

* **api:** add relative server URL and /docs pointer to OpenAPI spec ([5509a47](https://github.com/sthalbert/Longue-Vue/commit/5509a4740a4d3dd554d927c3ac4a3010052b6791))
* **api:** mount /docs and /openapi.yaml on the public listener ([7b912ba](https://github.com/sthalbert/Longue-Vue/commit/7b912ba0a9741e6973457dbc3deeaa69b35c5f98))
* interactive API docs via embedded Swagger UI (ADR-0025) ([e223ea6](https://github.com/sthalbert/Longue-Vue/commit/e223ea6d455aed3e6a252303b8e060141dccdea8))
* **swagger:** add embed.go with index/dist/spec //go:embed directives ([601a53b](https://github.com/sthalbert/Longue-Vue/commit/601a53bd681ca4c1329002a29fa427f119bb8c5d))
* **swagger:** add OpenAPISpecHandler serving embedded /openapi.yaml ([a1c12bf](https://github.com/sthalbert/Longue-Vue/commit/a1c12bfb81f8f8e3674adcf0a5f5e4c7d6216cdd))
* **swagger:** add SwaggerUIHandler and longue-vue index.html bootstrap ([82ae177](https://github.com/sthalbert/Longue-Vue/commit/82ae1779cf5df075ef59d9cf92620d97ec4f4afd))
* **ui:** add 'API Docs' link to the top-nav ([260bb47](https://github.com/sthalbert/Longue-Vue/commit/260bb47532101ee3ed4ee38b72bf9a5bde5bc04a))


### Bug Fixes

* **auth:** whitelist /openapi.yaml under must_change_password guard ([db58a3d](https://github.com/sthalbert/Longue-Vue/commit/db58a3db8f0ce526027d3e94603c44d7f2d58583))
* **swagger:** drop broken ?return= link, tighten index tests, cache-control on shell ([64ca2ca](https://github.com/sthalbert/Longue-Vue/commit/64ca2ca968e445dfdbdaea017feace7c80ffe3e6))
* **swagger:** extract bootstrap to init.js for CSP `script-src 'self'` ([5244ebf](https://github.com/sthalbert/Longue-Vue/commit/5244ebf7166669d0f3c7b91ba97f30c78029fc6c))
* **swagger:** handle If-None-Match: * per RFC 7232 §3.2 ([fcc7fcc](https://github.com/sthalbert/Longue-Vue/commit/fcc7fccc13867539ce1e23dd332bc5ae4c0b293c))
* **test:** gofmt swagger_docs_test, reuse noRedirect client, clarify comment ([71f2a67](https://github.com/sthalbert/Longue-Vue/commit/71f2a67a4dab894ff46b9057c50c1ac61cf33312))

## [0.20.0](https://github.com/sthalbert/Longue-Vue/compare/v0.19.2...v0.20.0) (2026-05-14)


### Features

* **adr:** history api ([611826f](https://github.com/sthalbert/Longue-Vue/commit/611826f3b05d03cb56b0060d4fa9b721b0ee323f))
* **api,ui:** admin-only cluster deletion with enriched audit trail (… ([b892409](https://github.com/sthalbert/Longue-Vue/commit/b892409603fe43d72578dcf2d8b382d414874959))
* **api,ui:** admin-only cluster deletion with enriched audit trail (ADR-0010) ([4557bcb](https://github.com/sthalbert/Longue-Vue/commit/4557bcb4e5fe340d93de55314467947031c9c2a2))
* **api:** add include_terminated query param to list endpoints (ADR-0021 phase 1) ([753498a](https://github.com/sthalbert/Longue-Vue/commit/753498ad5c1fda6539a200df9aaec24c0b973c37))
* **api:** add list/detail/refresh handlers for image versions ([cb1fbcf](https://github.com/sthalbert/Longue-Vue/commit/cb1fbcfa79ce85d1146159072f6d86919fd9f494))
* **api:** add UpsertOutcome enum for audit no-op detection ([e752e98](https://github.com/sthalbert/Longue-Vue/commit/e752e984549e4acec622f81825bf92efaf76822e))
* **api:** admin CRUD on image_versions_registries ([4628cd7](https://github.com/sthalbert/Longue-Vue/commit/4628cd77e22966e6234bb2dd3a657ded5bf9cd24))
* **api:** enrich workload/pod GETs with containers_versions ([2883b92](https://github.com/sthalbert/Longue-Vue/commit/2883b92447a2634c3f4bb2cda4dbeb9da11585f3))
* **api:** list handlers honor include_terminated query param (ADR-0021 phase 1) ([c82344d](https://github.com/sthalbert/Longue-Vue/commit/c82344d06ab4eb82adbb2b90b9a5a276b94e6abb))
* **api:** mount image-versions and registries routes ([e8a1af4](https://github.com/sthalbert/Longue-Vue/commit/e8a1af483159a8a916a435c761530f5b65ecb6d1))
* **api:** soft-delete cluster/namespace cascade to children (ADR-0021 phase 1) ([61224b5](https://github.com/sthalbert/Longue-Vue/commit/61224b58026182a4b9a038f06dbba400d08b9d49))
* **audit:** add SetAuditSkip + middleware skip bypass (no callers yet) ([436de3b](https://github.com/sthalbert/Longue-Vue/commit/436de3b33160575eb411714754fdd1df2e678de3))
* **audit:** drop empty reconcile audits via SetAuditSkip ([2c420e4](https://github.com/sthalbert/Longue-Vue/commit/2c420e43601e6eb92e9379d45eb04ae53a24c0a4))
* **audit:** drop empty VM reconcile audits via SetAuditSkip ([d79dbd4](https://github.com/sthalbert/Longue-Vue/commit/d79dbd4017c7be802c1fd2e751abb15564c5f7b0))
* **audit:** drop no-op CMDB writes from audit log (ADR-0024) ([4f08dfc](https://github.com/sthalbert/Longue-Vue/commit/4f08dfc563efe1a080984f4fe035c6b8108637a4))
* **audit:** drop no-op CreatePod audits via SetAuditSkip ([c2455f4](https://github.com/sthalbert/Longue-Vue/commit/c2455f4b7f56b04c5d0f9aa2f1660d720acc6c76))
* **audit:** drop no-op HandleUpsertVirtualMachine audits via SetAuditSkip ([9ce696a](https://github.com/sthalbert/Longue-Vue/commit/9ce696a0c12963693827635430820a6ab76575cf))
* **audit:** drop no-op upsert audits via SetAuditSkip for namespaced resources ([24f93bb](https://github.com/sthalbert/Longue-Vue/commit/24f93bb9eb22a3b669ee3bcec5ba3aac8e0a6cea))
* **auth:** accept legacy argos_pat_ tokens, emit longue_vue_pat_ ([dccbeeb](https://github.com/sthalbert/Longue-Vue/commit/dccbeeb0be0f184e7db071a4d2716e43a1f2ed6e))
* **auth:** add failed_login_count + locked_at columns to users ([7b0c14b](https://github.com/sthalbert/Longue-Vue/commit/7b0c14b9b32521a18004803e9aca575d3a0071ed))
* **auth:** add per-IP login rate limiting (5 req/min, ADR-0007 IMP-009) ([db78bca](https://github.com/sthalbert/Longue-Vue/commit/db78bca25f0150ce4ae06df862a515363cf74387))
* **auth:** auto-lock login after 6 consecutive failures ([039a085](https://github.com/sthalbert/Longue-Vue/commit/039a08593f00805dd26b6761ee64e80328b0810f))
* **auth:** boot-time admin rescue via LONGUE_VUE_ADMIN_RESCUE_PASSWORD ([917e36b](https://github.com/sthalbert/Longue-Vue/commit/917e36bec7c53c60fe0658123d0a64ba1c9e4d07))
* **auth:** expose failed_login_count, locked_at, unlock in OpenAPI ([eb2ba97](https://github.com/sthalbert/Longue-Vue/commit/eb2ba97aea34fb039b0eda83c31596d2d1db2fd8))
* **auth:** extend Store with IncrementFailedLogin/ResetFailedLogin ([685b07b](https://github.com/sthalbert/Longue-Vue/commit/685b07b62433f36985466ad0f9305d942a2d887d))
* **auth:** handle unlock field in user-update patch ([4c713a5](https://github.com/sthalbert/Longue-Vue/commit/4c713a5b7f4b37848e558933f86748895f4d8c53))
* **auth:** increment failed-login counter with FOR UPDATE serialization ([f12be5f](https://github.com/sthalbert/Longue-Vue/commit/f12be5f69b70c39856fb33a8b2c852d5e6f4260f))
* **auth:** per-account login lockout + admin rescue ([0c2cdb3](https://github.com/sthalbert/Longue-Vue/commit/0c2cdb364f95883f44831dfa536fd00b1d610c4a))
* **auth:** public-listener TLS posture, proxy trust, and last-admin … ([80711e6](https://github.com/sthalbert/Longue-Vue/commit/80711e63d0632aabc9d670f0549e1c42929007c1))
* **auth:** public-listener TLS posture, proxy trust, and last-admin guard ([9eabada](https://github.com/sthalbert/Longue-Vue/commit/9eabada80ac52c40fdb4e739fd0d431851a17236))
* **auth:** reset failed-login counter on successful login ([c729465](https://github.com/sthalbert/Longue-Vue/commit/c729465959897fdeba7633caf699984931712e40))
* **chart:** expose adminRescuePassword on longue-vue chart ([b8d91d8](https://github.com/sthalbert/Longue-Vue/commit/b8d91d80d7516e194f2077ee37b60ee0d91826af))
* **chart:** expose mcp.allowPlaintext for local/dev clusters without TLS ([313f7ef](https://github.com/sthalbert/Longue-Vue/commit/313f7efe6fc6d20d3a328ba94b78f4efae57e80a))
* **charts:** helm chart per deployable binary (ADR-0018) ([56a4efd](https://github.com/sthalbert/Longue-Vue/commit/56a4efd49ed8910879c7c77b23aa4a3af2d555e2))
* **charts:** helm chart per deployable binary (ADR-0018) ([d1ef329](https://github.com/sthalbert/Longue-Vue/commit/d1ef329585ff492e939889113cfe3e9165d61df4))
* container image versions enrichment (ADR-0022) ([72b799b](https://github.com/sthalbert/Longue-Vue/commit/72b799b76c0de46815551f1920d5b09164c33caf))
* **db:** migrations for image_versions tables and settings flag ([11f7cc8](https://github.com/sthalbert/Longue-Vue/commit/11f7cc857956ed8460ade45529421571159ed1e5))
* **dmz-gateway:** add DMZ ingest gateway for collector push traffic ([c511594](https://github.com/sthalbert/Longue-Vue/commit/c511594db6ac9852a865d3d8ced1011560aa2427))
* **dmz-gateway:** add DMZ ingest gateway for collector push traffic ([95bd4fb](https://github.com/sthalbert/Longue-Vue/commit/95bd4fbd110c8fdb35ad3e969bdddbe248012310))
* **eol:** add end-of-life enrichment via endoflife.date ([568b5b5](https://github.com/sthalbert/Longue-Vue/commit/568b5b5ece1502496e80a778c9fc20f09cd91c25))
* **eol:** add end-of-life enrichment via endoflife.date (ADR-0012) ([698c1b1](https://github.com/sthalbert/Longue-Vue/commit/698c1b103a150f4a471da32ee83478669c69156d))
* **eol:** add latest available version and redesign EOL inventory UI ([8a7ff3f](https://github.com/sthalbert/Longue-Vue/commit/8a7ff3f7891ae8e9071cba3c6e7343ad07774b83))
* **eol:** admin-toggleable EOL enrichment via UI settings ([b000976](https://github.com/sthalbert/Longue-Vue/commit/b0009760b34e2fedb150d17452064c374bc437af))
* **helm:** expose MCP SSE port in Service and Deployment ([7f05a11](https://github.com/sthalbert/Longue-Vue/commit/7f05a11c293b6efb456b8ba889d8eb8998e73132))
* **helm:** expose MCP SSE port in Service and Deployment ([a5eaaf2](https://github.com/sthalbert/Longue-Vue/commit/a5eaaf296d1fb82b56638c12371829743207233a))
* **history-api:** add integration tests for ListEntityHistory and GetEntityAsOf ([cc2106c](https://github.com/sthalbert/Longue-Vue/commit/cc2106c2849f4209f44a989f51b090abcd026de0))
* **history-api:** add phase 3 backend — history list + as_of endpoints (ADR-0021) ([e956b86](https://github.com/sthalbert/Longue-Vue/commit/e956b86c94091fd6eb219e14868dcca320a52064))
* **imageversions/registry:** add OCI client with bearer auth and pagination ([c1fd19b](https://github.com/sthalbert/Longue-Vue/commit/c1fd19b5d348d58139fe4fede86963853fb07cbb))
* **imageversions/registry:** hostname → effective HTTPS host ([cfbb155](https://github.com/sthalbert/Longue-Vue/commit/cfbb15515bd7fa9bd9385b20b0042d40cbd6c92e))
* **imageversions/registry:** hostname pattern matcher ([72b4c33](https://github.com/sthalbert/Longue-Vue/commit/72b4c3398be11a01dd7394d64e08305e2ebd8afb))
* **imageversions:** add Prometheus metrics for ticks and registry queries ([dd1a16b](https://github.com/sthalbert/Longue-Vue/commit/dd1a16b1b9607968c9f76b746c78f63003582dad))
* **imageversions:** image ref and tag parsing ([11ca493](https://github.com/sthalbert/Longue-Vue/commit/11ca4939c2c7faaaeeaa56a1687887ac9be4d04f))
* **imageversions:** pattern-aware latest tag computation ([a4cae00](https://github.com/sthalbert/Longue-Vue/commit/a4cae0049a8d41bdccdaff98fb1e4c9248cba824))
* **imageversions:** periodic enricher with trigger and reaping ([c39545a](https://github.com/sthalbert/Longue-Vue/commit/c39545a4936c074716993fab55752a584d3b8aaa))
* **impact:** add impact analysis graph with interactive UI (ADR-0013) ([27dfb3b](https://github.com/sthalbert/Longue-Vue/commit/27dfb3beacbf7a446749707a83a5595518f7055a))
* **impact:** add impact analysis graph with interactive UI (ADR-0013) ([7621980](https://github.com/sthalbert/Longue-Vue/commit/762198047bca47a319e18142a1b38c4559c5c8a8))
* **main:** start image versions enricher goroutine at boot ([1380cef](https://github.com/sthalbert/Longue-Vue/commit/1380cef7bf12136759c79bcd35f3709e83fe27f1))
* **mcp:** add get_cloud_account tool with credential redaction ([b06f229](https://github.com/sthalbert/Longue-Vue/commit/b06f2292eabedca19fc38fba2419396f786d99bc))
* **mcp:** add get_virtual_machine and list_vm_applications_distinct tools ([3de8469](https://github.com/sthalbert/Longue-Vue/commit/3de846912a928eac3edaf8a775158b1b22e3f6c9))
* **mcp:** add list_cloud_accounts tool with credential redaction ([24888be](https://github.com/sthalbert/Longue-Vue/commit/24888becb4271a200a0c9084c67a510c3dd31eb5))
* **mcp:** add list_image_versions, get_image_version, get_image_versions_summary tools ([359b6c3](https://github.com/sthalbert/Longue-Vue/commit/359b6c339db8b750f933e0dd2978b9c90be6e3c3))
* **mcp:** add list_virtual_machines tool with full filter set ([b89dbab](https://github.com/sthalbert/Longue-Vue/commit/b89dbabf496dba65f93b3524430a2a314bd026f4))
* **mcp:** add MCP server for AI-driven CMDB queries (ADR-0014) ([ad59d33](https://github.com/sthalbert/Longue-Vue/commit/ad59d3345e8bbbdd1d436c135646c51625f616ef))
* **mcp:** add MCP server for AI-driven CMDB queries (ADR-0014) ([2fdf934](https://github.com/sthalbert/Longue-Vue/commit/2fdf9346f4baeb50234ada1c2ae07e443d43abf3))
* **mcp:** add redactcloudaccount helper to strip access_key ([b0d619f](https://github.com/sthalbert/Longue-Vue/commit/b0d619f5a54efed1f250b7e1e8454536ea82fdda))
* **mcp:** bounded lru auth cache with revocation invalidation (high-01/03, med-03) ([d9c090e](https://github.com/sthalbert/Longue-Vue/commit/d9c090e6c8ec8163be41dc92a9d9c3f54619e187))
* **mcp:** bounded LRU auth cache with revocation invalidation (HIGH-01/03, MED-03) ([fbf817a](https://github.com/sthalbert/Longue-Vue/commit/fbf817a0b0f9771edbdc5d5e665ea8a3f7d4359c))
* **mcp:** emit audit_events rows for every tool call (crit-02) ([0405559](https://github.com/sthalbert/Longue-Vue/commit/040555919ba47d8a2fad9104b042c8e78282d7f8))
* **mcp:** emit audit_events rows for every tool call (CRIT-02) ([0a7fd9e](https://github.com/sthalbert/Longue-Vue/commit/0a7fd9e87d091c11c1e705a457e670ea54c325a3))
* **mcp:** extend search_images with virtual_machines section ([cd6ce42](https://github.com/sthalbert/Longue-Vue/commit/cd6ce423d0d547210d28d1dca4e56525a3ddece6))
* **mcp:** include virtual machines ([87c9b60](https://github.com/sthalbert/Longue-Vue/commit/87c9b60bea709f4fb5926ab08980d1cac42dc4b4))
* **mcp:** include virtual machines in get_eol_summary tool ([400e9e1](https://github.com/sthalbert/Longue-Vue/commit/400e9e1e716b323fcf05e27809e44a2924c73bbb))
* **mcp:** native tls 1.3 + loopback default for sse listener (crit-01, med-02) ([5e19f3d](https://github.com/sthalbert/Longue-Vue/commit/5e19f3d72ba3509d7e67ee975f94456d92f7c7ab))
* **mcp:** native TLS 1.3 + loopback default for SSE listener (CRIT-01, MED-02) ([a09e67e](https://github.com/sthalbert/Longue-Vue/commit/a09e67e15e9b4a3e7577b4935ed106f5b3d15634))
* **mcp:** per-token rate limiting on tool calls (high-02) ([17c39aa](https://github.com/sthalbert/Longue-Vue/commit/17c39aaffe9413edfe71f91641556ca1e99698f6))
* **mcp:** per-token rate limiting on tool calls (HIGH-02) ([016cf34](https://github.com/sthalbert/Longue-Vue/commit/016cf34a962c295d588c0af5496ef1a57ad569f0))
* **mcp:** security hardening ([9cb66db](https://github.com/sthalbert/Longue-Vue/commit/9cb66dbf908fed3a7115262d9b07b72a88d5a916))
* **metrics:** add audit_events_skipped_total counter ([7da5923](https://github.com/sthalbert/Longue-Vue/commit/7da592366cecff3ecbfdaf358a3bdcdaae4864e6))
* **migrations:** add terminated_at to clusters/namespaces/nodes/workloads (ADR-0021 phase 1) ([09768fc](https://github.com/sthalbert/Longue-Vue/commit/09768fcb97ecfc09b340cbeea2d407af859f175c))
* **security:** add HTTP security headers middleware (CSP, XCTO, XFO, HSTS) ([2a2cfd6](https://github.com/sthalbert/Longue-Vue/commit/2a2cfd693e8af6115efa11688edd51cb547a6dbe))
* **settings:** add image_versions_enabled toggle ([f35a83b](https://github.com/sthalbert/Longue-Vue/commit/f35a83bef21303b130e4ac87a74ac3cbc11d31fc))
* **store:** add CRUD for image_versions_registries ([3ece105](https://github.com/sthalbert/Longue-Vue/commit/3ece105d90df3b23b03d69d0264e444b909ca943))
* **store:** classify UpsertIngress outcome via CTE ([071add4](https://github.com/sthalbert/Longue-Vue/commit/071add4d57ca3e16925dc5848b70990b4499493e))
* **store:** classify UpsertNamespace outcome via CTE ([dde347a](https://github.com/sthalbert/Longue-Vue/commit/dde347a50dc06b2167514a060d36c17d84459762))
* **store:** classify UpsertNode outcome via CTE (preserves time-travel) ([fb09f9b](https://github.com/sthalbert/Longue-Vue/commit/fb09f9b2276bc58b16e02d3140d4636031cef04b))
* **store:** classify UpsertPersistentVolume outcome via CTE ([336c1e1](https://github.com/sthalbert/Longue-Vue/commit/336c1e1e6ab7720a022bbd029d9ce4c18d99e7e9))
* **store:** classify UpsertPersistentVolumeClaim outcome via CTE ([f292beb](https://github.com/sthalbert/Longue-Vue/commit/f292beb7b7935da48925bd1b275c13a9bdb9757e))
* **store:** classify UpsertPod outcome via CTE ([0650f05](https://github.com/sthalbert/Longue-Vue/commit/0650f0505fbd88c3cdfdf0664893261c93fe84c3))
* **store:** classify UpsertService outcome via CTE ([49cdaca](https://github.com/sthalbert/Longue-Vue/commit/49cdacad1d89023ccaf58cbf9ab8b4078d21c87c))
* **store:** classify UpsertVirtualMachine outcome via CTE ([4b99466](https://github.com/sthalbert/Longue-Vue/commit/4b9946697a5fbf589a985bd86d769ef9e111b090))
* **store:** classify UpsertWorkload outcome via CTE (preserves time-travel) ([4de522d](https://github.com/sthalbert/Longue-Vue/commit/4de522dc3cea5565586058e400e1a59cc16ca22f))
* **store:** image_versions CRUD, list, reaping, and ref discovery ([5b74a55](https://github.com/sthalbert/Longue-Vue/commit/5b74a550eb21a295cffa89495b1e76b9741e6fe3))
* **store:** include_terminated list filter on clusters/nodes/namespaces/workloads ([b08eab5](https://github.com/sthalbert/Longue-Vue/commit/b08eab526d8679cee53f02ddd406dcec6d6469eb))
* **store:** resurrect soft-deleted rows on upsert (ADR-0021 phase 1) ([46bd543](https://github.com/sthalbert/Longue-Vue/commit/46bd543874c3e7f5f9f283b8a0ae207e9396cbaa))
* **time-travel:** implement ADR-0021 Phase 2 — bi-temporal history snapshots ([bebf35f](https://github.com/sthalbert/Longue-Vue/commit/bebf35fd33668a566db2e1c109dbeec713930cbd))
* **ui/admin:** image registries CRUD page and settings toggle ([7bdb561](https://github.com/sthalbert/Longue-Vue/commit/7bdb5618ff5b7a5ea9830c865cf5f44cab681901))
* **ui/api:** add image-versions and registries client functions ([32fb904](https://github.com/sthalbert/Longue-Vue/commit/32fb9049734ad45d3dcdffea249f842e6ff52655))
* **ui:** add ContainerImageIcon for sidebar Container images entry ([e5d77c9](https://github.com/sthalbert/Longue-Vue/commit/e5d77c91236f16096a18ccc84fcdee35c26274ed))
* **ui:** add ContainerVersionBadge component ([5042667](https://github.com/sthalbert/Longue-Vue/commit/50426670f11d136cec6f4d8d42b351307ede2565))
* **ui:** add History tab to cluster detail page (ADR-0021 Phase 4) ([8280702](https://github.com/sthalbert/Longue-Vue/commit/828070212c771c35e54ef00e16d14d2ff97f27c4))
* **ui:** add Longue Vue favicons for browser tabs ([bdc3def](https://github.com/sthalbert/Longue-Vue/commit/bdc3defc924464c7064fbba356a7ce518134310f))
* **ui:** add Longue Vue favicons for browser tabs ([d93b99b](https://github.com/sthalbert/Longue-Vue/commit/d93b99b6cb846312d46506f216869cdd63d1b975))
* **ui:** add Service/PV/PVC detail pages and link names from list rows ([945279a](https://github.com/sthalbert/Longue-Vue/commit/945279acf37c96a41b35209920da7f934b0285ca))
* **ui:** add Service/PV/PVC detail pages and link names from list rows ([06f6cea](https://github.com/sthalbert/Longue-Vue/commit/06f6ceaaab7cb63b141d5543a035a5e21d7da660))
* **ui:** images inventory page, detail page, and sidebar link ([f1da2da](https://github.com/sthalbert/Longue-Vue/commit/f1da2da6b5a4e5080c71561412cbe66a41393fe3))
* **ui:** impact zoom/pan, paginated lookups, layout, playwright ([93738bd](https://github.com/sthalbert/Longue-Vue/commit/93738bdaee26b2c4401b41e12ae8cc77de247f54))
* **ui:** nouveau logo double-hexagone + restauration role pill ([2d9ee02](https://github.com/sthalbert/Longue-Vue/commit/2d9ee02dd3066e7f7797d7ea171471c9964b87e1))
* **ui:** nouveau logo double-hexagone + restauration role pill ([ac7c3d8](https://github.com/sthalbert/Longue-Vue/commit/ac7c3d80190d80b4be25f93faa140ab54c2fe66a))
* **ui:** per-column funnel filters and clickable summary cards on Container images ([ae6f1a1](https://github.com/sthalbert/Longue-Vue/commit/ae6f1a1db51bb0b44f1679dc98c036531a699732))
* **ui:** per-column value filters on entity tables (closes [#117](https://github.com/sthalbert/Longue-Vue/issues/117)) ([e3644b8](https://github.com/sthalbert/Longue-Vue/commit/e3644b8df960fcf24aaa4deb0f80a9dbd74b2cd3))
* **ui:** place container images entry under EOL in sidebar Tools section ([2f10ab0](https://github.com/sthalbert/Longue-Vue/commit/2f10ab0f68a451f1323f93ad096ea3521ef64418))
* **ui:** redesign with design system tokens, icons, and branded fonts ([b65210e](https://github.com/sthalbert/Longue-Vue/commit/b65210ee36a03c3d9ff7e802b72849c11adb86f4))
* **ui:** redesign with design system tokens, sidebar navigation, and branded fonts ([9b350f7](https://github.com/sthalbert/Longue-Vue/commit/9b350f7d6460a656a6dbe9902396d92535944b1d))
* **ui:** replace top nav with collapsible left sidebar ([8eb6c47](https://github.com/sthalbert/Longue-Vue/commit/8eb6c475bf820698b8e923c88d345871be8957e9))
* **ui:** resizable + per-column-value filters on entity tables ([df91806](https://github.com/sthalbert/Longue-Vue/commit/df91806ab8210d765dfcab9c0c03cc2f701395cf))
* **ui:** resizable columns on entity tables (closes [#116](https://github.com/sthalbert/Longue-Vue/issues/116)) ([687a0d5](https://github.com/sthalbert/Longue-Vue/commit/687a0d54e3185d8b306e403c5ffd1bbf208ebc76))
* **ui:** resizable columns on entity tables (closes [#116](https://github.com/sthalbert/Longue-Vue/issues/116)) ([7323d5a](https://github.com/sthalbert/Longue-Vue/commit/7323d5a83730ccc141c6e92b13726d892d054111))
* **ui:** show ContainerVersionBadge on workload/pod pages ([9abd8d3](https://github.com/sthalbert/Longue-Vue/commit/9abd8d36ae2dcb24d693698abd946df151e48584))
* **ui:** show latest_tag in pod runtime containers table, rename column to Last version ([1879487](https://github.com/sthalbert/Longue-Vue/commit/18794876d5286342db65f28e4ce1b0a478b3eb10))
* **ui:** sortable columns and card-click filtering on EOL dashboard ([93caa46](https://github.com/sthalbert/Longue-Vue/commit/93caa46540a733f4fb4a0ea602e711c85e8097e0))
* **ui:** surface lockout state on the admin Users page ([2459956](https://github.com/sthalbert/Longue-Vue/commit/2459956f2b62e661319f8cbe4cda65e97f1d3721))
* **ui:** time-travel history view with draggable A/B range ([464190c](https://github.com/sthalbert/Longue-Vue/commit/464190c38e163fea759c65ef7791393b5c0da59e))
* **vm-applications:** add VM applications inventory, EOL enrichment, and search filters (ADR-0019) ([7baca4c](https://github.com/sthalbert/Longue-Vue/commit/7baca4cd4cdd5ee587acdbb426f4557628a1bae8))
* **vm-applications:** add VM applications inventory, EOL enrichment,… ([f070593](https://github.com/sthalbert/Longue-Vue/commit/f070593030cf3b81733b1081e78ebdcd0172bb1e))
* **vm-collector:** add VM collector for non-Kubernetes platform infra ([0d78a1b](https://github.com/sthalbert/Longue-Vue/commit/0d78a1bb76814bf5b34a71b60649f2e47b9dac76))
* **vm-collector:** add VM collector for non-Kubernetes platform infrastructure (ADR-0015) ([2d3aca5](https://github.com/sthalbert/Longue-Vue/commit/2d3aca53340f0dc56cbeae65512f4a1451a75eb2))
* **vm-collector:** resolve AMI to image name and surface IP / image on the VM list ([a1433b1](https://github.com/sthalbert/Longue-Vue/commit/a1433b118a4ed408f70278b8f0b61057c843d798))


### Bug Fixes

* **api:** add workload_id server-side filter to /v1/pods ([8ae23d3](https://github.com/sthalbert/Longue-Vue/commit/8ae23d3177654ddcd21ce0cdcf96a740f12c27dd))
* **api:** mark /v1/auth/verify public to the application-layer auth m… ([d857988](https://github.com/sthalbert/Longue-Vue/commit/d857988b2857cb258a5ac58bd4d454db1de4da8a))
* **api:** mark /v1/auth/verify public to the application-layer auth middleware ([34fc17b](https://github.com/sthalbert/Longue-Vue/commit/34fc17bb820eaa9a91061029c0bc3201ef1edc50))
* **api:** mask internal error details in HTTP responses ([4e22824](https://github.com/sthalbert/Longue-Vue/commit/4e22824b3e8e147a44c29ef3bf9d503356c4b9fd))
* **audit:** resolve always-anonymous actor in audit events ([5c8607a](https://github.com/sthalbert/Longue-Vue/commit/5c8607ab75cd4a82ab09bc28de6f415876ba685a))
* **audit:** resolve always-anonymous actor in audit events ([7b08268](https://github.com/sthalbert/Longue-Vue/commit/7b082680908e01a3125145327fb1ee0ab0dd7618))
* **auth:** require delete scope on reconcile endpoints to prevent editor mass-deletion ([0c21b96](https://github.com/sthalbert/Longue-Vue/commit/0c21b9655919ccab033ee7f25ab9bd5468f8b47f))
* change argos by server in values ([7f839e4](https://github.com/sthalbert/Longue-Vue/commit/7f839e4e69f2267f1860b8a8291b827effeb6d8f))
* change argos by server in values ([f222167](https://github.com/sthalbert/Longue-Vue/commit/f2221679f0b94b22128965c106f31946c5fd641c))
* **db:** align migration up/down blocks with project IF EXISTS convention ([94982f5](https://github.com/sthalbert/Longue-Vue/commit/94982f5f823cf9db76ae9b783754b25463846df8))
* **deps:** bump go to 1.25.10 and golang.org/x/net to v0.53.0 ([3e901b7](https://github.com/sthalbert/Longue-Vue/commit/3e901b78843e9df1c68b2d870a7b4a2b11044030))
* **deps:** upgrade golang.org/x/net to v0.51.0 (GO-2026-4559) ([7656d20](https://github.com/sthalbert/Longue-Vue/commit/7656d20c4e52d616c9d79bc1a26aad65f904f055))
* **eol:** match major-only cycles for products like postgresql ([93fa302](https://github.com/sthalbert/Longue-Vue/commit/93fa302197dd1b917b990010a8767d5ed54ec3b2))
* **eol:** match major-only cycles for products like postgresql ([f6042e2](https://github.com/sthalbert/Longue-Vue/commit/f6042e2c9f9b4f34debd4f319209db3957b6d3fb))
* **history-api:** wire auth into AsOfMiddleware so ?as_of requests are authenticated ([850e6f5](https://github.com/sthalbert/Longue-Vue/commit/850e6f50849173d3aae4356c3848246312f89443))
* **imageversions/registry:** explicitly discard resp.Body.Close errors (gosec G104) ([f6aba1a](https://github.com/sthalbert/Longue-Vue/commit/f6aba1a1180ad8077cee3fb1fa3444f9110533fc))
* **imageversions:** preserve data when no registries enabled, log tick summary ([f5ab074](https://github.com/sthalbert/Longue-Vue/commit/f5ab074545b793056e6002b5205195c41d815fb2))
* **impact:** cap graph traversal at 500 nodes to prevent resource exhaustion ([26f36f2](https://github.com/sthalbert/Longue-Vue/commit/26f36f2ca8d07f079a90eb7b8cc204866fe08b7c))
* **ingestgw:** add CertReloader.Close to stop the fsnotify watcher ([d976d8e](https://github.com/sthalbert/Longue-Vue/commit/d976d8e558e5ba44e21ed858435ccf92c9d07aab))
* **lint:** satisfy staticcheck on argosd main + eol test nil checks ([fd86bff](https://github.com/sthalbert/Longue-Vue/commit/fd86bffb1629b03ed5c1ad823b26153871798d06))
* **lint:** switch workload range loops to index iteration (gocritic rangeValCopy) ([ca8ee0e](https://github.com/sthalbert/Longue-Vue/commit/ca8ee0e7ff85ebeb108538a48dc9208517b160f0))
* **longue-vue-collector:** clarify I/O-bound comment in values.yaml ([1bbbd38](https://github.com/sthalbert/Longue-Vue/commit/1bbbd389c4166c09c2bef62d798848520d8a7c39))
* **longue-vue-collector:** clarify I/O-bound comment in values.yaml ([5106cf0](https://github.com/sthalbert/Longue-Vue/commit/5106cf0a18d8fb7e85beb3074559e2752506c2ce))
* mark status as beta and bump go lib ([45355a3](https://github.com/sthalbert/Longue-Vue/commit/45355a3f70cd36873f103610aa55a3467f18fe8f))
* **mcp:** denial=401 not 400; filter node_name; recover from handler panics ([c86917c](https://github.com/sthalbert/Longue-Vue/commit/c86917c61099ecde95344ff89c586853e55f89aa))
* **mcp:** denial=401 not 400; filter node_name; recover from handler panics ([4c36f14](https://github.com/sthalbert/Longue-Vue/commit/4c36f14371204518b6ae5b40f53afb882591dd68))
* **mcp:** enforce stdio token at startup; mask auth errors to client (med-01, med-04) ([7e5b353](https://github.com/sthalbert/Longue-Vue/commit/7e5b35305ff927b984bb83ff24db7da5b72ff7a7))
* **mcp:** enforce stdio token at startup; mask auth errors to client (MED-01, MED-04) ([d8375ea](https://github.com/sthalbert/Longue-Vue/commit/d8375ea456b4cee9b60b52d7dd410d56c2467797))
* **mcp:** wire vm/cloud-account handlers into audit/finishDeferred pattern ([26e0583](https://github.com/sthalbert/Longue-Vue/commit/26e058359c2fd71466fde922c63116d57b7c4942))
* resolve pod display issues on large clusters and detail pagesFix/pod list UUID display ([6e57465](https://github.com/sthalbert/Longue-Vue/commit/6e574658208b60eedb6fd86109c064c8291844ee))
* **sec:** Fix MCP sec issue ([1b27a19](https://github.com/sthalbert/Longue-Vue/commit/1b27a19c204b3005ed78d2398e01e66c702b3733))
* **security:** bump Go to 1.25.9 and pin trivy-action to v0.36.0 ([e06d33f](https://github.com/sthalbert/Longue-Vue/commit/e06d33f13b58190c39f09b891bb9b8aa98f0ef92))
* **security:** clear initial security-scan baseline ([2ae1be0](https://github.com/sthalbert/Longue-Vue/commit/2ae1be09a12c0671f0cfa4c06efac658f8894c11))
* **security:** harden HTTP server, auth, and authorization (8 findings) ([5758733](https://github.com/sthalbert/Longue-Vue/commit/5758733f6ca1c0cff385dee5b5dc1813273766c0))
* **security:** respect severity filter when emitting trivy SARIF ([0351e43](https://github.com/sthalbert/Longue-Vue/commit/0351e4354dda58a3544233ad35b14dac96c5afad))
* **server:** add ReadTimeout/WriteTimeout/IdleTimeout to prevent slowloris ([8f3c49b](https://github.com/sthalbert/Longue-Vue/commit/8f3c49b68c95233355623778f89fd8124400fe83))
* **server:** cap request body at 1 MiB to prevent OOM via large payloads ([a8ea1b8](https://github.com/sthalbert/Longue-Vue/commit/a8ea1b8c0eb99859bf4b8bc43a7467340f202ba7))
* **store:** nil annotation guard, shared escapeLike, isolated test cleanup, Variant doc ([bf55c5a](https://github.com/sthalbert/Longue-Vue/commit/bf55c5ac92303982989d78960cc30668431294f7))
* **store:** qualify upsert CTE projections and tighten test isolation ([2e9fb4c](https://github.com/sthalbert/Longue-Vue/commit/2e9fb4cd9c6c6f380fb5076d1ed84642a2a3b694))
* **store:** tighten error assertions and slice initialization for image registries ([835cee5](https://github.com/sthalbert/Longue-Vue/commit/835cee5dfbcb91ce9b8b74739077c2d50a76bc82))
* **time-travel:** ignore longue-vue.io/eol.* annotations in diff ([e74ceae](https://github.com/sthalbert/Longue-Vue/commit/e74ceaec2141200d0a8e3c879f3e4a36d03b3358))
* **ui:** bump @vitejs/plugin-react for vite 8 and fix stray CSS brace ([630aa0d](https://github.com/sthalbert/Longue-Vue/commit/630aa0d6ff9f0d59f3e463bb0ef995015fc49ba0))
* **ui:** keep wide tables inside the viewport and reclaim wasted left gutter ([7fc2a18](https://github.com/sthalbert/Longue-Vue/commit/7fc2a18f8d868cc1bc52c35fa3732cc052a0e692))
* **ui:** keep wide tables inside the viewport and reclaim wasted left… ([d038518](https://github.com/sthalbert/Longue-Vue/commit/d0385185b3bdc9979b5da541a4fd3f14b8cba0c8))
* **ui:** make column-filter funnel button visible ([21f462f](https://github.com/sthalbert/Longue-Vue/commit/21f462f4cb58142a999758815c43406a13ea5611))
* **ui:** polyfill localStorage/sessionStorage in tests for Node 22+ ([b7ab24c](https://github.com/sthalbert/Longue-Vue/commit/b7ab24c7e0d5524962edfe0bcea5813f94f09112))
* **ui:** resolve workload and namespace names instead of UUIDs on pod pages ([840f4ef](https://github.com/sthalbert/Longue-Vue/commit/840f4efb5a78985204e53408b2cf5e67030e3bdb))
* **ui:** resolve workload names in node detail pod table ([3f9803b](https://github.com/sthalbert/Longue-Vue/commit/3f9803b1bc57def697ddd19f1268a489bdcde337))
* **ui:** resolve workload names in node detail pod table ([4742719](https://github.com/sthalbert/Longue-Vue/commit/47427192aa7d92ccb3bc32a6c429faa9a6b5110d))
* **ui:** scope WorkloadDetail pod fetch to namespace instead of cluster-wide ([c4044fc](https://github.com/sthalbert/Longue-Vue/commit/c4044fcb30a8166457ac742281828c03e6cedf12))
* **ui:** split image name and image id into separate columns / fields ([a9b7415](https://github.com/sthalbert/Longue-Vue/commit/a9b7415d6ad3c860ac6eb0a0599c5d42c49479a3))
* **vm-applications:** omit empty added_at/added_by on new rows in UI … ([4ed6b38](https://github.com/sthalbert/Longue-Vue/commit/4ed6b38e2b7474e7598c66702610ea21d7fd4ebe))
* **vm-applications:** omit empty added_at/added_by on new rows in UI editor ([2f1a6e8](https://github.com/sthalbert/Longue-Vue/commit/2f1a6e8a1f44ad0094fc03dc951bc31153a833b1))

## [0.19.2](https://github.com/sthalbert/Longue-Vue/compare/v0.19.1...v0.19.2) (2026-05-12)


### Bug Fixes

* **ui:** keep wide tables inside the viewport and reclaim wasted left gutter ([7fc2a18](https://github.com/sthalbert/Longue-Vue/commit/7fc2a18f8d868cc1bc52c35fa3732cc052a0e692))
* **ui:** keep wide tables inside the viewport and reclaim wasted left… ([d038518](https://github.com/sthalbert/Longue-Vue/commit/d0385185b3bdc9979b5da541a4fd3f14b8cba0c8))

## [0.19.1](https://github.com/sthalbert/Longue-Vue/compare/v0.19.0...v0.19.1) (2026-05-11)


### Bug Fixes

* change argos by server in values ([7f839e4](https://github.com/sthalbert/Longue-Vue/commit/7f839e4e69f2267f1860b8a8291b827effeb6d8f))
* change argos by server in values ([f222167](https://github.com/sthalbert/Longue-Vue/commit/f2221679f0b94b22128965c106f31946c5fd641c))
* **longue-vue-collector:** clarify I/O-bound comment in values.yaml ([1bbbd38](https://github.com/sthalbert/Longue-Vue/commit/1bbbd389c4166c09c2bef62d798848520d8a7c39))
* **longue-vue-collector:** clarify I/O-bound comment in values.yaml ([5106cf0](https://github.com/sthalbert/Longue-Vue/commit/5106cf0a18d8fb7e85beb3074559e2752506c2ce))
* **ui:** bump @vitejs/plugin-react for vite 8 and fix stray CSS brace ([630aa0d](https://github.com/sthalbert/Longue-Vue/commit/630aa0d6ff9f0d59f3e463bb0ef995015fc49ba0))
* **ui:** polyfill localStorage/sessionStorage in tests for Node 22+ ([b7ab24c](https://github.com/sthalbert/Longue-Vue/commit/b7ab24c7e0d5524962edfe0bcea5813f94f09112))

## [0.19.0](https://github.com/sthalbert/Longue-Vue/compare/v0.18.0...v0.19.0) (2026-05-10)


### Features

* **ui:** add Service/PV/PVC detail pages and link names from list rows ([945279a](https://github.com/sthalbert/Longue-Vue/commit/945279acf37c96a41b35209920da7f934b0285ca))
* **ui:** add Service/PV/PVC detail pages and link names from list rows ([06f6cea](https://github.com/sthalbert/Longue-Vue/commit/06f6ceaaab7cb63b141d5543a035a5e21d7da660))

## [0.18.0](https://github.com/sthalbert/Longue-Vue/compare/v0.17.0...v0.18.0) (2026-05-09)


### Features

* **auth:** add failed_login_count + locked_at columns to users ([7b0c14b](https://github.com/sthalbert/Longue-Vue/commit/7b0c14b9b32521a18004803e9aca575d3a0071ed))
* **auth:** auto-lock login after 6 consecutive failures ([039a085](https://github.com/sthalbert/Longue-Vue/commit/039a08593f00805dd26b6761ee64e80328b0810f))
* **auth:** boot-time admin rescue via LONGUE_VUE_ADMIN_RESCUE_PASSWORD ([917e36b](https://github.com/sthalbert/Longue-Vue/commit/917e36bec7c53c60fe0658123d0a64ba1c9e4d07))
* **auth:** expose failed_login_count, locked_at, unlock in OpenAPI ([eb2ba97](https://github.com/sthalbert/Longue-Vue/commit/eb2ba97aea34fb039b0eda83c31596d2d1db2fd8))
* **auth:** extend Store with IncrementFailedLogin/ResetFailedLogin ([685b07b](https://github.com/sthalbert/Longue-Vue/commit/685b07b62433f36985466ad0f9305d942a2d887d))
* **auth:** handle unlock field in user-update patch ([4c713a5](https://github.com/sthalbert/Longue-Vue/commit/4c713a5b7f4b37848e558933f86748895f4d8c53))
* **auth:** increment failed-login counter with FOR UPDATE serialization ([f12be5f](https://github.com/sthalbert/Longue-Vue/commit/f12be5f69b70c39856fb33a8b2c852d5e6f4260f))
* **auth:** per-account login lockout + admin rescue ([0c2cdb3](https://github.com/sthalbert/Longue-Vue/commit/0c2cdb364f95883f44831dfa536fd00b1d610c4a))
* **auth:** reset failed-login counter on successful login ([c729465](https://github.com/sthalbert/Longue-Vue/commit/c729465959897fdeba7633caf699984931712e40))
* **chart:** expose adminRescuePassword on longue-vue chart ([b8d91d8](https://github.com/sthalbert/Longue-Vue/commit/b8d91d80d7516e194f2077ee37b60ee0d91826af))
* **ui:** surface lockout state on the admin Users page ([2459956](https://github.com/sthalbert/Longue-Vue/commit/2459956f2b62e661319f8cbe4cda65e97f1d3721))

## [0.17.0](https://github.com/sthalbert/Longue-Vue/compare/v0.16.0...v0.17.0) (2026-05-09)


### Features

* **api:** add list/detail/refresh handlers for image versions ([cb1fbcf](https://github.com/sthalbert/Longue-Vue/commit/cb1fbcfa79ce85d1146159072f6d86919fd9f494))
* **api:** admin CRUD on image_versions_registries ([4628cd7](https://github.com/sthalbert/Longue-Vue/commit/4628cd77e22966e6234bb2dd3a657ded5bf9cd24))
* **api:** enrich workload/pod GETs with containers_versions ([2883b92](https://github.com/sthalbert/Longue-Vue/commit/2883b92447a2634c3f4bb2cda4dbeb9da11585f3))
* **api:** mount image-versions and registries routes ([e8a1af4](https://github.com/sthalbert/Longue-Vue/commit/e8a1af483159a8a916a435c761530f5b65ecb6d1))
* **chart:** expose mcp.allowPlaintext for local/dev clusters without TLS ([313f7ef](https://github.com/sthalbert/Longue-Vue/commit/313f7efe6fc6d20d3a328ba94b78f4efae57e80a))
* container image versions enrichment (ADR-0022) ([72b799b](https://github.com/sthalbert/Longue-Vue/commit/72b799b76c0de46815551f1920d5b09164c33caf))
* **db:** migrations for image_versions tables and settings flag ([11f7cc8](https://github.com/sthalbert/Longue-Vue/commit/11f7cc857956ed8460ade45529421571159ed1e5))
* **imageversions/registry:** add OCI client with bearer auth and pagination ([c1fd19b](https://github.com/sthalbert/Longue-Vue/commit/c1fd19b5d348d58139fe4fede86963853fb07cbb))
* **imageversions/registry:** hostname → effective HTTPS host ([cfbb155](https://github.com/sthalbert/Longue-Vue/commit/cfbb15515bd7fa9bd9385b20b0042d40cbd6c92e))
* **imageversions/registry:** hostname pattern matcher ([72b4c33](https://github.com/sthalbert/Longue-Vue/commit/72b4c3398be11a01dd7394d64e08305e2ebd8afb))
* **imageversions:** add Prometheus metrics for ticks and registry queries ([dd1a16b](https://github.com/sthalbert/Longue-Vue/commit/dd1a16b1b9607968c9f76b746c78f63003582dad))
* **imageversions:** image ref and tag parsing ([11ca493](https://github.com/sthalbert/Longue-Vue/commit/11ca4939c2c7faaaeeaa56a1687887ac9be4d04f))
* **imageversions:** pattern-aware latest tag computation ([a4cae00](https://github.com/sthalbert/Longue-Vue/commit/a4cae0049a8d41bdccdaff98fb1e4c9248cba824))
* **imageversions:** periodic enricher with trigger and reaping ([c39545a](https://github.com/sthalbert/Longue-Vue/commit/c39545a4936c074716993fab55752a584d3b8aaa))
* **main:** start image versions enricher goroutine at boot ([1380cef](https://github.com/sthalbert/Longue-Vue/commit/1380cef7bf12136759c79bcd35f3709e83fe27f1))
* **mcp:** add list_image_versions, get_image_version, get_image_versions_summary tools ([359b6c3](https://github.com/sthalbert/Longue-Vue/commit/359b6c339db8b750f933e0dd2978b9c90be6e3c3))
* **settings:** add image_versions_enabled toggle ([f35a83b](https://github.com/sthalbert/Longue-Vue/commit/f35a83bef21303b130e4ac87a74ac3cbc11d31fc))
* **store:** add CRUD for image_versions_registries ([3ece105](https://github.com/sthalbert/Longue-Vue/commit/3ece105d90df3b23b03d69d0264e444b909ca943))
* **store:** image_versions CRUD, list, reaping, and ref discovery ([5b74a55](https://github.com/sthalbert/Longue-Vue/commit/5b74a550eb21a295cffa89495b1e76b9741e6fe3))
* **ui/admin:** image registries CRUD page and settings toggle ([7bdb561](https://github.com/sthalbert/Longue-Vue/commit/7bdb5618ff5b7a5ea9830c865cf5f44cab681901))
* **ui/api:** add image-versions and registries client functions ([32fb904](https://github.com/sthalbert/Longue-Vue/commit/32fb9049734ad45d3dcdffea249f842e6ff52655))
* **ui:** add ContainerImageIcon for sidebar Container images entry ([e5d77c9](https://github.com/sthalbert/Longue-Vue/commit/e5d77c91236f16096a18ccc84fcdee35c26274ed))
* **ui:** add ContainerVersionBadge component ([5042667](https://github.com/sthalbert/Longue-Vue/commit/50426670f11d136cec6f4d8d42b351307ede2565))
* **ui:** images inventory page, detail page, and sidebar link ([f1da2da](https://github.com/sthalbert/Longue-Vue/commit/f1da2da6b5a4e5080c71561412cbe66a41393fe3))
* **ui:** per-column funnel filters and clickable summary cards on Container images ([ae6f1a1](https://github.com/sthalbert/Longue-Vue/commit/ae6f1a1db51bb0b44f1679dc98c036531a699732))
* **ui:** place container images entry under EOL in sidebar Tools section ([2f10ab0](https://github.com/sthalbert/Longue-Vue/commit/2f10ab0f68a451f1323f93ad096ea3521ef64418))
* **ui:** show ContainerVersionBadge on workload/pod pages ([9abd8d3](https://github.com/sthalbert/Longue-Vue/commit/9abd8d36ae2dcb24d693698abd946df151e48584))
* **ui:** show latest_tag in pod runtime containers table, rename column to Last version ([1879487](https://github.com/sthalbert/Longue-Vue/commit/18794876d5286342db65f28e4ce1b0a478b3eb10))


### Bug Fixes

* **db:** align migration up/down blocks with project IF EXISTS convention ([94982f5](https://github.com/sthalbert/Longue-Vue/commit/94982f5f823cf9db76ae9b783754b25463846df8))
* **imageversions/registry:** explicitly discard resp.Body.Close errors (gosec G104) ([f6aba1a](https://github.com/sthalbert/Longue-Vue/commit/f6aba1a1180ad8077cee3fb1fa3444f9110533fc))
* **imageversions:** preserve data when no registries enabled, log tick summary ([f5ab074](https://github.com/sthalbert/Longue-Vue/commit/f5ab074545b793056e6002b5205195c41d815fb2))
* **lint:** switch workload range loops to index iteration (gocritic rangeValCopy) ([ca8ee0e](https://github.com/sthalbert/Longue-Vue/commit/ca8ee0e7ff85ebeb108538a48dc9208517b160f0))
* **store:** nil annotation guard, shared escapeLike, isolated test cleanup, Variant doc ([bf55c5a](https://github.com/sthalbert/Longue-Vue/commit/bf55c5ac92303982989d78960cc30668431294f7))
* **store:** tighten error assertions and slice initialization for image registries ([835cee5](https://github.com/sthalbert/Longue-Vue/commit/835cee5dfbcb91ce9b8b74739077c2d50a76bc82))

## [0.16.0](https://github.com/sthalbert/Longue-Vue/compare/v0.15.0...v0.16.0) (2026-05-08)


### Features

* **adr:** history api ([611826f](https://github.com/sthalbert/Longue-Vue/commit/611826f3b05d03cb56b0060d4fa9b721b0ee323f))
* **api,ui:** admin-only cluster deletion with enriched audit trail (… ([b892409](https://github.com/sthalbert/Longue-Vue/commit/b892409603fe43d72578dcf2d8b382d414874959))
* **api,ui:** admin-only cluster deletion with enriched audit trail (ADR-0010) ([4557bcb](https://github.com/sthalbert/Longue-Vue/commit/4557bcb4e5fe340d93de55314467947031c9c2a2))
* **api:** add include_terminated query param to list endpoints (ADR-0021 phase 1) ([753498a](https://github.com/sthalbert/Longue-Vue/commit/753498ad5c1fda6539a200df9aaec24c0b973c37))
* **api:** list handlers honor include_terminated query param (ADR-0021 phase 1) ([c82344d](https://github.com/sthalbert/Longue-Vue/commit/c82344d06ab4eb82adbb2b90b9a5a276b94e6abb))
* **api:** soft-delete cluster/namespace cascade to children (ADR-0021 phase 1) ([61224b5](https://github.com/sthalbert/Longue-Vue/commit/61224b58026182a4b9a038f06dbba400d08b9d49))
* **auth:** accept legacy argos_pat_ tokens, emit longue_vue_pat_ ([dccbeeb](https://github.com/sthalbert/Longue-Vue/commit/dccbeeb0be0f184e7db071a4d2716e43a1f2ed6e))
* **auth:** add per-IP login rate limiting (5 req/min, ADR-0007 IMP-009) ([db78bca](https://github.com/sthalbert/Longue-Vue/commit/db78bca25f0150ce4ae06df862a515363cf74387))
* **auth:** public-listener TLS posture, proxy trust, and last-admin … ([80711e6](https://github.com/sthalbert/Longue-Vue/commit/80711e63d0632aabc9d670f0549e1c42929007c1))
* **auth:** public-listener TLS posture, proxy trust, and last-admin guard ([9eabada](https://github.com/sthalbert/Longue-Vue/commit/9eabada80ac52c40fdb4e739fd0d431851a17236))
* **charts:** helm chart per deployable binary (ADR-0018) ([56a4efd](https://github.com/sthalbert/Longue-Vue/commit/56a4efd49ed8910879c7c77b23aa4a3af2d555e2))
* **charts:** helm chart per deployable binary (ADR-0018) ([d1ef329](https://github.com/sthalbert/Longue-Vue/commit/d1ef329585ff492e939889113cfe3e9165d61df4))
* **dmz-gateway:** add DMZ ingest gateway for collector push traffic ([c511594](https://github.com/sthalbert/Longue-Vue/commit/c511594db6ac9852a865d3d8ced1011560aa2427))
* **dmz-gateway:** add DMZ ingest gateway for collector push traffic ([95bd4fb](https://github.com/sthalbert/Longue-Vue/commit/95bd4fbd110c8fdb35ad3e969bdddbe248012310))
* **eol:** add end-of-life enrichment via endoflife.date ([568b5b5](https://github.com/sthalbert/Longue-Vue/commit/568b5b5ece1502496e80a778c9fc20f09cd91c25))
* **eol:** add end-of-life enrichment via endoflife.date (ADR-0012) ([698c1b1](https://github.com/sthalbert/Longue-Vue/commit/698c1b103a150f4a471da32ee83478669c69156d))
* **eol:** add latest available version and redesign EOL inventory UI ([8a7ff3f](https://github.com/sthalbert/Longue-Vue/commit/8a7ff3f7891ae8e9071cba3c6e7343ad07774b83))
* **eol:** admin-toggleable EOL enrichment via UI settings ([b000976](https://github.com/sthalbert/Longue-Vue/commit/b0009760b34e2fedb150d17452064c374bc437af))
* **helm:** expose MCP SSE port in Service and Deployment ([7f05a11](https://github.com/sthalbert/Longue-Vue/commit/7f05a11c293b6efb456b8ba889d8eb8998e73132))
* **helm:** expose MCP SSE port in Service and Deployment ([a5eaaf2](https://github.com/sthalbert/Longue-Vue/commit/a5eaaf296d1fb82b56638c12371829743207233a))
* **history-api:** add integration tests for ListEntityHistory and GetEntityAsOf ([cc2106c](https://github.com/sthalbert/Longue-Vue/commit/cc2106c2849f4209f44a989f51b090abcd026de0))
* **history-api:** add phase 3 backend — history list + as_of endpoints (ADR-0021) ([e956b86](https://github.com/sthalbert/Longue-Vue/commit/e956b86c94091fd6eb219e14868dcca320a52064))
* **impact:** add impact analysis graph with interactive UI (ADR-0013) ([27dfb3b](https://github.com/sthalbert/Longue-Vue/commit/27dfb3beacbf7a446749707a83a5595518f7055a))
* **impact:** add impact analysis graph with interactive UI (ADR-0013) ([7621980](https://github.com/sthalbert/Longue-Vue/commit/762198047bca47a319e18142a1b38c4559c5c8a8))
* **mcp:** add get_cloud_account tool with credential redaction ([b06f229](https://github.com/sthalbert/Longue-Vue/commit/b06f2292eabedca19fc38fba2419396f786d99bc))
* **mcp:** add get_virtual_machine and list_vm_applications_distinct tools ([3de8469](https://github.com/sthalbert/Longue-Vue/commit/3de846912a928eac3edaf8a775158b1b22e3f6c9))
* **mcp:** add list_cloud_accounts tool with credential redaction ([24888be](https://github.com/sthalbert/Longue-Vue/commit/24888becb4271a200a0c9084c67a510c3dd31eb5))
* **mcp:** add list_virtual_machines tool with full filter set ([b89dbab](https://github.com/sthalbert/Longue-Vue/commit/b89dbabf496dba65f93b3524430a2a314bd026f4))
* **mcp:** add MCP server for AI-driven CMDB queries (ADR-0014) ([ad59d33](https://github.com/sthalbert/Longue-Vue/commit/ad59d3345e8bbbdd1d436c135646c51625f616ef))
* **mcp:** add MCP server for AI-driven CMDB queries (ADR-0014) ([2fdf934](https://github.com/sthalbert/Longue-Vue/commit/2fdf9346f4baeb50234ada1c2ae07e443d43abf3))
* **mcp:** add redactcloudaccount helper to strip access_key ([b0d619f](https://github.com/sthalbert/Longue-Vue/commit/b0d619f5a54efed1f250b7e1e8454536ea82fdda))
* **mcp:** bounded lru auth cache with revocation invalidation (high-01/03, med-03) ([d9c090e](https://github.com/sthalbert/Longue-Vue/commit/d9c090e6c8ec8163be41dc92a9d9c3f54619e187))
* **mcp:** bounded LRU auth cache with revocation invalidation (HIGH-01/03, MED-03) ([fbf817a](https://github.com/sthalbert/Longue-Vue/commit/fbf817a0b0f9771edbdc5d5e665ea8a3f7d4359c))
* **mcp:** emit audit_events rows for every tool call (crit-02) ([0405559](https://github.com/sthalbert/Longue-Vue/commit/040555919ba47d8a2fad9104b042c8e78282d7f8))
* **mcp:** emit audit_events rows for every tool call (CRIT-02) ([0a7fd9e](https://github.com/sthalbert/Longue-Vue/commit/0a7fd9e87d091c11c1e705a457e670ea54c325a3))
* **mcp:** extend search_images with virtual_machines section ([cd6ce42](https://github.com/sthalbert/Longue-Vue/commit/cd6ce423d0d547210d28d1dca4e56525a3ddece6))
* **mcp:** include virtual machines ([87c9b60](https://github.com/sthalbert/Longue-Vue/commit/87c9b60bea709f4fb5926ab08980d1cac42dc4b4))
* **mcp:** include virtual machines in get_eol_summary tool ([400e9e1](https://github.com/sthalbert/Longue-Vue/commit/400e9e1e716b323fcf05e27809e44a2924c73bbb))
* **mcp:** native tls 1.3 + loopback default for sse listener (crit-01, med-02) ([5e19f3d](https://github.com/sthalbert/Longue-Vue/commit/5e19f3d72ba3509d7e67ee975f94456d92f7c7ab))
* **mcp:** native TLS 1.3 + loopback default for SSE listener (CRIT-01, MED-02) ([a09e67e](https://github.com/sthalbert/Longue-Vue/commit/a09e67e15e9b4a3e7577b4935ed106f5b3d15634))
* **mcp:** per-token rate limiting on tool calls (high-02) ([17c39aa](https://github.com/sthalbert/Longue-Vue/commit/17c39aaffe9413edfe71f91641556ca1e99698f6))
* **mcp:** per-token rate limiting on tool calls (HIGH-02) ([016cf34](https://github.com/sthalbert/Longue-Vue/commit/016cf34a962c295d588c0af5496ef1a57ad569f0))
* **mcp:** security hardening ([9cb66db](https://github.com/sthalbert/Longue-Vue/commit/9cb66dbf908fed3a7115262d9b07b72a88d5a916))
* **migrations:** add terminated_at to clusters/namespaces/nodes/workloads (ADR-0021 phase 1) ([09768fc](https://github.com/sthalbert/Longue-Vue/commit/09768fcb97ecfc09b340cbeea2d407af859f175c))
* **security:** add HTTP security headers middleware (CSP, XCTO, XFO, HSTS) ([2a2cfd6](https://github.com/sthalbert/Longue-Vue/commit/2a2cfd693e8af6115efa11688edd51cb547a6dbe))
* **store:** include_terminated list filter on clusters/nodes/namespaces/workloads ([b08eab5](https://github.com/sthalbert/Longue-Vue/commit/b08eab526d8679cee53f02ddd406dcec6d6469eb))
* **store:** resurrect soft-deleted rows on upsert (ADR-0021 phase 1) ([46bd543](https://github.com/sthalbert/Longue-Vue/commit/46bd543874c3e7f5f9f283b8a0ae207e9396cbaa))
* **time-travel:** implement ADR-0021 Phase 2 — bi-temporal history snapshots ([bebf35f](https://github.com/sthalbert/Longue-Vue/commit/bebf35fd33668a566db2e1c109dbeec713930cbd))
* **ui:** add History tab to cluster detail page (ADR-0021 Phase 4) ([8280702](https://github.com/sthalbert/Longue-Vue/commit/828070212c771c35e54ef00e16d14d2ff97f27c4))
* **ui:** add Longue Vue favicons for browser tabs ([bdc3def](https://github.com/sthalbert/Longue-Vue/commit/bdc3defc924464c7064fbba356a7ce518134310f))
* **ui:** add Longue Vue favicons for browser tabs ([d93b99b](https://github.com/sthalbert/Longue-Vue/commit/d93b99b6cb846312d46506f216869cdd63d1b975))
* **ui:** impact zoom/pan, paginated lookups, layout, playwright ([93738bd](https://github.com/sthalbert/Longue-Vue/commit/93738bdaee26b2c4401b41e12ae8cc77de247f54))
* **ui:** nouveau logo double-hexagone + restauration role pill ([2d9ee02](https://github.com/sthalbert/Longue-Vue/commit/2d9ee02dd3066e7f7797d7ea171471c9964b87e1))
* **ui:** nouveau logo double-hexagone + restauration role pill ([ac7c3d8](https://github.com/sthalbert/Longue-Vue/commit/ac7c3d80190d80b4be25f93faa140ab54c2fe66a))
* **ui:** per-column value filters on entity tables (closes [#117](https://github.com/sthalbert/Longue-Vue/issues/117)) ([e3644b8](https://github.com/sthalbert/Longue-Vue/commit/e3644b8df960fcf24aaa4deb0f80a9dbd74b2cd3))
* **ui:** redesign with design system tokens, icons, and branded fonts ([b65210e](https://github.com/sthalbert/Longue-Vue/commit/b65210ee36a03c3d9ff7e802b72849c11adb86f4))
* **ui:** redesign with design system tokens, sidebar navigation, and branded fonts ([9b350f7](https://github.com/sthalbert/Longue-Vue/commit/9b350f7d6460a656a6dbe9902396d92535944b1d))
* **ui:** replace top nav with collapsible left sidebar ([8eb6c47](https://github.com/sthalbert/Longue-Vue/commit/8eb6c475bf820698b8e923c88d345871be8957e9))
* **ui:** resizable + per-column-value filters on entity tables ([df91806](https://github.com/sthalbert/Longue-Vue/commit/df91806ab8210d765dfcab9c0c03cc2f701395cf))
* **ui:** resizable columns on entity tables (closes [#116](https://github.com/sthalbert/Longue-Vue/issues/116)) ([687a0d5](https://github.com/sthalbert/Longue-Vue/commit/687a0d54e3185d8b306e403c5ffd1bbf208ebc76))
* **ui:** resizable columns on entity tables (closes [#116](https://github.com/sthalbert/Longue-Vue/issues/116)) ([7323d5a](https://github.com/sthalbert/Longue-Vue/commit/7323d5a83730ccc141c6e92b13726d892d054111))
* **ui:** sortable columns and card-click filtering on EOL dashboard ([93caa46](https://github.com/sthalbert/Longue-Vue/commit/93caa46540a733f4fb4a0ea602e711c85e8097e0))
* **ui:** time-travel history view with draggable A/B range ([464190c](https://github.com/sthalbert/Longue-Vue/commit/464190c38e163fea759c65ef7791393b5c0da59e))
* **vm-applications:** add VM applications inventory, EOL enrichment, and search filters (ADR-0019) ([7baca4c](https://github.com/sthalbert/Longue-Vue/commit/7baca4cd4cdd5ee587acdbb426f4557628a1bae8))
* **vm-applications:** add VM applications inventory, EOL enrichment,… ([f070593](https://github.com/sthalbert/Longue-Vue/commit/f070593030cf3b81733b1081e78ebdcd0172bb1e))
* **vm-collector:** add VM collector for non-Kubernetes platform infra ([0d78a1b](https://github.com/sthalbert/Longue-Vue/commit/0d78a1bb76814bf5b34a71b60649f2e47b9dac76))
* **vm-collector:** add VM collector for non-Kubernetes platform infrastructure (ADR-0015) ([2d3aca5](https://github.com/sthalbert/Longue-Vue/commit/2d3aca53340f0dc56cbeae65512f4a1451a75eb2))
* **vm-collector:** resolve AMI to image name and surface IP / image on the VM list ([a1433b1](https://github.com/sthalbert/Longue-Vue/commit/a1433b118a4ed408f70278b8f0b61057c843d798))


### Bug Fixes

* **api:** add workload_id server-side filter to /v1/pods ([8ae23d3](https://github.com/sthalbert/Longue-Vue/commit/8ae23d3177654ddcd21ce0cdcf96a740f12c27dd))
* **api:** mark /v1/auth/verify public to the application-layer auth m… ([d857988](https://github.com/sthalbert/Longue-Vue/commit/d857988b2857cb258a5ac58bd4d454db1de4da8a))
* **api:** mark /v1/auth/verify public to the application-layer auth middleware ([34fc17b](https://github.com/sthalbert/Longue-Vue/commit/34fc17bb820eaa9a91061029c0bc3201ef1edc50))
* **api:** mask internal error details in HTTP responses ([4e22824](https://github.com/sthalbert/Longue-Vue/commit/4e22824b3e8e147a44c29ef3bf9d503356c4b9fd))
* **audit:** resolve always-anonymous actor in audit events ([5c8607a](https://github.com/sthalbert/Longue-Vue/commit/5c8607ab75cd4a82ab09bc28de6f415876ba685a))
* **audit:** resolve always-anonymous actor in audit events ([7b08268](https://github.com/sthalbert/Longue-Vue/commit/7b082680908e01a3125145327fb1ee0ab0dd7618))
* **auth:** require delete scope on reconcile endpoints to prevent editor mass-deletion ([0c21b96](https://github.com/sthalbert/Longue-Vue/commit/0c21b9655919ccab033ee7f25ab9bd5468f8b47f))
* **deps:** bump go to 1.25.10 and golang.org/x/net to v0.53.0 ([3e901b7](https://github.com/sthalbert/Longue-Vue/commit/3e901b78843e9df1c68b2d870a7b4a2b11044030))
* **deps:** upgrade golang.org/x/net to v0.51.0 (GO-2026-4559) ([7656d20](https://github.com/sthalbert/Longue-Vue/commit/7656d20c4e52d616c9d79bc1a26aad65f904f055))
* **eol:** match major-only cycles for products like postgresql ([93fa302](https://github.com/sthalbert/Longue-Vue/commit/93fa302197dd1b917b990010a8767d5ed54ec3b2))
* **eol:** match major-only cycles for products like postgresql ([f6042e2](https://github.com/sthalbert/Longue-Vue/commit/f6042e2c9f9b4f34debd4f319209db3957b6d3fb))
* **history-api:** wire auth into AsOfMiddleware so ?as_of requests are authenticated ([850e6f5](https://github.com/sthalbert/Longue-Vue/commit/850e6f50849173d3aae4356c3848246312f89443))
* **impact:** cap graph traversal at 500 nodes to prevent resource exhaustion ([26f36f2](https://github.com/sthalbert/Longue-Vue/commit/26f36f2ca8d07f079a90eb7b8cc204866fe08b7c))
* **ingestgw:** add CertReloader.Close to stop the fsnotify watcher ([d976d8e](https://github.com/sthalbert/Longue-Vue/commit/d976d8e558e5ba44e21ed858435ccf92c9d07aab))
* **lint:** satisfy staticcheck on argosd main + eol test nil checks ([fd86bff](https://github.com/sthalbert/Longue-Vue/commit/fd86bffb1629b03ed5c1ad823b26153871798d06))
* mark status as beta and bump go lib ([45355a3](https://github.com/sthalbert/Longue-Vue/commit/45355a3f70cd36873f103610aa55a3467f18fe8f))
* **mcp:** denial=401 not 400; filter node_name; recover from handler panics ([c86917c](https://github.com/sthalbert/Longue-Vue/commit/c86917c61099ecde95344ff89c586853e55f89aa))
* **mcp:** denial=401 not 400; filter node_name; recover from handler panics ([4c36f14](https://github.com/sthalbert/Longue-Vue/commit/4c36f14371204518b6ae5b40f53afb882591dd68))
* **mcp:** enforce stdio token at startup; mask auth errors to client (med-01, med-04) ([7e5b353](https://github.com/sthalbert/Longue-Vue/commit/7e5b35305ff927b984bb83ff24db7da5b72ff7a7))
* **mcp:** enforce stdio token at startup; mask auth errors to client (MED-01, MED-04) ([d8375ea](https://github.com/sthalbert/Longue-Vue/commit/d8375ea456b4cee9b60b52d7dd410d56c2467797))
* **mcp:** wire vm/cloud-account handlers into audit/finishDeferred pattern ([26e0583](https://github.com/sthalbert/Longue-Vue/commit/26e058359c2fd71466fde922c63116d57b7c4942))
* resolve pod display issues on large clusters and detail pagesFix/pod list UUID display ([6e57465](https://github.com/sthalbert/Longue-Vue/commit/6e574658208b60eedb6fd86109c064c8291844ee))
* **sec:** Fix MCP sec issue ([1b27a19](https://github.com/sthalbert/Longue-Vue/commit/1b27a19c204b3005ed78d2398e01e66c702b3733))
* **security:** bump Go to 1.25.9 and pin trivy-action to v0.36.0 ([e06d33f](https://github.com/sthalbert/Longue-Vue/commit/e06d33f13b58190c39f09b891bb9b8aa98f0ef92))
* **security:** clear initial security-scan baseline ([2ae1be0](https://github.com/sthalbert/Longue-Vue/commit/2ae1be09a12c0671f0cfa4c06efac658f8894c11))
* **security:** harden HTTP server, auth, and authorization (8 findings) ([5758733](https://github.com/sthalbert/Longue-Vue/commit/5758733f6ca1c0cff385dee5b5dc1813273766c0))
* **security:** respect severity filter when emitting trivy SARIF ([0351e43](https://github.com/sthalbert/Longue-Vue/commit/0351e4354dda58a3544233ad35b14dac96c5afad))
* **server:** add ReadTimeout/WriteTimeout/IdleTimeout to prevent slowloris ([8f3c49b](https://github.com/sthalbert/Longue-Vue/commit/8f3c49b68c95233355623778f89fd8124400fe83))
* **server:** cap request body at 1 MiB to prevent OOM via large payloads ([a8ea1b8](https://github.com/sthalbert/Longue-Vue/commit/a8ea1b8c0eb99859bf4b8bc43a7467340f202ba7))
* **time-travel:** ignore longue-vue.io/eol.* annotations in diff ([e74ceae](https://github.com/sthalbert/Longue-Vue/commit/e74ceaec2141200d0a8e3c879f3e4a36d03b3358))
* **ui:** make column-filter funnel button visible ([21f462f](https://github.com/sthalbert/Longue-Vue/commit/21f462f4cb58142a999758815c43406a13ea5611))
* **ui:** resolve workload and namespace names instead of UUIDs on pod pages ([840f4ef](https://github.com/sthalbert/Longue-Vue/commit/840f4efb5a78985204e53408b2cf5e67030e3bdb))
* **ui:** resolve workload names in node detail pod table ([3f9803b](https://github.com/sthalbert/Longue-Vue/commit/3f9803b1bc57def697ddd19f1268a489bdcde337))
* **ui:** resolve workload names in node detail pod table ([4742719](https://github.com/sthalbert/Longue-Vue/commit/47427192aa7d92ccb3bc32a6c429faa9a6b5110d))
* **ui:** scope WorkloadDetail pod fetch to namespace instead of cluster-wide ([c4044fc](https://github.com/sthalbert/Longue-Vue/commit/c4044fcb30a8166457ac742281828c03e6cedf12))
* **ui:** split image name and image id into separate columns / fields ([a9b7415](https://github.com/sthalbert/Longue-Vue/commit/a9b7415d6ad3c860ac6eb0a0599c5d42c49479a3))
* **vm-applications:** omit empty added_at/added_by on new rows in UI … ([4ed6b38](https://github.com/sthalbert/Longue-Vue/commit/4ed6b38e2b7474e7598c66702610ea21d7fd4ebe))
* **vm-applications:** omit empty added_at/added_by on new rows in UI editor ([2f1a6e8](https://github.com/sthalbert/Longue-Vue/commit/2f1a6e8a1f44ad0094fc03dc951bc31153a833b1))

## [0.15.0](https://github.com/sthalbert/Longue-Vue/compare/v0.14.0...v0.15.0) (2026-05-06)


### Features

* **ui:** add Longue Vue favicons for browser tabs ([36f630c](https://github.com/sthalbert/Longue-Vue/commit/36f630c0974cf67c95196361911703872a0c1745))
* **ui:** add Longue Vue favicons for browser tabs ([3315a40](https://github.com/sthalbert/Longue-Vue/commit/3315a407f0af9324f09308e020902d435c697a74))
* **ui:** per-column value filters on entity tables (closes [#117](https://github.com/sthalbert/Longue-Vue/issues/117)) ([570aa65](https://github.com/sthalbert/Longue-Vue/commit/570aa65dd8b0156f9ad01d9a77ce98f00d3ebc41))
* **ui:** resizable + per-column-value filters on entity tables ([ad5e0bb](https://github.com/sthalbert/Longue-Vue/commit/ad5e0bb761573021cad60428bdb7645803439573))
* **ui:** resizable columns on entity tables (closes [#116](https://github.com/sthalbert/Longue-Vue/issues/116)) ([697adf5](https://github.com/sthalbert/Longue-Vue/commit/697adf5926b36964b962b1c3c0c7134fb4969165))
* **ui:** resizable columns on entity tables (closes [#116](https://github.com/sthalbert/Longue-Vue/issues/116)) ([475020b](https://github.com/sthalbert/Longue-Vue/commit/475020b8495f8e2a4776744c114e149068c4b31a))


### Bug Fixes

* **security:** bump Go to 1.25.9 and pin trivy-action to v0.36.0 ([ed8eb8b](https://github.com/sthalbert/Longue-Vue/commit/ed8eb8bbb5bb0aad33180b4524249fd41b5c1bde))
* **security:** clear initial security-scan baseline ([9492ef4](https://github.com/sthalbert/Longue-Vue/commit/9492ef4c97342403f13a69e73404b62c76d9698c))
* **security:** respect severity filter when emitting trivy SARIF ([a63ad01](https://github.com/sthalbert/Longue-Vue/commit/a63ad01cc57c794ad87f99463b367e4ec6158387))
* **ui:** make column-filter funnel button visible ([4de0e54](https://github.com/sthalbert/Longue-Vue/commit/4de0e54d975b4d272c8f74502ace8e3d13090d16))

## [0.12.2] — 2026-04-30

Helm charts realigned on `appVersion 0.12.2` across the family
(`longue-vue` 0.15.2, `longue-vue-collector` 0.1.2, `longue-vue-ingest-gw` 0.1.3,
`longue-vue-vm-collector` 0.1.2). Two bug fixes against 0.12.1: the DMZ ingest
gateway can actually verify tokens against longue-vue (a spec-level oversight
made the verify endpoint reject every call from the gateway with 401),
and EOL enrichment now matches major-only product cycles.

### Fixed

- **DMZ ingest gateway → longue-vue verify call always returned `401 missing
  or invalid credentials`, blocking every forwarded write.** `POST
  /v1/auth/verify` (ADR-0016 §5) is authenticated by the mTLS-only
  listener handshake, not by an `Authorization` header — the gateway
  sends the token to verify in the request body and never presents a
  bearer credential of its own. The OpenAPI spec did not declare
  `security: []` on the operation, so it inherited the document-wide
  `BearerAuth + SessionCookie` requirement; the codegen wrapper then set
  a non-nil empty `BearerAuthScopes` on the request context, and
  `auth.Middleware` interpreted that as "auth required" and 401-rejected
  every call before `VerifyToken` ran. The fix adds `security: []` to
  the operation (matching the existing pattern on `/healthz`,
  `/v1/auth/login`, and `/v1/auth/oidc/authorize`); `internal/api/` was
  regenerated; a new regression test
  `TestVerifyToken_PublicEndpoint_NoAuthHeader_Returns200` exercises the
  real `auth.Middleware` and locks the contract. The listener-level
  `RequireAndVerifyClientCert` is unchanged — mTLS remains the sole
  authentication mechanism for the call.
- **EOL enrichment fell through to `eol_status=unknown` for products
  whose endoflife.date cycle key is a single major version
  (e.g. `postgresql` `15`).** `extractMajorMinor` required at least two
  numeric components, so a VM application declared as `postgresql`
  version `15` never matched cycle `15` even though endoflife.date
  exposes it. Replaced with `extractCycleCandidates` returning
  `[major.minor, major]` in priority order; the resolver now retries
  with the major-only candidate before stubbing the annotation.

## [0.12.1] — 2026-04-30

Helm charts realigned on `appVersion 0.12.1` across the family
(`longue-vue` 0.15.1, `longue-vue-collector` 0.1.1, `longue-vue-ingest-gw` 0.1.2,
`longue-vue-vm-collector` 0.1.1). UI hotfix for the VM applications editor
introduced in 0.12.0; the collector binaries are unchanged but their
charts bump in lockstep so `helm list` shows a single coherent
appVersion across a longue-vue deployment.

### Fixed

- **`PATCH /v1/virtual-machines/{id}` returned `400 invalid JSON body` when
  adding a new application row from the UI.** The `ApplicationsCard` editor
  was sending `added_at: ""` and `added_by: ""` for fresh rows; the server
  decodes `added_at` into `time.Time`, which cannot parse the empty string,
  so the entire request was rejected before reaching the diff logic that
  would have stamped the missing values. The form now omits those fields
  entirely on new rows (and only re-sends them for rows that already carry
  server-stamped values), letting the server take its existing
  preserve-or-stamp path. No schema or API change — only the UI payload
  shape is fixed.

## [0.12.0] — 2026-04-29

Helm chart 0.15.0 / appVersion 0.12.0. Adds VM application inventory and EOL
enrichment for platform software (ADR-0019): operators can now record what runs
on each non-Kubernetes VM, the EOL enricher evaluates those declared versions
against endoflife.date, and the VM list grows six new server-side filters plus
a distinct-applications endpoint for autocomplete.

### Added

- **`virtual_machines.applications` JSONB column** (ADR-0019 §1, migration
  `00028_add_vm_applications.sql`) — operator-curated list of platform software
  entries per VM (`product`, `version`, `name`, `notes`, `added_at`,
  `added_by`). `product` is normalized server-side (trimmed, lower-cased,
  whitespace collapsed to hyphens) so `"Hashicorp Vault"` and `"vault"`
  deduplicate to the same key. The column is backed by a GIN
  `jsonb_path_ops` index for O(log n) `@>` containment queries; a functional
  index on `LOWER(name)` and a btree index on `image_id` are also added.
  `UpsertVirtualMachine` (the collector path) never touches `applications`;
  only `PATCH /v1/virtual-machines/{id}` does.
- **EOL enrichment for VM applications** (ADR-0019 §2) — the `internal/eol/`
  enricher gains a third pass, `enrichVirtualMachines`, that walks every
  non-terminated VM's `applications` list and writes `longue-vue.io/eol.<product>`
  annotations using the same endoflife.date lookup used for clusters and nodes.
  Products not on endoflife.date receive a stub annotation with
  `eol_status=unknown` so operators see the row was evaluated rather than
  silently dropped. Stale EOL annotations from removed applications are reaped
  automatically on the next enrichment tick.
- **Six new server-side filters on `GET /v1/virtual-machines`** (ADR-0019 §3)
  — `name` (case-insensitive substring on `name` / `display_name`), `image`
  (case-insensitive substring on `image_id` / `image_name`),
  `cloud_account_name` (resolves to UUID via an inner subquery on the UNIQUE
  index), `application` (JSONB containment on `applications[].product`,
  normalized server-side), `application_version` (narrows `application` to a
  specific version; ignored when `application` is absent), and `region` /
  `role` remain exact-match. All six AND with the existing filters and respect
  the `vm-collector` PAT account-binding restriction. LIKE metacharacters in
  `name` and `image` values are escaped before interpolation.
- **`GET /v1/virtual-machines/applications/distinct`** (ADR-0019 §3) —
  returns `{products: [{product, versions}]}` with up to 200 distinct
  normalized product names and, for each, the sorted list of distinct versions
  seen across non-terminated VMs. Requires `read` scope. Drives the cascading
  product → version dropdown in the VM list UI.
- **`applications` field on `PATCH /v1/virtual-machines/{id}`** (ADR-0019 §4)
  — accepts a `*[]VMApplication` with replace-not-merge semantics: the
  submitted list replaces the stored list in full. The handler diffs input
  against the existing list to preserve `added_at` / `added_by` for unchanged
  `(product, version, name)` tuples and stamps fresh values on new entries.
  Maximum 100 entries; per-field length caps enforced (product 64, version 64,
  name 200, notes 4096 characters).
- **VM Applications card on `/ui/virtual-machines/:id`** — read mode shows a
  table (product, version, name, notes, EOL status badge, latest available,
  added by, added at); edit mode flips to a per-row editor with add/remove
  buttons submitting the full list. Editor and admin see the Edit button;
  viewer and auditor see read-only.
- **Cascading filter on `/ui/virtual-machines`** — Application dropdown
  populates from `GET /v1/virtual-machines/applications/distinct`; selecting a
  product immediately narrows the App-version dropdown to the versions in
  inventory for that product.
- **Search and Clear buttons on the VM list filter bar** — replaces the
  previous debounced auto-apply. Filters now require explicit submission;
  Clear resets all inputs at once.

### Changed

- **`/ui/search/image` renamed "Search by image or application"** — the page
  now also surfaces platform VMs by image substring (`image_id` / `image_name`)
  and by exact normalized product (via `?application=<product>` on
  `GET /v1/virtual-machines`). The two result sets (K8s workloads/pods and
  platform VMs) are unioned by entity id and displayed in separate sections.
  The URL slug and browser title are updated; existing bookmarks using the old
  slug continue to work via a redirect.
- **EOL dashboard at `/ui/eol` now includes VMs as an entity dimension** —
  the aggregator reads `longue-vue.io/eol.*` annotations from `virtual_machines`
  alongside clusters and nodes. A new "Type" column (cluster / node / vm) and
  a corresponding filter chip appear in the summary card row. Row-level
  red/orange highlighting and the two-column-group layout ("What we run" /
  "What's available") are unchanged.
- **Filter layout on `/ui/virtual-machines` regrouped** — filters block sits
  above the VM search block, separated by a `border-top` divider.

## [0.11.2] — 2026-04-29

Charts-only release. `appVersion` stays at `0.11.1` — no binary changed —
but every longue-vue deployable now ships with a first-class Helm chart per
ADR-0018. Two new charts join the family: `charts/longue-vue-collector` (the
push-mode Kubernetes collector for air-gapped clusters) and
`charts/longue-vue-vm-collector` (the cloud-VM collector). The reference
Kustomize manifests under `deploy/` are demoted to "examples / first
contact" — Helm is now the supported deployment path for every binary.

### Added

- **`charts/longue-vue-collector`** — independent chart for the push-mode K8s
  collector (ADR-0009). One Helm release per source cluster. Surfaces
  `serverURL`, `clusterName`, operator-supplied `tokenSecret.existingSecret`,
  `kubeconfig.{mode=in-cluster|secret}`, polling cadence, mTLS-to-DMZ-gateway
  block, outbound proxy block, opt-in NetworkPolicy + PodDisruptionBudget,
  and the standard hardening defaults (UID 65532, `runAsNonRoot:true`,
  `readOnlyRootFilesystem`, drop ALL capabilities, seccomp `RuntimeDefault`).
  ClusterRole is genuinely minimal: `list` only, on the eleven resource
  types the collector polls.
- **`charts/longue-vue-vm-collector`** — independent chart for the cloud-VM
  collector (ADR-0015). One Helm release per cloud account. Surfaces the
  same operator-supplied PAT pattern, `account.{provider, name, region}`,
  the credential-refresh cadence, mTLS + proxy blocks, optional Service
  + ServiceMonitor for Prometheus scraping of `:9090/metrics`, and opt-in
  NetworkPolicy + PodDisruptionBudget. Creates no ClusterRole — the
  vm-collector never calls the Kubernetes API.
- **`docs/adr/adr-0018-helm-chart-per-deployable-binary.md`** — records
  the chart-per-binary policy: every deployable longue-vue binary ships with a
  Helm chart of its own, sibling to (not subchart of) `charts/longue-vue`.
  Independent chart versions, shared layout / labelling / hardening
  conventions copied from `charts/longue-vue-ingest-gw`.

### Security

- **`automountServiceAccountToken` is gated** in both new charts. The
  `longue-vue-vm-collector` pod hardcodes it to `false` (no K8s API access
  needed); `longue-vue-collector` ties it to `kubeconfig.mode == in-cluster`
  so the `kubeconfig.mode=secret` path doesn't gratuitously expose the
  projected SA token.
- **NetworkPolicy egress is scoped** when `networkPolicy.egressCIDRs` is
  set. Previously the unrestricted "any 443" rule sat alongside the
  CIDR-list rule, defeating the lockdown; now the 443 rule is suppressed
  when CIDRs are supplied and the egress is restricted to the listed
  ranges only.

### Migration

No DB migration. Operationally:

- Existing `deploy/collector/` and `deploy/vm-collector/` Kustomize
  manifests still work — they're now positioned as quick-start examples,
  not the supported production path. The Helm charts replace them as the
  recommended deployment surface and make air-gap, mTLS-to-DMZ-gateway,
  and one-release-per-cluster topologies first-class.
- Operators on Kustomize can migrate at their own pace: `helm template`
  the new chart, diff against the existing manifest set, and adopt
  release by release.

## [0.11.1] — 2026-04-29

Helm chart 0.14.0 / appVersion 0.11.1. Hardening release driven by the
2026-04-28 penetration test. Closes three P0 findings — plaintext-HTTP
credential transit (AUTH-VULN-01/02/03), forgeable `X-Forwarded-For`
rate-limit bypass (AUTH-VULN-04), and admin-account orphaning via the
admin-user lifecycle endpoints (AUTHZ-VULN-01/02). New ADR-0017 documents
the public-listener TLS posture and proxy-trust contract introduced here.

### Security

- **Native TLS termination on the public listener** (ADR-0017 §4) —
  longue-vue can now serve HTTPS directly when `LONGUE_VUE_PUBLIC_LISTEN_TLS_CERT`
  and `LONGUE_VUE_PUBLIC_LISTEN_TLS_KEY` are set. Cert + key are loaded at
  startup, hot-reloaded via fsnotify on file change (works with cert-manager,
  Vault Agent atomic-rename, manual file writes), and pinned to TLS 1.3 with
  session tickets disabled. Refuses to start on parse error rather than
  falling through to plain HTTP.
- **Trust-aware Secure cookie + HSTS + client IP resolution** (ADR-0017 §5)
  — `X-Forwarded-For` and `X-Forwarded-Proto` are honored only when the
  immediate TCP peer's IP falls inside `LONGUE_VUE_TRUSTED_PROXIES` (a
  comma-separated CIDR list). Empty list = ignore both headers entirely
  (the secure default). Fixes AUTH-VULN-04: a remote client sending
  `X-Forwarded-For: <victim-ip>` could previously bypass per-IP rate
  limits on `/v1/auth/login`. Fixes AUTH-VULN-02: the session cookie's
  `Secure` flag now reflects the resolved transport, not a forgeable
  XFP header. Fixes AUTH-VULN-03: HSTS is emitted only over a verified
  HTTPS request, with a force-emit override for operators declaring the
  full deployment HTTPS-only.
- **Startup posture guard** (ADR-0017 §7) — `LONGUE_VUE_REQUIRE_HTTPS=true`
  refuses to boot unless either native TLS is configured (cert + key
  present) or both `LONGUE_VUE_TRUSTED_PROXIES` is non-empty AND
  `LONGUE_VUE_SESSION_SECURE_COOKIE=always`. Fails closed: "warn and serve
  plain HTTP" is not an option once the operator has declared the
  deployment HTTPS-only.
- **Last-admin invariant guard on `DELETE /v1/admin/users/{id}` and
  `PATCH /v1/admin/users/{id}`** (AUTHZ-VULN-01/-02) — both endpoints
  now refuse with `409 Conflict` when the operation would leave the
  deployment with zero active admins. The check + write run in a single
  PostgreSQL transaction with `SELECT … FOR UPDATE` on the active-admin
  set, closing the TOCTOU race two concurrent demotions could otherwise
  exploit. New `Store.UpdateUserGuarded` and `Store.DeleteUserGuarded`
  methods plus a new `api.ErrLastAdmin` sentinel.

### Added

- **`internal/httputil`** — small package centralising the trust-aware
  client-IP / IsHTTPS helpers (`ParseTrustedCIDRs`, `ClientIP`, `IsHTTPS`).
  All previously-duplicated XFF parsing routes through it; downstream
  code is single-sourced.
- **Public-listener cert hot-reload** — `cmd/longue-vue/main.go:newCertReloader`
  reloads the public listener's cert + key on every TLS handshake when the
  on-disk file mtime advances (compatible with cert-manager rotations,
  Vault Agent atomic renames, and manual file writes). Sibling to the
  fsnotify-driven `internal/ingestgw/tls_reload.go` used by the DMZ
  gateway; the two listeners use mechanism-appropriate reload paths
  rather than a shared package.
- **Helm chart `longue-vue.tls` block** — `existingSecret` references a
  `kubernetes.io/tls` Secret; the chart mounts it at
  `/etc/longue-vue/tls` and wires `LONGUE_VUE_PUBLIC_LISTEN_TLS_CERT/_KEY`
  automatically.
- **Helm chart `longue-vue.trustedProxies` and `longue-vue.requireHTTPS`** —
  surface the new env vars so operators don't have to reach for
  `extraEnv:` overrides.
- **OpenAPI: `409 Conflict` on `PATCH /v1/admin/users/{id}`** —
  documents the last-admin guard so generated clients render the
  conflict correctly. The DELETE endpoint already declared 409.

### Changed

- **`api.AuthMiddleware` signature** — gains a `trustedProxies []*net.IPNet`
  parameter; callers must update. Pass nil to ignore proxy headers
  (the secure default).
- **`api.AuditMiddleware` signature** — gains a `trustedProxies []*net.IPNet`
  parameter so audit-event source IPs reflect the trust-aware client IP,
  not the immediate proxy peer.
- **`auth.Middleware` signature** — gains a `trustedProxies []*net.IPNet`
  parameter, threaded through to the cookie helpers
  (`SessionCookie`, `SetSessionCookie`, `ClearSessionCookie`).

### Migration

No DB migration. Two operational changes:

1. **If you run longue-vue behind a TLS-terminating reverse proxy** (the
   common pattern: ingress-nginx, Envoy, a cloud LB), set
   `LONGUE_VUE_TRUSTED_PROXIES` to the proxy's CIDR(s) and pin
   `LONGUE_VUE_SESSION_SECURE_COOKIE=always`. Without trust, the upgrade
   silently downgrades cookie security and rate-limit accuracy — the
   defaults are the safest possible state, not the most useful.

2. **If you want HSTS / require HTTPS**, set `LONGUE_VUE_REQUIRE_HTTPS=true`.
   The pod will refuse to start unless one of the two postures (native
   TLS or trusted-proxy + SecureAlways) is present — failing closed
   beats accidentally serving credentials over plain HTTP.

`charts/longue-vue` upgrades transparently with the existing values: the new
`longue-vue.tls`, `longue-vue.trustedProxies`, `longue-vue.requireHTTPS` keys all
default to empty / false, so existing releases are unaffected until the
operator opts in.

## [0.11.0] — 2026-04-28

Helm chart 0.13.0 / appVersion 0.11.0. Adds the DMZ ingest gateway track
from ADR-0016: longue-vue can now accept collector push traffic through a
hardened perimeter component (`longue-vue-ingest-gw`) without exposing longue-vue
to the internet. Also makes `POST /v1/clusters` idempotent on `name` so
the collector no longer needs a read-before-write at startup.

### Added

- **`longue-vue-ingest-gw` binary** (ADR-0016) — standalone stateless
  reverse-proxy for the DMZ. Exposes a TLS inbound listener (`:8443`,
  Envoy/WAF-fronted) and a health/metrics listener (`:9090`, pod-IP only,
  no TLS). No database, no queue, no replay buffer. Source under
  `cmd/longue-vue-ingest-gw/`; shared gateway logic under `internal/ingestgw/`.
  Built `CGO_ENABLED=0` from `Dockerfile.ingest-gw`, distroless base, UID
  65532.
- **Helm chart `longue-vue-ingest-gw`** (chart `0.1.0`) — first ship of the
  gateway chart. Ships independently of the umbrella `longue-vue` chart so the
  DMZ release contains only what belongs in the DMZ. Three TLS cert-source
  modes: `vault` (Vault Agent sidecar + PKI secrets engine, hot-rotate at
  50% TTL), `secret` (Kubernetes `kubernetes.io/tls` Secret, works with
  cert-manager), `file` (operator-mounted path, any tooling). Default 2
  replicas + PodDisruptionBudget. Optional ServiceMonitor + PrometheusRule
  (suggested alerts shipped as values block). NetworkPolicy restricts
  egress to longue-vue's ingest port only (plus Vault CIDRs in `vault` mode).
- **mTLS-only ingest listener on longue-vue** (ADR-0016 §3) — a second
  `*http.Server` starts when `LONGUE_VUE_INGEST_LISTEN_ADDR` is set (disabled
  when empty; existing deployments unaffected). The listener requires
  `RequireAndVerifyClientCert` (TLS 1.3 floor, session tickets disabled)
  and is wired by `api.NewIngestMux`, which registers exactly 19 routes:
  the 18 collector writes plus `POST /v1/auth/verify`. New env vars:
  `LONGUE_VUE_INGEST_LISTEN_ADDR`, `LONGUE_VUE_INGEST_LISTEN_TLS_CERT`,
  `LONGUE_VUE_INGEST_LISTEN_TLS_KEY`, `LONGUE_VUE_INGEST_LISTEN_CLIENT_CA_FILE`,
  `LONGUE_VUE_INGEST_LISTEN_CLIENT_CN_ALLOW`.
- **`POST /v1/auth/verify`** (ADR-0016 §5) — internal-only endpoint
  registered exclusively on the mTLS-only ingest listener (not on `:8080`).
  The gateway calls it to short-circuit invalid tokens before forwarding
  writes across the firewall. Response carries `valid`, `caller_id`,
  `kind`, `scopes`, `bound_cloud_account_id`. Rate-limited at longue-vue to
  100 req/s per source IP (burst 200) via the new `api.VerifyRateLimiter`.
  The `token` field in captured request bodies is scrubbed by the audit
  middleware.
- **`audit_events.source` column** (migration `00027_audit_events_source.sql`)
  — distinguishes `api` (public listener), `ingest_gw` (mTLS ingest
  listener, DMZ-origin writes), and `system` (synthetic longue-vue-emitted
  events). Empty strings in pre-ADR-0016 rows are treated as `api` for
  backwards compatibility. `AuditMiddleware` now accepts a `source` string
  argument; existing call sites pass `"api"`, the ingest-listener wiring
  passes `"ingest_gw"`. `GET /v1/admin/audit` accepts an optional `source`
  query parameter.
- **Gateway Prometheus metrics** — new metrics on the gateway's private
  registry: `longue_vue_ingest_gw_requests_total{method,route,status_class,outcome}`,
  `longue_vue_ingest_gw_request_duration_seconds`, `longue_vue_ingest_gw_upstream_duration_seconds`,
  `longue_vue_ingest_gw_token_verify_total{result}`,
  `longue_vue_ingest_gw_token_cache_total{event}`, `longue_vue_ingest_gw_token_cache_size`,
  `longue_vue_ingest_gw_cert_not_after_seconds`, `longue_vue_ingest_gw_cert_reload_total{result}`,
  `longue_vue_ingest_gw_body_bytes`, `longue_vue_ingest_gw_inflight_requests`,
  `longue_vue_ingest_gw_build_info`. longue-vue gains `longue_vue_auth_verify_total{result}`
  and `longue_vue_ingest_listener_client_cert_failures_total{reason}`.

### Changed

- **`POST /v1/clusters` is now idempotent on `name`** (ADR-0016 §6) —
  `api.Store.EnsureCluster` replaces `CreateCluster`. A new row returns
  `201 Created`; an existing row returns `200 OK` with the existing record
  (request body ignored on hit). The collector no longer issues a startup
  `GET /v1/clusters?name=…` — it unconditionally POSTs and follows up with
  PATCH regardless of the 200/201 response. One round-trip removed from
  every collector startup, for all deployments, whether or not the gateway
  is in play.
- **`api.NewServer` signature** — gains a `verifyLimiter *VerifyRateLimiter`
  parameter (pass nil to disable rate limiting, e.g. in test fixtures).
- **`api.AuditMiddleware` signature** — gains a `source string` parameter
  (`"api"` or `"ingest_gw"`); callers must update.

### Migration

Migration `00027_audit_events_source.sql` adds a nullable `source` column
to `audit_events`. The `LONGUE_VUE_AUTO_MIGRATE=true` default applies it on
startup. Rows inserted by the previous version carry a NULL source; queries
treat NULL as `"api"` for backwards compatibility.

`api.AuditMiddleware` now requires a `source` argument. Any code outside
`cmd/longue-vue/main.go` that constructs a middleware chain must be updated to
pass `"api"` or the appropriate source string.

`api.NewServer` requires the new `verifyLimiter` argument; pass
`api.NewVerifyRateLimiter()` in production and nil in tests.

No changes to existing API consumers. The new `200` status from
`POST /v1/clusters` is additive; clients treating `201` as the only
success code should be updated to also accept `200`, but the old behaviour
(checking for the row's existence first and then POSTing) still works
through the `PATCH` path.

## [0.10.0] — 2026-04-26

Helm chart 0.12.0 / appVersion 0.10.0. Adds the VM-collector track from
ADR-0015: longue-vue now inventories the non-Kubernetes platform VMs sitting
underneath the clusters (VPN, DNS, Bastion, Vault, …) per cloud account,
with encrypted-at-rest credentials and a separate push-mode collector
binary.

### Added

- **VM collector binary `longue-vue-vm-collector`** (ADR-0015 §1, §IMP-004) —
  standalone push-mode binary mirroring `longue-vue-collector`. Stateless,
  distroless, UID 65532, env-var configured. One binary instance per
  cloud account; multi-account = N deployments. Source under
  `cmd/longue-vue-vm-collector/`; reusable polling logic under
  `internal/vmcollector/`.
- **`cloud_accounts` table** (ADR-0015 §3) — operator-editable
  cloud-provider accounts with status workflow
  (`pending_credentials` → `active` → `error` / `disabled`),
  encrypted SK column, and curated metadata. Migration
  `00023_create_cloud_accounts.sql`.
- **`virtual_machines` table** (ADR-0015 §2) — top-level table for
  non-Kubernetes platform VMs. FK to `cloud_accounts(id)`
  `ON DELETE CASCADE`. Captures the rich Outscale payload (image AMI,
  keypair, VPC/subnet, NICs, SGs, block devices, deletion protection,
  provider creation date) plus the curated-metadata five-tuple. Soft
  delete via `terminated_at` so the audit history of decommissioned
  VMs is preserved. Migration `00024_create_virtual_machines.sql`.
- **`vm-collector` token scope** (ADR-0015 §5 / IMP-008) — narrowest
  scope in the system. Grants exactly: fetch own credentials, register
  placeholder cloud account, heartbeat status updates, upsert VMs,
  reconcile VMs. PATs are bound to a single `cloud_account_id` at
  issuance via the new `tokens.bound_cloud_account_id` column
  (migration `00025_add_token_bound_cloud_account.sql`); enforced by
  `auth.Caller.EnforceCloudAccountBinding`. The token-issuance UI
  requires picking a bound cloud account when the `vm-collector`
  preset is selected.
- **Master-key envelope encryption** (ADR-0015 §4 / IMP-002) — new
  package `internal/secrets/`. AES-256-GCM with the master key from
  `LONGUE_VUE_SECRETS_MASTER_KEY` (base64-encoded 32 bytes; rejected at
  startup on any other length). AAD bound to the row UUID so a
  database backup-restore cannot move a ciphertext between rows.
  Master-key fingerprint (first 8 hex chars of SHA-256) logged at
  startup. Master key is required only when at least one
  `cloud_accounts` row carries a non-NULL `secret_key_encrypted`.
- **Outscale provider** (ADR-0015 §IMP-003) — first cloud-provider
  implementation behind the new `internal/vmcollector/provider.Provider`
  seam. Wraps `github.com/outscale/osc-sdk-go/v2`, maps `osc.Vm` into
  the canonical `VM` struct, hardcodes `ansible_group` as the role-tag
  key, parses TINA-family instance types into CPU/memory, normalises
  Outscale's `shutting-down` to the AWS-spelling `terminating` so
  `power_state` carries one vocabulary across providers.
- **Tag-driven kube-node dedup** (ADR-0015 §8) — server-side check on
  `POST /v1/virtual-machines`: looks up `nodes.provider_id LIKE '%' ||
  $1 || '%'` against the posted `provider_vm_id` (Outscale CCM stamps
  the `VmId` substring into `node.spec.providerID`). 409
  `already_inventoried_as_kubernetes_node` on hit; the collector
  skips. Tag-independent — works for any cloud-controller-manager.
  Local pre-filter on `OscK8sClusterID/*` / `OscK8sNodeName=*` /
  `longue-vue.io/ignore=true` saves the round-trip per kube worker.
- **New endpoints** (ADR-0015 §IMP-006):
  - `POST /v1/admin/cloud-accounts`,
    `GET /v1/admin/cloud-accounts`,
    `GET /v1/admin/cloud-accounts/{id}`,
    `PATCH /v1/admin/cloud-accounts/{id}`,
    `PATCH /v1/admin/cloud-accounts/{id}/credentials`,
    `POST /v1/admin/cloud-accounts/{id}/disable` and `/enable`,
    `DELETE /v1/admin/cloud-accounts/{id}` — all admin scope.
  - `GET /v1/cloud-accounts/by-name/{name}/credentials` and
    `GET /v1/cloud-accounts/{id}/credentials` — `vm-collector` scope,
    the only places plaintext SK leaves the database.
  - `POST /v1/cloud-accounts` — `vm-collector` scope, idempotent
    first-contact registration.
  - `PATCH /v1/cloud-accounts/{id}/status` — `vm-collector` scope,
    heartbeat-only.
  - `POST /v1/virtual-machines`,
    `POST /v1/virtual-machines/reconcile` — `vm-collector` scope.
  - `GET /v1/virtual-machines`, `GET /v1/virtual-machines/{id}` —
    `read` scope.
  - `PATCH /v1/virtual-machines/{id}` — `write` scope, curated
    metadata only.
  - `DELETE /v1/virtual-machines/{id}` — `delete` scope, soft delete.
- **Soft-delete reconciliation** (ADR-0015 §9) —
  `POST /v1/virtual-machines/reconcile` flips `terminated_at = NOW()`
  + `power_state = 'terminated'` + `ready = false` for rows whose
  `provider_vm_id` is not in the keep list. Rows are never
  hard-deleted by reconciliation. A reappearing
  `(cloud_account_id, provider_vm_id)` is resurrected by clearing
  `terminated_at`.
- **Hybrid onboarding flow** (ADR-0015 §6) — operator deploys the
  collector with only a PAT and an account name; the collector posts
  a placeholder row to `POST /v1/cloud-accounts`, the admin sees a
  red "pending credentials" banner in the UI, pastes AK/SK, the
  collector picks them up on the next refresh tick. Hot AK/SK
  rotation works the same way: PATCH `/credentials`, collector picks
  up the new SK within `LONGUE_VUE_VM_COLLECTOR_CREDENTIAL_REFRESH`
  (default 1 h).
- **Virtual Machines and Cloud Accounts UI pages** (ADR-0015 §10) —
  `/ui/virtual-machines` list + detail (mirrors Node detail layout
  via cards extracted into `ui/src/components/inventory/`),
  `/ui/admin/cloud-accounts` list with status badges and "Set
  credentials" / "Rotate credentials" / "Disable" forms, "Issue
  collector token" button pre-binding the new PAT to the cloud
  account. Sidebar gains a new "Virtual Machines" entry with a
  distinct server/tower SVG icon. Home-page admin banner surfaces
  the count of `pending_credentials` accounts.
- **Prometheus metrics**:
  - longue-vue: `longue_vue_cloud_accounts_total{status}`,
    `longue_vue_cloud_accounts_pending_credentials` (gauge for alerting),
    `longue_vue_virtual_machines_total{cloud_account, terminated}`,
    `longue_vue_credentials_reads_total{cloud_account}`.
  - collector binary: `longue_vue_vm_collector_ticks_total{status}`,
    `longue_vue_vm_collector_tick_duration_seconds`,
    `longue_vue_vm_collector_vms_observed`,
    `longue_vue_vm_collector_vms_skipped_kubernetes_total`,
    `longue_vue_vm_collector_credential_refreshes_total{result}`,
    `longue_vue_vm_collector_last_success_timestamp_seconds`,
    `longue_vue_vm_collector_build_info{version}`. Exposed on a private
    registry on a localhost-only `/metrics` listener.

### Changed

- **`internal/auth.HasScope` no longer treats admin scope as implying
  `vm-collector`** (ADR-0015 §5). Only collector tokens carrying the
  scope explicitly can read plaintext SK; admin tokens can manage
  cloud-account metadata via the admin endpoints but cannot exercise
  the credentials-fetch endpoint. Preserves the
  SK-is-write-only-from-admin-endpoints guarantee.
- **Sidebar reorganised into Kubernetes / Cloud Infrastructure /
  Tools sections** so the new Virtual Machines entry sits alongside
  the cloud-account admin link without crowding the existing
  Kubernetes inventory drill-down.
- **Audit middleware now wraps the hand-written cloud-accounts and
  VM routes** (ADR-0015 §IMP-007). These hand-written routes
  previously bypassed `AuditMiddleware` (a security gap); they now
  produce audit rows for every write, plus every credentials-fetch
  GET (the response body is intentionally never logged, even at
  debug level). The scrubber list gains `secret_key` and
  `access_key`.

### Security

- **Encrypted-at-rest cloud-provider AK/SK** — secret keys live as
  AES-256-GCM ciphertexts AAD-bound to their row UUID; database
  backup-restore alone cannot move a ciphertext to another row.
  Master key required only when at least one row carries a non-NULL
  `secret_key_encrypted`; longue-vue refuses to start otherwise.
- **vm-collector PATs bound to a single cloud account** — a leaked
  collector PAT exposes exactly one account's credentials and one
  account's VM writes. Strictly less than a `read`-scope PAT
  (which can list every entity in the CMDB).
- **LIKE-wildcard escaping on the dedup query** — `_` and `%` inside
  `provider_vm_id` are escaped before interpolation into the
  `nodes.provider_id LIKE '%' || $1 || '%'` lookup, so a maliciously
  named VM cannot match every node row.

### Upgrading

Migrations `00023` / `00024` / `00025` are additive (the schema for
new tables, plus a nullable column on `tokens`); the
`LONGUE_VUE_AUTO_MIGRATE=true` default applies them on startup. Existing
deployments without any `cloud_accounts` row do not need
`LONGUE_VUE_SECRETS_MASTER_KEY`; longue-vue only refuses to start when the
table contains an encrypted SK and the env var is unset.

The Helm chart bumps to `0.12.0` / appVersion `0.10.0`. The new
`secrets.masterKey` value (delivered via a Kubernetes Secret, never
in `values.yaml`) is required only if you intend to register a cloud
account.

## [0.7.0] — 2026-04-24

### Added

- **Impact analysis graph** (ADR-0013) — server-side dependency graph
  traversal from any CMDB entity. New endpoint
  `GET /v1/impact/{entity_type}/{id}?depth=2` walks FK relationships
  bidirectionally across all 9 entity types with 4 relation types
  (`contains`, `owns`, `hosts`, `binds`). Depth-limited to 1–3 hops.
  Interactive SVG diagram on every entity detail page with depth selector
  and click-to-navigate. Prometheus metrics:
  `longue_vue_impact_queries_total`, `longue_vue_impact_query_duration_seconds`.

- **MCP server** (ADR-0014) — Model Context Protocol server exposing 17
  read-only CMDB tools for AI agents. SSE and stdio transports. Bearer
  token auth. Admin toggle at Admin > Settings. Prometheus metrics.

- **UI redesign — design system alignment** — CSS migrated to canonical
  design system tokens (~50 CSS variables). Space Grotesk (headings/body)
  and JetBrains Mono (code) webfonts installed via Google Fonts. 11 SVG
  entity icons added to sidebar navigation, list page headings, detail
  page headings, EOL dashboard, and login page.

- **Sidebar navigation** — top nav bar replaced with a left sidebar.
  Entity links (Clusters, Namespaces, Nodes, etc.) are always visible
  with icons. Burger button collapses the sidebar to icons-only (48px).
  Active link highlighted with cyan accent + left border. Top header
  bar retained for app title, username, role pill, and sign out.

### Changed

- **EOL Inventory redesign** — the EOL Dashboard is renamed to
  "End-of-Life Inventory". Table columns are grouped into "what we run"
  (Status, Product, Version, Patch, Entity, Cluster) and "what's
  available" (Latest Available, EOL Date, Checked) with a visual
  separator. Rows are highlighted red for EOL, orange for approaching
  EOL. Column renames: "Cycle" → Version, "Cycle Latest" → Patch.

- **`latest_available` field in EOL annotations** — the enricher now
  stores the newest version of the product published on endoflife.date
  (e.g. `1.32.3` when the entity runs `1.28`). Zero additional API
  calls — the data is already fetched.

### Fixed

- **Workload detail missing pods on large clusters** — the WorkloadDetail
  page fetched all pods cluster-wide (`limit=200`) and filtered
  client-side by `workload_id`. On clusters with 500+ pods,
  StatefulSet pods (long-lived, less recently updated) fell outside the
  first pagination page and were never displayed. Fixed by adding a
  server-side `workload_id` query parameter to `GET /v1/pods` so the
  API returns only the matching pods.

- **Pod pages showing UUIDs instead of names** — the Pods list page
  rendered workload as a truncated UUID; the Pod detail page rendered
  both namespace and workload as UUIDs. Both now resolve and display
  human-readable names with links.

### Security

- **HTTP security headers** — new middleware sets `Content-Security-Policy`,
  `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
  `Referrer-Policy`, and `Strict-Transport-Security` (HSTS, conditional
  on TLS) on every response.

- **Login rate limiting** (ADR-0007 IMP-009) — per-IP sliding window
  rate limiter on `POST /v1/auth/login`: 5 requests/minute, burst 5.
  Returns 429 when exceeded. Idle IPs evicted after 30 minutes.

- **golang.org/x/net upgrade** — v0.50.0 → v0.51.0 fixes GO-2026-4559
  (HTTP/2 server panic via crafted frames).

- **Request body size limit** — all POST/PATCH/PUT bodies are capped at
  1 MiB via `http.MaxBytesHandler`. Returns 413 when exceeded.

- **HTTP server timeouts** — `ReadTimeout: 30s`, `WriteTimeout: 60s`,
  `IdleTimeout: 120s` prevent slowloris-style connection exhaustion.

- **Error message sanitization** — `ResponseErrorHandlerFunc` and
  settings handlers no longer leak internal error details (database
  messages, constraint names) to clients. Errors are logged server-side
  and a generic message is returned.

- **Impact graph traversal cap** — graph nodes capped at 500 per query
  to prevent resource exhaustion on large clusters. Response includes
  `truncated: true` when the cap is hit.

- **Reconcile endpoints require `delete` scope** — all 8 reconcile
  endpoints (`POST /v1/{resource}/reconcile`) now require the `delete`
  scope instead of `write`. Prevents editors from mass-deleting
  resources via empty `keep_names`.

### Upgrading

The `workload_id` query parameter on `GET /v1/pods` and the impact
endpoint are additive. EOL annotations are updated with `latest_available`
on the next enrichment tick.

**Breaking change:** reconcile endpoints now require `delete` scope.
Existing push collector tokens with only `write` scope must be re-issued
with `write` + `delete` scopes.

## [0.1.1] — 2026-04-20

Patch release on top of `v0.1.0` "Canopus". Adds the first two steps of
the ADR-0008 asset-management rollout (curated metadata on Namespace
and Node, including `hardware_model`) and fixes three UUID-instead-of-
name rendering bugs on detail pages. Schema is additive only; `v0.1.0`
→ `v0.1.1` is a straight `LONGUE_VUE_AUTO_MIGRATE=true` bump, no data
migration required.

### Added

- **Curated metadata on Namespace**
  ([#56](https://github.com/sthalbert/longue-vue/pull/56)) — `owner` /
  `criticality` / `notes` / `runbook_url` / `annotations` (JSONB)
  columns editable at `/ui/namespaces/:id` by editor / admin. The
  collector's `UpsertNamespace` leaves these columns alone on conflict
  so per-tick upserts can't clobber operator edits.
- **Curated metadata on Node + `hardware_model`**
  ([#57](https://github.com/sthalbert/longue-vue/pull/57)) — same five
  curated columns on nodes plus a free-form `hardware_model` field for
  bare-metal installs to record a server model alongside the cloud-shaped
  `instance_type` populated by the collector. Closes the SNC §8.1.a
  "model" requirement for on-prem deployments. Editable at
  `/ui/nodes/:id`. `UpsertNode`'s `DO UPDATE SET` clause is explicit
  about which columns the collector owns; the new columns are absent
  from it by design.
- **ADR-0008** ([#55](https://github.com/sthalbert/longue-vue/pull/55)) —
  SecNumCloud v3.2 chapter 8 coverage. Maps every §8.1 sub-clause to a
  concrete longue-vue column or explicit cross-reference (licenses →
  Dependency-Track via `containers[].image`; §8.2 and §8.5 are
  procedural / out of system scope). DICT
  (disponibilité / intégrité / confidentialité / traçabilité) classification
  will land on Namespace + Workload in a later release at the Application
  abstraction.

### Fixed

- **Detail pages resolve parent names instead of UUIDs**
  ([#58](https://github.com/sthalbert/longue-vue/pull/58)) —
  `/ui/namespaces/:id` previously had no Cluster row at all;
  `/ui/nodes/:id` showed the cluster id as a truncated UUID;
  `/ui/workloads/:id` did the same for namespace. Each page now
  resolves the parent and renders a `<Link>` with its name. The
  Workload breadcrumb also gains cluster + namespace hops so the
  drill-down trail reads *"Workloads / <cluster> / <namespace> / this
  workload"* instead of dead-ending.
- **Namespace pods table shows workload name**
  ([#59](https://github.com/sthalbert/longue-vue/pull/59)) — the Workload
  column in `/ui/namespaces/:id`'s pods table rendered each pod's
  `workload_id` as a UUID link. Now renders the workload's name and
  kind (`web-frontend · Deployment`) by resolving against the
  in-scope workloads fetch — no extra network call.

### Schema migrations

- `00019_namespace_curated_metadata.sql` — adds 5 columns on
  `namespaces`.
- `00020_node_curated_metadata.sql` — adds 6 columns (5 curated +
  `hardware_model`) on `nodes`.

Both are additive; existing rows get NULL for the new columns and the
JSONB defaults to `{}`. No data rewrite, no downtime.

### Upgrading

```bash
# From v0.1.0. Keep your existing LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD — the
# bootstrap only fires when no admin exists, so it's a no-op here.
make build VERSION=0.1.1
# Point at the same DSN as v0.1.0; LONGUE_VUE_AUTO_MIGRATE=true (default)
# applies 00019 + 00020 on startup.
./bin/longue-vue
```

No client-side break: new columns show up as `null` on existing rows
and the UI renders an "Edit" placeholder until an editor fills them in.

## [0.1.0] — 2026-04-19 — "Canopus"

First tagged release. longue-vue is a Kubernetes-aware CMDB aligned with the
ANSSI **SecNumCloud (SNC)** qualification framework. Named after the
principal star of the old *Argo Navis* constellation — a classical
navigation marker.

### Highlights

- Multi-cluster polling collector mirrors a full Kubernetes inventory
  (nodes, namespaces, pods, workloads, services, ingresses, PVs, PVCs)
  into PostgreSQL and reconciles rows that disappear from the live
  listing.
- REST API is OpenAPI 3.1 contract-first with RFC 7807 errors, cursor
  pagination, and merge-patch updates.
- Dual-path authentication: humans log in with local password **or**
  OIDC (authorization-code flow with PKCE + nonce + state); machines
  carry admin-minted bearer tokens (argon2id-hashed, prefix-indexed).
- Four fixed roles — `admin` / `editor` / `auditor` / `viewer` — wired
  through the existing scope checks.
- React/TypeScript SPA embedded in the binary; admin panel, audit log
  viewer, component search, and inline cluster-metadata editor.
- Append-only audit log captures every write + every admin-panel read.
- Prometheus `/metrics` exposes per-cluster collector + HTTP counters.

### Architecture — ADRs

- **ADR-0001** — CMDB for SNC using the Kubernetes API as source of truth.
- **ADR-0002** — Mapping Kubernetes kinds onto the ANSSI cartography layers.
- **ADR-0003** — Workload polymorphism (Deployment / StatefulSet / DaemonSet
  on one table, discriminated by `kind`).
- **ADR-0004** — Ingress layer classification.
- **ADR-0005** — Multi-cluster collector topology
  (`LONGUE_VUE_COLLECTOR_CLUSTERS`).
- **ADR-0006** — Web UI bundled into longue-vue; curated-metadata columns.
- **ADR-0007** — Auth & RBAC (sessions + OIDC + bearer tokens).

### API & data model

- Nine resource kinds: **Cluster**, **Namespace**, **Node**, **Pod**,
  **Workload** (poly over Deployment / StatefulSet / DaemonSet),
  **Service**, **Ingress**, **PersistentVolume**,
  **PersistentVolumeClaim**. All carry a `layer` field derived from
  ADR-0002.
- FK chain `clusters → namespaces/nodes/persistent_volumes → pods /
  workloads / services / ingresses / persistent_volume_claims`, all
  `ON DELETE CASCADE`. Pods also carry a nullable `workload_id` FK to
  their top-level controller (`ON DELETE SET NULL`); PVCs carry a
  nullable `bound_volume_id` FK (same semantics).
- Pods and Workloads include a `containers` JSONB column for SBOM /
  CVE workflows. Nodes carry an enriched field set (role, cloud
  identity, OS stack, capacity + allocatable, conditions, taints).
  Services and Ingresses carry a `load_balancer` JSONB column so
  on-prem VIPs (MetalLB, Kube-VIP, hardware LBs) surface alongside
  cloud-provisioned ones.
- Filter endpoints for incident response:
  - `GET /v1/workloads?image=…` and `GET /v1/pods?image=…` —
    case-insensitive substring match over every container image.
  - `GET /v1/pods?node_name=…` — powers the "if this node dies, which
    pods are lost?" view.
- Merge-patch `PATCH /v1/clusters/{id}` supports **display_name,
  environment, provider, region, api_endpoint, labels, owner,
  criticality, notes, runbook_url, annotations**. Collector writes
  only `kubernetes_version`, so operator annotations are preserved
  across polls.

### Collector

- Polling-based; each tick refreshes the API-server version and lists
  every catalogued kind cluster-wide. Default 5 minute interval,
  configurable.
- Reconciliation (`LONGUE_VUE_COLLECTOR_RECONCILE=true`, default on) deletes
  rows that disappear from the live listing so the CMDB mirrors
  ground truth — required for ANSSI cartography fidelity. Runs only
  after a successful list so a transient Kubernetes error never wipes
  the store.
- Multi-cluster via `LONGUE_VUE_COLLECTOR_CLUSTERS` (JSON array of
  `{name, kubeconfig}` tuples); legacy single-cluster env vars still
  work. Empty kubeconfig falls back to in-cluster config.
- Exposes Prometheus counters and last-poll gauges per
  `(cluster, resource)`.

### Auth (ADR-0007)

- **Local login**: `POST /v1/auth/login` → server-side session cookie
  (HttpOnly, SameSite=Strict, 8h sliding).
- **OIDC**: `GET /v1/auth/oidc/authorize` → IdP →
  `GET /v1/auth/oidc/callback`; authorization-code flow with PKCE
  (S256) + nonce + state. Shadow users keyed on `(issuer, sub)`;
  first-login role is `viewer` — admins promote manually (authorization
  is never claim-driven).
- **Machine tokens**: `Authorization: Bearer longue_vue_pat_<prefix>_<suffix>`;
  argon2id-hashed at rest, 8-char prefix for O(1) lookup, plaintext
  shown once at creation (GitHub-PAT pattern). Minted in the admin UI.
- **First-run bootstrap**: creates a single `admin` user when none
  exists; password comes from `LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD` or a
  random 16-char string printed once to the startup log. Forced
  rotation on first login.
- Role → scope mapping: `admin` carries everything, `editor` =
  `read + write`, `auditor` = `read + audit`, `viewer` = `read`.

### Audit log

- `audit_events` table is append-only; the audit middleware observes
  every state-changing call plus every `/v1/admin/*` read and records
  actor + verb + target + status + source IP.
- Password / token / OIDC client-secret fields are scrubbed before
  the row is written.
- `GET /v1/admin/audit` with filters for actor, resource, action, and
  time window. `audit` scope (admin or auditor).

### Web UI (ADR-0006)

- Embedded React/TypeScript SPA at `/ui/` (build-tag `noui` disables
  the embed for backend-only builds).
- List and detail pages for every resource kind; **Cluster →
  Namespace → Workload → Pod** and **Cluster → Node** drill-downs.
- Node detail renders the full enriched picture: identity, OS &
  runtime, networking, resources (capacity vs allocatable), conditions
  with per-row health colouring, taints, labels — plus an
  impact-analysis callout of affected pods grouped by workload.
- Ingress detail surfaces the load-balancer block first, then rules
  and TLS.
- Component search at `/ui/search/image` with URL-persisted query.
- Admin panel at `/ui/admin/`: Users / Machine tokens / Active
  sessions / Audit. Auditors see only the Audit tab; admins see all.
- Inline "Ownership & context" editor on `/ui/clusters/:id` for
  editors and admins — edits environment, provider, region, labels,
  owner, criticality, runbook URL, notes, and annotations without the
  collector clobbering them.

### Operations

- Dockerfile: three-stage build (Node UI builder → Go builder →
  distroless runtime), static binary, runs as UID 65532.
- `deploy/` ships reference Kustomize manifests for running longue-vue in
  a Kubernetes cluster cataloguing itself via in-cluster
  ServiceAccount (`list` on every catalogued kind). Multi-cluster
  variant documented in `deploy/README.md`.
- `/metrics` mounted unauthenticated on the main mux (Prometheus
  scrape convention): HTTP request / duration counters, collector
  upsert / reconcile / error counters, per-`(cluster, resource)`
  last-poll gauges, `longue_vue_build_info`.
- CI (GitHub Actions): conventional-commit title check, `go vet` /
  `go build` / `go test -race` against a Postgres service container,
  `golangci-lint`, UI `npm run build` + typecheck, Docker image
  build verification.

### Known limitations

- REST + DB contracts may change incompatibly before `v1.0.0`. Pin to
  this tag for stability.
- Only the **Cluster** kind carries curated-metadata columns in
  `v0.1.0`; the same pattern will land on namespaces / nodes /
  workloads in a follow-up.
- No snapshots / time-travel yet (longer-horizon roadmap item from
  ADR-0001).
- No built-in MFA on the local password path; customers who need it
  should federate through their OIDC provider (which already gives
  every longue-vue instance MFA as a side-effect).

[0.1.1]: https://github.com/sthalbert/longue-vue/releases/tag/v0.1.1
[0.1.0]: https://github.com/sthalbert/longue-vue/releases/tag/v0.1.0
