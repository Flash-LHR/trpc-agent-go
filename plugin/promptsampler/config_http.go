//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptsampler

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

// ConfigHandlerOption configures a configHandler at construction time.
//
// At least one authentication option (WithAdminToken, WithAdminTokens or
// WithAuthFunc) must be provided. A ConfigHandler built without any auth
// option rejects every request with 401 to avoid accidentally exposing the
// control plane.
type ConfigHandlerOption func(*configHandler)

// WithAdminToken appends a single admin token to the handler's accepted
// token list. Multiple calls are additive.
//
// The admin token gates HTTP access to ConfigHandler (i.e. it is the
// operator credential). It is unrelated to RuntimeConfig.SamplerToken, which
// is forwarded to the log_collector as a business isolation label. These two
// tokens must not be reused or confused; leaking the admin token exposes the
// control plane, leaking the SamplerToken exposes the tenant.
func WithAdminToken(token string) ConfigHandlerOption {
	return func(h *configHandler) {
		if token == "" {
			return
		}
		h.adminTokens = append(h.adminTokens, []byte(token))
	}
}

// WithAdminTokens appends multiple admin tokens to the handler's accepted
// list. It is a convenience for token rotation scenarios.
func WithAdminTokens(tokens ...string) ConfigHandlerOption {
	return func(h *configHandler) {
		for _, t := range tokens {
			if t == "" {
				continue
			}
			h.adminTokens = append(h.adminTokens, []byte(t))
		}
	}
}

// WithAuthFunc installs a custom authentication predicate. When set, the
// function is consulted exclusively (static admin tokens are ignored unless
// the function chooses to look them up). Return true to accept the request.
//
// Use WithAuthFunc when the host process needs IAM / signature / allowlist
// logic beyond a static bearer token list.
func WithAuthFunc(fn func(*http.Request) bool) ConfigHandlerOption {
	return func(h *configHandler) { h.authFunc = fn }
}

// ConfigHandler returns an http.Handler that serves the sampler's
// control-plane API: GET, PUT and DELETE on a path whose prefix is chosen
// by the caller (see mux.Handle in the package documentation).
//
// The handler dispatches exclusively on r.Method and the ?app= query
// parameter and therefore works under any prefix. It does not own or
// validate URL paths beyond what the enclosing ServeMux delivered.
//
// Authentication is mandatory: a handler built without any auth option
// responds to every request with 401 Unauthorized.
func (s *PromptSampler) ConfigHandler(opts ...ConfigHandlerOption) http.Handler {
	h := &configHandler{sampler: s}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// configHandler is the unexported implementation returned by ConfigHandler.
// It is deliberately stateless with respect to URL prefixes: the enclosing
// ServeMux handles path routing.
type configHandler struct {
	sampler *PromptSampler
	// adminTokens holds valid bearer tokens for the default static-token
	// auth strategy. Comparisons use constant-time equality.
	adminTokens [][]byte
	// authFunc, when set, fully replaces the static token list and is the
	// sole source of truth for "is this request allowed".
	authFunc func(*http.Request) bool
}

// ServeHTTP implements http.Handler. The handler always authenticates
// first; then dispatches on r.Method.
func (h *configHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		// The auth failure path intentionally logs the remote address
		// and URL but never the token value, so that the log cannot be
		// abused to leak credentials.
		log.ErrorfContext(r.Context(),
			"[promptsampler] ConfigHandler: unauthorized: method=%s path=%s remote=%s",
			r.Method, r.URL.Path, r.RemoteAddr,
		)
		h.writeError(w, r, http.StatusUnauthorized, h.unauthorizedMessage())
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGet(w, r)
	case http.MethodPut:
		h.handlePut(w, r)
	case http.MethodDelete:
		h.handleDelete(w, r)
	default:
		w.Header().Set("Allow", strings.Join(
			[]string{http.MethodGet, http.MethodPut, http.MethodDelete},
			", ",
		))
		h.writeError(w, r, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ---------- auth ----------

// authenticate returns true when the request should be allowed.
//
// Resolution order:
//  1. If an authFunc is set, its return value is authoritative (ignores
//     static tokens entirely; callers who want both semantics can call
//     WithAdminToken logic themselves inside authFunc).
//  2. Otherwise, check the configured static admin tokens. A request is
//     accepted iff its bearer token matches one of them in constant time.
//  3. Otherwise (no auth configured at all), reject.
func (h *configHandler) authenticate(r *http.Request) bool {
	if h.authFunc != nil {
		return h.authFunc(r)
	}
	if len(h.adminTokens) == 0 {
		return false
	}
	token := extractToken(r)
	if token == "" {
		return false
	}
	provided := []byte(token)
	for _, accepted := range h.adminTokens {
		// ConstantTimeCompare returns 1 iff the byte slices are equal
		// *and* of the same length; we pre-check the length implicitly
		// by the comparison semantics.
		if subtle.ConstantTimeCompare(provided, accepted) == 1 {
			return true
		}
	}
	return false
}

// unauthorizedMessage returns the JSON error message used in 401 responses.
// It distinguishes "no auth configured" (a misconfiguration) from "bad
// token" so that operators see the root cause quickly.
func (h *configHandler) unauthorizedMessage() string {
	if h.authFunc == nil && len(h.adminTokens) == 0 {
		return "no admin auth configured for ConfigHandler"
	}
	return "unauthorized"
}

// extractToken reads the bearer token from the request. The Authorization
// header is preferred (so it never shows up in access logs); if absent, the
// ?admin_token= query parameter is used as a fallback.
func extractToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(auth, prefix) {
			return strings.TrimSpace(auth[len(prefix):])
		}
	}
	return r.URL.Query().Get("admin_token")
}

// ---------- GET ----------

// handleGet returns either the full snapshot (default + apps) when ?app is
// absent, or a single {"config": ..., "source": "override|default"} when
// ?app is supplied.
func (h *configHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	app, hasApp := readAppParam(r)

	if !hasApp {
		body := map[string]any{
			"config": h.sampler.GetConfig(),
			"apps":   h.sampler.ListAppConfigs(),
		}
		h.writeJSON(w, r, http.StatusOK, body)
		return
	}
	// Empty app value (?app=) is treated as "use default", mirroring the
	// PUT semantics. We surface that as source=default so operators can
	// tell their query hit the default branch.
	cfg, isOverride := h.sampler.GetAppConfig(app)
	source := "default"
	if isOverride {
		source = "override"
	}
	h.writeJSON(w, r, http.StatusOK, map[string]any{
		"config": cfg,
		"source": source,
	})
}

// ---------- PUT ----------

// configEnvelope is the shared on-the-wire shape for PUT requests and GET
// single-config responses. The outer wrapper keeps forward compatibility
// with platforms that already speak the historical contract.
type configEnvelope struct {
	Config *RuntimeConfig `json:"config"`
}

func (h *configHandler) handlePut(w http.ResponseWriter, r *http.Request) {
	cfg, err := decodeConfigBody(r)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if err := cfg.Validate(); err != nil {
		h.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	app, hasApp := readAppParam(r)
	if !hasApp || app == "" {
		if err := h.sampler.SetConfig(cfg); err != nil {
			h.writeError(w, r, http.StatusBadRequest, err.Error())
			return
		}
		// Respond with the latest snapshot so callers can confirm.
		h.writeJSON(w, r, http.StatusOK, map[string]any{
			"config": h.sampler.GetConfig(),
			"apps":   h.sampler.ListAppConfigs(),
		})
		return
	}

	if err := h.sampler.SetAppConfig(app, cfg); err != nil {
		h.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	effective, isOverride := h.sampler.GetAppConfig(app)
	source := "default"
	if isOverride {
		source = "override"
	}
	h.writeJSON(w, r, http.StatusOK, map[string]any{
		"config": effective,
		"source": source,
	})
}

// decodeConfigBody parses the PUT body into a RuntimeConfig, enforcing the
// outer "config" wrapper and guarding against multi-document bodies.
func decodeConfigBody(r *http.Request) (*RuntimeConfig, error) {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var envelope configEnvelope
	if err := dec.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("invalid request body: %v", err)
	}
	if envelope.Config == nil {
		return nil, errors.New("request body must contain a 'config' field")
	}
	// Reject trailing tokens so that clients can't smuggle extra config
	// objects past the JSON decoder by concatenating JSON documents.
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err == nil {
		return nil, errors.New("invalid request body: request must contain a single JSON object")
	}
	return envelope.Config, nil
}

// ---------- DELETE ----------

func (h *configHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	app, hasApp := readAppParam(r)
	if !hasApp || app == "" {
		w.Header().Set("Allow", strings.Join(
			[]string{http.MethodGet, http.MethodPut, http.MethodDelete},
			", ",
		))
		h.writeError(w, r, http.StatusMethodNotAllowed,
			"default config cannot be deleted, use PUT to reset it")
		return
	}
	if removed := h.sampler.DeleteAppConfig(app); !removed {
		h.writeError(w, r, http.StatusNotFound, "app override not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- helpers ----------

// readAppParam returns the ?app= value from the request. The second return
// value indicates whether the query parameter was present at all (as
// opposed to present-but-empty).
func readAppParam(r *http.Request) (app string, hasApp bool) {
	q := r.URL.Query()
	if _, ok := q["app"]; !ok {
		return "", false
	}
	return q.Get("app"), true
}

// writeJSON writes a JSON response body using the common Content-Type and
// status handling expected by the spec.
func (h *configHandler) writeJSON(
	w http.ResponseWriter, r *http.Request, status int, body any,
) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.ErrorfContext(r.Context(),
			"[promptsampler] ConfigHandler: write json failed: method=%s path=%s err=%v",
			r.Method, r.URL.Path, err,
		)
	}
}

// writeError writes a {"error": msg} JSON response. It is the single exit
// point for all failure paths, which keeps the wire format consistent and
// makes sure client code can rely on the shape.
func (h *configHandler) writeError(
	w http.ResponseWriter, r *http.Request, status int, msg string,
) {
	h.writeJSON(w, r, status, map[string]string{"error": msg})
}
