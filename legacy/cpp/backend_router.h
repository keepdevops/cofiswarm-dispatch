#pragma once

#include "agent.h"
#include "inference_backend.h"
#include "json.hpp"

#include <string>

struct RoutingContext {
    std::string mode_name;
    bool sequential_mode = false;
    double kv_pressure = 0.0;
};

struct RoutingDecision {
    BackendId backend = BackendId::LlamaMetal;
    std::string backend_name;
    std::string reason;
    bool used_fallback = false;
};

namespace backend_router {

void configure_from_startup(const nlohmann::json& startup_config);
bool enabled();

void set_dispatch_context(const RoutingContext& ctx);
void clear_dispatch_context();
const RoutingContext* current_context();

/// True when the router may alter dispatch (enabled + sequential mode).
bool should_route(const Agent& agent);

RoutingDecision resolve(const Agent& agent);
Agent materialize(const Agent& agent, const RoutingDecision& decision);
RoutingDecision make_decision(BackendId id, const std::string& reason, bool fallback = false);

void record_decision(const std::string& agent_name, const RoutingDecision& decision);
void record_probe_sample(BackendId id, double latency_ms);

nlohmann::json snapshot_decisions();
nlohmann::json decision_log_entries();

bool apple_silicon_metal_available();
std::string llama_metal_priority();

} // namespace backend_router
