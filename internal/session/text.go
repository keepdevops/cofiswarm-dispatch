package session

import (
	"fmt"
	"sort"
	"strings"
)

// Text helpers ported from legacy/cpp/session_store_text.cpp. Byte-based to mirror the C++
// std::string semantics; slices that could split a multi-byte rune are revalidated.

func jsonString(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

// includeName reports whether a section is enabled. With no "include" array in the policy, all
// sections are on; otherwise only the named ones.
func includeName(policy map[string]any, name string) bool {
	list, ok := policy["include"].([]any)
	if !ok {
		return true
	}
	for _, item := range list {
		if s, ok := item.(string); ok && s == name {
			return true
		}
	}
	return false
}

// trimBlock keeps the head (2/3) and tail (1/3) around a compaction marker when s exceeds
// maxChars; tiny budgets just hard-truncate.
func trimBlock(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	if maxChars < 256 {
		return strings.ToValidUTF8(s[:maxChars], "")
	}
	head := maxChars * 2 / 3
	tail := maxChars - head
	return strings.ToValidUTF8(s[:head], "") +
		fmt.Sprintf("\n\n[...compacted: omitted %d chars...]\n\n", len(s)-maxChars) +
		strings.ToValidUTF8(s[len(s)-tail:], "")
}

// firstLines keeps up to maxLines lines, capped at maxChars bytes, with a marker if truncated.
func firstLines(s string, maxLines, maxChars int) string {
	var out []byte
	lines := 0
	for i := 0; i < len(s); i++ {
		if len(out) >= maxChars {
			break
		}
		out = append(out, s[i])
		if s[i] == '\n' {
			lines++
			if lines >= maxLines {
				break
			}
		}
	}
	res := strings.ToValidUTF8(string(out), "")
	if len(s) > len(out) {
		res += "\n[...compacted...]"
	}
	return res
}

func appendSection(b *strings.Builder, title, body string) {
	if body == "" {
		return
	}
	b.WriteString("\n\n## " + title + "\n" + body)
}

func firstPromptForSession(sessions map[string]any, sessionID string) string {
	sess, ok := sessions[sessionID].(map[string]any)
	if !ok {
		return ""
	}
	runs, ok := sess["runs"].([]any)
	if !ok || len(runs) == 0 {
		return ""
	}
	first, _ := runs[0].(map[string]any)
	return jsonString(first, "prompt")
}

// policyInt reads an int policy field (JSON numbers arrive as float64), with a default.
func policyInt(policy map[string]any, key string, def int) int {
	switch n := policy[key].(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return def
	}
}

func policyStr(policy map[string]any, key, def string) string {
	if s, ok := policy[key].(string); ok && s != "" {
		return s
	}
	return def
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
