#pragma once
#include "coordinator_routes_dispatch_prepare.h"
#include "coordinator_routes_internal.h"
#include "code_fence_normalize.h"
#include <string>

inline void dispatch_write_history(
        CoordinatorState& st,
        const json& envelope,
        const std::string& user_prompt,
        double temperature,
        long long timestamp_ms,
        const std::string& session_id,
        const std::string& run_id,
        const std::string& parent_run_id,
        const std::string& effective_prompt,
        bool followup,
        bool quality_pass,
        const std::string& mode_name,
        const json& compaction) {
    json entry = envelope.value("agents", json::object());
    entry["prompt"]      = user_prompt;
    entry["temperature"] = temperature;
    entry["timestamp"]   = timestamp_ms;
    if (!envelope.value("final", json()).is_null()) entry["_final"] = envelope["final"];
    if (envelope.contains("mode"))  entry["_mode"]   = envelope["mode"];
    if (envelope.contains("meta"))  entry["_meta"]   = envelope["meta"];
    entry["_session_id"] = session_id;
    entry["_run_id"]     = run_id;
    code_fence::normalize_agents_in_entry(entry);
    {
        std::lock_guard<std::mutex> lock(st.history_mutex);
        st.history.push_back(entry);
        coordinator_save_history(st);
    }
    {
        std::lock_guard<std::mutex> lock(st.sessions_mutex);
        json run = {
            {"run_id",           run_id},
            {"parent_run_id",    parent_run_id},
            {"prompt",           user_prompt},
            {"effective_prompt", effective_prompt},
            {"followup",         followup},
            {"quality_pass",     quality_pass},
            {"mode",             mode_name},
            {"agents", [&]() {
                json agents = envelope.value("agents", json::object());
                code_fence::normalize_agents_in_entry(agents);
                return agents;
            }()},
            {"final",     envelope.value("final", json(nullptr))},
            {"timestamp", timestamp_ms}
        };
        if (followup) run["compaction"] = compaction;
        session_append_run(st.sessions, session_id, run);
        coordinator_save_sessions(st);
    }
}
