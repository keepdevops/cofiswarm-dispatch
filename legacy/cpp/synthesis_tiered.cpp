#include "synthesis_tiered.h"

#include "agent_client.h"
#include "synthesis_budget.h"

#include <cstdlib>
#include <cstring>
#include <strings.h>
#include <iostream>
#include <utility>
#include <vector>

namespace synthesis_tiered {

bool enabled_via_env() {
    const char* e = std::getenv("MATRIX_SYNTHESIS_TIERED");
    if (!e || !*e) return false;
    if (std::strcmp(e, "1") == 0) return true;
    if (strcasecmp(e, "true") == 0 || strcasecmp(e, "yes") == 0
        || strcasecmp(e, "on") == 0)
        return true;
    return false;
}

namespace {

std::string merge_prompt(const Agent& synthesizer,
                         const std::string& user_prompt,
                         const std::vector<std::pair<std::string, std::string>>& blk,
                         bool pipeline_stage_labels) {
    if (pipeline_stage_labels)
        return synthesis_budget::build_pipeline_synthesis_prompt(user_prompt, blk,
                                                                   &synthesizer);
    return synthesis_budget::build_cascade_synthesis_prompt(user_prompt, blk, &synthesizer);
}

}  // namespace

std::string reduce_pairwise(const Agent& synthesizer,
                            const std::string& user_prompt,
                            std::vector<std::pair<std::string, std::string>> blocks,
                            bool pipeline_stage_labels) {
    if (blocks.empty()) return "";
    if (blocks.size() == 1) {
        std::vector<std::pair<std::string, std::string>> one = {blocks[0]};
        return call_agent(synthesizer,
                          merge_prompt(synthesizer, user_prompt, one, pipeline_stage_labels));
    }

    int round = 0;
    while (blocks.size() > 1) {
        ++round;
        std::vector<std::pair<std::string, std::string>> next;
        next.reserve((blocks.size() + 1) / 2);
        for (size_t i = 0; i < blocks.size(); i += 2) {
            if (i + 1 < blocks.size()) {
                std::vector<std::pair<std::string, std::string>> pairblk = {
                    blocks[i], blocks[i + 1]
                };
                std::string prompt = merge_prompt(synthesizer, user_prompt, pairblk,
                                                  pipeline_stage_labels);
                std::string merged = call_agent(synthesizer, prompt);
                std::string tag = "merge_r" + std::to_string(round) + "_"
                                  + std::to_string(next.size());
                next.push_back({std::move(tag), std::move(merged)});
            } else {
                next.push_back(std::move(blocks[i]));
            }
        }
        blocks = std::move(next);
        if (round > 64) {
            std::cerr << "⚠️  [synthesis_tiered] aborting pairwise reduction after 64 rounds"
                      << std::endl;
            break;
        }
    }
    return blocks[0].second;
}

}  // namespace synthesis_tiered
