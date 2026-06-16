#include "rag_client_http.h"

#define CPPHTTPLIB_NO_EXCEPTIONS 1
#include "httplib.h"
#include "json.hpp"

#include <cstdio>
#include <iostream>
#include <sstream>

namespace rag {

bool parse_http_url(const std::string& url, std::string& host,
                    int& port, std::string& path) {
    const std::string prefix = "http://";
    if (url.substr(0, prefix.size()) != prefix) return false;
    std::string rest = url.substr(prefix.size());
    auto slash = rest.find('/');
    path = (slash == std::string::npos) ? "/" : rest.substr(slash);
    std::string hostport = (slash == std::string::npos) ? rest : rest.substr(0, slash);
    auto colon = hostport.rfind(':');
    if (colon == std::string::npos) { host = hostport; port = 80; }
    else {
        host = hostport.substr(0, colon);
        port = std::stoi(hostport.substr(colon + 1));
    }
    return !host.empty() && port > 0 && port < 65536;
}

std::vector<double> mlx_embed(const std::string& embed_url,
                               const std::string& query) {
    std::string host, path;
    int port = 0;
    if (!parse_http_url(embed_url, host, port, path)) {
        std::cerr << "❌ [rag] invalid embed_url: " << embed_url << std::endl;
        return {};
    }
    httplib::Client cli(host, port);
    cli.set_connection_timeout(2);
    cli.set_read_timeout(10);
    nlohmann::json body = {{"texts", {query}}};
    auto res = cli.Post(path.c_str(), body.dump(), "application/json");
    if (!res || res->status != 200) {
        std::cerr << "❌ [rag] embed sidecar error at " << embed_url
                  << " status=" << (res ? res->status : -1) << std::endl;
        return {};
    }
    try {
        auto j = nlohmann::json::parse(res->body);
        auto& vecs = j.at("vectors");
        if (vecs.empty()) return {};
        std::vector<double> out;
        out.reserve(vecs[0].size());
        for (double v : vecs[0]) out.push_back(v);
        return out;
    } catch (const std::exception& e) {
        std::cerr << "❌ [rag] embed sidecar parse error: " << e.what() << std::endl;
        return {};
    }
}

std::string vec_literal(const std::vector<double>& v) {
    std::ostringstream os;
    os << '[';
    for (size_t i = 0; i < v.size(); ++i) {
        if (i) os << ',';
        char buf[32];
        std::snprintf(buf, sizeof(buf), "%.6f", v[i]);
        os << buf;
    }
    os << ']';
    return os.str();
}

} // namespace rag
