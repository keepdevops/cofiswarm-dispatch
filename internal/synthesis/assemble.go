package synthesis

import (
	"log"
	"strings"
)

// assembleFit joins prefix + per-block (header+body) + footer into one string no larger than
// maxTotal. When it overflows, each body is trimmed proportionally to its share of the available
// room (min 128 chars each, redistributing slack), preserving the fixed prefix/headers/footer.
// Mutates bodies in place, mirroring the monolith's assemble_fit.
func assembleFit(prefix string, headers, bodies []string, footer string, maxTotal int) string {
	if len(bodies) != len(headers) {
		return prefix + footer
	}

	overhead := len(prefix) + len(footer)
	for _, h := range headers {
		overhead += len(h)
	}

	// Budget smaller than the fixed overhead: aggressively trim everything.
	if maxTotal <= overhead+16 {
		log.Print("[synthesis] context budget smaller than fixed overhead; aggressive trim")
		var b strings.Builder
		b.WriteString(truncateNote(prefix, maxTotal/3))
		for i := range bodies {
			b.WriteString(headers[i])
			b.WriteString(truncateNote(bodies[i], 256))
		}
		b.WriteString(footer)
		return truncateNote(b.String(), maxTotal)
	}

	if totalLen(prefix, headers, bodies, footer) <= maxTotal {
		return join(prefix, headers, bodies, footer)
	}

	room := maxTotal - overhead
	totalBody := 0
	for _, b := range bodies {
		totalBody += len(b)
	}
	if totalBody > room {
		alloc := make([]int, len(bodies))
		claimed := 0
		for i, b := range bodies {
			alloc[i] = room * len(b) / totalBody
			if alloc[i] < 128 {
				alloc[i] = 128
			}
			claimed += min(len(b), alloc[i])
		}
		slack := 0
		if room > claimed {
			slack = room - claimed
		}
		for i := range bodies {
			if slack == 0 {
				break
			}
			if len(bodies[i]) > alloc[i] {
				extra := min(slack, len(bodies[i])-alloc[i])
				alloc[i] += extra
				slack -= extra
			}
		}
		for i := range bodies {
			if len(bodies[i]) > alloc[i] {
				bodies[i] = truncateNote(bodies[i], alloc[i])
			}
		}
	}

	out := join(prefix, headers, bodies, footer)
	if len(out) > maxTotal {
		out = truncateNote(out, maxTotal)
	}
	log.Printf("[synthesis] reduced synthesizer prompt to fit ~%d approximate tokens "+
		"(set COFISWARM_SYNTHESIS_MAX_PROMPT_TOKENS or raise per-agent context / deploy)", maxTotal/4)
	return out
}

func totalLen(prefix string, headers, bodies []string, footer string) int {
	t := len(prefix) + len(footer)
	for i := range bodies {
		t += len(headers[i]) + len(bodies[i])
	}
	return t
}

func join(prefix string, headers, bodies []string, footer string) string {
	var b strings.Builder
	b.WriteString(prefix)
	for i := range bodies {
		b.WriteString(headers[i])
		b.WriteString(bodies[i])
	}
	b.WriteString(footer)
	return b.String()
}
