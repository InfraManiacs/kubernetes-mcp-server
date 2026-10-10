package sre

import (
	"testing"
)

func TestConfigValidate(t *testing.T) {
	t.Run("nil config is an error", func(t *testing.T) {
		var c *Config
		if err := c.Validate(); err == nil {
			t.Fatal("expected an error for a nil config")
		}
	})

	t.Run("trims whitespace from namespaces", func(t *testing.T) {
		c := &Config{AllowedNamespaces: []string{"  agents  ", "argocd"}}
		if err := c.Validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.AllowedNamespaces[0] != "agents" {
			t.Errorf("expected %q, got %q", "agents", c.AllowedNamespaces[0])
		}
	})

	t.Run("rejects an empty namespace entry", func(t *testing.T) {
		c := &Config{AllowedNamespaces: []string{"agents", "   "}}
		if err := c.Validate(); err == nil {
			t.Fatal("expected an error for a blank namespace entry")
		}
	})

	t.Run("rejects negative max_replicas", func(t *testing.T) {
		c := &Config{MaxReplicas: -1}
		if err := c.Validate(); err == nil {
			t.Fatal("expected an error for negative max_replicas")
		}
	})
}

func TestEffectiveMaxReplicas(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *Config
		want int
	}{
		{"nil falls back to the default", nil, DefaultMaxReplicas},
		{"zero falls back to the default", &Config{}, DefaultMaxReplicas},
		{"explicit value is honoured", &Config{MaxReplicas: 3}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.EffectiveMaxReplicas(); got != tc.want {
				t.Errorf("expected %d, got %d", tc.want, got)
			}
		})
	}
}

func TestEffectiveArgoCDNamespace(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *Config
		want string
	}{
		{"nil falls back to the default", nil, DefaultArgoCDNamespace},
		{"empty falls back to the default", &Config{}, DefaultArgoCDNamespace},
		{"explicit value is honoured", &Config{ArgoCDNamespace: "gitops"}, "gitops"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.EffectiveArgoCDNamespace(); got != tc.want {
				t.Errorf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestNamespaceAllowed(t *testing.T) {
	t.Run("an empty allowlist permits everything", func(t *testing.T) {
		c := &Config{}
		if !c.NamespaceAllowed("anything") {
			t.Error("expected an empty allowlist to permit any namespace")
		}
	})

	t.Run("a nil config permits everything", func(t *testing.T) {
		var c *Config
		if !c.NamespaceAllowed("anything") {
			t.Error("expected a nil config to permit any namespace")
		}
	})

	t.Run("a populated allowlist is exclusive", func(t *testing.T) {
		c := &Config{AllowedNamespaces: []string{"agents", "argocd"}}
		if !c.NamespaceAllowed("agents") {
			t.Error("expected agents to be allowed")
		}
		if c.NamespaceAllowed("kube-system") {
			t.Error("expected kube-system to be denied")
		}
	})
}
