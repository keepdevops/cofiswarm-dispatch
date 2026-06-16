#pragma once

#include "json.hpp"

#include <string>

struct SessionContinuation {
    std::string prompt;
    nlohmann::json compaction = nlohmann::json::object();
};

std::string session_new_id(const std::string& prefix);

void session_load(nlohmann::json& sessions, const std::string& path);
void session_save(const nlohmann::json& sessions, const std::string& path);

SessionContinuation session_build_continuation(
    const nlohmann::json& sessions,
    const std::string& session_id,
    const std::string& followup,
    const nlohmann::json& context_policy);

void session_append_run(nlohmann::json& sessions,
                        const std::string& session_id,
                        const nlohmann::json& run);
