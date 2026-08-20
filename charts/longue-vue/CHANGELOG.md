# Changelog

## [0.27.0](https://github.com/sthalbert/Longue-Vue/compare/chart-longue-vue-v0.26.1...chart-longue-vue-v0.27.0) (2026-08-19)


### Features

* **kyverno:** add Kyverno policy inventory and Policies view (ADR-0043) ([67c9f9e](https://github.com/sthalbert/Longue-Vue/commit/67c9f9e8a44a651f002209caa605d1f1c53b2afb))
* **kyverno:** add Kyverno policy inventory and Policies view (ADR-0043) ([23bf66e](https://github.com/sthalbert/Longue-Vue/commit/23bf66ee24fba2376cb7b0a102efb80cb5baff08))

## [0.26.1](https://github.com/sthalbert/Longue-Vue/compare/chart-longue-vue-v0.26.0...chart-longue-vue-v0.26.1) (2026-08-17)


### Bug Fixes

* **build:** rebuild binaries with Go 1.26.6 stdlib security fixes ([457c200](https://github.com/sthalbert/Longue-Vue/commit/457c20012c3be7a4f58ba8c7f5be311f0a8793da))
* **build:** rebuild binaries with Go 1.26.6 stdlib security fixes ([0ba51a1](https://github.com/sthalbert/Longue-Vue/commit/0ba51a1de8f18fff4816873a2d3a8fad7eec182a))

## [0.26.0](https://github.com/sthalbert/Longue-Vue/compare/chart-longue-vue-v0.25.0...chart-longue-vue-v0.26.0) (2026-06-10)


### Features

* **chart:** add Gateway API HTTPRoute exposure template ([34571a9](https://github.com/sthalbert/Longue-Vue/commit/34571a9897ba4ea9a3b2a95a844d83ac89bb354c))
* **chart:** add optional route-level Envoy Gateway SecurityPolicy ([c6f7a32](https://github.com/sthalbert/Longue-Vue/commit/c6f7a32b4c2c37157dac48704fb28c58cc48e951))
* **chart:** deprecate Ingress, surface HTTPRoute URL in NOTES ([ebb0bf4](https://github.com/sthalbert/Longue-Vue/commit/ebb0bf40c45df4bda4b956d5150b9f80141de124))
* **chart:** expose longue-vue via Gateway API (HTTPRoute + Envoy Gateway SecurityPolicy) ([24b6641](https://github.com/sthalbert/Longue-Vue/commit/24b664159731dbad0e564e883ee780671dbd807e))


### Bug Fixes

* **chart:** guard HTTPRoute parentRefs and require gateway name ([543514a](https://github.com/sthalbert/Longue-Vue/commit/543514aff5e6a04274d3ac8c86fcf05d78423db6))
* **chart:** make HTTPRoute/SecurityPolicy guards fire and use targetRefs ([834eb7a](https://github.com/sthalbert/Longue-Vue/commit/834eb7a620997f8142318240725b0c0fdd52539e))

## [0.25.0](https://github.com/sthalbert/Longue-Vue/compare/chart-longue-vue-v0.24.0...chart-longue-vue-v0.25.0) (2026-06-07)


### Features

* **chart:** sample flow-matrix PrometheusRule (disabled by default) ([84b7405](https://github.com/sthalbert/Longue-Vue/commit/84b74052f9d4090c40b5728df42087b82ab6bf93))
* cluster flow matrix — perimeter SG + internal NetPol synthesis vs declared reference ([40ad8b3](https://github.com/sthalbert/Longue-Vue/commit/40ad8b34abb82592247883a77bafd1cab8ab7af2))

## [0.24.0](https://github.com/sthalbert/Longue-Vue/compare/chart-longue-vue-v0.23.0...chart-longue-vue-v0.24.0) (2026-06-05)


### Features

* **flow-matrix:** phase 1 inventory — netpols + canonical SG model ([d7cfa0b](https://github.com/sthalbert/Longue-Vue/commit/d7cfa0bd365d81b4e30261afd4660eb0e9e2c9fe))

## [0.23.0](https://github.com/sthalbert/Longue-Vue/compare/chart-longue-vue-v0.22.0...chart-longue-vue-v0.23.0) (2026-06-02)


### Features

* **api:** create handler with validation for image origin mappings ([94bcf62](https://github.com/sthalbert/Longue-Vue/commit/94bcf62ddaaf56356ccf83e9d8ae5b41c0b8960f))
* **api:** make ingest verify rate limit configurable via env ([811db87](https://github.com/sthalbert/Longue-Vue/commit/811db87d040cef5b89b2d30f4cbae3de185e3ba7))
* **api:** patch and Delete for image origin mappings ([e9a8155](https://github.com/sthalbert/Longue-Vue/commit/e9a8155ed2c33f7e351ecd2c77318e1f960014eb))
* **auth:** per-account login lockout + admin rescue ([48580fc](https://github.com/sthalbert/Longue-Vue/commit/48580fc5be9dc1f6bb50e9e2c637d3ea3313d67a))
* **chart:** expose adminRescuePassword on longue-vue chart ([204d7cd](https://github.com/sthalbert/Longue-Vue/commit/204d7cdbe78a095ad092ccf9674d32f6f0445144))
* **chart:** expose mcp.allowPlaintext for local/dev clusters without TLS ([6fd05c9](https://github.com/sthalbert/Longue-Vue/commit/6fd05c93876bc694bc733e5c483bb1b86b022c94))
* **collector:** make client-go QPS/Burst configurable ([35a9eaa](https://github.com/sthalbert/Longue-Vue/commit/35a9eaa70954a2a923ec774eec7c5740c3d3d15a))
* container image versions enrichment (ADR-0022) ([8f5ddaf](https://github.com/sthalbert/Longue-Vue/commit/8f5ddafcad8dff20de1091c65a0616cb1cf64300))
* make ingest verify and collector kube rate limits configurable ([b1e489b](https://github.com/sthalbert/Longue-Vue/commit/b1e489b464acca793aa6e732eb3b79cce8923fd8))
* **store:** implement Create and Get for image origin mappings ([fc35177](https://github.com/sthalbert/Longue-Vue/commit/fc35177018a21f8beb1b024e9b0fffef707cae81))
* **store:** implement paginated List for image origin mappings ([31994cb](https://github.com/sthalbert/Longue-Vue/commit/31994cbaba0cf0717ddc2c5c09de894e14258ec5))
* **store:** implement Patch and Delete for image origin mappings ([fcb618d](https://github.com/sthalbert/Longue-Vue/commit/fcb618dea86e72fa29749454239ee37cb56f1940))
* **ui:** paginate NamespaceDetail sub-sections ([e24016d](https://github.com/sthalbert/Longue-Vue/commit/e24016d9f038c04956270f78526af8e1416f9154))
* **ui:** paginate WorkloadDetail and NodeDetail pod sections ([41d3e29](https://github.com/sthalbert/Longue-Vue/commit/41d3e29dbd34ee37d184115291b41ed9b417b495))


### Bug Fixes

* **security:** clear initial security-scan baseline ([31bb496](https://github.com/sthalbert/Longue-Vue/commit/31bb4967b90dbdec68352b2c7456baf03521b33f))

## [0.22.0](https://github.com/sthalbert/Longue-Vue/compare/chart-longue-vue-v0.21.0...chart-longue-vue-v0.22.0) (2026-05-28)


### Features

* **api:** make ingest verify rate limit configurable via env ([017762e](https://github.com/sthalbert/Longue-Vue/commit/017762e4eb92785c2f76fb1375957aeb73055b9c))
* **collector:** make client-go QPS/Burst configurable ([e208276](https://github.com/sthalbert/Longue-Vue/commit/e2082761f0b6a07d6d2c5a16ddafabc28ea9434f))
* make ingest verify and collector kube rate limits configurable ([fc5ee8c](https://github.com/sthalbert/Longue-Vue/commit/fc5ee8cabcaa87c2c0455b94a9d6f873f1f0ca48))

## [0.21.0](https://github.com/sthalbert/Longue-Vue/compare/chart-longue-vue-v0.20.0...chart-longue-vue-v0.21.0) (2026-05-27)


### Features

* **api:** create handler with validation for image origin mappings ([b711699](https://github.com/sthalbert/Longue-Vue/commit/b7116991766d9598084d991bf8aed7a725956057))
* **api:** patch and Delete for image origin mappings ([565cfae](https://github.com/sthalbert/Longue-Vue/commit/565cfae1e4191f1418cbe1cec33b9d024ec2e364))
* **store:** implement Create and Get for image origin mappings ([c7849f7](https://github.com/sthalbert/Longue-Vue/commit/c7849f7dc867a3d39a4266f68e0b12eddea9c65e))
* **store:** implement paginated List for image origin mappings ([bdfb899](https://github.com/sthalbert/Longue-Vue/commit/bdfb899844fad4fcfa4f16a61e2914fae8c9af71))
* **store:** implement Patch and Delete for image origin mappings ([1fea8ad](https://github.com/sthalbert/Longue-Vue/commit/1fea8ad1130628ee084bb7bbee8d3813c5f20314))

## [0.20.0](https://github.com/sthalbert/Longue-Vue/compare/chart-longue-vue-v0.19.0...chart-longue-vue-v0.20.0) (2026-05-26)


### Features

* **ui:** paginate NamespaceDetail sub-sections ([1153034](https://github.com/sthalbert/Longue-Vue/commit/11530343035d67ea0a2cb7c9d5743b9431d2abde))
* **ui:** paginate WorkloadDetail and NodeDetail pod sections ([0ba06ba](https://github.com/sthalbert/Longue-Vue/commit/0ba06bacfdf9a1332a90bd9c4ce88eae831d7d82))

## [0.19.0](https://github.com/sthalbert/Longue-Vue/compare/chart-longue-vue-v0.18.0...chart-longue-vue-v0.19.0) (2026-05-09)


### Features

* **auth:** per-account login lockout + admin rescue ([0c2cdb3](https://github.com/sthalbert/Longue-Vue/commit/0c2cdb364f95883f44831dfa536fd00b1d610c4a))
* **chart:** expose adminRescuePassword on longue-vue chart ([b8d91d8](https://github.com/sthalbert/Longue-Vue/commit/b8d91d80d7516e194f2077ee37b60ee0d91826af))

## [0.18.0](https://github.com/sthalbert/Longue-Vue/compare/chart-longue-vue-v0.17.1...chart-longue-vue-v0.18.0) (2026-05-09)


### Features

* **chart:** expose mcp.allowPlaintext for local/dev clusters without TLS ([313f7ef](https://github.com/sthalbert/Longue-Vue/commit/313f7efe6fc6d20d3a328ba94b78f4efae57e80a))
* container image versions enrichment (ADR-0022) ([72b799b](https://github.com/sthalbert/Longue-Vue/commit/72b799b76c0de46815551f1920d5b09164c33caf))

## [0.17.1](https://github.com/sthalbert/Longue-Vue/compare/chart-longue-vue-v0.17.0...chart-longue-vue-v0.17.1) (2026-05-08)


### Bug Fixes

* **security:** clear initial security-scan baseline ([2ae1be0](https://github.com/sthalbert/Longue-Vue/commit/2ae1be09a12c0671f0cfa4c06efac658f8894c11))

## [0.17.0](https://github.com/sthalbert/Longue-Vue/compare/chart-longue-vue-v0.16.0...chart-longue-vue-v0.17.0) (2026-05-06)


### Features

* implement push-mode collector for air-gapped clusters (ADR-0009) ([75d18cd](https://github.com/sthalbert/Longue-Vue/commit/75d18cda055efd8d75bb369383e3d164c1d0ce9a))


### Bug Fixes

* **security:** clear initial security-scan baseline ([9492ef4](https://github.com/sthalbert/Longue-Vue/commit/9492ef4c97342403f13a69e73404b62c76d9698c))
