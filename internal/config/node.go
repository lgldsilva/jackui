package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Node roles. A single deployment is always "primary" (the default). An "edge"
// node shares the primary's PostgreSQL database so users see the same state, but
// runs none of the singleton background jobs (downloads worker, seeding,
// watchlist checks, boot-time reconciliation) — those would race the primary's
// over the same rows. Downloads started on an edge are forwarded to PrimaryURL.
const (
	NodeRolePrimary = "primary"
	NodeRoleEdge    = "edge"
)

// NodeConfig selects this process's role in a multi-node deployment.
type NodeConfig struct {
	Role string
	// PrimaryURL is the base URL (scheme://host[:port]) the edge forwards
	// download/destination requests to. Required when Role is "edge".
	PrimaryURL string
}

// IsEdge reports whether this process runs as an edge node.
func (c *Config) IsEdge() bool { return c.Node.Role == NodeRoleEdge }

// NodeRoleName returns the effective role (empty means primary).
func (c *Config) NodeRoleName() string {
	if c.IsEdge() {
		return NodeRoleEdge
	}
	return NodeRolePrimary
}

func applyNodeEnv(cfg *Config) {
	if v := os.Getenv("JACKUI_NODE_ROLE"); v != "" {
		cfg.Node.Role = strings.ToLower(strings.TrimSpace(v))
	}
	if v := os.Getenv("JACKUI_PRIMARY_URL"); v != "" {
		cfg.Node.PrimaryURL = strings.TrimRight(strings.TrimSpace(v), "/")
	}
}

// Validate rejects an unknown role and an edge without a usable PrimaryURL, so a
// misconfigured edge fails at boot instead of silently acting as a second primary.
func (n NodeConfig) Validate() error {
	switch n.Role {
	case "", NodeRolePrimary:
		return nil
	case NodeRoleEdge:
		u, err := url.Parse(n.PrimaryURL)
		if n.PrimaryURL == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("node role %q requires JACKUI_PRIMARY_URL as an http(s) base URL (got %q)", NodeRoleEdge, n.PrimaryURL)
		}
		return nil
	default:
		return fmt.Errorf("unknown node role %q (want %q or %q)", n.Role, NodeRolePrimary, NodeRoleEdge)
	}
}
