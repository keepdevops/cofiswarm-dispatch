#pragma once
#include "json.hpp"
#include <string>

using json = nlohmann::json;

namespace stream_parse {

struct StreamRequest {
    std::string user_prompt;
    std::string session_id;
    std::string parent_run_id;
    double temperature = 0.7;
    bool followup = false;
    json context_policy = json::object();
};

inline StreamRequest parse(const std::string& body) {
    StreamRequest r;
    try {
        auto j = json::parse(body);
        r.user_prompt   = j.value("prompt", "");
        r.session_id    = j.value("session_id", std::string(""));
        r.parent_run_id = j.value("parent_run_id", std::string(""));
        r.temperature   = j.value("temperature", 0.7);
        r.followup      = j.value("followup", false);
        if (j.contains("context_policy") && j["context_policy"].is_object())
            r.context_policy = j["context_policy"];
    } catch (...) {
        r.user_prompt = body;
    }
    return r;
}

} // namespace stream_parse
