#pragma once
#include "session_store.h"
#include "session_store_text.h"
#include <sstream>

namespace session_ctx {

inline SessionContinuation build(
        const nlohmann::json& sessions,
        const std::string& session_id,
        const std::string& followup,
        const nlohmann::json& context_policy,
        const nlohmann::json* prev) {
    using json = nlohmann::json;
    SessionContinuation out;

    const size_t max_context_chars = context_policy.value("max_context_chars", 24000);
    const std::string target_agent = context_policy.value("target_agent", std::string("programmer"));
    const bool include_final    = include_name(context_policy, "final");
    const bool include_original = include_name(context_policy, "original_prompt");
    const bool include_target   = include_name(context_policy, target_agent);
    bool compacted = false;

    const std::string original = first_prompt_for_session(sessions, session_id);
    std::ostringstream os;
    os << "You are continuing an existing Matrix Swarm session. "
       << "Use the prior context below, then answer the new follow-up. "
       << "Do not restart from scratch unless the follow-up asks you to.\n";

    if (include_original) {
        const size_t budget = max_context_chars / 5;
        compacted = compacted || original.size() > budget;
        append_section(os, "Original user request", trim_block(original, budget));
    }
    if (include_final) {
        const std::string final = json_string(*prev, "final");
        const size_t budget = max_context_chars / 4;
        compacted = compacted || final.size() > budget;
        append_section(os, "Previous final answer", trim_block(final, budget));
    }
    if (prev->contains("agents") && (*prev)["agents"].is_object()) {
        const json& agents = (*prev)["agents"];
        if (include_target && agents.contains(target_agent) && agents[target_agent].is_string()) {
            const std::string target = agents[target_agent].get<std::string>();
            const size_t budget = max_context_chars / 2;
            compacted = compacted || target.size() > budget;
            append_section(os, "Previous " + target_agent + " answer", trim_block(target, budget));
        }
        std::ostringstream summary;
        for (auto it = agents.begin(); it != agents.end(); ++it) {
            if (it.key() == target_agent || !it.value().is_string()) continue;
            compacted = compacted || it.value().get<std::string>().size() > 900;
            summary << "- " << it.key() << ": "
                    << first_lines(it.value().get<std::string>(), 3, 900) << "\n";
        }
        append_section(os, "Other previous agent notes", summary.str());
    }

    append_section(os, "User follow-up", followup);
    os << "\n\nContinue from the previous answer. Add concrete detail and preserve useful prior work.";

    std::string built = os.str();
    if (built.size() > max_context_chars) {
        compacted = true;
        const size_t followup_budget = std::min<size_t>(followup.size(), max_context_chars / 4);
        built = trim_block(built, max_context_chars - followup_budget)
            + "\n\n## User follow-up\n"
            + trim_block(followup, followup_budget);
    }

    out.prompt = built;
    out.compaction = {
        {"used",              compacted},
        {"max_context_chars", max_context_chars},
        {"original_chars",    original.size()},
        {"built_chars",       built.size()},
        {"target_agent",      target_agent}
    };
    return out;
}

}  // namespace session_ctx
