#include "synthesis_budget.h"
#include "synthesis_budget_assemble.h"

#include <algorithm>
#include <cstdlib>
#include <string>
#include <vector>

namespace synthesis_budget {

size_t effective_max_prompt_chars(const Agent* synthesizer) {
    if (const char* e = std::getenv("MATRIX_SYNTHESIS_MAX_PROMPT_TOKENS")) {
        try {
            unsigned v = static_cast<unsigned>(std::stoul(e));
            if (v >= 256 && v <= 262144) return static_cast<size_t>(v) * 4;
        } catch (...) {}
    }
    if (synthesizer && synthesizer->context_window >= 512) {
        unsigned ctx = static_cast<unsigned>(synthesizer->context_window);
        unsigned mt = static_cast<unsigned>(std::max(1, synthesizer->max_tokens));
        const unsigned reserve = 768u;
        if (ctx <= mt + reserve + 64u) return 256u * 4u;
        unsigned avail = ctx - mt - reserve;
        avail = std::max(256u, std::min(avail, 262144u));
        return static_cast<size_t>(avail) * 4u;
    }
    return static_cast<size_t>(1400u) * 4u;
}

std::string build_pipeline_synthesis_prompt(
    const std::string& user_prompt,
    const std::vector<std::pair<std::string, std::string>>& stages_in_order,
    const Agent* synthesizer) {
    const std::string footer =
        "\n\nProduce ONE consolidated answer that integrates the "
        "above contributions. Resolve contradictions, drop redundancy, "
        "and keep only the strongest material. Do not enumerate the "
        "stages — write the final answer directly.";
    std::string prefix = "Original user request:\n<<<\n";
    prefix += user_prompt;
    prefix += "\n>>>\n\nThe following agents produced staged outputs:\n";

    std::vector<std::string> headers;
    std::vector<std::string> bodies;
    int n = 0;
    for (const auto& pr : stages_in_order) {
        ++n;
        std::string hdr = "\n--- Stage ";
        hdr += std::to_string(n);
        hdr += " (";
        hdr += pr.first;
        hdr += ") ---\n";
        headers.push_back(hdr);
        bodies.push_back(pr.second);
    }
    return assemble_fit(prefix, headers, bodies, footer,
                        effective_max_prompt_chars(synthesizer));
}

std::string build_cascade_synthesis_prompt(
    const std::string& user_prompt,
    const std::vector<std::pair<std::string, std::string>>& responses_in_order,
    const Agent* synthesizer) {
    const std::string footer =
        "\n\nProduce ONE consolidated answer that integrates the "
        "above contributions. Resolve contradictions, drop redundancy, "
        "and keep only the strongest material. Do not enumerate the "
        "responders — write the final answer directly.";
    std::string prefix = "Original user request:\n<<<\n";
    prefix += user_prompt;
    prefix += "\n>>>\n\nThe following agents responded in parallel:\n";

    std::vector<std::string> headers;
    std::vector<std::string> bodies;
    int n = 0;
    for (const auto& pr : responses_in_order) {
        ++n;
        std::string hdr = "\n--- Response ";
        hdr += std::to_string(n);
        hdr += " (";
        hdr += pr.first;
        hdr += ") ---\n";
        headers.push_back(hdr);
        bodies.push_back(pr.second);
    }
    return assemble_fit(prefix, headers, bodies, footer,
                        effective_max_prompt_chars(synthesizer));
}

std::string build_stream_synthesis_prompt(
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

}  // namespace synthesis_budget
