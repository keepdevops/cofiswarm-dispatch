#pragma once
#include "coordinator_routes_includes.h"
#include "coordinator_context.h"
#include "session_store.h"
#include "modes/mode.h"
#include <string>
#include <unordered_set>
#include <vector>

struct DispatchRequest {
    std::string prompt;
    double      temperature      = 0.7;
    bool        followup         = false;
    bool        quality_pass     = false;
    std::string session_id;
    std::string run_id;
    std::string parent_run_id;
    json        context_policy   = json::object();
    bool        use_rag          = false;
    int         rag_top_k        = 0;
    double      rag_min_score    = -1.0;
    std::unordered_set<std::string> rag_agents;
    std::string effective_prompt;
    json        compaction       = json::object();
    double      kv_pressure      = 0.0;  // 0–1; sent by frontend from kvReadings
    bool        rag_rerank       = false;
};

struct RagResult {
    std::string effective_prompt;
    std::string rag_block;
    json        rag_meta = json::object();
};

DispatchRequest dispatch_parse_request(const json& body);

// Applies RAG retrieval and follow-up context; may update effective_prompt.
RagResult dispatch_build_rag(const DispatchRequest& req, CoordinatorState& st);

// dispatch_write_history is defined inline in coordinator_routes_dispatch_history.h
