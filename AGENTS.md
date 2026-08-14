# inari-operator — Agent Guide

Platform-cluster operator for Inari: reconciles platform-scoped catalog items — tenant Keycloak realms/clients, DNS zones/records (ExternalDNS), cert issuers, shared ArgoCD projects, tenant namespaces on the platform cluster (plan §5.6).

Stack: Go, controller-runtime / kubebuilder

## Key architecture constraints
- Reconciles **platform-scoped Catalog Items** requested by tenants; lifecycle tied to the tenant (§5.6).
- Tenant Keycloak realms serve *workload* federation only — never platform user identity (§5.4).
- Tenant access to platform-cluster resources is namespace-isolated and mediated by impersonation (§5.6).
- Keycloak Admin REST (or Crossplane provider-keycloak) for realm/client management (§5.4).

## Conventions
- Conventional Commits; SemVer releases; container images/artifacts cosign-signed.
- Releases: release-please in **PR-only mode** (release-type `go`). `release-please.yml` (push to main, `skip-github-release: true`) only maintains the Release PR; merging it runs `release.yml` (push to main), which creates the tag + GitHub Release and publishes the GHCR image (`ghcr.io/7k-inari/inari-operator`, cosign keyless + SBOM + SLSA) and CRD/install bundles. **No tag-push triggers.** Per-commit edge images are a separate CI workflow. Details: `docs/release.md`.
- Write tests for new behavior; keep changes minimal and focused.
- Canonical architecture & development plan: https://github.com/7K-Inari/inari-docs/blob/main/docs/architecture/inari-platform-plan.md (section references below point into it).

## Platform design principles (apply everywhere)
1. Tenant-aware to the core — every object carries a tenant ID; every API decision is tenant-scoped.
2. Zero tenant credentials on the hub — no tenant kubeconfigs or cloud keys in the control plane.
3. Pull, never push — agents dial out; the control plane never initiates connections into tenant networks.
4. Desired state, eventually reconciled — GitOps/CR-based mutations, not imperative RPCs.
5. The catalog is a projection of reality — capabilities are discovered, not declared.
6. Small kernel, everything else extension.
7. Modular monolith first — strict internal module boundaries.
