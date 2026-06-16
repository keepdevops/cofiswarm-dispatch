#pragma once

#include <string>
#include <vector>

namespace synthesis_budget {

std::string truncate_note(const std::string& s, size_t max_chars);

/** prefix + each (header[i]+body[i]) + footer must fit in max_total bytes. */
std::string assemble_fit(const std::string& prefix,
    const std::vector<std::string>& headers,
    std::vector<std::string>& bodies,
    const std::string& footer,
    size_t max_total);

}  // namespace synthesis_budget
