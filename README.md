# inari-operator

Platform-cluster operator for Inari: reconciles platform-scoped catalog items — tenant Keycloak realms/clients, DNS zones/records (ExternalDNS), cert issuers, shared ArgoCD projects, tenant namespaces on the platform cluster (plan §5.6).

Stack: Go, controller-runtime / kubebuilder

## CRDs (group `platform.inari.io/v1alpha1`)

| Kind | Reconciles to |
|------|----------------|
| `KeycloakRealm` / `KeycloakClient` | Tenant realm + OIDC client via Keycloak Admin REST (ADR 0001); workload federation only, never platform user identity (§5.4). Client secrets are written to a Secret in the tenant namespace. |
| `DNSZone` / `DNSRecord` | ExternalDNS `DNSEndpoint` children; records must live inside the tenant zone. |
| `CertIssuer` | cert-manager `ClusterIssuer` (ACME or CA). |
| `ArgoProject` | Shared ArgoCD `AppProject` scoped to the tenant's repos/namespaces. |
| `TenantNamespace` | Namespace + `tenant-admin` RoleBinding + default-deny NetworkPolicy + optional ResourceQuota. |

All CRs carry `spec.tenantID`/`spec.namespace`, expose `status.conditions` for
the server's Resources Inventory, and are torn down via finalizers. When
`spec.impersonateServiceAccount` is set, tenant-visible writes go through the
tenant-scoped ServiceAccount (§5.6 impersonation).

## Develop

```sh
make test      # unit + envtest (auto-installs envtest binaries)
make lint
make manifests # regenerate CRDs + RBAC (controller-gen)
make deploy    # kustomize build config/default | kubectl apply -f -
```

Keycloak integration is enabled by setting `KEYCLOAK_URL` and
`KEYCLOAK_CLIENT_SECRET` (see `config/manager/manager.yaml`). ExternalDNS,
cert-manager and ArgoCD CRDs are prerequisites on the platform cluster.

Releases: see [docs/release.md](docs/release.md).


Part of the **Inari** multi-tenant Internal Developer Platform (GitHub org `7K-Inari`).
Canonical architecture & development plan: [inari-docs/docs/architecture/inari-platform-plan.md](https://github.com/7K-Inari/inari-docs/blob/main/docs/architecture/inari-platform-plan.md)
