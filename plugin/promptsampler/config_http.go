//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptsampler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

// ConfigHandlerOption configures a configHandler at construction time.
//
// ConfigHandler is permissive-by-default: when constructed with no options
// it serves every request. Pass WithAuthFunc(...) to attach a custom
// authentication predicate (IAM, signature, allowlist, static token, etc.)
// — that is the sole in-plugin extension point for access control.
//
// Tenant-level access control (deciding which SamplerToken values are
// acceptable) is deliberately out of scope here; it belongs to the
// downstream log_collector.
type ConfigHandlerOption func(*configHandler)

// WithAuthFunc installs a custom authentication predicate. When set,
// ConfigHandler invokes it on every request and serves only when it
// returns true; otherwise the request is rejected with 401 Unauthorized.
//
// Typical use cases:
//
//   - Bearer token allowlist:
//     WithAuthFunc(func(r *http.Request) bool {
//         return r.Header.Get("Authorization") == "Bearer " + os.Getenv("MY_TOKEN")
//     })
//
//   - IP allowlist, IAM session verification, or HMAC signature checks.
//
// Without WithAuthFunc, ConfigHandler does not authenticate requests.
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
// Authentication: ConfigHandler is permissive by default. When constructed
// without any WithAuthFunc option, every request is served. To impose
// access control, supply a WithAuthFunc predicate or wrap the returned
// handler in a caller-owned HTTP middleware.
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
	// authFunc, when non-nil, is consulted on every request; its return
	// value is the sole source of truth for "is this request allowed".
	// When nil, the handler serves all requests without authentication.
	authFunc func(*http.Request) bool
}

// ServeHTTP implements http.Handler. When an auth predicate has been
// supplied via WithAuthFunc, it is consulted first; otherwise the handler
// dispatches directly on r.Method.
func (h *configHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.authFunc != nil && !h.authFunc(r) {
		// The auth failure path intentionally logs the remote address
		// and URL but never request bodies or headers, so the log
		// cannot be abused to leak credentials.
		log.ErrorfContext(r.Context(),
			"[promptsampler] ConfigHandler: unauthorized: method=%s path=%s remote=%s",
			r.Method, r.URL.Path, r.RemoteAddr,
		)
		h.writeError(w, r, http.StatusUnauthorized, "unauthorized")
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

// ---------- GET ----------

// handleGet returns either the full snapshot (default + apps) when ?app is
// absent, or a single {"config": ..., "source": "override|default"} when
// ?app is supplied.
func (h *configHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	app, hasApp := readAppParam(r)

	if !hasApp {
		snapshot := h.sampler.GetConfig()
		appsOverride := h.sampler.ListAppConfigs()
		// [promptsampler-test] 便于确认 GET /config 的实际返回。
		log.ErrorfContext(r.Context(),
			"[promptsampler-test] ConfigHandler GET (all): remote=%s default_enabled=%v default_rate=%v overrides=%d",
			r.RemoteAddr, snapshot.Enabled, snapshot.SampleRate, len(appsOverride),
		)
		body := map[string]any{
			"config": snapshot,
			"apps":   appsOverride,
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
	// [promptsampler-test] 便于确认 GET /config?app=... 命中 override 还是 default。
	log.ErrorfContext(r.Context(),
		"[promptsampler-test] ConfigHandler GET (app): remote=%s app=%s source=%s enabled=%v rate=%v token=%s",
		r.RemoteAddr, app, source, cfg.Enabled, cfg.SampleRate, cfg.SamplerToken,
	)
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
		log.ErrorfContext(r.Context(),
			"[promptsampler-test] ConfigHandler PUT decode failed: remote=%s err=%v",
			r.RemoteAddr, err,
		)
		h.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if err := cfg.Validate(); err != nil {
		log.ErrorfContext(r.Context(),
			"[promptsampler-test] ConfigHandler PUT validate failed: remote=%s err=%v",
			r.RemoteAddr, err,
		)
		h.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	app, hasApp := readAppParam(r)
	if !hasApp || app == "" {
		if err := h.sampler.SetConfig(cfg); err != nil {
			log.ErrorfContext(r.Context(),
				"[promptsampler-test] ConfigHandler PUT set default failed: remote=%s err=%v",
				r.RemoteAddr, err,
			)
			h.writeError(w, r, http.StatusBadRequest, err.Error())
			return
		}
		log.ErrorfContext(r.Context(),
			"[promptsampler-test] ConfigHandler PUT default ok: remote=%s enabled=%v rate=%v token=%s",
			r.RemoteAddr, cfg.Enabled, cfg.SampleRate, cfg.SamplerToken,
		)
		// Respond with the latest snapshot so callers can confirm.
		h.writeJSON(w, r, http.StatusOK, map[string]any{
			"config": h.sampler.GetConfig(),
			"apps":   h.sampler.ListAppConfigs(),
		})
		return
	}

	if err := h.sampler.SetAppConfig(app, cfg); err != nil {
		log.ErrorfContext(r.Context(),
			"[promptsampler-test] ConfigHandler PUT set app failed: remote=%s app=%s err=%v",
			r.RemoteAddr, app, err,
		)
		h.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	effective, isOverride := h.sampler.GetAppConfig(app)
	source := "default"
	if isOverride {
		source = "override"
	}
	log.ErrorfContext(r.Context(),
		"[promptsampler-test] ConfigHandler PUT app ok: remote=%s app=%s source=%s enabled=%v rate=%v token=%s",
		r.RemoteAddr, app, source, effective.Enabled, effective.SampleRate, effective.SamplerToken,
	)
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
		log.ErrorfContext(r.Context(),
			"[promptsampler-test] ConfigHandler DELETE miss: remote=%s app=%s",
			r.RemoteAddr, app,
		)
		h.writeError(w, r, http.StatusNotFound, "app override not found")
		return
	}
	log.ErrorfContext(r.Context(),
		"[promptsampler-test] ConfigHandler DELETE ok: remote=%s app=%s",
		r.RemoteAddr, app,
	)
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
