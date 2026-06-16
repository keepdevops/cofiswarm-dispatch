#include "coordinator_routes_architect_synthesis.h"
#include "agent_stream.h"
#include "synthesis_budget.h"
#include "synthesis_tiered.h"
#include "utf8_sanitize.h"

void run_stream_synthesis(
    const Agent* synth_agent,
    const std::string& prompt,
    const std::string& mode,
    const std::vector<std::string>& contributors,
    std::map<std::string, std::string>& outputs,
    std::atomic<bool>* cancel,
    const WriteEventFn& write_event)
{
    if (!synth_agent || contributors.empty()) return;

    std::vector<std::pair<std::string, std::string>> pairs;
    pairs.reserve(contributors.size());
    for (const auto& nm : contributors)
        pairs.push_back({nm, outputs[nm]});

    write_event("synthesis_start",
        json({{"agent", synth_agent->name}}).dump());

    if (synthesis_tiered::enabled_via_env()) {
        std::string final_text = synthesis_tiered::reduce_pairwise(
            *synth_agent, prompt, std::move(pairs), mode == "pipeline");
        final_text = sanitize_invalid_utf8(final_text);
        constexpr size_t kChunk = 120;
        for (size_t i = 0; i < final_text.size();) {
            size_t n = utf8_safe_prefix_len(final_text.substr(i), kChunk);
            if (n == 0) n = std::min(kChunk, final_text.size() - i);
            std::string delta = final_text.substr(i, n);
            write_event("token",
                json({{"agent", synth_agent->name}, {"delta", delta}}).dump());
            i += n;
        }
    } else {
        std::string sp = synthesis_budget::build_stream_synthesis_prompt(
            prompt, pairs, synth_agent);
        auto on_chunk = [&](const std::string& delta) {
            write_event("token",
                json({{"agent", synth_agent->name}, {"delta", delta}}).dump());
        };
        try {
            agent_stream::stream_agent(*synth_agent,
                synth_agent->system_prompt, sp, on_chunk, cancel);
        } catch (const std::exception& e) {
            write_event("error",
                json({{"agent", synth_agent->name}, {"error", e.what()}}).dump());
        }
    }
    write_event("agent_done",
        json({{"agent", synth_agent->name}}).dump());
}
