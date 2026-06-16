#pragma once

#include "agent.h"
#include <string>
#include <vector>

// Thread-safe HTTP caller for a single agent. Handles engine-specific quirks
// (mlx serialization + drain delay, docker/vllm model injection, system-prompt
// merging for mlx). Errors are logged to std::cerr and returned as a
// human-readable string in the result — never silent.
std::string call_agent(const Agent& agent, const std::string& prompt);

// Same as call_agent, but replaces the agent's configured system_prompt for
// this one call. Useful for classifier/router invocations where the agent's
// role prompt would fight the structured output we need.
std::string call_agent_with_system(const Agent& agent,
                                   const std::string& system_prompt_override,
                                   const std::string& prompt);

// Same as call_agent, but injects mlx_session_store history for the given
// session_id (MLX engines only). Non-MLX agents ignore the session_id.
std::string call_agent_in_session(const Agent& agent,
                                  const std::string& prompt,
                                  const std::string& session_id);

// Install per-port concurrency limiters driven by Agent::max_concurrency.
// mlx agents default to 1 (serialized); others default to 0 (unlimited).
// Call once at startup, after agents are loaded from config.
void init_port_concurrency(const std::vector<Agent>& agents);
