# inari-operator

Platform-cluster operator for Inari: reconciles platform-scoped catalog items — tenant Keycloak realms/clients, DNS zones/records (ExternalDNS), cert issuers, shared ArgoCD projects, tenant namespaces on the platform cluster (plan §5.6).

Stack: Go, controller-runtime / kubebuilder

Part of the **Inari** multi-tenant Internal Developer Platform (GitHub org `7K-Inari`).
Canonical architecture & development plan: [inari-docs/docs/architecture/inari-platform-plan.md](https://github.com/7K-Inari/inari-docs/blob/main/docs/architecture/inari-platform-plan.md)
