#include "rag_embed.h"

#include "blake2b.h"

#include <cmath>
#include <cstdint>

namespace rag {

namespace {

// Python str.split() with no args: split on any whitespace run, drop empties.
// We match it via the standard isspace classification on bytes (matches
// Python's default for ASCII; tokens with multibyte Unicode whitespace are
// rare in source-code RAG and would diverge only on those exotic boundaries).
std::vector<std::string> py_split_whitespace(const std::string& s) {
    std::vector<std::string> out;
    size_t i = 0, n = s.size();
    while (i < n) {
        while (i < n && (unsigned char)s[i] <= ' ' &&
               (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' ||
                s[i] == '\r' || s[i] == '\v' || s[i] == '\f')) {
            ++i;
        }
        if (i >= n) break;
        size_t start = i;
        while (i < n && !(s[i] == ' ' || s[i] == '\t' || s[i] == '\n' ||
                          s[i] == '\r' || s[i] == '\v' || s[i] == '\f')) {
            ++i;
        }
        out.emplace_back(s.substr(start, i - start));
    }
    return out;
}

} // namespace

std::vector<double> hash_embed(const std::string& text) {
    std::vector<double> out(kEmbedDim, 0.0);

    std::vector<std::string> tokens = py_split_whitespace(text);
    if (tokens.empty()) tokens.push_back(text); // matches `or [text]`

    for (const auto& tok : tokens) {
        uint8_t h[16];
        matrix_blake2b(h, sizeof(h), tok.data(), tok.size());
        for (int i = 0; i < 16; ++i) {
            int idx = (i * 7) % kEmbedDim;
            out[idx] += (static_cast<int>(h[i]) - 128) / 128.0;
        }
    }

    double sumsq = 0.0;
    for (double v : out) sumsq += v * v;
    double norm = std::sqrt(sumsq);
    if (norm == 0.0) norm = 1.0;
    for (double& v : out) v /= norm;
    return out;
}

} // namespace rag
