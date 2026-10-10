package sre

import (
	"context"
	"slices"

	"github.com/containers/kubernetes-mcp-server/pkg/api"
	"github.com/containers/kubernetes-mcp-server/pkg/toolsets"
)

// Toolset provides a small set of named day-2 operations for an SRE agent.
//
// WHY THIS EXISTS, GIVEN resources_create_or_update ALREADY DOES ALL OF IT
//
// The core toolset can restart a workload and sync an Argo CD application —
// both are a patch, and resources_create_or_update sends arbitrary patches. The
// problem is not capability, it is NAMING. A caller that gates tools by name
// (an approval policy, an audit review, a human reading a prompt) sees one
// identical tool name for "restart a Deployment" and "rewrite a ClusterRole".
// It cannot distinguish them without parsing the payload every single time.
//
// Each tool here takes a namespace, a name, and nothing free-form. That makes
// the tool name carry the blast radius, so "allow restarts, ask before syncs,
// never allow arbitrary applies" becomes expressible as policy rather than as
// a code review of every call.
//
// This toolset is NOT enabled by default. Enable it with toolsets = [..., "sre"].
type Toolset struct{}

var _ api.Toolset = (*Toolset)(nil)

func (t *Toolset) GetName() string {
	return "sre"
}

func (t *Toolset) GetDescription() string {
	return "Narrow, individually-named day-2 operations (rolling restart, scale, Argo CD refresh and sync) for agent-driven SRE work"
}

func (t *Toolset) GetTools(_ context.Context, _ api.ToolsetContext) []api.ServerTool {
	return slices.Concat(
		initWorkloads(),
		initArgoCD(),
	)
}

func (t *Toolset) GetPrompts(_ context.Context, _ api.ToolsetContext) []api.ServerPrompt {
	return nil
}

func (t *Toolset) GetResources(_ context.Context, _ api.ToolsetContext) []api.ServerResource {
	return nil
}

func (t *Toolset) GetResourceTemplates(_ context.Context, _ api.ToolsetContext) []api.ServerResourceTemplate {
	return nil
}

func init() {
	toolsets.Register(&Toolset{})
}
