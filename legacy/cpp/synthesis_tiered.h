#pragma once

#include "agent.h"

#include <string>
#include <utility>
#include <vector>

namespace synthesis_tiered {

/// True when MATRIX_SYNTHESIS_TIERED is 1 / true / yes (case-insensitive for letters).
bool enabled_via_env();

/// Pairwise tournament reduction: each API call merges at most two contributions,
/// so prompts stay within the synthesizer budget. Final round returns one string.
/// If pipeline_stage_labels, uses pipeline synthesis prompts (numbered stages);
/// otherwise cascade-style ("responses").
std::string reduce_pairwise(const Agent& synthesizer,
                            const std::string& user_prompt,
                            std::vector<std::pair<std::string, std::string>> blocks,
                            bool pipeline_stage_labels);

}  // namespace synthesis_tiered
