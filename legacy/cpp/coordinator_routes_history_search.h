#pragma once
// GET /api/history/search?q=<term>&limit=<n>
// Case-insensitive substring match on prompt + agent response values.
// Returns newest-first, capped at limit (default 20, max 100).

#include "coordinator_context.h"
#include "httplib.h"
#include "json.hpp"
#include <algorithm>
#include <cctype>
#include <string>

namespace history_search {

inline bool icontains(const std::string& haystack, const std::string& needle) {
    if (needle.empty()) return true;
    auto it = std::search(haystack.begin(), haystack.end(),
                          needle.begin(), needle.end(),
                          [](unsigned char a, unsigned char b) {
                              return std::tolower(a) == std::tolower(b);
                          });
    return it != haystack.end();
}

inline bool entry_matches(const nlohmann::json& entry, const std::string& q) {
    if (entry.contains("prompt") && entry["prompt"].is_string())
        if (icontains(entry["prompt"].get<std::string>(), q)) return true;
    for (const auto& [k, v] : entry.items()) {
        if (v.is_string() && icontains(v.get<std::string>(), q)) return true;
    }
    return false;
}

} // namespace history_search

inline void register_coordinator_routes_history_search(httplib::Server& svr,
                                                        CoordinatorState& st) {
    svr.Get("/api/history/search", [&st](const httplib::Request& req,
                                         httplib::Response& res) {
        res.set_header("Access-Control-Allow-Origin", "*");
        const std::string q     = req.has_param("q")     ? req.get_param_value("q")     : "";
        int limit = 20;
        if (req.has_param("limit")) {
            try { limit = std::min(100, std::max(1, std::stoi(req.get_param_value("limit")))); }
            catch (...) {}
        }

        auto results = nlohmann::json::array();
        std::lock_guard<std::mutex> lock(st.history_mutex);
        for (auto it = st.history.rbegin(); it != st.history.rend(); ++it) {
            if ((int)results.size() >= limit) break;
            if (q.empty() || history_search::entry_matches(*it, q))
                results.push_back(*it);
        }
        res.set_content(results.dump(), "application/json");
    });
}
