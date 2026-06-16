#pragma once
#include "coordinator_routes_dispatch_prepare.h"
#include "coordinator_routes_includes.h"
#include "agent_metrics.h"
#include "token_ledger.h"
#include "tes.h"
#include <chrono>
#include <vector>
#include <string>

namespace dispatch_meta {

inline void stamp_envelope(
    json& envelope,
    const DispatchRequest& dreq,
    const RagResult& rag,
    const std::vector<std::string>& excluded_unhealthy,
    const std::string& qp_target,
    std::chrono::steady_clock::time_point dispatch_t0,
    int effective_max_select = -1)
{
    if (!envelope.contains("meta") || !envelope["meta"].is_object())
        envelope["meta"] = json::object();
    if (!excluded_unhealthy.empty())
        envelope["meta"]["excluded_unhealthy"] = excluded_unhealthy;
    envelope["meta"]["session_id"] = dreq.session_id;
    envelope["meta"]["run_id"]     = dreq.run_id;
    envelope["meta"]["followup"]   = dreq.followup;
    if (!rag.rag_meta.empty())     envelope["meta"]["rag"] = rag.rag_meta;
    if (dreq.quality_pass)
        envelope["meta"]["quality_pass"] = {{"used", true}, {"target", qp_target}};
    if (!dreq.parent_run_id.empty()) envelope["meta"]["parent_run_id"] = dreq.parent_run_id;
    if (dreq.followup)               envelope["meta"]["compaction"]     = dreq.compaction;
    {
        auto t1 = std::chrono::steady_clock::now();
        double total_ms = std::chrono::duration<double, std::milli>(t1 - dispatch_t0).count();
        envelope["meta"]["timings"] = agent_metrics::snapshot();
        envelope["meta"]["wall_ms"] = total_ms;
    }
    // Token budget snapshot
    if (!dreq.session_id.empty()) {
        envelope["meta"]["token_budget"] = token_ledger::snapshot(dreq.session_id);
    }
    if (effective_max_select >= 0)
        envelope["meta"]["effective_max_select"] = effective_max_select;

    // TES computed after token_budget is written
    double tes_val = tes::compute(envelope["meta"]);
    if (tes_val > 0.0)
        envelope["meta"]["tes"] = tes_val;
}

} // namespace dispatch_meta
