#include "agent_client.h"
#include "agent_client_http.h"
#include "agent_health.h"
#include "backend_router.h"
#include "inference_backend.h"
#include "mlx_inflight.h"
#include "response_cache.h"
#include "utf8_sanitize.h"

#include "agent_client_pool.h"

#include <chrono>
#include <iostream>
#include <memory>
#include <thread>

// One retry on transient failure (5xx / empty body / network error). 4xx and
// successful responses return immediately.
static constexpr int RETRY_ATTEMPTS   = 2;
static constexpr int RETRY_BACKOFF_MS = 250;

static std::string call_agent_impl(const Agent& agent,
                                   const std::string& system_prompt_in,
                                   const std::string& prompt_in,
                                   const std::string& session_id = "") {
    std::string system_prompt = system_prompt_in;
    if (!agent.description.empty()) {
        system_prompt = "# Role\n" + agent.description + "\n\n" + system_prompt_in;
    }
    system_prompt = sanitize_invalid_utf8(system_prompt);
    std::string prompt = sanitize_invalid_utf8(prompt_in);

    if (auto cached = response_cache::lookup(agent, system_prompt, prompt)) {
        return *cached;
    }
    const RoutingDecision routing = backend_router::resolve(agent);
    Agent work = backend_router::materialize(agent, routing);
    if (backend_router::should_route(agent))
        backend_router::record_decision(agent.name, routing);

    std::unique_ptr<mlx_inflight::Scope> mlx_pressure;
    if (work.engine == "mlx")
        mlx_pressure = std::make_unique<mlx_inflight::Scope>(work.port);
    semaphore_acquire(work.port);

    AttemptResult attempt;
    auto dispatch_once = [&](const Agent& a, BackendId id) {
        return inference_backend::complete(id, a, system_prompt, prompt, session_id);
    };
    for (int i = 0; i < RETRY_ATTEMPTS; ++i) {
        attempt = dispatch_once(work, routing.backend);
        if (!attempt.ok && attempt.retryable && backend_router::should_route(agent)) {
            BackendId alt = routing.backend == BackendId::LlamaMetal
                ? BackendId::PythonMlx : BackendId::LlamaMetal;
            if (inference_backend::supports(agent, alt)) {
                AttemptResult fb = dispatch_once(backend_router::materialize(agent,
                    backend_router::make_decision(alt, "load_failure_fallback", true)),
                    alt);
                if (fb.ok || !fb.retryable) {
                    backend_router::record_decision(agent.name,
                        backend_router::make_decision(alt, "load_failure_fallback", true));
                    attempt = fb;
                }
            }
        }
        if (attempt.ok || !attempt.retryable) break;
        if (i + 1 < RETRY_ATTEMPTS) {
            std::cerr << "🔁 [retry] " << agent.name << " transient failure; "
                      << "retrying in " << RETRY_BACKOFF_MS << "ms" << std::endl;
            std::this_thread::sleep_for(std::chrono::milliseconds(RETRY_BACKOFF_MS));
        }
    }

    std::string result = attempt.text;
    if (result.empty()) {
        result = "Agent " + agent.name + " (Port "
               + std::to_string(agent.port) + ") is not responding.";
    } else if (attempt.ok) {
        response_cache::store(agent, system_prompt, prompt, result);
    }
    const bool had_waiters = semaphore_release_has_waiters(work.port);
    if (work.engine == "mlx" && had_waiters)
        std::this_thread::sleep_for(std::chrono::milliseconds(200));
    agent_health::record(agent.name, attempt.ok);
    return result;
}

std::string call_agent(const Agent& agent, const std::string& prompt) {
    return call_agent_impl(agent, agent.system_prompt, prompt);
}

std::string call_agent_in_session(const Agent& agent,
                                  const std::string& prompt,
                                  const std::string& session_id) {
    return call_agent_impl(agent, agent.system_prompt, prompt, session_id);
}

std::string call_agent_with_system(const Agent& agent,
                                   const std::string& system_prompt_override,
                                   const std::string& prompt) {
    return call_agent_impl(agent, system_prompt_override, prompt);
}
