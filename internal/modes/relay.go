package modes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

var modePorts = map[string]int{
	"flat": 8021, "pipeline": 8022, "cascade": 8023, "router": 8024,
}

func Normalize(name string) string {
	switch strings.TrimSpace(strings.ToLower(name)) {
	case "", "flat", "mode-flat":
		return "flat"
	case "pipeline", "mode-pipeline":
		return "pipeline"
	case "cascade", "mode-cascade":
		return "cascade"
	case "router", "mode-router":
		return "router"
	default:
		return name
	}
}

func Port(mode string) (int, bool) {
	key := Normalize(mode)
	p, ok := modePorts[key]
	if !ok {
		return 0, false
	}
	if v := os.Getenv("COFISWARM_MODE_" + strings.ToUpper(key) + "_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n, true
		}
	}
	return p, true
}

func baseURL() string {
	if v := os.Getenv("COFISWARM_MODE_HOST"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1"
}

func registryURL() string {
	if v := os.Getenv("COFISWARM_AGENT_REGISTRY_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1:8012"
}

func ActiveMode() string {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(registryURL() + "/api/modes/active")
	if err != nil {
		return "flat"
	}
	defer resp.Body.Close()
	var out struct {
		Mode string `json:"mode"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil || out.Mode == "" {
		return "flat"
	}
	return Normalize(out.Mode)
}

func Execute(mode, prompt string, modeConfig map[string]any) (map[string]any, error) {
	port, ok := Port(mode)
	if !ok {
		return nil, fmt.Errorf("unknown mode %q", mode)
	}
	payload := map[string]any{"prompt": prompt}
	if len(modeConfig) > 0 {
		payload["mode_config"] = modeConfig
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s:%d/v1/execute", baseURL(), port)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	timeout := 120 * time.Second
	if v := os.Getenv("COFISWARM_MODE_EXECUTE_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			timeout = time.Duration(n) * time.Second
		}
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mode plugin %s: %s", mode, string(raw))
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	return env, nil
}
