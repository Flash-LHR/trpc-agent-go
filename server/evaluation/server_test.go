//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package evaluation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coreevaluation "trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	agentlog "trpc.group/trpc-go/trpc-agent-go/log"
)

type fakeAgentEvaluator struct {
	evaluate func(ctx context.Context, evalSetID string, opt ...coreevaluation.Option) (*coreevaluation.EvaluationResult, error)
	close    func() error
}

func (f *fakeAgentEvaluator) Evaluate(ctx context.Context, evalSetID string, opt ...coreevaluation.Option) (*coreevaluation.EvaluationResult, error) {
	if f.evaluate != nil {
		return f.evaluate(ctx, evalSetID, opt...)
	}
	return nil, errors.New("evaluate is not configured")
}

func (f *fakeAgentEvaluator) Close() error {
	if f.close != nil {
		return f.close()
	}
	return nil
}

type fakeEvalSetManager struct {
	get        func(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error)
	create     func(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error)
	list       func(ctx context.Context, appName string) ([]string, error)
	delete     func(ctx context.Context, appName, evalSetID string) error
	getCase    func(ctx context.Context, appName, evalSetID, evalCaseID string) (*evalset.EvalCase, error)
	addCase    func(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error
	updateCase func(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error
	deleteCase func(ctx context.Context, appName, evalSetID, evalCaseID string) error
	close      func() error
}

func (f *fakeEvalSetManager) Get(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	if f.get != nil {
		return f.get(ctx, appName, evalSetID)
	}
	return nil, errors.New("get is not configured")
}

func (f *fakeEvalSetManager) Create(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	if f.create != nil {
		return f.create(ctx, appName, evalSetID)
	}
	return nil, errors.New("create is not configured")
}

func (f *fakeEvalSetManager) List(ctx context.Context, appName string) ([]string, error) {
	if f.list != nil {
		return f.list(ctx, appName)
	}
	return nil, errors.New("list is not configured")
}

func (f *fakeEvalSetManager) Delete(ctx context.Context, appName, evalSetID string) error {
	if f.delete != nil {
		return f.delete(ctx, appName, evalSetID)
	}
	return errors.New("delete is not configured")
}

func (f *fakeEvalSetManager) GetCase(ctx context.Context, appName, evalSetID, evalCaseID string) (*evalset.EvalCase, error) {
	if f.getCase != nil {
		return f.getCase(ctx, appName, evalSetID, evalCaseID)
	}
	return nil, errors.New("getCase is not configured")
}

func (f *fakeEvalSetManager) AddCase(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error {
	if f.addCase != nil {
		return f.addCase(ctx, appName, evalSetID, evalCase)
	}
	return errors.New("addCase is not configured")
}

func (f *fakeEvalSetManager) UpdateCase(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error {
	if f.updateCase != nil {
		return f.updateCase(ctx, appName, evalSetID, evalCase)
	}
	return errors.New("updateCase is not configured")
}

func (f *fakeEvalSetManager) DeleteCase(ctx context.Context, appName, evalSetID, evalCaseID string) error {
	if f.deleteCase != nil {
		return f.deleteCase(ctx, appName, evalSetID, evalCaseID)
	}
	return errors.New("deleteCase is not configured")
}

func (f *fakeEvalSetManager) Close() error {
	if f.close != nil {
		return f.close()
	}
	return nil
}

type fakeEvalResultManager struct {
	save  func(ctx context.Context, appName string, evalSetResult *evalresult.EvalSetResult) (string, error)
	get   func(ctx context.Context, appName, evalSetResultID string) (*evalresult.EvalSetResult, error)
	list  func(ctx context.Context, appName string) ([]string, error)
	close func() error
}

func (f *fakeEvalResultManager) Save(ctx context.Context, appName string, evalSetResult *evalresult.EvalSetResult) (string, error) {
	if f.save != nil {
		return f.save(ctx, appName, evalSetResult)
	}
	return "", errors.New("save is not configured")
}

func (f *fakeEvalResultManager) Get(ctx context.Context, appName, evalSetResultID string) (*evalresult.EvalSetResult, error) {
	if f.get != nil {
		return f.get(ctx, appName, evalSetResultID)
	}
	return nil, errors.New("get is not configured")
}

func (f *fakeEvalResultManager) List(ctx context.Context, appName string) ([]string, error) {
	if f.list != nil {
		return f.list(ctx, appName)
	}
	return nil, errors.New("list is not configured")
}

func (f *fakeEvalResultManager) Close() error {
	if f.close != nil {
		return f.close()
	}
	return nil
}

type failingResponseWriter struct {
	header     http.Header
	statusCode int
}

type stubLogger struct {
	errorfCalls []string
}

func (s *stubLogger) Debug(args ...any) {}

func (s *stubLogger) Debugf(format string, args ...any) {}

func (s *stubLogger) Info(args ...any) {}

func (s *stubLogger) Infof(format string, args ...any) {}

func (s *stubLogger) Warn(args ...any) {}

func (s *stubLogger) Warnf(format string, args ...any) {}

func (s *stubLogger) Error(args ...any) {}

func (s *stubLogger) Errorf(format string, args ...any) {
	s.errorfCalls = append(s.errorfCalls, fmt.Sprintf(format, args...))
}

func (s *stubLogger) Fatal(args ...any) {}

func (s *stubLogger) Fatalf(format string, args ...any) {}

func (w *failingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failingResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

func (w *failingResponseWriter) Write(p []byte) (int, error) {
	return 0, errors.New("write failed")
}

func newTestEvalSet(evalSetID string, evalCaseIDs ...string) *evalset.EvalSet {
	evalCases := make([]*evalset.EvalCase, 0, len(evalCaseIDs))
	for _, evalCaseID := range evalCaseIDs {
		evalCases = append(evalCases, &evalset.EvalCase{EvalID: evalCaseID})
	}
	return &evalset.EvalSet{
		EvalSetID:   evalSetID,
		Name:        evalSetID + "-name",
		Description: evalSetID + "-description",
		EvalCases:   evalCases,
	}
}

func newTestEvalResult(evalSetResultID, evalSetID string, numRuns int) *evalresult.EvalSetResult {
	return &evalresult.EvalSetResult{
		EvalSetResultID:   evalSetResultID,
		EvalSetResultName: evalSetResultID + "-name",
		EvalSetID:         evalSetID,
		Summary: &evalresult.EvalSetResultSummary{
			OverallStatus: status.EvalStatusPassed,
			NumRuns:       numRuns,
		},
	}
}

func intPtr(v int) *int {
	return &v
}

func newTestEvaluationResult(evalSetID, evalSetResultID string, numRuns int, executionTime time.Duration) *coreevaluation.EvaluationResult {
	return &coreevaluation.EvaluationResult{
		EvalSetID:     evalSetID,
		OverallStatus: status.EvalStatusPassed,
		ExecutionTime: executionTime,
		EvalResult:    newTestEvalResult(evalSetResultID, evalSetID, numRuns),
	}
}

func newTestServer(t *testing.T, opts ...Option) *Server {
	t.Helper()
	baseOpts := []Option{
		WithAppName("demo-app"),
		WithAgentEvaluator(&fakeAgentEvaluator{
			evaluate: func(ctx context.Context, evalSetID string, opt ...coreevaluation.Option) (*coreevaluation.EvaluationResult, error) {
				return newTestEvaluationResult(evalSetID, "result-default", 1, time.Second), nil
			},
		}),
		WithEvalSetManager(&fakeEvalSetManager{
			list: func(ctx context.Context, appName string) ([]string, error) {
				return []string{}, nil
			},
			get: func(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
				return newTestEvalSet(evalSetID), nil
			},
		}),
		WithEvalResultManager(&fakeEvalResultManager{
			list: func(ctx context.Context, appName string) ([]string, error) {
				return []string{}, nil
			},
			get: func(ctx context.Context, appName, evalSetResultID string) (*evalresult.EvalSetResult, error) {
				return newTestEvalResult(evalSetResultID, "math-basic", 1), nil
			},
		}),
	}
	baseOpts = append(baseOpts, opts...)
	srv, err := New(baseOpts...)
	require.NoError(t, err)
	return srv
}

func TestNewValidation(t *testing.T) {
	_, err := New(
		WithAppName("demo-app"),
		WithEvalSetManager(&fakeEvalSetManager{}),
		WithEvalResultManager(&fakeEvalResultManager{}),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent evaluator")
}

func TestNewCustomPaths(t *testing.T) {
	srv := newTestServer(t,
		WithBasePath("/api/evaluation"),
		WithRunsPath("/executions"),
		WithSetsPath("/datasets"),
		WithResultsPath("/outputs"),
	)
	assert.Equal(t, "/api/evaluation", srv.BasePath())
	assert.Equal(t, "/api/evaluation/executions", srv.RunsPath())
	assert.Equal(t, "/api/evaluation/datasets", srv.SetsPath())
	assert.Equal(t, "/api/evaluation/outputs", srv.ResultsPath())
}

func TestDefaultPathsAreRESTful(t *testing.T) {
	srv := newTestServer(t)
	assert.Equal(t, "/evaluation", srv.BasePath())
	assert.Equal(t, "/evaluation/sets", srv.SetsPath())
	assert.Equal(t, "/evaluation/runs", srv.RunsPath())
	assert.Equal(t, "/evaluation/results", srv.ResultsPath())
}

func TestHandleCreateRun(t *testing.T) {
	srv := newTestServer(t, WithAgentEvaluator(&fakeAgentEvaluator{
		evaluate: func(ctx context.Context, evalSetID string, opt ...coreevaluation.Option) (*coreevaluation.EvaluationResult, error) {
			return newTestEvaluationResult(evalSetID, "result-1", 2, 2*time.Second), nil
		},
	}))
	body, err := json.Marshal(&RunEvaluationRequest{SetID: "math-basic", NumRuns: intPtr(2)})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, srv.RunsPath(), bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusCreated, recorder.Code)
	assert.Empty(t, recorder.Header().Get("Location"))
	var resp CreateRunResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.NotNil(t, resp.EvaluationResult)
	assert.Equal(t, "math-basic", resp.EvaluationResult.EvalSetID)
	assert.Equal(t, "passed", string(resp.EvaluationResult.OverallStatus))
	assert.Equal(t, 2*time.Second, resp.EvaluationResult.ExecutionTime)
	require.NotNil(t, resp.EvaluationResult.EvalResult)
	assert.Equal(t, "result-1", resp.EvaluationResult.EvalResult.EvalSetResultID)
	require.NotNil(t, resp.EvaluationResult.EvalResult.Summary)
	assert.Equal(t, 2, resp.EvaluationResult.EvalResult.Summary.NumRuns)
}

func TestRespondJSONEncodingError(t *testing.T) {
	srv := newTestServer(t)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, srv.SetsPath(), nil)
	srv.respondJSON(recorder, req, http.StatusOK, map[string]any{"bad": make(chan int)})
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "encode response")
}

func TestRespondJSONWriteError(t *testing.T) {
	srv := newTestServer(t)
	writer := &failingResponseWriter{}
	req := httptest.NewRequest(http.MethodGet, srv.SetsPath(), nil)
	logger := &stubLogger{}
	originalLogger := agentlog.Default
	agentlog.Default = logger
	defer func() {
		agentlog.Default = originalLogger
	}()
	srv.respondJSON(writer, req, http.StatusOK, map[string]string{"status": "ok"})
	assert.Equal(t, http.StatusOK, writer.statusCode)
	require.Len(t, logger.errorfCalls, 1)
	assert.Contains(t, logger.errorfCalls[0], "write response body")
}

func TestHandleCreateRunNotFound(t *testing.T) {
	srv := newTestServer(t, WithAgentEvaluator(&fakeAgentEvaluator{
		evaluate: func(ctx context.Context, evalSetID string, opt ...coreevaluation.Option) (*coreevaluation.EvaluationResult, error) {
			return nil, os.ErrNotExist
		},
	}))
	req := httptest.NewRequest(http.MethodPost, srv.RunsPath(), bytes.NewBufferString(`{"setId":"missing-set"}`))
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusNotFound, recorder.Code)
	assert.JSONEq(t, `{"error":"not found"}`, recorder.Body.String())
}

func TestHandleCreateRunTimeoutReturnsGatewayTimeout(t *testing.T) {
	srv := newTestServer(t,
		WithTimeout(10*time.Millisecond),
		WithAgentEvaluator(&fakeAgentEvaluator{
			evaluate: func(ctx context.Context, evalSetID string, opt ...coreevaluation.Option) (*coreevaluation.EvaluationResult, error) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(200 * time.Millisecond):
					return newTestEvaluationResult(evalSetID, "result-late", 1, time.Second), nil
				}
			},
		}),
	)
	req := httptest.NewRequest(http.MethodPost, srv.RunsPath(), bytes.NewBufferString(`{"setId":"math-basic"}`))
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusGatewayTimeout, recorder.Code)
	assert.JSONEq(t, `{"error":"evaluation timed out"}`, recorder.Body.String())
}

func TestHandleRunsRejectsReadRequests(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, srv.RunsPath(), nil)
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusMethodNotAllowed, recorder.Code)
	assert.Equal(t, http.MethodPost, recorder.Header().Get("Allow"))
	req = httptest.NewRequest(http.MethodGet, srv.RunsPath()+"/run-1", nil)
	recorder = httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusNotFound, recorder.Code)
}

func TestHandleSetQueries(t *testing.T) {
	srv := newTestServer(t, WithEvalSetManager(&fakeEvalSetManager{
		list: func(ctx context.Context, appName string) ([]string, error) {
			return []string{"math-basic"}, nil
		},
		get: func(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
			return newTestEvalSet(evalSetID, "case-1", "case-2"), nil
		},
	}))
	t.Run("list", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, srv.SetsPath(), nil)
		recorder := httptest.NewRecorder()
		srv.Handler().ServeHTTP(recorder, req)
		require.Equal(t, http.StatusOK, recorder.Code)
		var resp ListSetsResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
		require.Len(t, resp.Sets, 1)
		assert.Equal(t, "math-basic", resp.Sets[0].EvalSetID)
		assert.Len(t, resp.Sets[0].EvalCases, 2)
	})
	t.Run("list redirects trailing slash", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, srv.SetsPath()+"/", nil)
		recorder := httptest.NewRecorder()
		srv.Handler().ServeHTTP(recorder, req)
		require.Equal(t, http.StatusPermanentRedirect, recorder.Code)
		assert.Equal(t, srv.SetsPath(), recorder.Header().Get("Location"))
	})
	t.Run("detail", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, srv.SetsPath()+"/math-basic", nil)
		recorder := httptest.NewRecorder()
		srv.Handler().ServeHTTP(recorder, req)
		require.Equal(t, http.StatusOK, recorder.Code)
		var resp GetSetResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
		require.NotNil(t, resp.Set)
		assert.Equal(t, "math-basic", resp.Set.EvalSetID)
		assert.Len(t, resp.Set.EvalCases, 2)
	})
	t.Run("detail redirects trailing slash", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, srv.SetsPath()+"/math-basic/", nil)
		recorder := httptest.NewRecorder()
		srv.Handler().ServeHTTP(recorder, req)
		require.Equal(t, http.StatusPermanentRedirect, recorder.Code)
		assert.Equal(t, srv.SetsPath()+"/math-basic", recorder.Header().Get("Location"))
	})
	t.Run("detail with escaped slash", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, srv.SetsPath()+"/a%2Fb", nil)
		recorder := httptest.NewRecorder()
		srv.Handler().ServeHTTP(recorder, req)
		require.Equal(t, http.StatusOK, recorder.Code)
		var resp GetSetResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
		require.NotNil(t, resp.Set)
		assert.Equal(t, "a/b", resp.Set.EvalSetID)
	})
	t.Run("detail rejects additional segments", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, srv.SetsPath()+"/a/b", nil)
		recorder := httptest.NewRecorder()
		srv.Handler().ServeHTTP(recorder, req)
		require.Equal(t, http.StatusNotFound, recorder.Code)
	})
}

func TestHandleRunCollectionRedirectsTrailingSlash(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, srv.RunsPath()+"/", bytes.NewBufferString(`{"setId":"math-basic"}`))
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusPermanentRedirect, recorder.Code)
	assert.Equal(t, srv.RunsPath(), recorder.Header().Get("Location"))
}

func TestHandleCreateRunRejectsUnknownFields(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, srv.RunsPath(), bytes.NewBufferString(`{"setId":"math-basic","unexpected":true}`))
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "unknown field")
}

func TestHandleCreateRunRejectsZeroNumRuns(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, srv.RunsPath(), bytes.NewBufferString(`{"setId":"math-basic","numRuns":0}`))
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.JSONEq(t, `{"error":"numRuns must be greater than 0 when provided"}`, recorder.Body.String())
}

func TestHandleResultQueries(t *testing.T) {
	srv := newTestServer(t, WithEvalResultManager(&fakeEvalResultManager{
		list: func(ctx context.Context, appName string) ([]string, error) {
			return []string{"result-1", "result-2"}, nil
		},
		get: func(ctx context.Context, appName, evalSetResultID string) (*evalresult.EvalSetResult, error) {
			if evalSetResultID == "result-2" {
				return newTestEvalResult("result-2", "trace-basic", 1), nil
			}
			return newTestEvalResult(evalSetResultID, "math-basic", 1), nil
		},
	}))
	t.Run("list", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, srv.ResultsPath(), nil)
		recorder := httptest.NewRecorder()
		srv.Handler().ServeHTTP(recorder, req)
		require.Equal(t, http.StatusOK, recorder.Code)
		var resp ListResultsResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
		assert.Len(t, resp.Results, 2)
	})
	t.Run("list redirects trailing slash", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, srv.ResultsPath()+"/", nil)
		recorder := httptest.NewRecorder()
		srv.Handler().ServeHTTP(recorder, req)
		require.Equal(t, http.StatusPermanentRedirect, recorder.Code)
		assert.Equal(t, srv.ResultsPath(), recorder.Header().Get("Location"))
	})
	t.Run("filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, srv.ResultsPath()+"?setId=math-basic", nil)
		recorder := httptest.NewRecorder()
		srv.Handler().ServeHTTP(recorder, req)
		require.Equal(t, http.StatusOK, recorder.Code)
		var resp ListResultsResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
		require.Len(t, resp.Results, 1)
		assert.Equal(t, "math-basic", resp.Results[0].EvalSetID)
	})
	t.Run("detail", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, srv.ResultsPath()+"/result-1", nil)
		recorder := httptest.NewRecorder()
		srv.Handler().ServeHTTP(recorder, req)
		require.Equal(t, http.StatusOK, recorder.Code)
		var resp GetResultResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
		require.NotNil(t, resp.Result)
		assert.Equal(t, "result-1", resp.Result.EvalSetResultID)
		assert.Equal(t, "math-basic", resp.Result.EvalSetID)
	})
	t.Run("detail redirects trailing slash", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, srv.ResultsPath()+"/result-1/", nil)
		recorder := httptest.NewRecorder()
		srv.Handler().ServeHTTP(recorder, req)
		require.Equal(t, http.StatusPermanentRedirect, recorder.Code)
		assert.Equal(t, srv.ResultsPath()+"/result-1", recorder.Header().Get("Location"))
	})
}

func TestRespondStatusErrorUsesSafeMessageAndLogs(t *testing.T) {
	srv := newTestServer(t)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, srv.ResultsPath(), nil)
	logger := &stubLogger{}
	originalLogger := agentlog.Default
	agentlog.Default = logger
	defer func() {
		agentlog.Default = originalLogger
	}()
	srv.respondStatusError(recorder, req, errors.New("sensitive backend detail"))
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.JSONEq(t, `{"error":"internal server error"}`, recorder.Body.String())
	require.Len(t, logger.errorfCalls, 1)
	assert.Contains(t, logger.errorfCalls[0], "sensitive backend detail")
}
