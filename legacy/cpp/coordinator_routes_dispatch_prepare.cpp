#include "coordinator_routes_dispatch_prepare.h"
#include "coordinator_routes_dispatch_history.h"
#include "rag_client.h"
#include "rag_config.h"
#include "rag_rerank.h"
#include "rag_trajectory.h"
#include "session_context.h"
#include "session_store.h"
#include <chrono>
#include <iostream>
#include <unordered_set>

DispatchRequest dispatch_parse_request(const json& body) {
    DispatchRequest r;
    r.prompt          = body.value("prompt", "");
    r.temperature     = body.value("temperature", 0.7);
    r.followup        = body.value("followup", false);
    r.quality_pass    = body.value("quality_pass", false);
    r.session_id      = body.value("session_id", std::string(""));
    r.parent_run_id   = body.value("parent_run_id", std::string(""));
    r.context_policy  = body.value("context_policy", json::object());
    r.use_rag         = body.value("use_rag", false);
    r.rag_top_k       = body.value("rag_top_k", 0);
    r.rag_min_score   = body.value("rag_min_score", -1.0);
    r.rag_rerank      = body.value("rag_rerank", false);
    if (body.contains("rag_agents") && body["rag_agents"].is_array()) {
        for (const auto& a : body["rag_agents"])
            if (a.is_string()) r.rag_agents.insert(a.get<std::string>());
    }
    r.kv_pressure = body.value("kv_pressure", 0.0);
    if (r.session_id.empty()) r.session_id = session_new_id("sess");
    r.run_id = session_new_id("run");
    return r;
}

RagResult dispatch_build_rag(const DispatchRequest& req, CoordinatorState& st) {
    RagResult result;
    result.effective_prompt = req.effective_prompt;
    if (!req.use_rag) return result;

    rag::Settings rag_s = rag::settings_from_config(st.startup_config);
    if (req.rag_top_k > 0) rag_s.top_k = std::min(req.rag_top_k, 20);
    if (req.rag_min_score >= 0.0 && req.rag_min_score <= 1.0)
        rag_s.min_score = req.rag_min_score;

    if (!rag_s.enabled) {
        result.rag_meta = {{"requested", true}, {"used", false},
                           {"reason", "rag.enabled is false in coordinator config"}};
        return result;
    }

    auto hits = rag::retrieve(rag_s, req.prompt);

    json sources = json::array();
    if (req.rag_rerank && !hits.empty()) {
        auto scored = rag_rerank::rerank(req.prompt, hits);
        std::vector<rag::Hit> reordered;
        reordered.reserve(scored.size());
        for (const auto& s : scored) {
            reordered.push_back(s.hit);
            sources.push_back({{"source_path", s.hit.source_path},
                               {"chunk_idx",   s.hit.chunk_idx},
                               {"distance",    s.hit.distance},
                               {"relevance",   s.relevance},
                               {"content",     s.hit.content}});
        }
        hits = std::move(reordered);
    } else {
        for (const auto& h : hits)
            sources.push_back({{"source_path", h.source_path}, {"chunk_idx", h.chunk_idx},
                               {"distance", h.distance}, {"content", h.content}});
    }

    std::string block = rag::render_context_block(hits);
    if (!block.empty()) {
        if (req.rag_agents.empty()) result.effective_prompt = block + result.effective_prompt;
        else result.rag_block = block;
    }

    json rag_agents_arr = json::array();
    for (const auto& n : req.rag_agents) rag_agents_arr.push_back(n);

    result.rag_meta = {{"requested", true}, {"used", !hits.empty()},
                       {"top_k", rag_s.top_k}, {"min_score", rag_s.min_score},
                       {"reranked", req.rag_rerank}, {"hits", sources}};
    if (!req.rag_agents.empty()) result.rag_meta["targeted_agents"] = rag_agents_arr;

    // Record RAG trajectory for distillation pipeline
    if (!hits.empty()) {
        long long now_ms = std::chrono::duration_cast<std::chrono::milliseconds>(
            std::chrono::system_clock::now().time_since_epoch()).count();
        rag_trajectory::Entry traj;
        traj.session_id   = req.session_id;
        traj.run_id       = req.run_id;
        traj.query        = req.prompt;
        traj.hits         = sources;
        traj.reranked     = req.rag_rerank;
        traj.timestamp_ms = now_ms;
        rag_trajectory::record(std::move(traj));
    }

    return result;
}
