# Changelog

## [0.0.3](https://github.com/7K-Inari/inari-operator/compare/inari-operator-v0.0.2...inari-operator-v0.0.3) (2026-08-21)


### Features

* config manifests, Dockerfile, CI workflow, ADR 0001, docs ([50ccbab](https://github.com/7K-Inari/inari-operator/commit/50ccbab40086e5dd131db72f752a0c4f6902f3a4))
* platform Catalog Item CRDs, reconcilers, and CI scaffold (M3-W1) ([0b8252f](https://github.com/7K-Inari/inari-operator/commit/0b8252f3f8be854b8bd61e11a29b1171223777ed))
* platform.inari.io/v1alpha1 API types and Keycloak Admin REST client ([4e77047](https://github.com/7K-Inari/inari-operator/commit/4e770471ba9e75d71fd4bd2d903fe429238eace5))
* reconcilers for platform catalog items (keycloak, dns, certs, argo, namespaces) ([fc1cda5](https://github.com/7K-Inari/inari-operator/commit/fc1cda5185deeb8a03f9493b30dc2b14f1b1b211))


### Bug Fixes

* **ci:** drop fragile commit-message gate; let release-please self-detect ([f284094](https://github.com/7K-Inari/inari-operator/commit/f2840949efa05763e1e98d24ba7351a0c3f24f14))
* **ci:** grant issues/pull-requests write to release workflow ([3ac3cef](https://github.com/7K-Inari/inari-operator/commit/3ac3cefdfb15ed2151a6482381527595acc40f8f))
* **ci:** grant issues/pull-requests write to release workflow ([d516330](https://github.com/7K-Inari/inari-operator/commit/d51633040bf4b2fa2bd321b1f8b79a0daa82c824))
* **ci:** make release workflow valid — hashFiles() may not combine with needs in a job-level if ([f49717b](https://github.com/7K-Inari/inari-operator/commit/f49717bd3ae71cf25b706a6f1c4f0dafb43ba233))
* **ci:** repair release workflow job gating ([3a2f06e](https://github.com/7K-Inari/inari-operator/commit/3a2f06e89bb6819a51788a687023abfbf26518c5))
* resolve KeycloakClient realm deterministically and correct client drift ([530bc88](https://github.com/7K-Inari/inari-operator/commit/530bc88c553e6794782cd716e4ffefb1ad69088d))
* retry once on 401 with fresh token; bound tenant child names to 63 chars ([67424ff](https://github.com/7K-Inari/inari-operator/commit/67424ffb4e3eccc387cbe9f41e6672dae62de7cf))


### Build System

* bump Dockerfile to golang:1.24 to match go.mod ([a5bd636](https://github.com/7K-Inari/inari-operator/commit/a5bd63608eb8758c1734a60f2aba6b3d8491aed2))


### Continuous Integration

* run checks on PRs only ([cc7bf2a](https://github.com/7K-Inari/inari-operator/commit/cc7bf2a6ee8dfd53fa9a87faddc8d133949f2374))
* run checks on PRs only ([b694c92](https://github.com/7K-Inari/inari-operator/commit/b694c92d51ac6c3cd2791f3bcea2df9bc6ebceca))

## [0.0.2](https://github.com/7K-Inari/inari-operator/compare/inari-operator-v0.0.1...inari-operator-v0.0.2) (2026-08-14)


### Bug Fixes

* gate release.yml to release-please merge commits; install kustomize via pinned binary ([4d8fa0c](https://github.com/7K-Inari/inari-operator/commit/4d8fa0c9b7de36a9578ca45b19a3fed1734be157))


### Documentation

* seed AGENTS.md agent guide ([4584b48](https://github.com/7K-Inari/inari-operator/commit/4584b488677b46c2e9a6b8f2ca9d66853dffcc24))
* seed README ([c2dd5b3](https://github.com/7K-Inari/inari-operator/commit/c2dd5b3afae80ab360e49b359a0913873023e253))


### Continuous Integration

* add release-please PR-only release pipeline ([a32e39f](https://github.com/7K-Inari/inari-operator/commit/a32e39ffd588d659388efd372c9ad041890cedd9))
* add release-please PR-only release pipeline ([1b859eb](https://github.com/7K-Inari/inari-operator/commit/1b859eb41ca10b52c60ca3b24e441881fba94a08))
