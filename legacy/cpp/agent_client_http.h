#pragma once
#include "agent.h"
#include <string>

// Return shape of one HTTP attempt. `retryable` distinguishes transient
// failures (5xx, empty body, network error) from deterministic ones (4xx).
struct AttemptResult {
    std::string text;
    bool ok        = false;
    bool retryable = false;
};

AttemptResult call_agent_once(const Agent& agent,
                              const std::string& system_prompt,
                              const std::string& prompt,
                              const std::string& session_id = "");
