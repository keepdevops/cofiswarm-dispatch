// Package prepare ports the architecture-appropriate slice of the monolith's dispatch route glue
// (legacy/cpp/coordinator_routes_dispatch.cpp + _prepare.cpp + _meta.h): parse the dispatch
// request, build the rag-augmented effective prompt, and stamp response meta.
//
// The monolith's route also drove ~12 in-process subsystems that are intentionally OUT of the
// standalone scope (and were never carved into dispatch's legacy/): token ledger / budget
// hierarchy, adaptive select, context gate, kv auto-clear, rl-trajectory logging, swarm
// supervisor, contract ledger, agent-health breakers, rss generator, rag rerank/trajectory, tes.
// Orchestration itself now lives in the mode services; this package only prepares + annotates.
package prepare

import (
	"encoding/json"

	"github.com/keepdevops/cofiswarm-dispatch/internal/rag"
)

// Request is the parsed dispatch body (ports DispatchRequest).
type Request struct {
	Prompt        string         `json:"prompt"`
	Temperature   float64        `json:"temperature"`
	Followup      bool           `json:"followup"`
	QualityPass   bool           `json:"quality_pass"`
	SessionID     string         `json:"session_id"`
	RunID         string         `json:"-"`
	ParentRunID   string         `json:"parent_run_id"`
	ContextPolicy map[string]any `json:"context_policy"`
	UseRAG        bool           `json:"use_rag"`
	RagTopK       int            `json:"rag_top_k"`
	RagMinScore   float64        `json:"rag_min_score"`
	RagAgents     []string       `json:"rag_agents"`
	RagKinds      []string       `json:"rag_kinds"`  // restrict use_rag retrieval to these memory kinds
	UseMemory     bool           `json:"use_memory"` // inject declarative memory (default true via Parse)
	Mode          string         `json:"mode"`
	ModeConfig    map[string]any `json:"mode_config"`
	KVPressure    float64        `json:"kv_pressure"`
}

// MemoryKinds are the declarative-memory kinds injected on every prompt as always-on context,
// independent of use_rag (Phase A, Tier 2).
var MemoryKinds = []string{"fact", "preference"}

const defaultMemoryTopK = 3

// Parse decodes a dispatch body, applying the monolith's defaults and minting session/run ids via
// newID. rag_min_score defaults to -1 ("unset") so a caller can distinguish it from 0.0; use_memory
// defaults to true so long-term memory reaches every prompt unless explicitly disabled.
func Parse(raw []byte, newID func(prefix string) string) (Request, error) {
	r := Request{Temperature: 0.7, RagMinScore: -1, UseMemory: true}
	if err := json.Unmarshal(raw, &r); err != nil {
		return Request{}, err
	}
	if r.SessionID == "" {
		r.SessionID = newID("sess")
	}
	r.RunID = newID("run")
	return r, nil
}

// RagResult carries the rag-augmented prompt plus targeted block and meta (ports RagResult).
type RagResult struct {
	EffectivePrompt string
	RagBlock        string         // set instead of prepending when rag_agents target specific agents
	RagMeta         map[string]any // nil when rag was not requested
}

// Retriever is the rag dependency (satisfied by *rag.Client).
type Retriever interface {
	Retrieve(rag.Settings, string) []rag.Hit
}

// retrieveMemory fetches always-on declarative memory (facts/preferences) for the prompt,
// independent of use_rag. Returns nil when disabled or memory is off.
func retrieveMemory(req Request, retriever Retriever, base rag.Settings) []rag.Hit {
	if !req.UseMemory || !base.Enabled {
		return nil
	}
	ms := base
	ms.Kinds = MemoryKinds
	ms.TopK = defaultMemoryTopK
	if base.TopK > 0 && base.TopK < defaultMemoryTopK {
		ms.TopK = base.TopK
	}
	return retriever.Retrieve(ms, req.Prompt)
}

func memoryMeta(hits []rag.Hit) map[string]any {
	items := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		items = append(items, map[string]any{
			"source_path": h.SourcePath, "kind": h.Kind, "distance": h.Distance,
		})
	}
	return map[string]any{"used": len(hits) > 0, "kinds": MemoryKinds, "hits": items}
}

// BuildRAG augments effPrompt with retrieved context (ports dispatch_build_rag, minus the
// not-ported rerank + trajectory recording). Declarative memory (facts/preferences) is always
// prepended when use_memory is set; document retrieval runs additionally when use_rag is set, with
// per-request top_k/min_score/kinds overrides. When rag_agents is empty the document block is
// prepended too; otherwise it is returned in RagBlock for the mode to route to those agents.
// Memory always prepends to the prompt regardless of targeting.
func BuildRAG(req Request, effPrompt string, retriever Retriever, base rag.Settings) RagResult {
	out := RagResult{EffectivePrompt: effPrompt}
	memHits := retrieveMemory(req, retriever, base)
	memBlock := rag.RenderContextBlock(memHits)

	if !req.UseRAG {
		out.EffectivePrompt = memBlock + out.EffectivePrompt
		if len(memHits) > 0 {
			out.RagMeta = map[string]any{"requested": false, "used": false, "memory": memoryMeta(memHits)}
		}
		return out
	}
	s := base
	if req.RagTopK > 0 {
		if s.TopK = req.RagTopK; s.TopK > 20 {
			s.TopK = 20
		}
	}
	if req.RagMinScore >= 0 && req.RagMinScore <= 1 {
		s.MinScore = req.RagMinScore
	}
	if len(req.RagKinds) > 0 {
		s.Kinds = req.RagKinds
	}
	if !s.Enabled {
		out.EffectivePrompt = memBlock + out.EffectivePrompt
		out.RagMeta = map[string]any{"requested": true, "used": false, "reason": "rag.enabled is false"}
		return out
	}

	hits := retriever.Retrieve(s, req.Prompt)
	sources := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		sources = append(sources, map[string]any{
			"source_path": h.SourcePath, "chunk_idx": h.ChunkIdx, "distance": h.Distance, "content": h.Content,
		})
	}
	docBlock := rag.RenderContextBlock(hits)
	if len(req.RagAgents) == 0 {
		out.EffectivePrompt = memBlock + docBlock + out.EffectivePrompt
	} else {
		out.EffectivePrompt = memBlock + out.EffectivePrompt
		out.RagBlock = docBlock
	}
	out.RagMeta = map[string]any{
		"requested": true, "used": len(hits) > 0,
		"top_k": s.TopK, "min_score": s.MinScore, "hits": sources,
	}
	if len(memHits) > 0 {
		out.RagMeta["memory"] = memoryMeta(memHits)
	}
	if len(req.RagAgents) > 0 {
		out.RagMeta["targeted_agents"] = req.RagAgents
	}
	return out
}

// StampMeta annotates the mode envelope's meta with dispatch bookkeeping (ports the portable
// subset of dispatch_meta::stamp_envelope — token_budget/tes/agent_metrics are not ported).
func StampMeta(envelope map[string]any, req Request, ragRes RagResult, compaction map[string]any, wallMs float64, routing map[string]any) {
	meta, ok := envelope["meta"].(map[string]any)
	if !ok {
		meta = map[string]any{}
		envelope["meta"] = meta
	}
	meta["session_id"] = req.SessionID
	meta["run_id"] = req.RunID
	meta["followup"] = req.Followup
	meta["wall_ms"] = wallMs
	if ragRes.RagMeta != nil {
		meta["rag"] = ragRes.RagMeta
	}
	if req.QualityPass {
		meta["quality_pass"] = map[string]any{"used": true, "target": targetAgent(req)}
	}
	if req.ParentRunID != "" {
		meta["parent_run_id"] = req.ParentRunID
	}
	if req.Followup && compaction != nil {
		meta["compaction"] = compaction
	}
	if len(routing) > 0 {
		meta["routing"] = routing
	}
}

func targetAgent(req Request) string {
	if t, ok := req.ContextPolicy["target_agent"].(string); ok && t != "" {
		return t
	}
	return "programmer"
}
