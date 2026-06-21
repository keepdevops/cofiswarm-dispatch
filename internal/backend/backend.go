// Package backend ports the monolith's backend router (legacy/cpp/backend_router.cpp +
// inference_backend.cpp): given an agent and the current dispatch context, it decides which
// inference backend (llama.cpp/Metal vs Python MLX) should serve the run, with override,
// auto-heuristics, latency-probe biasing, and fallback. Pure decision logic — no I/O.
package backend

import "strings"

// ID identifies an inference backend. Mirrors the legacy BackendId enum.
type ID int

const (
	LlamaMetal ID = iota // llama.cpp with Metal offload
	PythonMlx            // Python MLX (Apple unified memory)
)

// Name is the stable wire name for a backend (was inference_backend::id_name).
func (id ID) Name() string {
	switch id {
	case LlamaMetal:
		return "llama_metal"
	case PythonMlx:
		return "python_mlx"
	default:
		return "unknown"
	}
}

// FromName resolves a backend name (and aliases) to an ID. ok is false for unknown names
// (the ID is then meaningless and must not be used). Was inference_backend::from_name.
func FromName(name string) (id ID, ok bool) {
	switch name {
	case "llama_metal", "llama", "llama.cpp":
		return LlamaMetal, true
	case "python_mlx", "mlx":
		return PythonMlx, true
	default:
		return LlamaMetal, false
	}
}

// Agent is the subset of the monolith's Agent struct the router reads. Only these fields
// influence routing; callers populate them from their own roster representation.
type Agent struct {
	Name             string   `json:"name"`
	Engine           string   `json:"engine"`
	Backend          string   `json:"backend"`
	Model            string   `json:"model"`
	Tags             []string `json:"tags"`
	InferenceBackend string   `json:"inference_backend"` // per-agent override: llama|mlx|auto|<name>
}

func (a Agent) hasTag(needle string) bool {
	for _, t := range a.Tags {
		if t == needle {
			return true
		}
	}
	return false
}

// looksGGUF reports whether the model path has a .gguf suffix (case-insensitive) — the legacy
// signal that a model is llama.cpp-loadable.
func looksGGUF(model string) bool {
	return strings.HasSuffix(strings.ToLower(model), ".gguf")
}

// Supports reports whether the agent can run on the given backend (was inference_backend::supports).
func Supports(a Agent, id ID) bool {
	switch id {
	case LlamaMetal:
		return a.Engine == "llama" || a.Backend == "llama" || looksGGUF(a.Model)
	case PythonMlx:
		return a.Engine == "mlx" || a.Backend == "mlx" || strings.Contains(a.Model, "mlx")
	default:
		return false
	}
}

// engineDefault is the backend implied purely by the agent's engine (the legacy fallback).
func engineDefault(a Agent) ID {
	if a.Engine == "mlx" {
		return PythonMlx
	}
	return LlamaMetal
}
