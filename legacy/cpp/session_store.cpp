#include "session_store.h"
#include "session_store_text.h"
#include "session_store_context.h"

#include <chrono>
#include <fstream>
#include <iostream>
#include <sstream>

using json = nlohmann::json;

namespace {

long long epoch_ms() {
    return std::chrono::duration_cast<std::chrono::milliseconds>(
        std::chrono::system_clock::now().time_since_epoch()).count();
}

const json* latest_run_for_session(const json& sessions, const std::string& session_id) {
    if (!sessions.is_object() || !sessions.contains(session_id)) return nullptr;
    const json& sess = sessions[session_id];
    if (!sess.contains("runs") || !sess["runs"].is_array() || sess["runs"].empty()) return nullptr;
    return &sess["runs"].back();
}

}  // namespace

std::string session_new_id(const std::string& prefix) {
    static unsigned long counter = 0;
    std::ostringstream os;
    os << prefix << "_" << epoch_ms() << "_" << std::hex << ++counter;
    return os.str();
}

void session_load(json& sessions, const std::string& path) {
    std::ifstream f(path);
    if (!f.is_open()) { sessions = json::object(); return; }
    try {
        json doc = json::parse(f);
        sessions = doc.is_object() ? doc : json::object();
    } catch (const std::exception& e) {
        std::cerr << "❌ Failed to parse sessions: " << e.what() << std::endl;
        sessions = json::object();
    }
}

void session_save(const json& sessions, const std::string& path) {
    std::ofstream f(path);
    if (!f.is_open()) {
        std::cerr << "❌ Failed to open sessions file for writing: " << path << std::endl;
        return;
    }
    f << sessions.dump(2);
}

SessionContinuation session_build_continuation(
        const json& sessions,
        const std::string& session_id,
        const std::string& followup,
        const json& context_policy) {
    const json* prev = latest_run_for_session(sessions, session_id);
    if (!prev) {
        SessionContinuation out;
        out.prompt = followup;
        out.compaction = {{"used", false}, {"reason", "session_not_found"}};
        return out;
    }
    return session_ctx::build(sessions, session_id, followup, context_policy, prev);
}

void session_append_run(json& sessions,
                        const std::string& session_id,
                        const json& run) {
    const long long now = epoch_ms();
    json& sess = sessions[session_id];
    if (!sess.is_object()) sess = json::object();
    sess["id"] = session_id;
    if (!sess.contains("created_at")) sess["created_at"] = now;
    sess["updated_at"] = now;
    if (!sess.contains("runs") || !sess["runs"].is_array()) sess["runs"] = json::array();
    sess["runs"].push_back(run);
}
