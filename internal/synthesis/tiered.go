package synthesis

import (
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/keepdevops/cofiswarm-dispatch/internal/agent"
)

// Caller invokes the synthesizer agent with a prompt. *agent.Client satisfies it; the indirection
// keeps tiered reduction testable without a live backend.
type Caller interface {
	Call(a agent.Agent, prompt string) string
}

// The ported agent client is the production Caller — guaranteed at compile time.
var _ Caller = (*agent.Client)(nil)

// EnabledViaEnv reports whether tiered pairwise synthesis is on
// (COFISWARM_SYNTHESIS_TIERED in {1,true,yes,on}, case-insensitive; renamed from MATRIX_).
func EnabledViaEnv() bool {
	switch strings.ToLower(os.Getenv("COFISWARM_SYNTHESIS_TIERED")) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func mergePrompt(synth agent.Agent, userPrompt string, blk []Block, pipelineLabels bool) string {
	if pipelineLabels {
		return BuildPipelinePrompt(userPrompt, blk, &synth)
	}
	return BuildCascadePrompt(userPrompt, blk, &synth)
}

// ReducePairwise merges contributions two at a time, round by round, until a single answer
// remains — each merge fits the synthesizer's budget via mergePrompt. Bounded at 64 rounds.
func ReducePairwise(caller Caller, synth agent.Agent, userPrompt string, blocks []Block, pipelineLabels bool) string {
	if len(blocks) == 0 {
		return ""
	}
	if len(blocks) == 1 {
		return caller.Call(synth, mergePrompt(synth, userPrompt, blocks[:1], pipelineLabels))
	}

	round := 0
	for len(blocks) > 1 {
		round++
		next := make([]Block, 0, (len(blocks)+1)/2)
		for i := 0; i < len(blocks); i += 2 {
			if i+1 < len(blocks) {
				pair := []Block{blocks[i], blocks[i+1]}
				merged := caller.Call(synth, mergePrompt(synth, userPrompt, pair, pipelineLabels))
				next = append(next, Block{Label: "merge_r" + strconv.Itoa(round) + "_" + strconv.Itoa(len(next)), Body: merged})
			} else {
				next = append(next, blocks[i]) // odd one out carries forward
			}
		}
		blocks = next
		if round > 64 {
			log.Print("[synthesis] aborting pairwise reduction after 64 rounds")
			break
		}
	}
	return blocks[0].Body
}
