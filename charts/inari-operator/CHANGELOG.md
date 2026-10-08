# Changelog

## [0.2.0](https://github.com/7K-Inari/inari-operator/compare/inari-operator-chart-v0.1.2...inari-operator-chart-v0.2.0) (2026-10-08)


### ⚠ BREAKING CHANGES

* **operator:** ClusterDex status.valuesConfigMap is replaced by status.dexApplication; spec.argocd.oidcClientSecretRef and spec.dex.configSecretRef are required when the baseline is enabled.

### Features

* **operator:** add HA guardrails to the inari-operator chart ([ce46d5c](https://github.com/7K-Inari/inari-operator/commit/ce46d5c4de07cfe15c41dfcb58add7615c7ca132))
* **operator:** add HA guardrails to the inari-operator chart ([4a18ebc](https://github.com/7K-Inari/inari-operator/commit/4a18ebcc861421f5c422c95d7b9bf736d5726d5b))
* **operator:** render cluster-local Dex as ArgoCD Application from the official dexidp chart ([#26](https://github.com/7K-Inari/inari-operator/issues/26)) ([3e25fbf](https://github.com/7K-Inari/inari-operator/commit/3e25fbfda400aedab089ff75c7e084713ea10ddd))
* **operator:** W3 ClusterDex per-cluster Dex SSO baseline rendering ([7c8e5bc](https://github.com/7K-Inari/inari-operator/commit/7c8e5bc51c2b51037004d130560bbb60fa59d7a7))
* **operator:** W3 ClusterDex per-cluster Dex SSO baseline rendering ([1329f98](https://github.com/7K-Inari/inari-operator/commit/1329f98a6fb43cacd7ab0f2f8bab6cf5bab7adaf))

## [0.1.2](https://github.com/7K-Inari/inari-operator/compare/inari-operator-chart-v0.1.1...inari-operator-chart-v0.1.2) (2026-09-24)


### Bug Fixes

* **operator:** chart keycloak client-id/token-realm wiring; bind grant for admin clusterrole ([#20](https://github.com/7K-Inari/inari-operator/issues/20)) ([096f42d](https://github.com/7K-Inari/inari-operator/commit/096f42d289d39da36c18992ea981bee909a28592))

## [0.1.1](https://github.com/7K-Inari/inari-operator/compare/inari-operator-chart-v0.1.0...inari-operator-chart-v0.1.1) (2026-08-28)


### Features

* add inari-operator and inari-operator-crds helm charts ([#13](https://github.com/7K-Inari/inari-operator/issues/13)) ([701c148](https://github.com/7K-Inari/inari-operator/commit/701c14854425053c7fcd1b4bc0915742660240d9))

## Changelog
