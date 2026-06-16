#pragma once
// Thread-local session ID so call_agent can record to the token ledger
// without carrying session_id through every call-site signature.
// Set at dispatch entry, cleared on exit.

#include <string>

namespace session_context {

namespace detail {
inline thread_local std::string tl_id;
}

inline void set(const std::string& id)   { detail::tl_id = id; }
inline void clear()                       { detail::tl_id.clear(); }
inline const std::string& current()      { return detail::tl_id; }

} // namespace session_context
