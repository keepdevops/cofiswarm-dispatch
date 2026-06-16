#pragma once
#include "json.hpp"
#include <string>
#include <vector>

namespace rag {

// Parse "http://host:port/path" → components. Returns false on malformed URL.
bool parse_http_url(const std::string& url, std::string& host,
                    int& port, std::string& path);

// Call the ingest sidecar's /embed endpoint for a semantic query vector.
// Returns empty on any network or parse error (always logged).
std::vector<double> mlx_embed(const std::string& embed_url,
                               const std::string& query);

// Format a pgvector literal from a dense float vector: "[v0,v1,...]".
std::string vec_literal(const std::vector<double>& v);

} // namespace rag
