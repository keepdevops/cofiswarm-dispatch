#pragma once
#include "coordinator_routes_dispatch_prepare.h"
#include "coordinator_routes_internal.h"
#include "coordinator_routes_push.h"
#include "symbolic_importance.h"
#include "rag_embed.h"
#include "kv_auto_clear.h"
#include "rl_trajectory_logger.h"
#include <cmath>
#include <iostream>
#include <map>
#include <string>
#include <thread>

namespace dispatch_post {

// Symbolic importance scoring on agent outputs + RAG similarity enrichment.
// Writes importance / avg_importance / rag_agent_similarities into envelope meta.
inline void score_importance(json& envelope, const DispatchRequest& dreq) {
    std::map<std::string, std::string> outputs;
    if (envelope.contains("agents") && envelope["agents"].is_object()) {
        for (const auto& [k, v] : envelope["agents"].items())
            if (v.is_string()) outputs[k] = v.get<std::string>();
    }
    if (outputs.empty()) return;

    auto ranked = symbolic_importance::rank(outputs);
    double avg_imp = symbolic_importance::average(ranked);
    nlohmann::json imp_json = nlohmann::json::object();
    for (const auto& [name, sc] : ranked) imp_json[name] = sc;
    envelope["meta"]["importance"] = imp_json;
    envelope["meta"]["avg_importance"] = avg_imp;

    // Enrich RAG trajectory with similarity-to-action (hash embed proxy)
    if (dreq.session_id.empty()) return;
    auto q_emb = rag::hash_embed(dreq.prompt);
    std::map<std::string, double> sims;
    for (const auto& [name, text] : outputs) {
        auto r_emb = rag::hash_embed(text);
        if (!q_emb.empty() && !r_emb.empty() && q_emb.size() == r_emb.size()) {
            double dot = 0.0, na = 0.0, nb = 0.0;
            for (size_t i = 0; i < q_emb.size(); ++i) {
                dot += q_emb[i] * r_emb[i];
                na  += q_emb[i] * q_emb[i];
                nb  += r_emb[i] * r_emb[i];
            }
            sims[name] = (na > 0 && nb > 0)
                ? dot / (std::sqrt(na) * std::sqrt(nb)) : 0.0;
        }
    }
    // Update the most-recent trajectory entry for this run
    // (rag_trajectory doesn't support mutable update; store in meta for now)
    if (!sims.empty()) {
        nlohmann::json sim_json = nlohmann::json::object();
        for (const auto& [k, v] : sims) sim_json[k] = v;
        envelope["meta"]["rag_agent_similarities"] = sim_json;
    }
}

// Auto KV clear: fire after dispatch if pressure + query divergence threshold met.
// Proactively bleeds off lowest-importance slots before the full-clear threshold.
inline void maybe_auto_clear_kv(CoordinatorState& st, json& envelope,
                                const DispatchRequest& dreq) {
    if (dreq.kv_pressure <= 0.0) return;
    std::lock_guard<std::mutex> lk(st.kv_auto_clear_mutex);

    // Proactive partial evict: bleed off lowest-importance slots at 60% before
    // hitting the full-clear threshold at 75%. Uses uniform port pressure (scalar).
    {
        std::map<int,double> port_pres;
        std::map<int,std::string> port_out;
        for (const auto& a : st.agents)
            port_pres[a.port] = dreq.kv_pressure;
        if (kv_auto_clear::maybe_partial_evict(
                st.agents, port_pres, port_out, st.kv_auto_clear_config))
            envelope["meta"]["kv_partial_evict"] = true;
    }

    if (kv_auto_clear::should_clear(
            st.kv_auto_clear_state, dreq.prompt,
            dreq.kv_pressure, st.kv_auto_clear_config)) {
        kv_auto_clear::clear_kv(st.agents);
        envelope["meta"]["auto_clear_kv"] = true;
    }
}

// Assemble + record the RL trajectory for this run, then fire the async
// distillation quality webhook (non-blocking) when configured.
inline void record_trajectory(CoordinatorState& st, const json& envelope,
                              const DispatchRequest& dreq,
                              const std::string& mode_name, long long ms) {
    rl_traj::Trajectory traj;
    traj.session_id       = dreq.session_id;
    traj.run_id           = dreq.run_id;
    traj.mode             = mode_name;
    traj.prompt           = dreq.prompt;
    traj.timestamp_ms     = ms;
    traj.kv_pressure_before = dreq.kv_pressure;
    traj.kv_auto_cleared  = envelope["meta"].value("auto_clear_kv", false);

    const auto& m = envelope["meta"];
    if (m.contains("token_budget") && m["token_budget"].is_object()) {
        traj.tokens_consumed = m["token_budget"].value("consumed", 0);
        traj.budget          = m["token_budget"].value("budget", 0);
    }
    traj.tes = m.value("tes", 0.0);

    if (m.contains("context_gate") && m["context_gate"].value("triggered", false)) {
        traj.gate_triggered = true;
        traj.fidelity_ratio = m["context_gate"].value("fidelity_ratio", 1.0);
    }
    if (m.contains("rag") && m["rag"].is_object()) {
        traj.rag_hits = m["rag"].value("hits", nlohmann::json::array());
        int hits   = (int)traj.rag_hits.size();
        int top_k  = m["rag"].value("top_k", 0);
        traj.rag_hit_rate = (top_k > 0) ? (double)hits / top_k : 0.0;
    }
    if (m.contains("contracts")) traj.contracts = m["contracts"];
    traj.any_overrun = m.value("contract_overrun", false);

    if (m.contains("importance") && m["importance"].is_object())
        for (const auto& [k, v] : m["importance"].items())
            if (v.is_number()) traj.importance_scores[k] = v.get<double>();

    if (envelope.contains("agents") && envelope["agents"].is_object())
        for (const auto& [k, v] : envelope["agents"].items())
            if (v.is_string()) traj.agent_outputs[k] = v.get<std::string>();

    // Capture quality score after record() computes it
    double recorded_quality = -1.0;
    {
        // Peek at the last recorded entry's quality score
        auto snap = rl_traj::snapshot(dreq.session_id);
        if (!snap.empty()) recorded_quality = snap[0].value("quality_score", -1.0);
    }
    rl_traj::record(std::move(traj));

    // Async quality webhook — non-blocking
    if (!st.distillation_push_url.empty()
        && recorded_quality >= st.distillation_quality_threshold) {
        std::string push_url = st.distillation_push_url;
        std::string sid_snap = dreq.session_id;
        std::string rid_snap = dreq.run_id;
        double q = recorded_quality;
        std::thread([push_url, sid_snap, rid_snap, q]() {
            try {
                nlohmann::json payload = {
                    {"event",        "trajectory_quality"},
                    {"session_id",   sid_snap},
                    {"run_id",       rid_snap},
                    {"quality_score", q},
                };
                distillation_push::post_jsonl(
                    push_url + "/api/webhook/trajectory",
                    payload.dump());
            } catch (const std::exception& e) {
                std::cerr << "⚠️ [dispatch] quality webhook failed: "
                          << e.what() << std::endl;
            }
        }).detach();
    }
}

} // namespace dispatch_post
