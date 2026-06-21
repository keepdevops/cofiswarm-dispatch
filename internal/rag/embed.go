package rag

import (
	"log"
	"math"
	"strconv"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// HashEmbed is the deterministic 768-d hash embedder — a byte-exact port of the monolith's
// hash_embed (itself a port of the Python HashEmbedder): BLAKE2b-128 per whitespace token,
// scattered into the vector, then L2-normalized. Same vectors → retrieval against hash indexes.
//
// NOTE: BLAKE2b with digest_size=16 mixes the output length into its IV, so it is NOT the first
// 16 bytes of BLAKE2b-256 — blake2b.New(16, nil) is required for byte-exactness.
func HashEmbed(text string) []float64 {
	out := make([]float64, EmbedDim)

	tokens := pySplitWhitespace(text)
	if len(tokens) == 0 {
		tokens = []string{text} // matches Python `tokens = text.split() or [text]`
	}
	for _, tok := range tokens {
		hasher, err := blake2b.New(16, nil)
		if err != nil { // only fails for an out-of-range size (16 is valid) — fail loud, never silent
			log.Fatalf("rag: blake2b.New(16): %v", err)
		}
		_, _ = hasher.Write([]byte(tok))
		h := hasher.Sum(nil) // 16-byte BLAKE2b-128 digest
		for i := 0; i < 16; i++ {
			idx := (i * 7) % EmbedDim
			out[idx] += (float64(h[i]) - 128) / 128.0
		}
	}

	sumsq := 0.0
	for _, v := range out {
		sumsq += v * v
	}
	norm := math.Sqrt(sumsq)
	if norm == 0 {
		norm = 1
	}
	for i := range out {
		out[i] /= norm
	}
	return out
}

// pySplitWhitespace mirrors Python str.split() with no args: split on any ASCII whitespace run,
// dropping empties.
func pySplitWhitespace(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			return true
		default:
			return false
		}
	})
}

// VecLiteral builds a pgvector literal "[a,b,...]" with 6-decimal precision, matching
// orchestration/rag/store.py._vec_literal and the monolith's vec_literal.
func VecLiteral(v []float64) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(x, 'f', 6, 64))
	}
	b.WriteByte(']')
	return b.String()
}
