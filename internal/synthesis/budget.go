// Package synthesis ports the monolith's synthesizer prompt budgeting + tiered reduction
// (legacy/cpp/synthesis_budget.cpp, synthesis_budget_assemble.cpp, synthesis_tiered.cpp): it
// assembles a single synthesizer prompt from multiple agent contributions, trimming each
// proportionally to fit the synthesizer's context budget, and optionally reduces many
// contributions pairwise to stay within budget.
package synthesis

import (
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/keepdevops/cofiswarm-dispatch/internal/agent"
)

const truncMarker = "\n\n[…truncated for synthesizer context budget]"

// Block is one labeled contribution (was std::pair<string,string>: label, body).
type Block struct {
	Label string
	Body  string
}

// EffectiveMaxPromptChars bounds the synthesizer *user* message size (chars). Honors
// COFISWARM_SYNTHESIS_MAX_PROMPT_TOKENS (renamed from MATRIX_); else derives from the
// synthesizer's context window minus max_tokens and a fixed reserve. synth may be nil.
func EffectiveMaxPromptChars(synth *agent.Agent) int {
	if e := os.Getenv("COFISWARM_SYNTHESIS_MAX_PROMPT_TOKENS"); e != "" {
		if v, err := strconv.Atoi(e); err == nil {
			if v >= 256 && v <= 262144 {
				return v * 4
			}
		} else {
			log.Printf("[synthesis] COFISWARM_SYNTHESIS_MAX_PROMPT_TOKENS=%q not an int; ignoring", e)
		}
	}
	if synth != nil && synth.ContextWindow >= 512 {
		ctx := synth.ContextWindow
		mt := synth.MaxTokens
		if mt < 1 {
			mt = 1
		}
		const reserve = 768
		if ctx <= mt+reserve+64 {
			return 256 * 4
		}
		avail := ctx - mt - reserve
		avail = clamp(avail, 256, 262144)
		return avail * 4
	}
	return 1400 * 4
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// truncateNote trims s to at most maxChars, appending the budget marker unless the cap is tiny.
// Trims to a valid UTF-8 boundary so a multi-byte rune is never split.
func truncateNote(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	if maxChars <= 80 {
		return strings.ToValidUTF8(s[:maxChars], "")
	}
	head := strings.ToValidUTF8(s[:maxChars-1], "")
	return head + truncMarker
}

// pipelineFooter / cascadeFooter / streamFooter preserve the monolith's exact wording per path.
const (
	pipelineFooter = "\n\nProduce ONE consolidated answer that integrates the above contributions. " +
		"Resolve contradictions, drop redundancy, and keep only the strongest material. " +
		"Do not enumerate the stages — write the final answer directly."
	cascadeFooter = "\n\nProduce ONE consolidated answer that integrates the above contributions. " +
		"Resolve contradictions, drop redundancy, and keep only the strongest material. " +
		"Do not enumerate the responders — write the final answer directly."
	streamFooter = "\n\nProduce ONE consolidated answer that integrates the above contributions. " +
		"Resolve contradictions, drop redundancy. Write the final answer directly."
)

// BuildPipelinePrompt builds the pipeline (staged) synthesis prompt.
func BuildPipelinePrompt(userPrompt string, stages []Block, synth *agent.Agent) string {
	return buildPrompt(userPrompt, "\n>>>\n\nThe following agents produced staged outputs:\n",
		stages, "Stage", pipelineFooter, synth)
}

// BuildCascadePrompt builds the cascade (parallel) synthesis prompt.
func BuildCascadePrompt(userPrompt string, responses []Block, synth *agent.Agent) string {
	return buildPrompt(userPrompt, "\n>>>\n\nThe following agents responded in parallel:\n",
		responses, "Response", cascadeFooter, synth)
}

// BuildStreamPrompt builds the SSE-stream synthesis prompt (numbered blocks).
func BuildStreamPrompt(userPrompt string, contributors []Block, synth *agent.Agent) string {
	return buildPrompt(userPrompt, "\n>>>\n\nThe following agents contributed:\n",
		contributors, "", streamFooter, synth)
}

func buildPrompt(userPrompt, intro string, blocks []Block, kind, footer string, synth *agent.Agent) string {
	prefix := "Original user request:\n<<<\n" + userPrompt + intro
	headers := make([]string, len(blocks))
	bodies := make([]string, len(blocks))
	for i, b := range blocks {
		n := strconv.Itoa(i + 1)
		if kind == "" { // stream path: "--- N (label) ---"
			headers[i] = "\n--- " + n + " (" + b.Label + ") ---\n"
		} else {
			headers[i] = "\n--- " + kind + " " + n + " (" + b.Label + ") ---\n"
		}
		bodies[i] = b.Body
	}
	return assembleFit(prefix, headers, bodies, footer, EffectiveMaxPromptChars(synth))
}
