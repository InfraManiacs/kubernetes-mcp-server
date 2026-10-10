package sre

import (
	"strings"
	"testing"
)

func TestResolveKind(t *testing.T) {
	t.Run("accepts canonical kinds", func(t *testing.T) {
		for _, in := range []string{"Deployment", "StatefulSet", "DaemonSet"} {
			got, err := resolveKind(workloadKinds, in)
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", in, err)
			}
			if got != in {
				t.Errorf("expected %q, got %q", in, got)
			}
		}
	})

	t.Run("is case and whitespace insensitive", func(t *testing.T) {
		got, err := resolveKind(workloadKinds, "  deployment ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "Deployment" {
			t.Errorf("expected Deployment, got %q", got)
		}
	})

	t.Run("rejects an unknown kind and names the valid ones", func(t *testing.T) {
		_, err := resolveKind(workloadKinds, "CronJob")
		if err == nil {
			t.Fatal("expected an error for an unsupported kind")
		}
		if !strings.Contains(err.Error(), "Deployment") {
			t.Errorf("expected the error to list valid kinds, got %q", err)
		}
	})

	t.Run("DaemonSet is not scalable", func(t *testing.T) {
		if _, err := resolveKind(scalableKinds, "DaemonSet"); err == nil {
			t.Fatal("expected DaemonSet to be rejected for scaling: it has no scale subresource")
		}
	})
}

func TestUnstructuredToJSON(t *testing.T) {
	out, err := unstructuredToJSON(map[string]any{"operation": map[string]any{"prune": false}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != `{"operation":{"prune":false}}` {
		t.Errorf("unexpected JSON: %s", out)
	}
}
