//go:build !no_authz

package mw

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// ExtAuthz calls an external authorization endpoint before every inbound request.
// A 200 response (or true OPA result) allows the request; anything else yields 403.
// On network error the behaviour is governed by cfg.ExtAuthz.FailOpen.
func ExtAuthz(cfg config.Config, next http.Handler, log *logging.Logger) http.Handler {
	client := authzClient(cfg.ExtAuthz)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authzAllow(r, cfg.ExtAuthz, client, log) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authzAllow(r *http.Request, cfg config.ExtAuthzConfig, client *http.Client, log *logging.Logger) bool {
	ctx, cancel := context.WithTimeout(r.Context(), cfg.Timeout.Duration)
	defer cancel()

	b, err := json.Marshal(authzPayload(r, cfg.Transport))
	if err != nil {
		log.Warn("authz_marshal_fail", map[string]any{"err": err.Error()})
		return cfg.FailOpen
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authzRequestURL(cfg), bytes.NewReader(b))
	if err != nil {
		log.Warn("authz_build_fail", map[string]any{"err": err.Error()})
		return cfg.FailOpen
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		log.Warn("authz_call_fail", map[string]any{"err": err.Error(), "fail_open": cfg.FailOpen})
		return cfg.FailOpen
	}
	defer resp.Body.Close()

	if cfg.Transport == "opa" {
		return authzParseOPA(resp, log)
	}
	return resp.StatusCode == http.StatusOK
}

// authzPayload builds the JSON body sent to the decision endpoint.
// For "opa" transport the input is wrapped in {"input":...} per OPA convention.
func authzPayload(r *http.Request, transport string) any {
	input := map[string]any{
		"method":  r.Method,
		"path":    r.URL.Path,
		"query":   r.URL.RawQuery,
		"headers": authzHeaders(r.Header),
		"remote":  r.RemoteAddr,
	}
	if transport == "opa" {
		return map[string]any{"input": input}
	}
	return input
}

func authzHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vv := range h {
		out[strings.ToLower(k)] = strings.Join(vv, ",")
	}
	return out
}

// authzParseOPA reads {"result":true/false} from an OPA policy evaluation response.
func authzParseOPA(resp *http.Response, log *logging.Logger) bool {
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var body struct {
		Result bool `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		log.Warn("authz_opa_parse_fail", map[string]any{"err": err.Error()})
		return false
	}
	return body.Result
}

// authzRequestURL returns the HTTP URL for the authz request.
// Unix socket endpoints (unix:///socket) use http://localhost/<cfg.ExtAuthz.Path>.
func authzRequestURL(cfg config.ExtAuthzConfig) string {
	if strings.HasPrefix(cfg.Endpoint, "unix://") {
		path := cfg.Path
		if path == "" {
			path = "/"
		}
		return "http://localhost" + path
	}
	return cfg.Endpoint
}

// authzClient builds an *http.Client suited to the configured endpoint.
// Unix socket endpoints get a custom dialer; HTTP(S) endpoints use the default.
func authzClient(cfg config.ExtAuthzConfig) *http.Client {
	if strings.HasPrefix(cfg.Endpoint, "unix://") {
		socketPath := strings.TrimPrefix(cfg.Endpoint, "unix://")
		return &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
				},
			},
		}
	}
	return &http.Client{}
}