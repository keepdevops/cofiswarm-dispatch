#pragma once
// Progressive compaction — drops lowest-importance history runs first
// when session context exceeds a target character budget.

#include "symbolic_importance.h"
#include "json.hpp"
#include <algorithm>
#include <map>
#include <string>
#include <vector>

namespace session_compaction {

/// Score a session run by symbolic importance of its text content.
inline double score_run(const nlohmann::json& run) {
    std::map<std::string, std::string> outputs;
    if (run.contains("agents") && run["agents"].is_object()) {
        for (const auto& [k, v] : run["agents"].items())
            if (v.is_string()) outputs[k] = v.get<std::string>();
    }
    if (run.contains("final") && run["final"].is_string())
        outputs["_final"] = run["final"].get<std::string>();
    if (outputs.empty()) return 0.1; // minimal importance for prompt-only
    auto ranked = symbolic_importance::rank(outputs);
    return symbolic_importance::average(ranked);
}

/// Count approximate chars in a run's text content.
inline size_t run_chars(const nlohmann::json& run) {
    size_t n = run.value("prompt", "").size();
    if (run.contains("agents") && run["agents"].is_object())
        for (const auto& [k, v] : run["agents"].items())
            if (v.is_string()) n += v.get<std::string>().size();
    if (run.contains("final") && run["final"].is_string())
        n += run["final"].get<std::string>().size();
    return n;
}

/// Return run_ids to drop to meet target_chars budget.
/// Drops lowest-scoring runs first; preserves the most recent run always.
inline std::vector<std::string> runs_to_drop(
    const nlohmann::json& session_runs,
    size_t target_chars)
{
    if (!session_runs.is_array() || session_runs.empty()) return {};

    // Score all runs
    std::vector<std::pair<double, size_t>> scored; // (score, index)
    size_t total_chars = 0;
    for (size_t i = 0; i < session_runs.size(); ++i) {
        scored.emplace_back(score_run(session_runs[i]), i);
        total_chars += run_chars(session_runs[i]);
    }
    if (total_chars <= target_chars) return {};

    // Sort by score ascending (drop weakest first), but always keep last run
    std::sort(scored.begin(), scored.end(),
              [&](const auto& a, const auto& b) { return a.first < b.first; });

    std::vector<std::string> to_drop;
    for (auto& [sc, idx] : scored) {
        if (idx == session_runs.size() - 1) continue; // never drop latest
        if (total_chars <= target_chars) break;
        const auto& run = session_runs[idx];
        std::string run_id = run.value("run_id", "");
        if (!run_id.empty()) to_drop.push_back(run_id);
        total_chars -= run_chars(run);
    }
    return to_drop;
}

} // namespace session_compaction
