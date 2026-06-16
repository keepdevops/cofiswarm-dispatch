#include "rag_config.h"

#include <cstdlib>

namespace rag {

namespace {
constexpr const char* kDefaultDsn =
    "postgresql://matrix:matrix@127.0.0.1:5433/matrix_rag";
constexpr const char* kDefaultEmbedUrl =
    "http://127.0.0.1:8001/embed";

std::string env_or(const char* key, const std::string& fallback) {
    const char* v = std::getenv(key);
    return (v && *v) ? std::string(v) : fallback;
}
} // namespace

Settings settings_from_config(const nlohmann::json& root) {
    Settings s;
    if (root.is_object() && root.contains("rag") && root["rag"].is_object()) {
        const auto& r = root["rag"];
        s.enabled   = r.value("enabled", false);
        s.top_k     = r.value("top_k", 3);
        s.min_score = r.value("min_score", 1.0);
        s.embedder  = r.value("embedder", std::string("hash"));
        s.dsn       = r.value("dsn", std::string(""));
        s.embed_url = r.value("embed_url", std::string(""));
    }
    s.dsn       = env_or("RAG_DSN",       s.dsn.empty()       ? kDefaultDsn       : s.dsn);
    s.embed_url = env_or("RAG_EMBED_URL", s.embed_url.empty() ? kDefaultEmbedUrl  : s.embed_url);
    if (s.top_k < 1)  s.top_k = 1;
    if (s.top_k > 50) s.top_k = 50;
    return s;
}

} // namespace rag
