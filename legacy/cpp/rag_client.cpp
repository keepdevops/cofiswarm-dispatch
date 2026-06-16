#include "rag_client.h"
#include "rag_client_conn.h"
#include "rag_client_http.h"
#include "rag_embed.h"

#include <iostream>
#include <sstream>

namespace rag {

constexpr const char* kSearchSql =
    "SELECT id, source_path, chunk_idx, content, "
    "       embedding <=> $1::vector AS distance "
    "  FROM chunks "
    " ORDER BY embedding <=> $1::vector "
    " LIMIT $2";

std::vector<Hit> retrieve(const Settings& s, const std::string& query) {
    std::vector<Hit> hits;
    if (!s.enabled || query.empty()) return hits;
    std::vector<double> emb;
    if (s.embedder == "hash") {
        emb = hash_embed(query);
    } else if (s.embedder == "mlx" || s.embedder == "bge") {
        emb = mlx_embed(s.embed_url, query);
        if (emb.empty()) return hits;
    } else {
        std::cerr << "❌ [rag] unknown embedder '" << s.embedder
                  << "'; supported: hash, mlx, bge" << std::endl;
        return hits;
    }
    std::string lit = vec_literal(emb);
    std::string k   = std::to_string(s.top_k);

    rag_conn::Conn& c = rag_conn::singleton();
    std::lock_guard<std::mutex> lock(c.mu);
    if (!rag_conn::ensure_open_locked(c, s.dsn)) return hits;

    const char* params[2] = { lit.c_str(), k.c_str() };
    PGresult* res = PQexecParams(
        c.pg, kSearchSql, 2, nullptr, params, nullptr, nullptr, 0);
    if (!res || PQresultStatus(res) != PGRES_TUPLES_OK) {
        std::cerr << "❌ [rag] search failed: "
                  << (res ? PQresultErrorMessage(res) : "no result") << std::endl;
        if (res) PQclear(res);
        return hits;
    }
    int rows = PQntuples(res);
    hits.reserve(rows);
    for (int i = 0; i < rows; ++i) {
        Hit h;
        h.id          = std::strtoll(PQgetvalue(res, i, 0), nullptr, 10);
        h.source_path = PQgetvalue(res, i, 1);
        h.chunk_idx   = std::atoi(PQgetvalue(res, i, 2));
        h.content     = PQgetvalue(res, i, 3);
        h.distance    = std::strtod(PQgetvalue(res, i, 4), nullptr);
        if (h.distance <= s.min_score) hits.push_back(std::move(h));
    }
    PQclear(res);
    return hits;
}

std::string render_context_block(const std::vector<Hit>& hits) {
    if (hits.empty()) return {};
    std::ostringstream os;
    os << "<context source=\"rag\">\n";
    for (size_t i = 0; i < hits.size(); ++i) {
        os << "[#" << i << " " << hits[i].source_path
           << " chunk=" << hits[i].chunk_idx
           << " distance=" << hits[i].distance << "]\n"
           << hits[i].content << "\n";
    }
    os << "</context>\n\n";
    return os.str();
}

bool health_check(const Settings& s, std::string* error_out) {
    if (!s.enabled) {
        if (error_out) *error_out = "rag.enabled is false";
        return false;
    }
    rag_conn::Conn& c = rag_conn::singleton();
    std::lock_guard<std::mutex> lock(c.mu);
    if (!rag_conn::ensure_open_locked(c, s.dsn)) {
        if (error_out) *error_out = "pgvector connect failed";
        return false;
    }
    PGresult* res = PQexec(c.pg, "SELECT 1");
    bool ok = res && PQresultStatus(res) == PGRES_TUPLES_OK;
    if (!ok) {
        std::string msg = res ? PQresultErrorMessage(res) : "no result";
        std::cerr << "❌ [rag] health probe failed: " << msg << std::endl;
        if (error_out) *error_out = msg;
    }
    if (res) PQclear(res);
    return ok;
}

void shutdown_for_test() {
    rag_conn::Conn& c = rag_conn::singleton();
    std::lock_guard<std::mutex> lock(c.mu);
    if (c.pg) { PQfinish(c.pg); c.pg = nullptr; c.current_dsn.clear(); }
}

} // namespace rag
