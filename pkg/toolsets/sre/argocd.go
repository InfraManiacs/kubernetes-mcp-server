package sre

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"github.com/containers/kubernetes-mcp-server/pkg/api"
)

// applicationsGVR is the Argo CD Application resource. Argo CD is driven
// entirely through this CRD — `argocd app sync` is a write to .operation and
// `argocd app get --refresh` is a write to the refresh annotation — so these
// tools need no Argo CD API server, no CLI binary and no second credential.
var applicationsGVR = schema.GroupVersionResource{
	Group:    "argoproj.io",
	Version:  "v1alpha1",
	Resource: "applications",
}

const (
	// RefreshAnnotation asks the application controller to re-compare live
	// state against git. The controller removes the annotation when done.
	RefreshAnnotation = "argocd.argoproj.io/refresh"
	// SyncInitiatedBy is recorded on every sync this server starts, so the Argo
	// CD UI and audit log attribute it to the agent rather than to a person.
	SyncInitiatedBy = "kubernetes-mcp-server-sre"
)

func initArgoCD() []api.ServerTool {
	return []api.ServerTool{
		{Tool: api.Tool{
			Name: "sre_argocd_list",
			Description: "List Argo CD Applications with their sync status, health status and revision, as a compact table. " +
				"Use this rather than a generic resource list when surveying GitOps drift — it returns one line per application instead of full manifests.",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"namespace": {
						Type:        "string",
						Description: "Namespace holding the Application objects. Defaults to the server's configured Argo CD namespace",
					},
					"outOfSyncOnly": {
						Type:        "boolean",
						Description: "If true, return only applications that are not Synced or not Healthy",
					},
				},
			},
			Annotations: api.ToolAnnotations{
				Title:           "SRE: List Argo CD applications",
				ReadOnlyHint:    ptr.To(true),
				DestructiveHint: ptr.To(false),
				OpenWorldHint:   ptr.To(true),
			},
		}, RBAC: api.RBACBounded(api.RBACRequirement{
			Verbs: []string{"list"},
			Target: api.RBACTarget{Resource: &api.RBACResourceTarget{
				APIGroup: "argoproj.io",
				Resource: "applications",
			}},
			Namespace: &api.RBACNamespace{Argument: "namespace"},
		}), Handler: argoCDList},

		{Tool: api.Tool{
			Name: "sre_argocd_refresh",
			Description: "Ask Argo CD to re-compare one Application against git, equivalent to `argocd app get --refresh`. " +
				"This recomputes drift only — it does NOT apply anything to the cluster and changes no live resource. Use it before reading sync status to be sure the status is current.",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name": {
						Type:        "string",
						Description: "Name of the Argo CD Application",
					},
					"namespace": {
						Type:        "string",
						Description: "Namespace holding the Application object. Defaults to the server's configured Argo CD namespace",
					},
					"hard": {
						Type:        "boolean",
						Description: "If true, perform a hard refresh, which also re-renders manifests from source instead of using the cache",
					},
				},
				Required: []string{"name"},
			},
			Annotations: api.ToolAnnotations{
				Title: "SRE: Refresh Argo CD application",
				// It writes an annotation, so it is not strictly read-only, but
				// it cannot change any live cluster resource.
				ReadOnlyHint:    ptr.To(false),
				DestructiveHint: ptr.To(false),
				IdempotentHint:  ptr.To(true),
				OpenWorldHint:   ptr.To(true),
			},
		}, RBAC: api.RBACBounded(api.RBACRequirement{
			Verbs: []string{"get", "patch"},
			Target: api.RBACTarget{Resource: &api.RBACResourceTarget{
				APIGroup: "argoproj.io",
				Resource: "applications",
			}},
			Namespace:    &api.RBACNamespace{Argument: "namespace"},
			ResourceName: &api.RBACResourceName{Argument: "name"},
		}), Handler: argoCDRefresh},

		{Tool: api.Tool{
			Name: "sre_argocd_sync",
			Description: "Trigger an Argo CD sync of one Application, equivalent to `argocd app sync`. This APPLIES the git-declared state to the live cluster and can restart or replace running workloads. " +
				"Pruning is disabled unless explicitly requested.",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name": {
						Type:        "string",
						Description: "Name of the Argo CD Application to sync",
					},
					"namespace": {
						Type:        "string",
						Description: "Namespace holding the Application object. Defaults to the server's configured Argo CD namespace",
					},
					"revision": {
						Type:        "string",
						Description: "Optional git revision to sync to. Defaults to the Application's configured target revision",
					},
					"prune": {
						Type:        "boolean",
						Description: "If true, delete live resources that no longer exist in git. Defaults to false. This is the destructive half of a sync",
					},
					"dryRun": {
						Type:        "boolean",
						Description: "If true, Argo CD computes the sync without applying it",
					},
				},
				Required: []string{"name"},
			},
			Annotations: api.ToolAnnotations{
				Title:           "SRE: Sync Argo CD application",
				ReadOnlyHint:    ptr.To(false),
				DestructiveHint: ptr.To(true),
				IdempotentHint:  ptr.To(false),
				OpenWorldHint:   ptr.To(true),
			},
		}, RBAC: api.RBACBounded(api.RBACRequirement{
			Verbs: []string{"get", "patch"},
			Target: api.RBACTarget{Resource: &api.RBACResourceTarget{
				APIGroup: "argoproj.io",
				Resource: "applications",
			}},
			Namespace:    &api.RBACNamespace{Argument: "namespace"},
			ResourceName: &api.RBACResourceName{Argument: "name"},
		}), Handler: argoCDSync},
	}
}

// argoCDNamespace resolves the Application namespace, defaulting to the
// configured Argo CD namespace rather than the client's current namespace —
// Application objects almost never live in the namespace being operated on.
func argoCDNamespace(params api.ToolHandlerParams, raw string) (string, error) {
	cfg := toolsetConfig(params)
	namespace := strings.TrimSpace(raw)
	if namespace == "" {
		namespace = cfg.EffectiveArgoCDNamespace()
	}
	if !cfg.NamespaceAllowed(namespace) {
		return "", fmt.Errorf(
			"namespace %q is not in the sre toolset's allowed_namespaces (%s)",
			namespace, strings.Join(cfg.AllowedNamespaces, ", "))
	}
	return namespace, nil
}

func argoCDList(params api.ToolHandlerParams) (*api.ToolCallResult, error) {
	p := api.WrapParams(params)
	rawNamespace := p.OptionalString("namespace", "")
	outOfSyncOnly := p.OptionalBool("outOfSyncOnly", false)
	if err := p.Err(); err != nil {
		return api.NewToolCallResult("", err), nil
	}

	namespace, err := argoCDNamespace(params, rawNamespace)
	if err != nil {
		return api.NewToolCallResult("", err), nil
	}

	list, err := params.DynamicClient().Resource(applicationsGVR).Namespace(namespace).List(params, metav1.ListOptions{})
	if err != nil {
		return api.NewToolCallResult("", fmt.Errorf("failed to list Argo CD applications in %s: %w", namespace, err)), nil
	}

	type row struct{ name, sync, health, revision string }
	rows := make([]row, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		sync, _, _ := unstructured.NestedString(item.Object, "status", "sync", "status")
		health, _, _ := unstructured.NestedString(item.Object, "status", "health", "status")
		revision, _, _ := unstructured.NestedString(item.Object, "status", "sync", "revision")
		if sync == "" {
			sync = "Unknown"
		}
		if health == "" {
			health = "Unknown"
		}
		if outOfSyncOnly && sync == "Synced" && health == "Healthy" {
			continue
		}
		if len(revision) > 8 {
			revision = revision[:8]
		}
		rows = append(rows, row{item.GetName(), sync, health, revision})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })

	if len(rows) == 0 {
		if outOfSyncOnly {
			return api.NewToolCallResult(
				fmt.Sprintf("# All Argo CD applications in %s are Synced and Healthy", namespace), nil), nil
		}
		return api.NewToolCallResult(fmt.Sprintf("# No Argo CD applications found in %s", namespace), nil), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Argo CD applications in %s (%d)\n\n", namespace, len(rows))
	fmt.Fprintf(&b, "%-36s %-12s %-12s %s\n", "NAME", "SYNC", "HEALTH", "REVISION")
	for _, r := range rows {
		fmt.Fprintf(&b, "%-36s %-12s %-12s %s\n", r.name, r.sync, r.health, r.revision)
	}
	return api.NewToolCallResult(b.String(), nil), nil
}

func argoCDRefresh(params api.ToolHandlerParams) (*api.ToolCallResult, error) {
	p := api.WrapParams(params)
	name := p.RequiredString("name")
	rawNamespace := p.OptionalString("namespace", "")
	hard := p.OptionalBool("hard", false)
	if err := p.Err(); err != nil {
		return api.NewToolCallResult("", err), nil
	}

	namespace, err := argoCDNamespace(params, rawNamespace)
	if err != nil {
		return api.NewToolCallResult("", err), nil
	}

	refreshType := "normal"
	if hard {
		refreshType = "hard"
	}
	patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{%q:%q}}}`, RefreshAnnotation, refreshType))

	_, err = params.DynamicClient().Resource(applicationsGVR).Namespace(namespace).
		Patch(params, name, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: SyncInitiatedBy})
	if err != nil {
		return api.NewToolCallResult("", fmt.Errorf("failed to refresh Argo CD application %s/%s: %w", namespace, name, err)), nil
	}

	return api.NewToolCallResult(fmt.Sprintf(
		"# Requested %s refresh of Argo CD application %s/%s\n\n"+
			"The application controller clears the annotation once the comparison completes. "+
			"Re-read with sre_argocd_list to see the updated sync status. No live resource was changed.",
		refreshType, namespace, name), nil), nil
}

func argoCDSync(params api.ToolHandlerParams) (*api.ToolCallResult, error) {
	p := api.WrapParams(params)
	name := p.RequiredString("name")
	rawNamespace := p.OptionalString("namespace", "")
	revision := p.OptionalString("revision", "")
	prune := p.OptionalBool("prune", false)
	dryRun := p.OptionalBool("dryRun", false)
	if err := p.Err(); err != nil {
		return api.NewToolCallResult("", err), nil
	}

	namespace, err := argoCDNamespace(params, rawNamespace)
	if err != nil {
		return api.NewToolCallResult("", err), nil
	}

	client := params.DynamicClient().Resource(applicationsGVR).Namespace(namespace)

	// Refuse to queue a second operation over a running one. Argo CD would
	// reject it anyway, but failing here gives the agent a message it can act
	// on instead of a generic conflict.
	existing, err := client.Get(params, name, metav1.GetOptions{})
	if err != nil {
		return api.NewToolCallResult("", fmt.Errorf("failed to read Argo CD application %s/%s: %w", namespace, name, err)), nil
	}
	if phase, found, _ := unstructured.NestedString(existing.Object, "status", "operationState", "phase"); found &&
		(phase == "Running" || phase == "Terminating") {
		return api.NewToolCallResult("", fmt.Errorf(
			"Argo CD application %s/%s already has an operation in phase %q; wait for it to finish before syncing again",
			namespace, name, phase)), nil
	}

	syncOp := map[string]any{
		"syncStrategy": map[string]any{"hook": map[string]any{}},
		"prune":        prune,
	}
	if revision != "" {
		syncOp["revision"] = revision
	}
	if dryRun {
		syncOp["dryRun"] = true
	}
	operation := map[string]any{
		"initiatedBy": map[string]any{"username": SyncInitiatedBy, "automated": false},
		"sync":        syncOp,
		"info":        []any{map[string]any{"name": "Reason", "value": "Initiated by kubernetes-mcp-server sre toolset"}},
	}

	patchObj := map[string]any{"operation": operation}
	patch, err := unstructuredToJSON(patchObj)
	if err != nil {
		return api.NewToolCallResult("", fmt.Errorf("failed to build sync operation: %w", err)), nil
	}

	if _, err := client.Patch(params, name, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: SyncInitiatedBy}); err != nil {
		return api.NewToolCallResult("", fmt.Errorf("failed to sync Argo CD application %s/%s: %w", namespace, name, err)), nil
	}

	target := revision
	if target == "" {
		target = "the application's configured target revision"
	}
	mode := "sync"
	if dryRun {
		mode = "dry-run sync"
	}
	return api.NewToolCallResult(fmt.Sprintf(
		"# Started %s of Argo CD application %s/%s\n\nrevision: %s\nprune: %t\n\n"+
			"The application controller runs the operation asynchronously. "+
			"Poll status with sre_argocd_list, or read status.operationState for the result.",
		mode, namespace, name, target, prune), nil), nil
}
