# ADR 0001: Manage tenant Keycloak realms via the Admin REST API directly

## Status

Accepted

## Context

`inari-operator` reconciles `KeycloakRealm` / `KeycloakClient` platform Catalog
Items (plan §5.4, §5.6). Tenant realms serve *workload* federation only — never
platform user identity. Two realistic implementation paths:

1. **Direct Keycloak Admin REST client.** A thin internal client
   (`internal/keycloak`) authenticates with a confidential admin client
   (client-credentials grant) and creates/updates/deletes realms and clients.
2. **Crossplane provider-keycloak.** The operator would render provider
   managed resources and project their status back onto our CRDs.

(The keycloak.org Keycloak Operator was rejected up front: it manages Keycloak
*instances*, not realms/clients as tenant self-service resources.)

## Decision

We use the **direct Admin REST client** (option 1).

## Consequences

- No Crossplane installation is required on the platform cluster; the operator
  stays a small kernel (design principle 6).
- Full control over idempotency, finalizers, status conditions, and
  audit-friendly events — all first-class requirements in §5.6.
- Testable with envtest + an httptest fake of the Admin REST API; no provider
  CRDs to install in test.
- We own token caching/retry logic; the client caches tokens with expiry and
  treats 404s as idempotent no-ops on delete.
- If Crossplane is later adopted platform-wide, `internal/keycloak` is a small,
  isolated seam that can be swapped behind the reconcilers' interface.

## Notes

- Admin credentials come from the `inari-operator-keycloak` Secret
  (`url`, `client-secret`) consumed via env vars in the manager Deployment.
- Teardown is finalizer-driven: deleting a `KeycloakRealm`/`KeycloakClient`
  deletes the remote realm/client (and the tenant-namespace credential Secret)
  before the CR is released.
