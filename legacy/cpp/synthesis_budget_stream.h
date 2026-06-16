#pragma once
// Inline stream-synthesis prompt builder — included only by synthesis_budget.cpp.

#include "synthesis_budget.h"
#include "synthesis_budget_assemble.h"
#include <string>
#include <vector>
#include <utility>

namespace synthesis_budget {

inline std::string build_stream_synthesis_prompt(
    const std::string& user_prompt,
    const std::vector<std::pair<std::string, std::string>>& contributors_in_order,
    const Agent* synthesizer) {
    const std::string footer =
        "\n\nProduce ONE consolidated answer that integrates the "
        "above contributions. Resolve contradictions, drop redundancy. "
        "Write the final answer directly.";
    std::string prefix = "Original user request:\n<<<\n";
    prefix += user_prompt;
    prefix += "\n>>>\n\nThe following agents contributed:\n";

    std::vector<std::string> headers;
    std::vector<std::string> bodies;
    int n = 0;
    for (const auto& pr : contributors_in_order) {
        ++n;
        std::string hdr = "\n--- " + std::to_string(n) + " (" + pr.first + ") ---\n";
        headers.push_back(hdr);
        bodies.push_back(pr.second);
    }
    return assemble_fit(prefix, headers, bodies, footer,
                        effective_max_prompt_chars(synthesizer));
}

} // namespace synthesis_budget
