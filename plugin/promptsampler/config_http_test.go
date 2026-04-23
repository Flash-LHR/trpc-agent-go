//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptsampler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// configEnvelopeResp mirrors the on-the-wire shape returned by GET/PUT for a
// single-config response (with "source") or the full snapshot (with "apps").
// Using a single struct with optional fields keeps the tests compact.
type configEnvelopeResp struct {
	Config *RuntimeConfig            `json:"config"`
	Apps   map[string]*RuntimeConfig `json:"apps,omitempty"`
	Source string                    `json:"source,omitempty"`
	Error  string                    `json:"error,omitempty"`
}

// newTestSampler constructs a sampler suitable for ConfigHandler tests.
// Sampling itself is disabled so tests don't need to wire a writer.
func newTestSampler(t *testing.T) *PromptSampler {
	t.Helper()
	return New()
}

// do builds a request, executes it against the handler and returns the
// recorder plus the decoded response body. decoding is tolerant; on empty
// bodies it returns a zero envelope.
func do(
	t *testing.T,
	h http.Handler,
	method, target string,
	bodyJSON string,
	headers map[string]string,
) (*httptest.ResponseRecorder, configEnvelopeResp) {
	t.Helper()
	var body *bytes.Buffer
	if bodyJSON != "" {
		body = bytes.NewBufferString(bodyJSON)
	} else {
		body = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, target, body)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp configEnvelopeResp
	raw := rec.Body.Bytes()
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = json.Unmarshal(raw, &resp)
	}
	return rec, resp
}

// ---------- auth ----------

// TestConfigHandler_NoOptions_Serves verifies the permissive-by-default
// contract: ConfigHandler() without any option must serve every request
// rather than returning 401.
func TestConfigHandler_NoOptions_Serves(t *testing.T) {
	s := newTestSampler(t)
	h := s.ConfigHandler()

	rec, _ := do(t, h, http.MethodGet, "/config", "", nil)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

// TestConfigHandler_AuthFunc_True_Accepts verifies that a predicate
// returning true lets the request through to the normal route handlers.
func TestConfigHandler_AuthFunc_True_Accepts(t *testing.T) {
	s := newTestSampler(t)
	h := s.ConfigHandler(WithAuthFunc(func(*http.Request) bool { return true }))

	rec, _ := do(t, h, http.MethodGet, "/config", "", nil)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestConfigHandler_AuthFunc_False_Rejects verifies that a predicate
// returning false produces a 401 with the canonical "unauthorized" body
// and leaves the sampler's configuration untouched.
func TestConfigHandler_AuthFunc_False_Rejects(t *testing.T) {
	s := newTestSampler(t)
	before := s.GetConfig()

	h := s.ConfigHandler(WithAuthFunc(func(*http.Request) bool { return false }))

	rec, resp := do(t, h, http.MethodPut, "/config",
		`{"config":{"enabled":true,"sample_rate":1.0,"sampler_token":"x"}}`, nil)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "unauthorized", resp.Error)

	// Config must not have changed.
	after := s.GetConfig()
	assert.Equal(t, before.Enabled, after.Enabled)
	assert.InDelta(t, before.SampleRate, after.SampleRate, 0)
	assert.Equal(t, before.SamplerToken, after.SamplerToken)
}

// TestConfigHandler_AuthFunc_ReceivesRequest verifies the predicate gets
// the full *http.Request so callers can read headers, path, or anything
// else to make the allow/deny decision.
func TestConfigHandler_AuthFunc_ReceivesRequest(t *testing.T) {
	s := newTestSampler(t)
	h := s.ConfigHandler(WithAuthFunc(func(r *http.Request) bool {
		return r.Header.Get("X-Allow") == "1"
	}))

	recPass, _ := do(t, h, http.MethodGet, "/config", "",
		map[string]string{"X-Allow": "1"})
	assert.Equal(t, http.StatusOK, recPass.Code)

	recDeny, _ := do(t, h, http.MethodGet, "/config", "",
		map[string]string{"X-Allow": "0"})
	assert.Equal(t, http.StatusUnauthorized, recDeny.Code)
}

// ---------- method dispatch ----------

func TestConfigHandler_UnsupportedMethod_Returns405(t *testing.T) {
	s := newTestSampler(t)
	h := s.ConfigHandler()

	rec, resp := do(t, h, http.MethodPost, "/config", "", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("Allow"))
	assert.Equal(t, "method not allowed", resp.Error)
}

// ---------- GET ----------

func TestConfigHandler_GET_DefaultReturnsDefaultAndApps(t *testing.T) {
	s := newTestSampler(t)
	require.NoError(t, s.SetConfig(&RuntimeConfig{
		Enabled: true, SampleRate: 0.1, SamplerToken: "d-tok",
	}))
	require.NoError(t, s.SetAppConfig("A", &RuntimeConfig{
		Enabled: true, SampleRate: 1.0, SamplerToken: "a-tok",
	}))

	h := s.ConfigHandler()
	rec, resp := do(t, h, http.MethodGet, "/config", "", nil)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, resp.Config, "default config must be present")
	assert.InDelta(t, 0.1, resp.Config.SampleRate, 0)
	assert.Equal(t, "d-tok", resp.Config.SamplerToken)
	require.NotNil(t, resp.Apps, "apps map must be present")
	require.Contains(t, resp.Apps, "A")
	assert.InDelta(t, 1.0, resp.Apps["A"].SampleRate, 0)
}

func TestConfigHandler_GET_AppHitReturnsOverride(t *testing.T) {
	s := newTestSampler(t)
	require.NoError(t, s.SetAppConfig("A", &RuntimeConfig{
		Enabled: true, SampleRate: 0.42, SamplerToken: "a",
	}))
	h := s.ConfigHandler()

	rec, resp := do(t, h, http.MethodGet, "/config?app=A", "", nil)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "override", resp.Source)
	assert.InDelta(t, 0.42, resp.Config.SampleRate, 0)
}

func TestConfigHandler_GET_AppMissFallsBackToDefault(t *testing.T) {
	s := newTestSampler(t)
	require.NoError(t, s.SetConfig(&RuntimeConfig{SampleRate: 0.1}))
	h := s.ConfigHandler()

	rec, resp := do(t, h, http.MethodGet, "/config?app=unknown", "", nil)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "default", resp.Source)
	assert.InDelta(t, 0.1, resp.Config.SampleRate, 0)
}

// ---------- PUT ----------

func TestConfigHandler_PUT_Default_ReplacesConfig(t *testing.T) {
	s := newTestSampler(t)
	h := s.ConfigHandler()

	body := `{"config":{"enabled":true,"sample_rate":0.7,"sampler_token":"new"}}`
	rec, resp := do(t, h, http.MethodPut, "/config", body, nil)

	assert.Equal(t, http.StatusOK, rec.Code)
	got := s.GetConfig()
	assert.True(t, got.Enabled)
	assert.InDelta(t, 0.7, got.SampleRate, 0)
	assert.Equal(t, "new", got.SamplerToken)
	assert.InDelta(t, 0.7, resp.Config.SampleRate, 0)
}

func TestConfigHandler_PUT_App_WritesOverride(t *testing.T) {
	s := newTestSampler(t)
	h := s.ConfigHandler()

	body := `{"config":{"enabled":true,"sample_rate":1.0,"sampler_token":"A"}}`
	rec, resp := do(t, h, http.MethodPut, "/config?app=A", body, nil)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "override", resp.Source)

	got, isOverride := s.GetAppConfig("A")
	assert.True(t, isOverride)
	assert.InDelta(t, 1.0, got.SampleRate, 0)
}

func TestConfigHandler_PUT_Invalid_RateOutOfRange(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"negative", `{"config":{"enabled":true,"sample_rate":-0.1}}`},
		{"greaterThanOne", `{"config":{"enabled":true,"sample_rate":1.1}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTestSampler(t)
			h := s.ConfigHandler()

			rec, resp := do(t, h, http.MethodPut, "/config", c.body, nil)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, resp.Error, "sample_rate must be between 0 and 1")
		})
	}
}

func TestConfigHandler_PUT_Invalid_MissingConfigWrapper(t *testing.T) {
	s := newTestSampler(t)
	h := s.ConfigHandler()

	// A flat body without the "config" outer wrapper must be rejected.
	// The exact message depends on whether the unknown-field check or the
	// nil-config check fires first; both paths are acceptable 400s per
	// the spec, so we assert only the status code and non-empty error.
	body := `{"enabled":true,"sample_rate":0.5}`
	rec, resp := do(t, h, http.MethodPut, "/config", body, nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.NotEmpty(t, resp.Error)
}

func TestConfigHandler_PUT_Invalid_EmptyConfigField(t *testing.T) {
	s := newTestSampler(t)
	h := s.ConfigHandler()

	// Envelope with an explicit null config must surface the
	// "must contain a 'config' field" message so operators understand the
	// contract.
	body := `{"config":null}`
	rec, resp := do(t, h, http.MethodPut, "/config", body, nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, resp.Error, "config")
}

func TestConfigHandler_PUT_Invalid_NotJSON(t *testing.T) {
	s := newTestSampler(t)
	h := s.ConfigHandler()

	rec, resp := do(t, h, http.MethodPut, "/config", `{not json}`, nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, resp.Error, "invalid request body")
}

// ---------- DELETE ----------

func TestConfigHandler_DELETE_DefaultIsMethodNotAllowed(t *testing.T) {
	s := newTestSampler(t)
	h := s.ConfigHandler()

	rec, resp := do(t, h, http.MethodDelete, "/config", "", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Contains(t, resp.Error, "default config cannot be deleted")
}

func TestConfigHandler_DELETE_AppRemovesOverride(t *testing.T) {
	s := newTestSampler(t)
	require.NoError(t, s.SetAppConfig("A", &RuntimeConfig{SampleRate: 1.0}))
	h := s.ConfigHandler()

	rec, _ := do(t, h, http.MethodDelete, "/config?app=A", "", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Post-delete: override must be gone; GET returns default source.
	_, isOverride := s.GetAppConfig("A")
	assert.False(t, isOverride)
}

func TestConfigHandler_DELETE_UnknownAppIs404(t *testing.T) {
	s := newTestSampler(t)
	h := s.ConfigHandler()

	rec, resp := do(t, h, http.MethodDelete, "/config?app=never", "", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "app override not found", resp.Error)
}

// ---------- end-to-end: PUT then sampling decision ----------

func TestConfigHandler_PUT_TakesEffectOnNextSampling(t *testing.T) {
	s := newTestSampler(t)
	h := s.ConfigHandler()

	// Initially disabled -> no sampling.
	require.NoError(t, s.SetConfig(&RuntimeConfig{Enabled: false, SampleRate: 1.0}))
	assert.False(t, s.shouldSample(nil))

	// PUT via HTTP to enable sampling at rate=1.
	body := `{"config":{"enabled":true,"sample_rate":1.0,"sampler_token":"x"}}`
	rec, _ := do(t, h, http.MethodPut, "/config", body, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	assert.True(t, s.shouldSample(nil))
}

// ---------- concurrency ----------

func TestConfigHandler_Concurrent_NoRaces(t *testing.T) {
	s := newTestSampler(t)
	h := s.ConfigHandler()

	var wg sync.WaitGroup
	const workers = 16
	const iters = 50

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			app := fmt.Sprintf("app-%d", w)
			for i := 0; i < iters; i++ {
				// PUT overrides for different apps.
				body := fmt.Sprintf(
					`{"config":{"enabled":true,"sample_rate":%g,"sampler_token":"t"}}`,
					float64(i%100)/100.0,
				)
				req := httptest.NewRequest(http.MethodPut,
					"/config?app="+app,
					strings.NewReader(body),
				)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
			}
		}(w)
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				req := httptest.NewRequest(http.MethodGet, "/config", nil)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
			}
		}()
	}
	wg.Wait()

	// After the storm, all workers' overrides must be present.
	apps := s.ListAppConfigs()
	assert.Equal(t, workers, len(apps))
}

// ---------- integration smoke ----------

func TestConfigHandler_EndToEnd_ViaHTTPServer(t *testing.T) {
	s := newTestSampler(t)
	srv := httptest.NewServer(s.ConfigHandler())
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// PUT default config.
	body := `{"config":{"enabled":true,"sample_rate":0.25,"sampler_token":"d"}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		srv.URL+"/config", strings.NewReader(body))
	require.NoError(t, err)
	rsp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)

	// GET default config and assert round-trip.
	req, err = http.NewRequestWithContext(ctx, http.MethodGet,
		srv.URL+"/config", nil)
	require.NoError(t, err)
	rsp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer rsp.Body.Close()

	var got configEnvelopeResp
	require.NoError(t, json.NewDecoder(rsp.Body).Decode(&got))
	require.NotNil(t, got.Config)
	assert.InDelta(t, 0.25, got.Config.SampleRate, 0)
	assert.Equal(t, "d", got.Config.SamplerToken)
}
