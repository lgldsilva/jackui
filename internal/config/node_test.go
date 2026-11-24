package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNodeConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		node    NodeConfig
		wantErr string
	}{
		{"empty role is primary", NodeConfig{}, ""},
		{"explicit primary", NodeConfig{Role: NodeRolePrimary}, ""},
		{"edge with http url", NodeConfig{Role: NodeRoleEdge, PrimaryURL: "http://10.0.0.1:8989"}, ""},
		{"edge with https url", NodeConfig{Role: NodeRoleEdge, PrimaryURL: "https://home.example"}, ""},
		{"edge without url", NodeConfig{Role: NodeRoleEdge}, "requires JACKUI_PRIMARY_URL"},
		{"edge with scheme-less url", NodeConfig{Role: NodeRoleEdge, PrimaryURL: "10.0.0.1:8989"}, "requires JACKUI_PRIMARY_URL"},
		{"edge with ftp url", NodeConfig{Role: NodeRoleEdge, PrimaryURL: "ftp://x"}, "requires JACKUI_PRIMARY_URL"},
		{"edge with host-less url", NodeConfig{Role: NodeRoleEdge, PrimaryURL: "http://"}, "requires JACKUI_PRIMARY_URL"},
		{"unknown role", NodeConfig{Role: "replica"}, "unknown node role"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.node.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestIsEdgeAndRoleName(t *testing.T) {
	c := &Config{}
	if c.IsEdge() || c.NodeRoleName() != NodeRolePrimary {
		t.Fatalf("zero config must be primary, got edge=%v name=%q", c.IsEdge(), c.NodeRoleName())
	}
	c.Node.Role = NodeRoleEdge
	if !c.IsEdge() || c.NodeRoleName() != NodeRoleEdge {
		t.Fatalf("edge config: edge=%v name=%q", c.IsEdge(), c.NodeRoleName())
	}
}

func TestApplyNodeEnvNormalizes(t *testing.T) {
	t.Setenv("JACKUI_NODE_ROLE", "  EDGE ")
	t.Setenv("JACKUI_PRIMARY_URL", " http://10.0.0.1:8989/ ")
	c := &Config{}
	applyNodeEnv(c)
	if c.Node.Role != NodeRoleEdge {
		t.Fatalf("role = %q", c.Node.Role)
	}
	if c.Node.PrimaryURL != "http://10.0.0.1:8989" {
		t.Fatalf("primary url = %q", c.Node.PrimaryURL)
	}
}

func TestLoadRejectsEdgeWithoutPrimaryURL(t *testing.T) {
	t.Setenv("JACKUI_NODE_ROLE", "edge")
	t.Setenv("JACKUI_PRIMARY_URL", "")
	dir := t.TempDir()

	// Existing config file path.
	existing := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(existing, []byte("port: 8989\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(existing); err == nil || !strings.Contains(err.Error(), "JACKUI_PRIMARY_URL") {
		t.Fatalf("Load(existing) err = %v, want primary url error", err)
	}

	// Fresh boot path (file missing): must fail too and not write a config.
	fresh := filepath.Join(dir, "fresh.yaml")
	if _, err := Load(fresh); err == nil {
		t.Fatal("Load(fresh) accepted an edge without primary url")
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Fatalf("fresh config should not be written on validation failure (stat err=%v)", err)
	}
}

func TestLoadAcceptsEdgeWithPrimaryURL(t *testing.T) {
	t.Setenv("JACKUI_NODE_ROLE", "edge")
	t.Setenv("JACKUI_PRIMARY_URL", "http://10.0.0.1:8989")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("port: 8989\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.IsEdge() || cfg.Node.PrimaryURL != "http://10.0.0.1:8989" {
		t.Fatalf("node = %+v", cfg.Node)
	}
}

// The role is env-only: a Settings save must not persist it, or dropping
// JACKUI_NODE_ROLE would leave the node stuck on edge.
func TestSaveDoesNotPersistNodeRole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	c := &Config{Port: 8989, Node: NodeConfig{Role: NodeRoleEdge, PrimaryURL: "http://10.0.0.1:8989"}}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "edge") || strings.Contains(string(data), "10.0.0.1") {
		t.Fatalf("node config leaked into config.yaml:\n%s", data)
	}
}
