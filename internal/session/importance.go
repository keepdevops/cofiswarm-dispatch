package session

import (
	"sort"
	"strings"
)

// Symbolic importance scoring for agent outputs (ported from the monolith's
// symbolic_importance.h, whose impl lives in the proxy/coordinator cpp_core, not dispatch's
// legacy/). Training-free: combines a length norm with a lexical-diversity (entropy) proxy.

// scored pairs an agent name with its importance score.
type scored struct {
	Name  string
	Score float64
}

// normScore is the length score relative to the peer average, capped at 1.0.
func normScore(text string, peerAvgLen float64) float64 {
	if peerAvgLen <= 0 || text == "" {
		return 0
	}
	if v := float64(len(text)) / (peerAvgLen * 1.5); v < 1.0 {
		return v
	}
	return 1.0
}

// entropyScore is unique tokens / total tokens (lexical diversity), 0–1.
func entropyScore(text string) float64 {
	if text == "" {
		return 0
	}
	tokens := strings.Fields(text)
	if len(tokens) == 0 {
		return 0
	}
	unique := make(map[string]struct{}, len(tokens))
	for _, t := range tokens {
		unique[t] = struct{}{}
	}
	return float64(len(unique)) / float64(len(tokens))
}

// importanceScore is the combined 0–1 score: 0.6*norm + 0.4*entropy.
func importanceScore(text string, peerAvgLen float64) float64 {
	return 0.6*normScore(text, peerAvgLen) + 0.4*entropyScore(text)
}

// rankOutputs ranks agent→text by combined importance, descending (ties broken by name for
// determinism — C++ std::sort left ties unspecified).
func rankOutputs(outputs map[string]string) []scored {
	if len(outputs) == 0 {
		return nil
	}
	var avgLen float64
	for _, v := range outputs {
		avgLen += float64(len(v))
	}
	avgLen /= float64(len(outputs))

	ranked := make([]scored, 0, len(outputs))
	for k, v := range outputs {
		ranked = append(ranked, scored{Name: k, Score: importanceScore(v, avgLen)})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}
		return ranked[i].Name < ranked[j].Name
	})
	return ranked
}

// averageImportance averages the ranked scores.
func averageImportance(ranked []scored) float64 {
	if len(ranked) == 0 {
		return 0
	}
	var sum float64
	for _, p := range ranked {
		sum += p.Score
	}
	return sum / float64(len(ranked))
}
