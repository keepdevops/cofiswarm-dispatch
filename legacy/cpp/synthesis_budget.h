#pragma once

#include "agent.h"

#include <string>
#include <utility>
#include <vector>

namespace synthesis_budget {

/// Upper bound on synthesizer *user* message size (chars). Uses
/// MATRIX_SYNTHESIS_MAX_PROMPT_TOKENS if set; else derives from the synthesizer's
/// context_window minus max_tokens and a fixed reserve (system / template slack).
size_t effective_max_prompt_chars(const Agent* synthesizer);

/// Pipeline / cascade non-streaming synthesis user prompts (matches prior wording).
std::string build_pipeline_synthesis_prompt(
    const std::string& user_prompt,
    const std::vector<std::pair<std::string, std::string>>& stages_in_order,
    const Agent* synthesizer = nullptr);

std::string build_cascade_synthesis_prompt(
    const std::string& user_prompt,
    const std::vector<std::pair<std::string, std::string>>& responses_in_order,
    const Agent* synthesizer = nullptr);

/// SSE stream path uses "agents contributed" / numbered blocks (same budget logic).
std::string build_stream_synthesis_prompt(
    const std::string& user_prompt,
    const std::vector<std::pair<std::string, std::string>>& contributors_in_order,
    const Agent* synthesizer = nullptr);

}  // namespace synthesis_budget
