#include "inference_backend.h"
#include "agent_client_http.h"

namespace inference_backend {

namespace {

Agent with_engine(const Agent& agent, const std::string& engine) {
    Agent out = agent;
    out.engine = engine;
    return out;
}

} // namespace

AttemptResult complete(BackendId id,
                       const Agent& agent,
                       const std::string& system_prompt,
                       const std::string& prompt,
                       const std::string& session_id) {
    switch (id) {
    case BackendId::LlamaMetal:
        return call_agent_once(with_engine(agent, "llama"), system_prompt, prompt, session_id);
    case BackendId::PythonMlx:
        return call_agent_once(with_engine(agent, "mlx"), system_prompt, prompt, session_id);
    }
    return call_agent_once(agent, system_prompt, prompt, session_id);
}

} // namespace inference_backend
