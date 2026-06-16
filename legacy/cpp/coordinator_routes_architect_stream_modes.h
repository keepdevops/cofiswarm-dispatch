#pragma once

#include "coordinator_routes_architect_synthesis.h"
#include "agent.h"
#include "json.hpp"

#include <atomic>
#include <map>
#include <string>
#include <vector>

// session_id is threaded through to stream_agent for MLX history injection (MS-149).
// Non-MLX agents ignore it.
void stream_parallel_agents(const std::vector<const Agent*>& parallel_agents,
                            const std::string& prompt,
                            std::atomic<bool>* cancel,
                            const WriteEventFn& write_event,
                            std::map<std::string, std::string>& outputs,
                            const std::string& session_id = "");

void run_stream_pipeline_mode(const std::vector<Agent>& agents,
                              const nlohmann::json& cfg,
                              const std::string& synth_name,
                              const Agent* synth_agent,
                              const std::string& prompt,
                              const std::string& mode,
                              std::atomic<bool>* cancel,
                              const WriteEventFn& write_event,
                              std::map<std::string, std::string>& outputs,
                              std::vector<std::string>& participants,
                              const std::string& session_id = "");

void run_stream_router_mode(const std::vector<Agent>& agents,
                            const nlohmann::json& cfg,
                            const std::string& prompt,
                            std::atomic<bool>* cancel,
                            const WriteEventFn& write_event,
                            std::map<std::string, std::string>& outputs,
                            const std::string& session_id = "");

// flat: broadcast to every agent in parallel; no reducer.
void run_stream_flat_mode(const std::vector<Agent>& agents,
                          const std::string& synth_name,
                          const std::string& prompt,
                          std::atomic<bool>* cancel,
                          const WriteEventFn& write_event,
                          std::map<std::string, std::string>& outputs,
                          std::vector<std::string>& participants,
                          const std::string& session_id = "");

// cascade: parallel broadcast, then a synthesizer reduces all outputs into one.
void run_stream_cascade_mode(const std::vector<Agent>& agents,
                             const std::string& synth_name,
                             const Agent* synth_agent,
                             const std::string& prompt,
                             std::atomic<bool>* cancel,
                             const WriteEventFn& write_event,
                             std::map<std::string, std::string>& outputs,
                             std::vector<std::string>& participants,
                             const std::string& session_id = "");
