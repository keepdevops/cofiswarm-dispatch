#include "coordinator_routes_architect_stream_modes.h"
#include "agent_stream.h"

#include <atomic>
#include <chrono>
#include <cstdlib>
#include <map>
#include <mutex>
#include <thread>

namespace {
static constexpr size_t TOKEN_BATCH_BYTES = 128;

// Per-agent wall-clock budget for the parallel broadcast (cascade/flat). The
// barrier below joins every agent before the synthesizer runs, so one slow or
// degenerate proposer (e.g. a model stuck emitting filler to its max_tokens)
// would otherwise stall the whole run. When the budget elapses we cancel that
// agent's stream, keep whatever it produced, and let the barrier release.
// Override with MATRIX_CASCADE_AGENT_DEADLINE_SECS (0 disables the cap).
static int cascade_agent_deadline_secs() {
    if (const char* e = std::getenv("MATRIX_CASCADE_AGENT_DEADLINE_SECS")) {
        char* end = nullptr;
        long v = std::strtol(e, &end, 10);
        if (end != e && v >= 0 && v <= 3600) return static_cast<int>(v);
    }
    return 90;
}
}

void stream_parallel_agents(const std::vector<const Agent*>& parallel_agents,
                            const std::string& prompt,
                            std::atomic<bool>* cancel,
                            const WriteEventFn& write_event,
                            std::map<std::string, std::string>& outputs,
                            const std::string& session_id) {
    std::mutex out_mu;
    const int deadline_secs = cascade_agent_deadline_secs();
    const auto deadline = std::chrono::steady_clock::now()
        + std::chrono::seconds(deadline_secs > 0 ? deadline_secs : 3600);
    const bool deadline_on = deadline_secs > 0;
    std::vector<std::thread> threads;
    threads.reserve(parallel_agents.size());
    for (const Agent* a : parallel_agents) {
        threads.emplace_back([a, &prompt, &session_id, cancel, &write_event,
                              &outputs, &out_mu, deadline, deadline_on]() {
            std::string assembled;
            std::string batch;
            // Local cancel so we can stop just this agent at its deadline without
            // tripping the shared run-wide cancel. Seeded from the global flag.
            std::atomic<bool> agent_cancel{cancel && cancel->load()};
            bool timed_out = false;
            auto flush_batch = [&]() {
                if (batch.empty()) return;
                write_event("token",
                    nlohmann::json({{"agent", a->name}, {"delta", batch}}).dump());
                batch.clear();
            };
            auto on_chunk = [&](const std::string& delta) {
                assembled += delta;
                batch    += delta;
                if (batch.size() >= TOKEN_BATCH_BYTES) flush_batch();
                if (cancel && cancel->load()) { agent_cancel.store(true); return; }
                if (deadline_on && std::chrono::steady_clock::now() >= deadline) {
                    timed_out = true;
                    agent_cancel.store(true);
                }
            };
            try {
                agent_stream::stream_agent(*a, a->system_prompt, prompt, on_chunk,
                                           &agent_cancel, session_id);
            } catch (const std::exception& e) {
                flush_batch();
                write_event("error",
                    nlohmann::json({{"agent", a->name}, {"error", e.what()}}).dump());
            }
            flush_batch();
            if (timed_out)
                write_event("token", nlohmann::json({{"agent", a->name},
                    {"delta", "\n[… stopped at cascade time budget]"}}).dump());
            {
                std::lock_guard<std::mutex> lk(out_mu);
                outputs[a->name] = assembled;
            }
            write_event("agent_done", nlohmann::json({{"agent", a->name}}).dump());
        });
    }
    for (auto& t : threads) t.join();
}

// Shared broadcast step for flat and cascade: stream every agent (except the
// designated synthesizer) in parallel. Kept as a helper so each mode below is
// its own method without duplicating the fan-out logic.
static void stream_broadcast_participants(const std::vector<Agent>& agents,
                                          const std::string& synth_name,
                                          const std::string& prompt,
                                          std::atomic<bool>* cancel,
                                          const WriteEventFn& write_event,
                                          std::map<std::string, std::string>& outputs,
                                          std::vector<std::string>& participants,
                                          const std::string& session_id) {
    std::vector<const Agent*> bcast;
    for (const auto& a : agents) {
        if (a.name == synth_name) continue;
        bcast.push_back(&a);
        participants.push_back(a.name);
    }
    stream_parallel_agents(bcast, prompt, cancel, write_event, outputs, session_id);
}

// flat: broadcast to every agent in parallel; no reducer.
void run_stream_flat_mode(const std::vector<Agent>& agents,
                          const std::string& synth_name,
                          const std::string& prompt,
                          std::atomic<bool>* cancel,
                          const WriteEventFn& write_event,
                          std::map<std::string, std::string>& outputs,
                          std::vector<std::string>& participants,
                          const std::string& session_id) {
    stream_broadcast_participants(agents, synth_name, prompt, cancel,
                                  write_event, outputs, participants, session_id);
}

// cascade: parallel broadcast, then a synthesizer reduces all outputs into one.
void run_stream_cascade_mode(const std::vector<Agent>& agents,
                             const std::string& synth_name,
                             const Agent* synth_agent,
                             const std::string& prompt,
                             std::atomic<bool>* cancel,
                             const WriteEventFn& write_event,
                             std::map<std::string, std::string>& outputs,
                             std::vector<std::string>& participants,
                             const std::string& session_id) {
    stream_broadcast_participants(agents, synth_name, prompt, cancel,
                                  write_event, outputs, participants, session_id);
    run_stream_synthesis(synth_agent, prompt, "cascade", participants,
                         outputs, cancel, write_event);
}
