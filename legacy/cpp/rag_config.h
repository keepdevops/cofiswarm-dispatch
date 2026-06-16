#pragma once
// Parses the optional `rag:` block from the coordinator's active JSON config,
// plus the RAG_DSN env override. Mirrors orchestration/rag/store.py defaults.

#include "json.hpp"

#include <string>

namespace rag {

struct Settings {
    bool enabled       = false;
    int  top_k         = 3;
    double min_score   = 1.0;   // cosine distance ceiling (1.0 = no filter)
    std::string embedder  = "hash";
    std::string dsn;
    // URL of the ingest sidecar /embed endpoint; used when embedder == "mlx".
    std::string embed_url = "http://127.0.0.1:8001/embed";
};

// `root` is the full coordinator config JSON (the same object loaded from
// matrix-active-config.json). Looks for a top-level "rag" object; missing
// values fall back to defaults. RAG_DSN env always overrides any dsn key.
Settings settings_from_config(const nlohmann::json& root);

} // namespace rag
