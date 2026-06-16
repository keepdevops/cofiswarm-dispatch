#pragma once
#include "coordinator_routes_includes.h"
#include <atomic>
#include <functional>
#include <map>
#include <string>
#include <vector>

using WriteEventFn = std::function<void(const std::string&, const std::string&)>;

/** Run the synthesis agent over a set of contributor outputs.
 *  Writes token/agent_done events via write_event. No-op if synth_agent is null. */
void run_stream_synthesis(
    const Agent* synth_agent,
    const std::string& prompt,
    const std::string& mode,
    const std::vector<std::string>& contributors,
    std::map<std::string, std::string>& outputs,
    std::atomic<bool>* cancel,
    const WriteEventFn& write_event);
