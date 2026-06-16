#include "agent_client_http.h"
#include "agent_client_pool.h"
#include "agent_metrics.h"
#include "backend_router.h"
#include "httplib.h"
#include "inference_backend.h"
#include "json.hpp"
#include "kv_router.h"
#include "mlx_inflight.h"
#include "prefix_cache.h"
#include "session_context.h"
#include "token_ledger.h"
#include "utf8_sanitize.h"
#ifdef MATRIX_MLX_NATIVE_COORD
#include "mlx_session_store.h"
#endif

#include <chrono>
#include <iostream>

using json = nlohmann::json;

AttemptResult call_agent_once(const Agent& agent,
                              const std::string& system_prompt,
                              const std::string& prompt,
                              const std::string& session_id) {
    AttemptResult out;
    try {
        auto cli_ptr = pool_checkout(agent.port, agent.read_timeout_secs);
        httplib::Client& cli = *cli_ptr;

        // Enforce max_input_tokens cap: ~4 chars per token (rough estimate)
        const std::string& eff_prompt = (agent.max_input_tokens > 0
            && (int)prompt.size() > agent.max_input_tokens * 4)
            ? prompt.substr(0, static_cast<size_t>(agent.max_input_tokens) * 4)
            : prompt;

        json messages = json::array();
        bool history_injected = false;
#ifdef MATRIX_MLX_NATIVE_COORD
        if (agent.engine == "mlx" && !session_id.empty()) {
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
            if (agent.engine == "mlx" && !system_prompt.empty()) {
                messages.push_back({{"role", "user"}, {"content", system_prompt + "\n\n" + eff_prompt}});
            } else {
                if (!system_prompt.empty())
                    messages.push_back({{"role", "system"}, {"content", system_prompt}});
                messages.push_back({{"role", "user"}, {"content", eff_prompt}});
            }
        }
        // Token-budget gate: estimate cost as prompt chars/4 + max_output_tokens.
        // acquire_tokens blocks if the port's KV budget is exhausted.
        const int out_cap = agent.max_output_tokens > 0 ? agent.max_output_tokens : agent.max_tokens;
        const int estimated_tokens = static_cast<int>(eff_prompt.size() / 4) + out_cap;
        semaphore_acquire_tokens(agent.port, estimated_tokens);

        json body = {{"messages", messages}, {"max_tokens", out_cap}};
        if (!agent.model.empty() && (agent.backend == "docker" || agent.backend == "vllm"
                                     || agent.backend == "docker-vllm")) {
            body["model"] = agent.model;
        }
        if (agent.engine == "llama") {
            if (agent.max_output_tokens > 0) body["num_predict"] = agent.max_output_tokens;
            body["cache_prompt"] = true;
            body["stop"] = {"<|im_end|>", "<|im_start|>",
                            "<|eot_id|>", "<|start_header_id|>",
                            "<|endoftext|>"};
        }

        auto t_start = std::chrono::steady_clock::now();
        auto res = cli.Post("/v1/chat/completions", body.dump(), "application/json");
        auto t_end = std::chrono::steady_clock::now();

        if (res && res->status == 200) {
            auto j = json::parse(sanitize_invalid_utf8(res->body));
            if (j.contains("choices") && !j["choices"].empty()) {
                out.text = strip_template_leakage(
                    sanitize_invalid_utf8(j["choices"][0]["message"]["content"].get<std::string>()));
            }
            if (agent.engine == "llama") {
                kv_router::note_prefix(agent.name, system_prompt + "\n" + prompt);
                prefix_cache::record(agent.port, system_prompt + "\n" + prompt);
            }
            long ctoks = -1, ptoks = -1, cached = -1;
            if (j.contains("usage") && j["usage"].is_object()) {
                ctoks = j["usage"].value("completion_tokens", -1L);
                ptoks = j["usage"].value("prompt_tokens", -1L);
                if (j["usage"].contains("prompt_tokens_details")
                    && j["usage"]["prompt_tokens_details"].is_object())
                    cached = j["usage"]["prompt_tokens_details"].value("cached_tokens", -1L);
            }
            // timings.cache_n is the authoritative KV prompt-cache reuse count.
            if (cached < 0 && j.contains("timings") && j["timings"].is_object())
                cached = j["timings"].value("cache_n", -1L);
            // Release actual tokens used; fall back to estimate if usage unavailable.
            const int actual_tokens = (ptoks >= 0 && ctoks >= 0)
                ? static_cast<int>(ptoks + ctoks) : estimated_tokens;
            semaphore_release_tokens(agent.port, actual_tokens);

            if (agent.engine == "mlx" && ctoks >= 0) {
                double secs = std::chrono::duration<double>(t_end - t_start).count();
                mlx_inflight::record_completion(agent.port, secs, ctoks);
            }
            if (!out.text.empty()) {
                out.ok = true;
                double ms = std::chrono::duration<double, std::milli>(t_end - t_start).count();
                agent_metrics::record(agent.name, ms, ctoks, ptoks, cached);
                BackendId bid = agent.engine == "mlx" ? BackendId::PythonMlx : BackendId::LlamaMetal;
                backend_router::record_probe_sample(bid, ms);
                token_ledger::add(session_context::current(), ptoks, ctoks);
            } else {
                out.retryable = true;
            }
        } else if (res) {
            semaphore_release_tokens(agent.port, estimated_tokens);
            out.retryable = (res->status >= 500 && res->status < 600);
            try {
                auto err = json::parse(sanitize_invalid_utf8(res->body));
                if (err.contains("error") && err["error"].contains("message")) {
                    out.text = "[" + agent.name + " error] "
                             + sanitize_invalid_utf8(err["error"]["message"].get<std::string>());
                }
            } catch (...) {
                std::cerr << "[coordinator] Non-JSON error body from " << agent.name
                          << " (status " << res->status << ")" << std::endl;
            }
        } else {
            semaphore_release_tokens(agent.port, estimated_tokens);
            out.retryable = true;
        }

        if (!out.retryable)
            pool_checkin(agent.port, std::move(cli_ptr));
        return out;
    } catch (const std::exception& e) {
        std::cerr << "[coordinator] call_agent exception for " << agent.name
                  << ": " << e.what() << std::endl;
        out.text = "Connection Error (" + agent.name + "): " + std::string(e.what());
        out.retryable = true;
        return out;
    }
}
