#include "coordinator_routes_includes.h"
#include "coordinator_routes_internal.h"
#include "coordinator_routes_architect_stream_modes.h"
#include "coordinator_routes_architect_persist.h"
#include "coordinator_routes_architect_stream_parse.h"
#include "session_context.h"
#include "token_ledger.h"
#include "token_budget_hierarchy.h"
#include "session_store.h"
#include "backend_router.h"
#ifdef MATRIX_MLX_NATIVE_COORD
#include "mlx_session_store.h"
#endif

#include <condition_variable>
#include <deque>
#include <mutex>
#include <thread>

void register_coordinator_routes_architect_stream(httplib::Server& svr, CoordinatorState& st) {
    svr.Post("/api/architect/stream", [&st](const httplib::Request& req, httplib::Response& res) {
        res.set_header("Access-Control-Allow-Origin", "*");

        auto sreq = stream_parse::parse(req.body);
        if (sreq.user_prompt.empty()) {
            res.status = 400;
            res.set_content("{\"error\":\"empty prompt\"}", "application/json");
            return;
        }

        if (sreq.session_id.empty()) sreq.session_id = session_new_id("sess");
        const std::string run_id = session_new_id("run");

        std::string effective_prompt = sreq.user_prompt;
        if (sreq.followup && !sreq.session_id.empty()) {
            std::lock_guard<std::mutex> lock(st.sessions_mutex);
            SessionContinuation cont = session_build_continuation(
                st.sessions, sreq.session_id, sreq.user_prompt, sreq.context_policy);
            effective_prompt = cont.prompt;
        }

        const std::string mode_name = modes::active();
        json cfg_for_mode;
        {
            std::lock_guard<std::mutex> lock(st.modes_config_mutex);
            cfg_for_mode = st.modes_config.contains(mode_name)
                ? st.modes_config[mode_name] : json::object();
        }
        auto filtered = filter_agents_for_mode(st, mode_name);
        filtered.erase(std::remove_if(filtered.begin(), filtered.end(),
            [](const Agent& a){ return agent_health::is_open(a.name); }),
            filtered.end());

        auto agents_snap        = std::make_shared<std::vector<Agent>>(std::move(filtered));
        auto prompt_snap        = std::make_shared<std::string>(effective_prompt);
        auto cfg_snap           = std::make_shared<json>(std::move(cfg_for_mode));
        auto mode_snap          = std::make_shared<std::string>(mode_name);
        auto cancel             = std::make_shared<std::atomic<bool>>(false);
        auto session_id_snap    = std::make_shared<std::string>(sreq.session_id);
        auto run_id_snap        = std::make_shared<std::string>(run_id);
        auto parent_run_id_snap = std::make_shared<std::string>(sreq.parent_run_id);
        auto temperature_snap   = std::make_shared<double>(sreq.temperature);
        auto user_prompt_snap   = std::make_shared<std::string>(sreq.user_prompt);
        // MS-161 Phase C: KV pressure for routing (optional; absent in stream body → 0).
        double kv_pressure = 0.0;
        try {
            auto jb = json::parse(req.body);
            if (jb.is_object()) kv_pressure = jb.value("kv_pressure", 0.0);
        } catch (...) { /* body already validated by stream_parse; default 0 */ }
        auto kv_pressure_snap = std::make_shared<double>(kv_pressure);
        agent_metrics::reset();

        // Budget: apply global budget to session before streaming begins
        {
            int gb = resolve_budget(st.token_budget_hierarchy, mode_name);
            if (cfg_for_mode.contains("token_budget")
                && cfg_for_mode["token_budget"].is_number_integer())
                gb = cfg_for_mode["token_budget"].get<int>();
            if (gb > 0) token_ledger::set_budget(sreq.session_id, gb);
        }

        // Hard-stop on overrun before opening the stream
        if (st.reject_on_overrun && token_ledger::get(sreq.session_id).overrun()) {
            res.status = 429;
            res.set_content(json({
                {"error",      "token_budget_exceeded"},
                {"session_id", sreq.session_id},
            }).dump(), "application/json");
            return;
        }

        res.set_chunked_content_provider("text/event-stream",
            [agents_snap, prompt_snap, user_prompt_snap, cfg_snap, mode_snap, cancel,
             session_id_snap, run_id_snap, parent_run_id_snap, temperature_snap,
             kv_pressure_snap, &st]
            (size_t /*offset*/, httplib::DataSink& sink) -> bool {
                session_context::set(*session_id_snap);

                // Decoupled SSE writer. Under cascade/flat the broadcast fans out
                // one thread per agent, each calling write_event from inside its
                // llama read-callback. If those calls wrote to the sink directly
                // (under a shared mutex) the network write would serialize all
                // producers AND backpressure into the llama sockets, throttling
                // generation. Instead producers enqueue pre-formatted frames and a
                // single writer thread drains them to the sink — sink.write never
                // sits on the generation hot path.
                std::mutex q_mu;
                std::condition_variable q_cv;
                std::deque<std::string> q_frames;
                bool q_closed = false;

                WriteEventFn write_event = [&](const std::string& event,
                                               const std::string& data_json) {
                    std::string frame = "event: " + event + "\ndata: "
                                        + data_json + "\n\n";
                    {
                        std::lock_guard<std::mutex> lk(q_mu);
                        q_frames.push_back(std::move(frame));
                    }
                    q_cv.notify_one();
                };

                std::thread sink_writer([&]() {
                    for (;;) {
                        std::string frame;
                        {
                            std::unique_lock<std::mutex> lk(q_mu);
                            q_cv.wait(lk, [&] {
                                return q_closed || !q_frames.empty();
                            });
                            if (q_frames.empty()) return;  // closed and drained
                            frame = std::move(q_frames.front());
                            q_frames.pop_front();
                        }
                        // Client gone: signal cancel so producers wind down, then
                        // drop remaining frames instead of blocking on a dead sink.
                        if (!sink.is_writable()) {
                            cancel->store(true);
                            std::lock_guard<std::mutex> lk(q_mu);
                            q_frames.clear();
                            continue;
                        }
                        sink.write(frame.data(), frame.size());
                    }
                });

                std::string synth_name;
                if (cfg_snap->contains("synthesizer")
                    && (*cfg_snap)["synthesizer"].is_string())
                    synth_name = (*cfg_snap)["synthesizer"].get<std::string>();

                const Agent* synth_agent = nullptr;
                for (const auto& a : *agents_snap)
                    if (a.name == synth_name) { synth_agent = &a; break; }

                std::map<std::string, std::string> outputs;
                std::vector<std::string> participants;

                // MS-161 Phase C: activate backend routing for sequential stream
                // modes (pipeline/cascade/router). Flat stays legacy because
                // should_route() rejects mode_name == "flat". Cleared after the run.
                {
                    RoutingContext rctx;
                    rctx.mode_name      = *mode_snap;
                    rctx.sequential_mode = (*mode_snap == "pipeline"
                                            || *mode_snap == "cascade"
                                            || *mode_snap == "router");
                    rctx.kv_pressure    = *kv_pressure_snap;
                    backend_router::set_dispatch_context(rctx);
                }

#ifdef MATRIX_MLX_NATIVE_COORD
                // MS-149: seed mlx_sessions with the user turn once if any MLX
                // agent is in the run, so stream_mlx can inject history.
                {
                    bool has_mlx = false;
                    for (const auto& a : *agents_snap)
                        if (a.engine == "mlx") { has_mlx = true; break; }
                    if (has_mlx) {
                        mlx_sessions().cleanup_idle();
                        mlx_sessions().append_message(
                            *session_id_snap, "user", *user_prompt_snap);
                    }
                }
#endif

                if (*mode_snap == "pipeline") {
                    run_stream_pipeline_mode(*agents_snap, *cfg_snap, synth_name,
                        synth_agent, *prompt_snap, *mode_snap, cancel.get(),
                        write_event, outputs, participants, *session_id_snap);
                } else if (*mode_snap == "router") {
                    run_stream_router_mode(*agents_snap, *cfg_snap, *prompt_snap,
                        cancel.get(), write_event, outputs, *session_id_snap);
                } else if (*mode_snap == "cascade") {
                    run_stream_cascade_mode(*agents_snap, synth_name, synth_agent,
                        *prompt_snap, cancel.get(), write_event,
                        outputs, participants, *session_id_snap);
                } else {
                    run_stream_flat_mode(*agents_snap, synth_name, *prompt_snap,
                        cancel.get(), write_event, outputs, participants,
                        *session_id_snap);
                }

                write_event("metrics", agent_metrics::snapshot().dump());

                // MS-161 Phase C: surface per-agent routing decisions, then clear
                // the dispatch context so it never leaks into the next request.
                if (backend_router::enabled()) {
                    auto routing = backend_router::snapshot_decisions();
                    if (!routing.empty()) write_event("routing", routing.dump());
                }
                backend_router::clear_dispatch_context();

                persist_stream_run(*user_prompt_snap, *temperature_snap, *mode_snap,
                    *session_id_snap, *run_id_snap, *parent_run_id_snap,
                    outputs, st, write_event);

                session_context::clear();

                // Close the queue and let the writer flush every enqueued frame
                // (metrics/routing/persist events) before we finalize the stream.
                {
                    std::lock_guard<std::mutex> lk(q_mu);
                    q_closed = true;
                }
                q_cv.notify_one();
                sink_writer.join();

                if (sink.is_writable()) {
                    std::string fin = "event: done\ndata: [DONE]\n\n";
                    sink.write(fin.data(), fin.size());
                }
                sink.done();
                return true;
            });
    });
}
