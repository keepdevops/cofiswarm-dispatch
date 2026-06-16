#pragma once
#include "json.hpp"
#include <sstream>
#include <string>

using json = nlohmann::json;

std::string json_string(const json& j, const std::string& key);
bool        include_name(const json& policy, const std::string& name);
std::string trim_block(const std::string& s, size_t max_chars);
std::string first_lines(const std::string& s, size_t max_lines, size_t max_chars);
void        append_section(std::ostringstream& os,
                           const std::string& title, const std::string& body);
std::string first_prompt_for_session(const json& sessions, const std::string& session_id);
