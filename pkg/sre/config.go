package sre

import (
	"context"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/containers/kubernetes-mcp-server/pkg/config"
)

// DefaultMaxReplicas bounds sre_scale_workload when max_replicas is unset.
const DefaultMaxReplicas = 20

// DefaultArgoCDNamespace is where Argo CD Application objects are looked for
// when argocd_namespace is unset.
const DefaultArgoCDNamespace = "argocd"

// Config holds the sre toolset configuration.
//
// The sre toolset exists to give an agent a small set of NAMED day-2 verbs
// instead of a generic apply. Every bound here is a second line of defence
// behind RBAC, not a replacement for it: the ServiceAccount this server runs
// as should already be unable to do anything these fields forbid.
type Config struct {
	// AllowedNamespaces restricts every sre tool to this set. Empty means "any
	// namespace the ServiceAccount can reach", which is only sensible when the
	// RBAC is itself namespace-scoped.
	AllowedNamespaces []string `toml:"allowed_namespaces,omitempty"`
	// MaxReplicas caps sre_scale_workload. Zero means DefaultMaxReplicas.
	MaxReplicas int `toml:"max_replicas,omitempty"`
	// ArgoCDNamespace is where Application objects live. Empty means
	// DefaultArgoCDNamespace.
	ArgoCDNamespace string `toml:"argocd_namespace,omitempty"`
}

var _ config.ExtendedConfig = (*Config)(nil)

func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("sre config is nil")
	}
	for i, ns := range c.AllowedNamespaces {
		trimmed := strings.TrimSpace(ns)
		if trimmed == "" {
			return fmt.Errorf("allowed_namespaces entry %d must not be empty", i)
		}
		c.AllowedNamespaces[i] = trimmed
	}
	if c.MaxReplicas < 0 {
		return fmt.Errorf("max_replicas must not be negative, got %d", c.MaxReplicas)
	}
	c.ArgoCDNamespace = strings.TrimSpace(c.ArgoCDNamespace)
	return nil
}

// EffectiveMaxReplicas returns the configured cap, or the default.
func (c *Config) EffectiveMaxReplicas() int {
	if c == nil || c.MaxReplicas == 0 {
		return DefaultMaxReplicas
	}
	return c.MaxReplicas
}

// EffectiveArgoCDNamespace returns the configured Argo CD namespace, or the default.
func (c *Config) EffectiveArgoCDNamespace() string {
	if c == nil || c.ArgoCDNamespace == "" {
		return DefaultArgoCDNamespace
	}
	return c.ArgoCDNamespace
}

// NamespaceAllowed reports whether namespace is within allowed_namespaces.
// An empty allowlist permits everything — RBAC is then the only bound.
func (c *Config) NamespaceAllowed(namespace string) bool {
	if c == nil || len(c.AllowedNamespaces) == 0 {
		return true
	}
	for _, ns := range c.AllowedNamespaces {
		if ns == namespace {
			return true
		}
	}
	return false
}

func sreToolsetParser(_ context.Context, primitive toml.Primitive, md toml.MetaData) (config.ExtendedConfig, error) {
	var cfg Config
	if err := md.PrimitiveDecode(primitive, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func init() {
	config.RegisterToolsetConfig("sre", sreToolsetParser)
}
