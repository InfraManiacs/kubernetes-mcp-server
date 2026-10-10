package sre

import (
	"fmt"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"github.com/containers/kubernetes-mcp-server/pkg/api"
)

// RestartedAtAnnotation is the annotation `kubectl rollout restart` writes.
// Using the same key means a restart issued here is indistinguishable from one
// issued by a human with kubectl, including in `kubectl rollout history`.
const RestartedAtAnnotation = "kubectl.kubernetes.io/restartedAt"

func initWorkloads() []api.ServerTool {
	return []api.ServerTool{
		{Tool: api.Tool{
			Name: "sre_restart_workload",
			Description: "Perform a rolling restart of a single Kubernetes workload, equivalent to `kubectl rollout restart`. " +
				"Patches only the pod template's restartedAt annotation, which causes the controller to roll pods one at a time, " +
				"honouring the workload's own update strategy and PodDisruptionBudget. Does not change the workload's spec, image, or replica count.",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"namespace": {
						Type:        "string",
						Description: "Namespace of the workload to restart",
					},
					"name": {
						Type:        "string",
						Description: "Name of the workload to restart",
					},
					"kind": {
						Type:        "string",
						Description: "Kind of the workload. One of: Deployment, StatefulSet, DaemonSet",
						Enum:        []any{"Deployment", "StatefulSet", "DaemonSet"},
					},
				},
				Required: []string{"namespace", "name", "kind"},
			},
			Annotations: api.ToolAnnotations{
				Title:        "SRE: Restart workload",
				ReadOnlyHint: ptr.To(false),
				// A rolling restart replaces pods but does not delete or alter
				// any declared state: the spec is identical afterwards.
				DestructiveHint: ptr.To(false),
				IdempotentHint:  ptr.To(false),
				OpenWorldHint:   ptr.To(true),
			},
		}, RBAC: api.RBACBounded(api.RBACRequirement{
			Verbs: []string{"get", "patch"},
			Target: api.RBACTarget{GVK: &api.RBACGVKTarget{
				APIVersionArgument: "apps/v1",
				KindArgument:       "kind",
			}},
			Namespace:    &api.RBACNamespace{Argument: "namespace"},
			ResourceName: &api.RBACResourceName{Argument: "name"},
		}), Handler: restartWorkload},

		{Tool: api.Tool{
			Name: "sre_scale_workload",
			Description: "Set the replica count of a single Deployment or StatefulSet. Changes only the replica count — no other field of the workload is touched. " +
				"The replica count is bounded by the server's configured maximum.",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"namespace": {
						Type:        "string",
						Description: "Namespace of the workload to scale",
					},
					"name": {
						Type:        "string",
						Description: "Name of the workload to scale",
					},
					"kind": {
						Type:        "string",
						Description: "Kind of the workload. One of: Deployment, StatefulSet",
						Enum:        []any{"Deployment", "StatefulSet"},
					},
					"replicas": {
						Type:        "integer",
						Description: "Desired replica count. Must be zero or greater, and no greater than the server's configured maximum",
						Minimum:     ptr.To(float64(0)),
					},
				},
				Required: []string{"namespace", "name", "kind", "replicas"},
			},
			Annotations: api.ToolAnnotations{
				Title:        "SRE: Scale workload",
				ReadOnlyHint: ptr.To(false),
				// Scaling to zero stops a service, so this is destructive in the
				// sense the annotation means: it can remove running capacity.
				DestructiveHint: ptr.To(true),
				IdempotentHint:  ptr.To(true),
				OpenWorldHint:   ptr.To(true),
			},
		}, RBAC: api.RBACBounded(api.RBACRequirement{
			Verbs: []string{"get", "update"},
			Target: api.RBACTarget{GVK: &api.RBACGVKTarget{
				APIVersionArgument: "apps/v1",
				KindArgument:       "kind",
				Subresource:        "scale",
			}},
			Namespace:    &api.RBACNamespace{Argument: "namespace"},
			ResourceName: &api.RBACResourceName{Argument: "name"},
		}), Handler: scaleWorkload},
	}
}

func restartWorkload(params api.ToolHandlerParams) (*api.ToolCallResult, error) {
	p := api.WrapParams(params)
	name := p.RequiredString("name")
	rawKind := p.RequiredString("kind")
	rawNamespace := p.OptionalString("namespace", "")
	if err := p.Err(); err != nil {
		return api.NewToolCallResult("", err), nil
	}

	cfg := toolsetConfig(params)
	kind, err := resolveKind(workloadKinds, rawKind)
	if err != nil {
		return api.NewToolCallResult("", err), nil
	}
	namespace, err := resolveNamespace(params, cfg, rawNamespace)
	if err != nil {
		return api.NewToolCallResult("", err), nil
	}

	// A strategic merge patch of just this annotation is exactly what
	// `kubectl rollout restart` sends. It cannot alter any other field.
	restartedAt := time.Now().Format(time.RFC3339)
	patch := []byte(fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{%q:%q}}}}}`,
		RestartedAtAnnotation, restartedAt))

	opts := metav1.PatchOptions{FieldManager: "kubernetes-mcp-server-sre"}
	switch kind {
	case "Deployment":
		_, err = params.AppsV1().Deployments(namespace).Patch(params, name, types.StrategicMergePatchType, patch, opts)
	case "StatefulSet":
		_, err = params.AppsV1().StatefulSets(namespace).Patch(params, name, types.StrategicMergePatchType, patch, opts)
	case "DaemonSet":
		_, err = params.AppsV1().DaemonSets(namespace).Patch(params, name, types.StrategicMergePatchType, patch, opts)
	}
	if err != nil {
		return api.NewToolCallResult("", fmt.Errorf("failed to restart %s %s/%s: %w", kind, namespace, name, err)), nil
	}

	return api.NewToolCallResult(fmt.Sprintf(
		"# Restarted %s %s/%s\n\nrestartedAt: %s\n\nPods roll according to the workload's own update strategy and PodDisruptionBudget. "+
			"Use pods_list_in_namespace or resources_get to watch the rollout complete.",
		kind, namespace, name, restartedAt), nil), nil
}

func scaleWorkload(params api.ToolHandlerParams) (*api.ToolCallResult, error) {
	p := api.WrapParams(params)
	name := p.RequiredString("name")
	rawKind := p.RequiredString("kind")
	rawNamespace := p.OptionalString("namespace", "")
	replicas := p.OptionalInt64("replicas", -1)
	if err := p.Err(); err != nil {
		return api.NewToolCallResult("", err), nil
	}
	if replicas < 0 {
		return api.NewToolCallResult("", fmt.Errorf("replicas is required and must be zero or greater")), nil
	}

	cfg := toolsetConfig(params)
	maxReplicas := cfg.EffectiveMaxReplicas()
	if replicas > int64(maxReplicas) {
		return api.NewToolCallResult("", fmt.Errorf(
			"replicas %d exceeds the sre toolset's max_replicas of %d", replicas, maxReplicas)), nil
	}

	kind, err := resolveKind(scalableKinds, rawKind)
	if err != nil {
		return api.NewToolCallResult("", err), nil
	}
	namespace, err := resolveNamespace(params, cfg, rawNamespace)
	if err != nil {
		return api.NewToolCallResult("", err), nil
	}

	// Read the current scale first so the result can report the transition,
	// which is what makes the audit trail readable after the fact.
	var current *autoscalingv1.Scale
	switch kind {
	case "Deployment":
		current, err = params.AppsV1().Deployments(namespace).GetScale(params, name, metav1.GetOptions{})
	case "StatefulSet":
		current, err = params.AppsV1().StatefulSets(namespace).GetScale(params, name, metav1.GetOptions{})
	}
	if err != nil {
		return api.NewToolCallResult("", fmt.Errorf("failed to read scale of %s %s/%s: %w", kind, namespace, name, err)), nil
	}

	if current.Spec.Replicas == int32(replicas) {
		return api.NewToolCallResult(fmt.Sprintf(
			"# %s %s/%s already at %d replicas\n\nNo change made.", kind, namespace, name, replicas), nil), nil
	}

	previous := current.Spec.Replicas
	current.Spec.Replicas = int32(replicas)
	opts := metav1.UpdateOptions{FieldManager: "kubernetes-mcp-server-sre"}
	switch kind {
	case "Deployment":
		_, err = params.AppsV1().Deployments(namespace).UpdateScale(params, name, current, opts)
	case "StatefulSet":
		_, err = params.AppsV1().StatefulSets(namespace).UpdateScale(params, name, current, opts)
	}
	if err != nil {
		return api.NewToolCallResult("", fmt.Errorf("failed to scale %s %s/%s: %w", kind, namespace, name, err)), nil
	}

	return api.NewToolCallResult(fmt.Sprintf(
		"# Scaled %s %s/%s\n\nreplicas: %d -> %d", kind, namespace, name, previous, replicas), nil), nil
}
