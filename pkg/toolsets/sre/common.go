package sre

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/containers/kubernetes-mcp-server/pkg/api"
	sreconfig "github.com/containers/kubernetes-mcp-server/pkg/sre"
)

// workloadKinds are the only kinds the sre verbs will act on. Anything else is
// rejected before a request reaches the API server, so the error the agent sees
// names the constraint rather than returning an RBAC denial it cannot act on.
var workloadKinds = map[string]string{
	"deployment":  "Deployment",
	"statefulset": "StatefulSet",
	"daemonset":   "DaemonSet",
}

// scalableKinds is workloadKinds minus DaemonSet, which has no scale subresource.
var scalableKinds = map[string]string{
	"deployment":  "Deployment",
	"statefulset": "StatefulSet",
}

func toolsetConfig(params api.ToolHandlerParams) *sreconfig.Config {
	if params.Config == nil {
		return &sreconfig.Config{}
	}
	if c, ok := params.Config.GetToolsetConfig("sre"); ok {
		if sc, ok := c.(*sreconfig.Config); ok {
			return sc
		}
	}
	return &sreconfig.Config{}
}

// resolveKind normalises a kind argument against the supplied allowlist.
func resolveKind(kinds map[string]string, raw string) (string, error) {
	kind, ok := kinds[strings.ToLower(strings.TrimSpace(raw))]
	if !ok {
		valid := make([]string, 0, len(kinds))
		for _, v := range kinds {
			valid = append(valid, v)
		}
		return "", fmt.Errorf("unsupported kind %q, must be one of: %s", raw, strings.Join(valid, ", "))
	}
	return kind, nil
}

// resolveNamespace applies the default namespace and then the allowlist.
func resolveNamespace(params api.ToolHandlerParams, cfg *sreconfig.Config, raw string) (string, error) {
	namespace := params.NamespaceOrDefault(raw)
	if namespace == "" {
		return "", fmt.Errorf("namespace is required")
	}
	if !cfg.NamespaceAllowed(namespace) {
		return "", fmt.Errorf(
			"namespace %q is not in the sre toolset's allowed_namespaces (%s)",
			namespace, strings.Join(cfg.AllowedNamespaces, ", "))
	}
	return namespace, nil
}

// unstructuredToJSON marshals a patch body built from plain Go maps.
func unstructuredToJSON(obj map[string]any) ([]byte, error) {
	return json.Marshal(obj)
}
