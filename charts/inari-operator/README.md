# inari-operator

Deployment, RBAC and ServiceAccount for the Inari platform operator. Install
`inari-operator-crds` first (see `docs/helm-migration.md` in the operator
repo).

## Production posture (HA)

The operator runs **active-passive**: leader election is enabled by default
(`leaderElection.enabled: true`), so multiple replicas are safe — one holds
the lease, the rest stand by. For a 99.9% availability target, run two
replicas spread across nodes (and zones, where available):

```yaml
replicaCount: 2
leaderElection:
  enabled: true # default
podAntiAffinity:
  enabled: true # preferred spread across kubernetes.io/hostname
topologySpreadConstraints:
  - maxSkew: 1
    topologyKey: topology.kubernetes.io/zone
    whenUnsatisfiable: ScheduleAnyway
```

With `replicaCount >= 2` the chart renders a PodDisruptionBudget
(`pdb.minAvailable: 1`) so voluntary disruptions (node drains, cluster
upgrades) keep at least one replica running. At the default single replica no
PDB is rendered — a single-replica install must never block a drain.

Use the `affinity` value as an escape hatch for node affinity or
*required* anti-affinity; it is merged alongside the `podAntiAffinity`
preset.
