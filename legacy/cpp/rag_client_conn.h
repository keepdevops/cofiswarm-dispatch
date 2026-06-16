#pragma once
#include <libpq-fe.h>
#include <iostream>
#include <mutex>
#include <string>

namespace rag_conn {

struct Conn {
    std::mutex mu;
    PGconn*    pg  = nullptr;
    std::string current_dsn;

    ~Conn() { if (pg) PQfinish(pg); }
};

inline Conn& singleton() {
    static Conn c;
    return c;
}

inline bool ensure_open_locked(Conn& c, const std::string& dsn) {
    if (c.pg && PQstatus(c.pg) == CONNECTION_OK && c.current_dsn == dsn)
        return true;
    if (c.pg) { PQfinish(c.pg); c.pg = nullptr; }
    c.pg = PQconnectdb(dsn.c_str());
    c.current_dsn = dsn;
    if (PQstatus(c.pg) != CONNECTION_OK) {
        std::cerr << "❌ [rag] connect failed: " << PQerrorMessage(c.pg) << std::endl;
        PQfinish(c.pg);
        c.pg = nullptr;
        return false;
    }
    return true;
}

}  // namespace rag_conn
