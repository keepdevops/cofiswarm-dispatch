#include "synthesis_budget_assemble.h"

#include <algorithm>
#include <iostream>
#include <string>
#include <vector>

namespace synthesis_budget {

std::string truncate_note(const std::string& s, size_t max_chars) {
    if (s.size() <= max_chars) return s;
    if (max_chars <= 80) return s.substr(0, max_chars);
    return s.substr(0, max_chars - 1) + "\n\n[…truncated for synthesizer context budget]";
}

std::string assemble_fit(const std::string& prefix,
    const std::vector<std::string>& headers,
    std::vector<std::string>& bodies,
    const std::string& footer,
    size_t max_total) {
    auto total_len = [&]() {
        size_t t = prefix.size() + footer.size();
        for (size_t i = 0; i < bodies.size(); ++i) t += headers[i].size() + bodies[i].size();
        return t;
    };

    if (bodies.size() != headers.size()) return prefix + footer;

    size_t overhead = prefix.size() + footer.size();
    for (const auto& h : headers) overhead += h.size();

    if (max_total <= overhead + 16) {
        std::cerr << "⚠️  [synthesis] context budget smaller than fixed overhead; aggressive trim"
                  << std::endl;
        std::string p = truncate_note(prefix, max_total / 3);
        std::string out = p;
        for (size_t i = 0; i < bodies.size(); ++i) {
            out += headers[i];
            out += truncate_note(bodies[i], 256);
        }
        out += footer;
        return truncate_note(out, max_total);
    }

    if (total_len() <= max_total) return [&]() {
            std::string out;
            out.reserve(max_total);
            out += prefix;
            for (size_t i = 0; i < bodies.size(); ++i) {
                out += headers[i];
                out += bodies[i];
            }
            out += footer;
            return out;
        }();

    size_t room = max_total - overhead;

    {
        size_t n = bodies.size();
        if (n == 0) n = 1;
        size_t total_body = 0;
        for (const auto& b : bodies) total_body += b.size();

        if (total_body > room) {
            std::vector<size_t> alloc(bodies.size());
            size_t claimed = 0;
            for (size_t i = 0; i < bodies.size(); ++i) {
                alloc[i] = (room * bodies[i].size()) / total_body;
                alloc[i] = std::max<size_t>(alloc[i], 128);
                claimed += std::min(bodies[i].size(), alloc[i]);
            }
            size_t slack = room > claimed ? room - claimed : 0;
            if (slack > 0) {
                for (size_t i = 0; i < bodies.size() && slack > 0; ++i) {
                    if (bodies[i].size() > alloc[i]) {
                        size_t extra = std::min(slack, bodies[i].size() - alloc[i]);
                        alloc[i] += extra;
                        slack -= extra;
                    }
                }
            }
            for (size_t i = 0; i < bodies.size(); ++i) {
                if (bodies[i].size() > alloc[i])
                    bodies[i] = truncate_note(bodies[i], alloc[i]);
            }
        }
    }

    std::string out;
    out.reserve(std::min(max_total, total_len()));
    out += prefix;
    for (size_t i = 0; i < bodies.size(); ++i) {
        out += headers[i];
        out += bodies[i];
    }
    out += footer;
    if (out.size() > max_total) out = truncate_note(out, max_total);

    std::cerr << "⚠️  [synthesis] reduced synthesizer prompt to fit "
              << (max_total / 4) << " approximate tokens (set MATRIX_SYNTHESIS_MAX_PROMPT_TOKENS "
              << "or raise per-agent context / deploy)" << std::endl;
    return out;
}

}  // namespace synthesis_budget
