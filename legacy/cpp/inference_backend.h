#pragma once

#include "agent.h"
#include "agent_client_http.h"

#include <string>

/// Identifies a concrete inference transport implementation.
enum class BackendId {
    LlamaMetal,  /// llama-server HTTP (Metal GPU layers on Apple Silicon)
    PythonMlx,   /// mlx_lm.server HTTP (Python MLX coordinator path)
};

namespace inference_backend {

const char* id_name(BackendId id);
BackendId   from_name(const std::string& name, bool* ok = nullptr);

/// Sync completion via the selected backend (HTTP to agent port).
AttemptResult complete(BackendId id,
                       const Agent& agent,
                       const std::string& system_prompt,
                       const std::string& prompt,
                       const std::string& session_id = "");

/// Whether this agent's spawn config can use the given backend.
bool supports(const Agent& agent, BackendId id);

} // namespace inference_backend
