#include "coordinator_routes_includes.h"
#include "coordinator_routes_internal.h"
#include "backend_router.h"
#include "coordinator_routes_dispatch_prepare.h"
#include "coordinator_routes_dispatch_meta.h"
#include "coordinator_routes_dispatch_history.h"
#include "coordinator_routes_dispatch_post.h"
#include "session_context.h"
#include "token_ledger.h"
#include "token_budget_hierarchy.h"
#include "adaptive_select.h"
#include "symbolic_importance.h"
#include "rag_trajectory.h"
#include "rag_embed.h"
#include "context_gate.h"
#include "kv_auto_clear.h"
#include "rl_trajectory_logger.h"
#include "swarm_supervisor.h"
#include "coordinator_routes_push.h"
#include "rss_generator.h"
#include "token_ledger.h"
#include <algorithm>
#include <cmath>
#include <map>
#include <thread>
#include <unordered_set>

void register_coordinator_routes_dispatch(httplib::Server& svr, CoordinatorState& st) {
    // 5. Swarm dispatch — delegate to active mode
    svr.Post("/api/architect", [&st](const httplib::Request& req, httplib::Response& res) {
        res.set_header("Access-Control-Allow-Origin", "*");
        std::cout << "\n🚀 [Swarm Matrix] Incoming broadcast" << std::endl;
        if (req.body.empty()) {
            res.status = 400;
            res.set_content("{\"error\":\"empty body\"}", "application/json");
            return;
        }
        try {
            auto j_body = json::parse(req.body);
            DispatchRequest dreq = dispatch_parse_request(j_body);

            // Set thread-local session for token ledger recording in call_agent
            session_context::set(dreq.session_id);

            dreq.effective_prompt = dreq.prompt;
            if (dreq.followup) {
                std::lock_guard<std::mutex> lock(st.sessions_mutex);
                SessionContinuation cont = session_build_continuation(
                    st.sessions, dreq.session_id, dreq.prompt, dreq.context_policy);
                dreq.effective_prompt = cont.prompt;
                dreq.compaction       = cont.compaction;
            }

            RagResult rag = dispatch_build_rag(dreq, st);
            dreq.effective_prompt = rag.effective_prompt;

            // Summarization gate: compress prompt if it exceeds max_input_chars
            nlohmann::json gate_meta;
            {
                std::vector<Agent> gate_agents = filter_agents_for_mode(st, modes::active());
                dreq.effective_prompt = context_gate::maybe_summarise(
                    st.context_gate_config, gate_agents, dreq.effective_prompt, gate_meta);
            }

            std::cout << "📝 Prompt: " << dreq.prompt
                      << (dreq.followup ? " (follow-up)" : "") << std::endl;

            const std::string mode_name = modes::active();
            const Mode* mode = modes::get(mode_name);
            if (!mode) {
                session_context::clear();
                res.status = 500;
                res.set_content(json({{"error", "no active mode registered"}}).dump(),
                                "application/json");
                return;
            }
            json cfg_for_mode;
            {
                std::lock_guard<std::mutex> lock(st.modes_config_mutex);
                cfg_for_mode = st.modes_config.contains(mode_name)
                    ? st.modes_config[mode_name] : json::object();
            }

            // Token budget: mode override > global; set if not already budgeted this session
            // Resolve budget: agent-level > mode-level > global (hierarchy)
            int effective_budget = resolve_budget(st.token_budget_hierarchy, mode_name);
            if (cfg_for_mode.contains("token_budget") && cfg_for_mode["token_budget"].is_number_integer())
                effective_budget = cfg_for_mode["token_budget"].get<int>();
            if (effective_budget > 0)
                token_ledger::set_budget(dreq.session_id, effective_budget);

            // Adaptive max_select: shrink under KV pressure or budget overrun
            auto ledger = token_ledger::get(dreq.session_id);

            // Hard-stop on overrun when configured
            if (st.reject_on_overrun && ledger.overrun()) {
                session_context::clear();
                // RSS Token Regulation event (no-op unless rss.enabled).
                rss_generator::publish(rss_generator::Category::TokenRegulation,
                    "Token budget exceeded",
                    "session=" + dreq.session_id
                    + " consumed=" + std::to_string(ledger.consumed)
                    + " budget=" + std::to_string(ledger.budget));
                res.status = 429;
                res.set_content(json({
                    {"error",      "token_budget_exceeded"},
                    {"session_id", dreq.session_id},
                    {"consumed",   ledger.consumed},
                    {"budget",     ledger.budget},
                }).dump(), "application/json");
                return;
            }
            int base_max_select = cfg_for_mode.value("max_select", 5);
            // Use contract-aware, importance-rewarding adaptive select
            adaptive_select::Factors asel_factors{
                base_max_select, dreq.kv_pressure,
                st.contract_ledger.any_overrun(),
                -1.0  // importance from previous run; updated post-dispatch
            };
            int effective_max_select = adaptive_select::compute(asel_factors);
            cfg_for_mode["max_select"] = effective_max_select;

            std::vector<Agent> mode_agents = filter_agents_for_mode(st, mode_name);
            std::vector<std::string> excluded_unhealthy;
            mode_agents.erase(std::remove_if(mode_agents.begin(), mode_agents.end(),
                [&](const Agent& a) {
                    if (agent_health::is_open(a.name)) {
                        excluded_unhealthy.push_back(a.name);
                        return true;
                    }
                    return false;
                }), mode_agents.end());
            if (!excluded_unhealthy.empty()) {
                std::cerr << "🔴 [dispatch] excluding " << excluded_unhealthy.size()
                          << " agent(s) with open breaker:";
                for (const auto& n : excluded_unhealthy) std::cerr << ' ' << n;
                std::cerr << std::endl;
            }

            // Init per-agent contracts using hierarchy budgets
            {
                std::lock_guard<std::mutex> lk(st.contract_ledger.mu);
                st.contract_ledger.contracts.clear();
                st.contract_ledger.audit.clear();
            }
            for (const auto& a : mode_agents) {
                int alloc = resolve_budget(st.token_budget_hierarchy, mode_name, a.name);
                st.contract_ledger.init(a.name, alloc);
            }

            // Supervisor policy: analyse + apply before mode run
            nlohmann::json supervisor_meta;
            if (st.supervisor_enabled) {
                auto recent = rl_traj::snapshot(dreq.session_id);
                // Limit to last 5 for perf
                if (recent.size() > 5) recent = nlohmann::json(
                    std::vector<nlohmann::json>(recent.begin(), recent.begin() + 5));
                auto supv = swarm_supervisor::analyse(
                    mode_agents, st.contract_ledger,
                    dreq.kv_pressure, recent, true);
                if (supv.any_intervention) {
                    mode_agents = swarm_supervisor::apply(mode_agents, supv);
                    std::lock_guard<std::mutex> lk(st.supervisor_audit_mutex);
                    st.supervisor_audit.push_back(supv.audit_entry);
                }
                supervisor_meta = supv.audit_entry;
            }

            const std::string qp_target = dreq.context_policy.value("target_agent", std::string("programmer"));
            ModeContext ctx{mode_agents, dreq.effective_prompt, dreq.temperature,
                            cfg_for_mode, dreq.quality_pass, qp_target,
                            ledger.remaining(), dreq.kv_pressure,
                            rag.rag_block, dreq.rag_agents};

            agent_metrics::reset();
            RoutingContext rctx;
            rctx.mode_name = mode_name;
            rctx.sequential_mode = (mode_name == "pipeline" || mode_name == "cascade"
                                    || mode_name == "router");
            rctx.kv_pressure = dreq.kv_pressure;
            backend_router::set_dispatch_context(rctx);
            auto dispatch_t0 = std::chrono::steady_clock::now();
            json envelope;
            try { envelope = mode->run(ctx); }
            catch (const std::exception& e) {
                backend_router::clear_dispatch_context();
                session_context::clear();
                std::cerr << "❌ [mode:" << mode_name << "] " << e.what() << std::endl;
                res.status = 500;
                res.set_content(json({{"error", e.what()}, {"mode", mode_name}}).dump(),
                                "application/json");
                return;
            }

            auto now = std::chrono::system_clock::now();
            auto ms  = std::chrono::duration_cast<std::chrono::milliseconds>(
                now.time_since_epoch()).count();

            dispatch_meta::stamp_envelope(envelope, dreq, rag, excluded_unhealthy,
                                          qp_target, dispatch_t0, effective_max_select);
            envelope["meta"]["adaptive_select"] = {
                {"base_max_select", base_max_select},
                {"effective_max_select", effective_max_select},
                {"kv_pressure", dreq.kv_pressure},
                {"contract_overrun", st.contract_ledger.any_overrun()},
            };
            if (backend_router::enabled()) {
                auto routing = backend_router::snapshot_decisions();
                if (!routing.empty())
                    envelope["meta"]["routing"] = routing;
            }
            backend_router::clear_dispatch_context();
            if (gate_meta.value("triggered", false))
                envelope["meta"]["context_gate"] = gate_meta;
            if (st.supervisor_enabled && !supervisor_meta.is_null())
                envelope["meta"]["supervisor"] = supervisor_meta;

            // Symbolic importance scoring on agent outputs + RAG similarity enrichment
            dispatch_post::score_importance(envelope, dreq);

            // Contract snapshot in meta
            {
                auto snap = st.contract_ledger.snapshot();
                if (!snap.empty()) {
                    envelope["meta"]["contracts"] = snap;
                    if (st.contract_ledger.any_overrun())
                        envelope["meta"]["contract_overrun"] = true;
                }
            }

            dispatch_write_history(st, envelope, dreq.prompt, dreq.temperature, ms,
                                   dreq.session_id, dreq.run_id, dreq.parent_run_id,
                                   dreq.effective_prompt, dreq.followup,
                                   dreq.quality_pass, mode_name, dreq.compaction);

            // Auto KV clear: fire after dispatch if pressure + query divergence threshold met
            dispatch_post::maybe_auto_clear_kv(st, envelope, dreq);

            // Assemble and record RL trajectory + async quality webhook
            dispatch_post::record_trajectory(st, envelope, dreq, mode_name, ms);

            // RSS History event (no-op unless rss.enabled).
            rss_generator::publish(rss_generator::Category::History,
                "Dispatch: " + mode_name,
                "agents=" + std::to_string(mode_agents.size())
                + " wall_ms=" + std::to_string(envelope["meta"].value("wall_ms", 0.0)));

            session_context::clear();
            res.set_content(envelope.dump(), "application/json");
            std::cout << "✅ [Swarm Matrix] Response sent (mode=" << mode_name << ")" << std::endl;

        } catch (const std::exception& e) {
            session_context::clear();
            std::cerr << "❌ [Swarm Matrix] Error: " << e.what() << std::endl;
            res.status = 400;
            res.set_content("{\"error\":\"Invalid JSON or logic error\"}", "application/json");
        }
    });
}
