package httpapi

import (
	"github.com/keepdevops/cofiswarm-dispatch/internal/agent"
	"github.com/keepdevops/cofiswarm-dispatch/internal/prepare"
)

// mergeRosterRAG folds per-agent RAG targeting from the agent-registry roster into the request:
// any agent whose roster entry has use_rag=true is added to the targeted set (req.RagAgents) and
// RAG is enabled, so the prepare step retrieves context for those agents even when the caller did
// not ask. Request-level intent wins — an explicit rag_top_k / rag_kinds is never overwritten, and
// already-targeted agents are not duplicated. Fails open: an empty roster (registry down) leaves
// the request untouched (per-agent RAG simply stays off).
func mergeRosterRAG(req prepare.Request, roster []agent.Agent) prepare.Request {
	if len(roster) == 0 {
		return req
	}
	targeted := make(map[string]bool, len(req.RagAgents))
	for _, n := range req.RagAgents {
		targeted[n] = true
	}
	var added []string
	maxTopK := 0
	seenKind := map[string]bool{}
	var kinds []string
	for _, a := range roster {
		if !a.UseRAG {
			continue
		}
		if !targeted[a.Name] {
			added = append(added, a.Name)
			targeted[a.Name] = true
		}
		if a.RagTopK > maxTopK {
			maxTopK = a.RagTopK
		}
		for _, k := range a.RagKinds {
			if !seenKind[k] {
				seenKind[k] = true
				kinds = append(kinds, k)
			}
		}
	}
	if len(added) == 0 {
		return req // no roster agent opts into RAG (or all already targeted)
	}
	req.UseRAG = true
	req.RagAgents = append(req.RagAgents, added...)
	if req.RagTopK == 0 {
		req.RagTopK = maxTopK
	}
	if len(req.RagKinds) == 0 {
		req.RagKinds = kinds
	}
	return req
}
