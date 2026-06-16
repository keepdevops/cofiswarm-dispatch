#include "coordinator_routes_architect_persist.h"
#include "code_fence_normalize.h"
#include "coordinator_context.h"
#include "session_store.h"
#include <chrono>

void persist_stream_run(
    const std::string& user_prompt,
    double temperature,
    const std::string& mode,
    const std::string& session_id,
    const std::string& run_id,
    const std::string& parent_run_id,
    const std::map<std::string, std::string>& outputs,
    CoordinatorState& st,
    const WriteEventFn& write_event)
{
    long long ms = std::chrono::duration_cast<std::chrono::milliseconds>(
        std::chrono::system_clock::now().time_since_epoch()).count();

    json entry = json::object();
    for (const auto& [name, text] : outputs) entry[name] = text;
    entry["prompt"]      = user_prompt;
    entry["temperature"] = temperature;
    entry["timestamp"]   = ms;
    entry["_session_id"] = session_id;
    entry["_run_id"]     = run_id;
    entry["_mode"]       = mode;
    code_fence::normalize_agents_in_entry(entry);

    {
        std::lock_guard<std::mutex> lock(st.history_mutex);
        st.history.push_back(entry);
        coordinator_save_history(st);
    }
    {
        std::lock_guard<std::mutex> lock(st.sessions_mutex);
        json run = {
            {"run_id",        run_id},
            {"parent_run_id", parent_run_id},
            {"prompt",        user_prompt},
            {"mode",          mode},
            {"agents",        entry},
            {"timestamp",     ms}
        };
        session_append_run(st.sessions, session_id, run);
        coordinator_save_sessions(st);
    }
    write_event("session", json({
        {"session_id", session_id},
        {"run_id",     run_id}
    }).dump());
}
