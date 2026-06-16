#include "inference_backend.h"

#include <algorithm>
#include <cctype>

namespace inference_backend {

namespace {

bool looks_gguf(const std::string& model) {
    if (model.size() >= 5) {
        std::string tail = model.substr(model.size() - 5);
        for (auto& c : tail) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
        return tail == ".gguf";
    }
    return false;
}

} // namespace

const char* id_name(BackendId id) {
    switch (id) {
    case BackendId::LlamaMetal: return "llama_metal";
    case BackendId::PythonMlx:  return "python_mlx";
    }
    return "unknown";
}

BackendId from_name(const std::string& name, bool* ok) {
    if (name == "llama_metal" || name == "llama" || name == "llama.cpp") {
        if (ok) *ok = true;
        return BackendId::LlamaMetal;
    }
    if (name == "python_mlx" || name == "mlx") {
        if (ok) *ok = true;
        return BackendId::PythonMlx;
    }
    if (ok) *ok = false;
    return BackendId::LlamaMetal;
}

bool supports(const Agent& agent, BackendId id) {
    if (id == BackendId::LlamaMetal) {
        if (agent.engine == "llama") return true;
        if (agent.backend == "llama") return true;
        return looks_gguf(agent.model);
    }
    if (id == BackendId::PythonMlx) {
        if (agent.engine == "mlx") return true;
        if (agent.backend == "mlx") return true;
        if (agent.model.find("mlx") != std::string::npos) return true;
    }
    return false;
}

} // namespace inference_backend
