# SRE Toolset

Narrow, individually-named day-2 operations for agent-driven SRE work: rolling
restart, scale, and Argo CD refresh/sync.

The toolset is **not enabled by default**. Turn it on with
`toolsets = ["core", "sre"]`.

## Why this exists when `resources_create_or_update` already does all of it

Everything here is a patch, and the `core` toolset can already send arbitrary
patches. The problem this solves is not capability, it is **naming**.

Anything that gates tools by name — an approval policy, an audit review, a human
reading a prompt before clicking approve — sees one identical tool name for
"restart a Deployment" and "rewrite a ClusterRole". It cannot tell them apart
without parsing the payload on every single call, and a reviewer who reads every
payload is a slow `kubectl`, not an approval gate.

Each tool here takes a namespace, a name, and nothing free-form. The tool name
carries the blast radius, which makes a policy like

```
sre_argocd_list        allow    # read
sre_argocd_refresh     allow    # recompares against git, changes no live resource
sre_restart_workload   ask
sre_scale_workload     ask
sre_argocd_sync        ask      # applies git to the cluster
resources_create_or_update  deny
pods_exec                   deny
```

expressible as configuration rather than as a code review of every call.

## Tools

| Tool | Read-only | Destructive | What it does |
|------|-----------|-------------|--------------|
| `sre_argocd_list` | ✓ | | Applications as a compact table: name, sync, health, revision |
| `sre_argocd_refresh` | | | Re-compare one Application against git. Changes no live resource |
| `sre_restart_workload` | | | `kubectl rollout restart` for one workload |
| `sre_scale_workload` | | ✓ | Set replica count on one Deployment or StatefulSet |
| `sre_argocd_sync` | | ✓ | Apply git-declared state to the cluster |

Notes on the classification, because the annotations drive real filtering:

- **`sre_restart_workload` is not destructive.** It patches only the pod
  template's `kubectl.kubernetes.io/restartedAt` annotation — the same key
  `kubectl rollout restart` writes, so the restart is indistinguishable from a
  human's in `kubectl rollout history`. The workload spec is identical
  afterwards, and pods roll according to the workload's own update strategy and
  PodDisruptionBudget.
- **`sre_scale_workload` is destructive** because scaling to zero removes
  running capacity.
- **`sre_argocd_refresh` is not read-only** (it writes an annotation) but is not
  destructive either: it only triggers a comparison.
- **`sre_argocd_sync` is destructive**, and `prune` defaults to `false` — the
  half of a sync that deletes live resources must be asked for explicitly.

Argo CD is driven entirely through its `Application` CRD: `argocd app sync` is a
write to `.operation` and `argocd app get --refresh` is a write to an
annotation. So these tools need no Argo CD API server, no CLI binary, and no
second credential. Syncs are attributed to `kubernetes-mcp-server-sre` in the
Argo CD UI rather than to a person.

## Graded postures

The toolset respects the process-wide access controls, and filtering happens at
**registration** — a tool removed this way returns `unknown tool` when invoked
by name, rather than merely being hidden from `tools/list`.

| Config | Tools registered |
|--------|------------------|
| `read_only = true` | `sre_argocd_list` |
| `disable_destructive = true` | `+ sre_argocd_refresh`, `sre_restart_workload` |
| neither | all five |

`disable_destructive = true` is the useful middle setting: the agent can restart
a workload and recompute drift, but cannot scale anything or apply git.

## Configuration

```toml
toolsets = ["core", "sre"]

[toolset_configs.sre]
allowed_namespaces = ["agents", "argocd", "platform-data"]
max_replicas = 10
argocd_namespace = "argocd"
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `allowed_namespaces` | string array | _(any)_ | Allowlist every `sre_*` tool is restricted to |
| `max_replicas` | integer | `20` | Upper bound for `sre_scale_workload` |
| `argocd_namespace` | string | `argocd` | Namespace holding `Application` objects |

These are a second line of defence **behind** RBAC, not a replacement for it.
Their real job is to turn a would-be opaque RBAC denial into an error that names
the constraint, so the agent can correct itself instead of retrying.

## RBAC

Every tool declares its own RBAC requirement, so the ClusterRole below is the
minimum that makes the toolset work. Grant nothing broader: the credential is
the boundary that holds even if tool policy is misconfigured.

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kubernetes-mcp-server-sre
rules:
  # Read surface (core toolset)
  - apiGroups: [""]
    resources: [pods, pods/log, services, configmaps, namespaces, nodes, events]
    verbs: [get, list]
  - apiGroups: [apps]
    resources: [deployments, statefulsets, daemonsets, replicasets]
    verbs: [get, list]
  - apiGroups: [metrics.k8s.io]
    resources: [pods, nodes]
    verbs: [get, list]

  # sre_restart_workload — patch only, no update/delete
  - apiGroups: [apps]
    resources: [deployments, statefulsets, daemonsets]
    verbs: [patch]

  # sre_scale_workload — the scale subresource only
  - apiGroups: [apps]
    resources: [deployments/scale, statefulsets/scale]
    verbs: [get, update]

  # sre_argocd_* — Applications only, and no delete
  - apiGroups: [argoproj.io]
    resources: [applications]
    verbs: [get, list, patch]
```

Deliberately absent: `delete` on anything, `create` on anything,
`secrets`, `pods/exec`, and every `*` verb or resource. A bug in this toolset
still cannot delete a StatefulSet or read a Secret, because the credential
cannot.

Scope it further with a RoleBinding per namespace instead of a
ClusterRoleBinding where the workload namespaces are known in advance.
