#pragma once
// POST /api/history/:run_id/fork
// Creates a new session seeded with the context of a past history entry.
// Returns { fork_session_id, source_run_id, prompt }.

#include "coordinator_context.h"
#include "session_store.h"
#include "httplib.h"
#include "json.hpp"
#include <chrono>
#include <string>

inline void register_coordinator_routes_history_fork(httplib::Server& svr,
                                                      CoordinatorState& st) {
    svr.Post(R"(/api/history/([^/]+)/fork)",
             [&st](const httplib::Request& req, httplib::Response& res) {
        res.set_header("Access-Control-Allow-Origin", "*");
        const std::string run_id = req.matches[1];

        // Find the history entry
        nlohmann::json entry;
        {
            std::lock_guard<std::mutex> lk(st.history_mutex);
            for (const auto& e : st.history) {
                if (e.value("_run_id", "") == run_id) { entry = e; break; }
            }
        }
        if (entry.is_null()) {
            res.status = 404;
            res.set_content("{\"error\":\"run_id not found\"}", "application/json");
            return;
        }

        const std::string fork_session_id = session_new_id("fork");
        const std::string prompt = entry.value("prompt", "");

        // Build a synthetic run record for the fork session so followups work
        long long now_ms = std::chrono::duration_cast<std::chrono::milliseconds>(
            std::chrono::system_clock::now().time_since_epoch()).count();
        nlohmann::json run = {
            {"run_id",        session_new_id("run")},
            {"parent_run_id", run_id},
            {"prompt",        prompt},
            {"effective_prompt", prompt},
            {"followup",      false},
            {"quality_pass",  false},
            {"mode",          entry.value("_mode", "")},
            {"agents",        nlohmann::json::object()},
            {"final",         entry.contains("_final") ? entry["_final"] : nlohmann::json(nullptr)},
            {"timestamp",     now_ms},
        };
        // Carry agent responses into run so session_build_continuation can use them
        for (const auto& [k, v] : entry.items()) {
            if (!k.empty() && k[0] != '_' && k != "prompt" && k != "temperature"
                && k != "timestamp" && v.is_string()) {
                run["agents"][k] = v;
            }
        }

        {
            std::lock_guard<std::mutex> lk(st.sessions_mutex);
            session_append_run(st.sessions, fork_session_id, run);
            coordinator_save_sessions(st);
        }

        std::cout << "⑂ [fork] " << run_id << " → " << fork_session_id << std::endl;
        res.set_content(nlohmann::json({
            {"fork_session_id", fork_session_id},
            {"source_run_id",   run_id},
            {"prompt",          prompt},
        }).dump(), "application/json");
    });
}
