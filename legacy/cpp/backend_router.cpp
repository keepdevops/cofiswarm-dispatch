#include "backend_router.h"

#include <cstdlib>
#include <iostream>
#include <map>
#include <mutex>
#if defined(__APPLE__)
#include <sys/sysctl.h>
#endif
#include <vector>

namespace backend_router {

namespace {

struct Config {
    bool enabled = false;
    std::string llama_priority = "normal"; // normal | high
};

struct ProbeStats {
    int samples = 0;
    double ema_ms = 0.0;
};

Config g_cfg;
std::mutex g_mu;
RoutingContext g_ctx;
bool g_ctx_set = false;

std::map<std::string, RoutingDecision> g_last_decisions;
std::vector<nlohmann::json> g_decision_log;
std::map<BackendId, ProbeStats> g_probe;

bool env_truthy(const char* name) {
    const char* v = std::getenv(name);
    return v && v[0] && v[0] != '0' && std::string(v) != "false";
}

bool tag_has(const Agent& agent, const char* needle) {
    for (const auto& t : agent.tags) {
        if (t == needle) return true;
    }
    return false;
}

BackendId engine_default(const Agent& agent) {
    return agent.engine == "mlx" ? BackendId::PythonMlx : BackendId::LlamaMetal;
}

RoutingDecision legacy_decision(const Agent& agent) {
    RoutingDecision d;
    d.backend = engine_default(agent);
    d.backend_name = inference_backend::id_name(d.backend);
    d.reason = "legacy_engine";
    return d;
}

BackendId pick_auto(const Agent& agent, const RoutingContext& ctx, std::string& reason_out) {
    const bool metal = apple_silicon_metal_available();
    const bool high  = llama_metal_priority() == "high";

    if (ctx.kv_pressure > 0.7 && inference_backend::supports(agent, BackendId::LlamaMetal)) {
        reason_out = "kv_pressure_llama_prefix_cache";
        return BackendId::LlamaMetal;
    }

    if (metal && ctx.sequential_mode && inference_backend::supports(agent, BackendId::LlamaMetal)) {
        if (high || tag_has(agent, "coding") || tag_has(agent, "planning") || tag_has(agent, "review")) {
            reason_out = "sequential_apple_silicon";
            return BackendId::LlamaMetal;
        }
    }

    if (inference_backend::supports(agent, BackendId::PythonMlx)
        && (agent.engine == "mlx" || tag_has(agent, "vision"))) {
        reason_out = "mlx_unified_memory";
        return BackendId::PythonMlx;
    }

    if (inference_backend::supports(agent, BackendId::LlamaMetal)) {
        reason_out = "default_llama_metal";
        return BackendId::LlamaMetal;
    }

    reason_out = "default_python_mlx";
    return BackendId::PythonMlx;
}

BackendId faster_probe_backend(const Agent& agent) {
    BackendId best = engine_default(agent);
    double best_ms = 1e18;
    for (auto id : {BackendId::LlamaMetal, BackendId::PythonMlx}) {
        if (!inference_backend::supports(agent, id)) continue;
        std::lock_guard<std::mutex> lk(g_mu);
        auto it = g_probe.find(id);
        if (it == g_probe.end() || it->second.samples < 2) continue;
        if (it->second.ema_ms < best_ms) {
            best_ms = it->second.ema_ms;
            best = id;
        }
    }
    return best;
}

} // namespace

void configure_from_startup(const nlohmann::json& startup_config) {
    std::lock_guard<std::mutex> lk(g_mu);
    g_cfg.enabled = env_truthy("MATRIX_BACKEND_ROUTING");
    if (startup_config.contains("coordinator")
        && startup_config["coordinator"].contains("backend_routing")
        && startup_config["coordinator"]["backend_routing"].is_object()) {
        const auto& br = startup_config["coordinator"]["backend_routing"];
        if (br.contains("enabled") && br["enabled"].is_boolean())
            g_cfg.enabled = br["enabled"].get<bool>() || g_cfg.enabled;
        if (br.contains("llama_metal_priority") && br["llama_metal_priority"].is_string())
            g_cfg.llama_priority = br["llama_metal_priority"].get<std::string>();
    }
    const char* pri = std::getenv("LLAMA_METAL_PRIORITY");
    if (pri && pri[0]) g_cfg.llama_priority = pri;
}

bool enabled() {
    std::lock_guard<std::mutex> lk(g_mu);
    return g_cfg.enabled;
}

void set_dispatch_context(const RoutingContext& ctx) {
    std::lock_guard<std::mutex> lk(g_mu);
    g_ctx = ctx;
    g_ctx_set = true;
}

void clear_dispatch_context() {
    std::lock_guard<std::mutex> lk(g_mu);
    g_ctx_set = false;
    g_last_decisions.clear();
}

const RoutingContext* current_context() {
    std::lock_guard<std::mutex> lk(g_mu);
    return g_ctx_set ? &g_ctx : nullptr;
}

bool should_route(const Agent& agent) {
    (void)agent;
    if (!enabled()) return false;
    const auto* ctx = current_context();
    if (!ctx || !ctx->sequential_mode) return false;
    if (ctx->mode_name == "flat") return false;
    return true;
}

RoutingDecision resolve(const Agent& agent) {
    if (!should_route(agent)) return legacy_decision(agent);

    const RoutingContext& ctx = *current_context();
    const std::string& ov = agent.inference_backend;

    if (ov == "llama_metal" || ov == "llama") {
        if (inference_backend::supports(agent, BackendId::LlamaMetal))
            return make_decision(BackendId::LlamaMetal, "agent_override");
        return make_decision(engine_default(agent), "override_unsupported_fallback", true);
    }
    if (ov == "python_mlx" || ov == "mlx") {
        if (inference_backend::supports(agent, BackendId::PythonMlx))
            return make_decision(BackendId::PythonMlx, "agent_override");
        return make_decision(engine_default(agent), "override_unsupported_fallback", true);
    }

    if (ov.empty() || ov == "auto") {
        std::string reason;
        BackendId id = pick_auto(agent, ctx, reason);
        BackendId probed = faster_probe_backend(agent);
        if (probed != id && inference_backend::supports(agent, probed)) {
            id = probed;
            reason += "+probe";
        }
        if (!inference_backend::supports(agent, id))
            return make_decision(engine_default(agent), "auto_unsupported_fallback", true);
        return make_decision(id, reason);
    }

    bool ok = false;
    BackendId named = inference_backend::from_name(ov, &ok);
    if (ok && inference_backend::supports(agent, named))
        return make_decision(named, "named_override");
    return legacy_decision(agent);
}

Agent materialize(const Agent& agent, const RoutingDecision& decision) {
    Agent out = agent;
    out.engine = (decision.backend == BackendId::PythonMlx) ? "mlx" : "llama";
    return out;
}

RoutingDecision make_decision(BackendId id, const std::string& reason, bool fallback) {
    RoutingDecision d;
    d.backend = id;
    d.backend_name = inference_backend::id_name(id);
    d.reason = reason;
    d.used_fallback = fallback;
    return d;
}

void record_decision(const std::string& agent_name, const RoutingDecision& decision) {
    if (agent_name.empty()) return;
    std::lock_guard<std::mutex> lk(g_mu);
    g_last_decisions[agent_name] = decision;
    g_decision_log.push_back({
        {"agent", agent_name},
        {"backend", decision.backend_name},
        {"reason", decision.reason},
        {"fallback", decision.used_fallback},
    });
    if (g_decision_log.size() > 256) g_decision_log.erase(g_decision_log.begin());
}

void record_probe_sample(BackendId id, double latency_ms) {
    if (latency_ms <= 0.0) return;
    std::lock_guard<std::mutex> lk(g_mu);
    ProbeStats& s = g_probe[id];
    if (s.samples == 0) s.ema_ms = latency_ms;
    else s.ema_ms = 0.8 * s.ema_ms + 0.2 * latency_ms;
    ++s.samples;
}

nlohmann::json snapshot_decisions() {
    std::lock_guard<std::mutex> lk(g_mu);
    nlohmann::json out = nlohmann::json::object();
    for (const auto& kv : g_last_decisions) {
        out[kv.first] = {
            {"backend", kv.second.backend_name},
            {"reason", kv.second.reason},
            {"fallback", kv.second.used_fallback},
        };
    }
    return out;
}

nlohmann::json decision_log_entries() {
    std::lock_guard<std::mutex> lk(g_mu);
    return nlohmann::json(g_decision_log);
}

bool apple_silicon_metal_available() {
#if defined(__APPLE__) && defined(__aarch64__)
    return true;
#elif defined(__APPLE__)
    // Apple x86 (e.g. under Rosetta): probe for an arm64-capable host.
    char brand[256] = {};
    size_t len = sizeof(brand);
    if (sysctlbyname("hw.optional.arm64", brand, &len, nullptr, 0) == 0 && brand[0])
        return true;
    return false;
#else
    return false;   // non-Apple (Linux): no Metal / Apple Silicon
#endif
}

std::string llama_metal_priority() {
    std::lock_guard<std::mutex> lk(g_mu);
    return g_cfg.llama_priority;
}

} // namespace backend_router
