#pragma once
// POST /api/history/diff  body: { run_id_a, run_id_b }
// Returns both history entries for client-side word-level diffing.

#include "coordinator_context.h"
#include "httplib.h"
#include "json.hpp"
#include <string>

namespace history_diff {

inline nlohmann::json extract_entry(const nlohmann::json& raw) {
    nlohmann::json out;
    out["run_id"]  = raw.value("_run_id", "");
    out["prompt"]  = raw.value("prompt", "");
    out["final"]   = raw.contains("_final") ? raw["_final"] : nlohmann::json(nullptr);
    nlohmann::json agents = nlohmann::json::object();
    for (const auto& [k, v] : raw.items()) {
        if (!k.empty() && k[0] != '_' && k != "prompt" && k != "temperature"
            && k != "timestamp" && v.is_string()) {
            agents[k] = v;
        }
    }
    out["agents"] = agents;
    return out;
}

} // namespace history_diff

inline void register_coordinator_routes_history_diff(httplib::Server& svr,
                                                      CoordinatorState& st) {
    svr.Post("/api/history/diff", [&st](const httplib::Request& req,
                                        httplib::Response& res) {
        res.set_header("Access-Control-Allow-Origin", "*");
        nlohmann::json body;
        try { body = nlohmann::json::parse(req.body); }
        catch (...) {
            res.status = 400;
            res.set_content("{\"error\":\"invalid JSON\"}", "application/json");
            return;
        }

        const std::string id_a = body.value("run_id_a", "");
        const std::string id_b = body.value("run_id_b", "");
        if (id_a.empty() || id_b.empty()) {
            res.status = 400;
            res.set_content("{\"error\":\"run_id_a and run_id_b required\"}",
                            "application/json");
            return;
        }

        nlohmann::json entry_a, entry_b;
        {
            std::lock_guard<std::mutex> lk(st.history_mutex);
            for (const auto& e : st.history) {
                if (e.value("_run_id", "") == id_a) entry_a = e;
                if (e.value("_run_id", "") == id_b) entry_b = e;
                if (!entry_a.is_null() && !entry_b.is_null()) break;
            }
        }

        if (entry_a.is_null() || entry_b.is_null()) {
            res.status = 404;
            nlohmann::json err = {{"error", "run_id not found"},
                                  {"missing_a", entry_a.is_null()},
                                  {"missing_b", entry_b.is_null()}};
            res.set_content(err.dump(), "application/json");
            return;
        }

        nlohmann::json result = {
            {"a", history_diff::extract_entry(entry_a)},
            {"b", history_diff::extract_entry(entry_b)},
        };
        res.set_content(result.dump(), "application/json");
    });
}
