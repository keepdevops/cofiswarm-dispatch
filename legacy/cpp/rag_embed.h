#pragma once
// Deterministic hash-based 768-d embedder. Byte-exact port of the Python
// HashEmbedder in orchestration/rag/embed.py — same vectors → meaningful
// retrieval against indexes built by `matrixctl rag index --embedder hash`.

#include <string>
#include <vector>

namespace rag {

constexpr int kEmbedDim = 768;

// Returns a length-kEmbedDim L2-normalised vector. Tokenisation is Python
// str.split() semantics (whitespace, drop empties; if no tokens, use whole
// string as a single token, matching `tokens = text.split() or [text]`).
std::vector<double> hash_embed(const std::string& text);

} // namespace rag
