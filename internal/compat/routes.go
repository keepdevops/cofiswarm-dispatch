package compat

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/swarm-config", serveSwarmConfig)
	mux.HandleFunc("/api/models", serveModels)
	mux.HandleFunc("/api/memory", serveMemory)
	mux.HandleFunc("/api/mlx/pressure", serveMlxPressure)
	mux.HandleFunc("/api/mlx/session/clear", serveMlxSessionClear)
	mux.HandleFunc("/api/clear-cache", serveClearCache)
	mux.HandleFunc("/api/cache", serveCache)
	mux.HandleFunc("/api/cache/clear", serveCacheClear)
	mux.HandleFunc("/api/cache/config", serveCacheConfig)
	mux.HandleFunc("/api/rag/health", proxyRagHealth)
	mux.HandleFunc("/api/logs", serveLogs)
}

func cors(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
}

func serveSwarmConfig(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	path := os.Getenv("COFISWARM_SWARM_CONFIG")
	if path == "" {
		path = "/etc/cofiswarm/config/swarm-config.json"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func serveModels(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	manifest := os.Getenv("COFISWARM_MODELS_MANIFEST")
	if manifest == "" {
		home := os.Getenv("HOME")
		if home != "" {
			manifest = filepath.Join(home, "cofiswarm", "repos", "cofiswarm-models", "catalog", "manifest.json")
		}
	}
	type manifestFile struct {
		ServerGroups []struct {
			Model string `json:"model"`
		} `json:"server_groups"`
	}
	models := []string{}
	if b, err := os.ReadFile(manifest); err == nil {
		var mf manifestFile
		if json.Unmarshal(b, &mf) == nil {
			seen := map[string]bool{}
			for _, g := range mf.ServerGroups {
				if g.Model != "" && !seen[g.Model] {
					seen[g.Model] = true
					models = append(models, g.Model)
				}
			}
		}
	}
	_ = json.NewEncoder(w).Encode(models)
}

func serveMemory(w http.ResponseWriter, r *http.Request) {
	cors(w)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": true, "source": "stub", "note": "host memory via slot-manager sprint 17+",
	})
}

func serveMlxPressure(w http.ResponseWriter, r *http.Request) {
	cors(w)
	upstream := os.Getenv("COFISWARM_SLOT_MANAGER_URL")
	if upstream == "" {
		upstream = "http://127.0.0.1:8013"
	}
	resp, err := http.Get(strings.TrimRight(upstream, "/") + "/api/pressure")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"endpoints": []any{}, "unified_memory": nil})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var endpoints []any
	_ = json.Unmarshal(body, &endpoints)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"endpoints": endpoints, "unified_memory": nil, "source": "slot-manager",
	})
}

func serveMlxSessionClear(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func serveClearCache(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "note": "kvpool eviction sprint 17+"})
}

func serveCache(w http.ResponseWriter, r *http.Request) {
	cors(w)
	_ = json.NewEncoder(w).Encode(map[string]any{"enabled": false, "entries": 0})
}

func serveCacheClear(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func serveCacheConfig(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func proxyRagHealth(w http.ResponseWriter, r *http.Request) {
	cors(w)
	base := os.Getenv("COFISWARM_RAG_URL")
	if base == "" {
		base = "http://127.0.0.1:8001"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(strings.TrimRight(base, "/") + "/health")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func serveLogs(w http.ResponseWriter, r *http.Request) {
	cors(w)
	_ = json.NewEncoder(w).Encode(map[string]any{"logs": []any{}})
}
