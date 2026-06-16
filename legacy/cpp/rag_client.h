#pragma once
// libpq-backed retrieval client. Embeds the query via rag_embed and runs the
// same cosine-distance ANN query as orchestration/rag/store.py:97-105.
//
// All DB errors are logged via std::cerr and degrade to an empty result set —
// the coordinator must remain available when pgvector is unreachable.

#include "json.hpp"
#include "rag_config.h"

#include <string>
#include <vector>

namespace rag {

struct Hit {
    long long  id          = 0;
    std::string source_path;
    int         chunk_idx  = 0;
    std::string content;
    double      distance   = 0.0;
};

/// Run a retrieve against pgvector. Returns at most `top_k` hits ordered by
/// ascending cosine distance, filtered by `min_score` (distance <= min_score).
/// Returns an empty vector on any error (always logged) or when disabled.
std::vector<Hit> retrieve(const Settings& s, const std::string& query);

/// Format the hits as a Markdown context block to prepend to a prompt. Empty
/// when no hits. Used by dispatch when use_rag is true.
std::string render_context_block(const std::vector<Hit>& hits);

/// Liveness probe. Runs `SELECT 1` against the same DSN/connection used by
/// retrieve(). Returns true on success; on failure, fills `error_out` (when
/// non-null) with the libpq error message and logs it.
bool health_check(const Settings& s, std::string* error_out);

/// Test seam: close any cached connection. Safe to call from a single thread.
void shutdown_for_test();

/// Build a pgvector literal "[a,b,...]" with 6-decimal precision, matching
/// orchestration/rag/store.py:_vec_literal. Exposed for unit tests.
std::string vec_literal(const std::vector<double>& v);

} // namespace rag
