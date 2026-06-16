#include "session_store_text.h"
#include <string>

std::string json_string(const json& j, const std::string& key) {
    return j.contains(key) && j[key].is_string() ? j[key].get<std::string>() : std::string{};
}

bool include_name(const json& policy, const std::string& name) {
    if (!policy.contains("include") || !policy["include"].is_array()) return true;
    for (const auto& item : policy["include"]) {
        if (item.is_string() && item.get<std::string>() == name) return true;
    }
    return false;
}

std::string trim_block(const std::string& s, size_t max_chars) {
    if (s.size() <= max_chars) return s;
    if (max_chars < 256) return s.substr(0, max_chars);
    const size_t head = max_chars * 2 / 3;
    const size_t tail = max_chars - head;
    return s.substr(0, head)
        + "\n\n[...compacted: omitted "
        + std::to_string(s.size() - max_chars)
        + " chars...]\n\n"
        + s.substr(s.size() - tail);
}

std::string first_lines(const std::string& s, size_t max_lines, size_t max_chars) {
    std::string out;
    size_t lines = 0;
    for (char c : s) {
        if (out.size() >= max_chars) break;
        out.push_back(c);
        if (c == '\n' && ++lines >= max_lines) break;
    }
    if (s.size() > out.size()) out += "\n[...compacted...]";
    return out;
}

void append_section(std::ostringstream& os,
                    const std::string& title,
                    const std::string& body) {
    if (body.empty()) return;
    os << "\n\n## " << title << "\n" << body;
}

std::string first_prompt_for_session(const json& sessions, const std::string& session_id) {
    if (!sessions.is_object() || !sessions.contains(session_id)) return {};
    const json& runs = sessions[session_id].value("runs", json::array());
    if (!runs.is_array() || runs.empty()) return {};
    return json_string(runs.front(), "prompt");
}
