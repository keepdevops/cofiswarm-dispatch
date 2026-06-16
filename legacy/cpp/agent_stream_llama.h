#pragma once
// Inline streaming implementations — included only by agent_stream.cpp.

#include "agent_client_pool.h"
#include "agent_metrics.h"
#include "agent_client.h"
#include "agent_stream_pool.h"
#include "agent_stream_sse.h"
#include "httplib.h"
#include "json.hpp"
#include "kv_router.h"
#include "mlx_inflight.h"
#include "session_context.h"
#include "token_ledger.h"
#include "utf8_sanitize.h"
#ifdef MATRIX_MLX_NATIVE_COORD
#include "mlx_session_store.h"
#endif

#include <atomic>
#include <chrono>
#include <iostream>
#include <string>
#include <thread>

namespace agent_stream {

using json = nlohmann::json;

inline std::string stream_llama(const Agent& agent,
                                const std::string& system_prompt_in,
                                const std::string& prompt_in,
                                OnChunk on_chunk,
                                std::atomic<bool>* cancel) {
    auto cli_ptr = stream_pool_checkout(agent.port, agent.read_timeout_secs);
    httplib::Client& cli = *cli_ptr;

    const std::string system_prompt = sanitize_invalid_utf8(system_prompt_in);
    const std::string prompt = sanitize_invalid_utf8(prompt_in);
    json messages = json::array();
    if (!system_prompt.empty())
        messages.push_back({{"role", "system"}, {"content", system_prompt}});
    messages.push_back({{"role", "user"}, {"content", prompt}});

    json body = {
        {"messages", messages},
        {"max_tokens", agent.max_tokens},
        {"stream", true},
        {"cache_prompt", true},
        // Ask llama.cpp to emit the final usage/timings frame in streaming mode so
        // we can read prompt_tokens + cached prefix tokens (KV reuse) for meta.
        {"stream_options", {{"include_usage", true}}},
        {"stop", {"<|im_end|>", "<|im_start|>",
                  "<|eot_id|>", "<|start_header_id|>",
                  "<|endoftext|>"}}
    };

    std::string accumulated;
    std::string buf;
    std::string last_data;  // MS-72: last non-DONE data: payload for timings/usage
    bool done = false;

    auto receiver = [&](const char* data, size_t n) -> bool {
        if (cancel && cancel->load()) return false;
        buf.append(data, n);
        // #302: capture the last JSON data: line (skips [DONE]) for real token counts.
        sse::capture_last_json_data(std::string(data, n), last_data);
        sse::drain_frames(buf, on_chunk, accumulated, done);
        return true;
    };

    auto t_start = std::chrono::steady_clock::now();
    auto res = cli.Post("/v1/chat/completions",
                        httplib::Headers{{"Accept", "text/event-stream"}},
                        body.dump(), "application/json",
                        receiver);

    if (!res) {
        // A deliberate cancel (run-wide stop or per-agent cascade deadline) aborts
        // the transfer mid-stream, which surfaces here as a falsy result. That's
        // not a failure — keep whatever already streamed and return quietly.
        if (cancel && cancel->load()) {
            accumulated = strip_template_leakage(std::move(accumulated));
            return accumulated;
        }
        std::cerr << "[agent_stream] " << agent.name
                  << " stream connect failed" << std::endl;
        std::string fallback = "Agent " + agent.name + " (Port "
                               + std::to_string(agent.port) + ") is not responding.";
        on_chunk(fallback);
        return fallback;
    }
    stream_pool_checkin(agent.port, std::move(cli_ptr));
    if (!buf.empty()) {
        buf += "\n\n";
        sse::drain_frames(buf, on_chunk, accumulated, done);
    }
    auto t_end = std::chrono::steady_clock::now();
    accumulated = strip_template_leakage(std::move(accumulated));
    if (!accumulated.empty()) {
        kv_router::note_prefix(agent.name, system_prompt + "\n" + prompt);
        double ms = std::chrono::duration<double, std::milli>(t_end - t_start).count();
        // MS-72: real token counts from the final timings/usage frame. Also capture
        // KV prompt-cache reuse (cached prefix tokens) so meta.timings can report the
        // reused-vs-recomputed ratio. total_ptoks = cached + evaluated.
        long total_ptoks = -1, cached_ptoks = -1;
        long ctoks = static_cast<long>(accumulated.size() / 4 + 1);
        if (!last_data.empty()) {
            try {
                auto j = json::parse(last_data);
                if (j.contains("usage")) {
                    const auto& u = j["usage"];
                    if (u.contains("prompt_tokens"))     total_ptoks = u["prompt_tokens"].get<long>();
                    if (u.contains("completion_tokens")) ctoks       = u["completion_tokens"].get<long>();
                    if (u.contains("prompt_tokens_details")
                        && u["prompt_tokens_details"].contains("cached_tokens"))
                        cached_ptoks = u["prompt_tokens_details"]["cached_tokens"].get<long>();
                }
                if (j.contains("timings")) {
                    const auto& t = j["timings"];
                    if (t.contains("cache_n"))          cached_ptoks = t["cache_n"].get<long>();  // authoritative
                    if (t.contains("predicted_n"))      ctoks        = t["predicted_n"].get<long>();
                    // legacy llama.cpp field names (pre-b87xx builds)
                    if (t.contains("tokens_predicted")) ctoks        = t["tokens_predicted"].get<long>();
                    if (t.contains("tokens_evaluated") && total_ptoks < 0)
                        total_ptoks = t["tokens_evaluated"].get<long>()
                                      + (cached_ptoks > 0 ? cached_ptoks : 0);
                }
            } catch (...) {}
        }
        if (total_ptoks < 0)
            total_ptoks = static_cast<long>((system_prompt.size() + prompt.size()) / 4 + 1);
        if (ctoks <= 0) { ctoks = 1; for (char c : accumulated) if (c == ' ') ++ctoks; }
        agent_metrics::record(agent.name, ms, ctoks, total_ptoks, cached_ptoks);
        token_ledger::add(session_context::current(), total_ptoks, ctoks);
    }
    return accumulated;
}

// MS-148: true SSE streaming for mlx_lm.server (stream=true + sse::drain_frames).
// MS-149: session_id injects mlx_session_store history (caller appends user
//         message before calling, so get_messages returns full history including
//         current turn — don't add the current prompt again).
inline std::string stream_mlx(const Agent& agent,
                               const std::string& system_prompt_in,
                               const std::string& prompt_in,
                               OnChunk on_chunk,
                               std::atomic<bool>* cancel = nullptr,
                               const std::string& session_id = "") {
    mlx_inflight::Scope inflight(agent.port);
    semaphore_acquire(agent.port);

    auto cli_ptr = stream_pool_checkout(agent.port, agent.read_timeout_secs);
    httplib::Client& cli = *cli_ptr;

    const std::string system_prompt = sanitize_invalid_utf8(system_prompt_in);
    const std::string prompt        = sanitize_invalid_utf8(prompt_in);

    json messages = json::array();
    bool history_injected = false;
#ifdef MATRIX_MLX_NATIVE_COORD
    if (!session_id.empty()) {
        auto hist = mlx_sessions().get_messages(session_id);
        if (!hist.empty()) {
            if (!system_prompt.empty())
                messages.push_back({{"role", "system"}, {"content", system_prompt}});
            for (const auto& m : hist) messages.push_back(m);
            history_injected = true;
        }
    }
#endif
    if (!history_injected) {
        if (!system_prompt.empty())
            messages.push_back({{"role", "system"}, {"content", system_prompt}});
        messages.push_back({{"role", "user"}, {"content", prompt}});
    }

    json body = {
        {"messages",   messages},
        {"max_tokens", agent.max_tokens},
        {"stream",     true},
        {"stop",       {"<|im_end|>", "<|im_start|>", "<|eot_id|>", "<|endoftext|>"}},
    };

    std::string accumulated;
    std::string buf;
    std::string last_data;  // MS-72: last non-DONE data: payload for usage counts
    bool done = false;

    auto receiver = [&](const char* data, size_t n) -> bool {
        if (cancel && cancel->load()) return false;
        buf.append(data, n);
        // #302: capture the last JSON data: line (skips [DONE]) for real usage counts.
        sse::capture_last_json_data(std::string(data, n), last_data);
        sse::drain_frames(buf, on_chunk, accumulated, done);
        return true;
    };

    auto t_start = std::chrono::steady_clock::now();
    auto res     = cli.Post("/v1/chat/completions",
                            httplib::Headers{{"Accept", "text/event-stream"}},
                            body.dump(), "application/json",
                            receiver);

    const bool had_waiters = semaphore_release_has_waiters(agent.port);
    if (had_waiters)
        std::this_thread::sleep_for(std::chrono::milliseconds(200));

    if (!res) {
        std::cerr << "[mlx_stream] " << agent.name << " connect failed" << std::endl;
        std::string err = "Agent " + agent.name + " (Port "
                          + std::to_string(agent.port) + ") is not responding.";
        on_chunk(err);
        return err;
    }

    stream_pool_checkin(agent.port, std::move(cli_ptr));
    if (!buf.empty()) { buf += "\n\n"; sse::drain_frames(buf, on_chunk, accumulated, done); }

    auto t_end = std::chrono::steady_clock::now();
    accumulated = strip_template_leakage(std::move(accumulated));
    if (!accumulated.empty()) {
        double secs  = std::chrono::duration<double>(t_end - t_start).count();
        // MS-72: prefer real usage counts; fall back to char estimate
        long ptoks = static_cast<long>((system_prompt.size() + prompt.size()) / 4 + 1);
        long ctoks = static_cast<long>(accumulated.size() / 4 + 1);
        if (!last_data.empty()) {
            try {
                auto j = json::parse(last_data);
                if (j.contains("usage")) {
                    auto& u = j["usage"];
                    if (u.contains("prompt_tokens"))     ptoks = u["prompt_tokens"].get<long>();
                    if (u.contains("completion_tokens")) ctoks = u["completion_tokens"].get<long>();
                }
            } catch (...) {}
        }
        mlx_inflight::record_completion(agent.port, secs, ctoks);
        double ms    = std::chrono::duration<double, std::milli>(t_end - t_start).count();
        agent_metrics::record(agent.name, ms, ctoks, -1);
        token_ledger::add(session_context::current(), ptoks, ctoks);
#ifdef MATRIX_MLX_NATIVE_COORD
        // MS-149: record assistant response in the MLX session store
        if (!session_id.empty())
            mlx_sessions().append_message(session_id, "assistant", accumulated);
#endif
    }
    return accumulated;
}

} // namespace agent_stream
