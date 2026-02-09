package graph

import (
	"os"
	"strings"
)

// perfDisableGraphFastPaths can be set to disable internal fast-path
// optimizations (useful for debugging / A-B validation).
//
// When unset, fast paths are enabled by default.
var perfDisableGraphFastPaths = envBool("TRPC_AGENT_GO_DISABLE_GRAPH_FASTPATHS")

func graphFastPathsEnabled() bool { return !perfDisableGraphFastPaths }

func envBool(key string) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return false
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

