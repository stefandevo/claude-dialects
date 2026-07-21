package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testDashboardAuthority = "127.0.0.1:43123"
	testDashboardURL       = "http://127.0.0.1:43123/"
	testDashboardCSRF      = "test-dashboard-csrf-token"
)

type fakeDashboardService struct {
	listDialectsFn          func(bool) (DialectListResult, error)
	dialectFn               func(string) (DialectView, string, error)
	createDialectFn         func(DialectInput, string) (DialectMutationResult, error)
	updateDialectFn         func(DialectInput, string) (DialectMutationResult, error)
	removeDialectFn         func(string, string) error
	startDialectFn          func(string) (RuntimeStatus, error)
	stopDialectFn           func(string) (RuntimeStatus, error)
	restartDialectFn        func(string) (RuntimeStatus, error)
	dialectStatusFn         func(string) (RuntimeStatus, error)
	cursorStatusFn          func() CursorRuntimeStatus
	installCursorRuntimeFn  func() (CursorInstallResult, error)
	listNativeLaunchersFn   func() ([]NativeLauncherView, string, error)
	installNativeLauncherFn func(NativeLauncherInput, string) (NativeLauncherResult, error)
	removeNativeLauncherFn  func(string, string) error
}

func (service *fakeDashboardService) ListDialects(includeStatus bool) (DialectListResult, error) {
	if service.listDialectsFn != nil {
		return service.listDialectsFn(includeStatus)
	}
	return DialectListResult{Dialects: []DialectView{}, Revision: "revision-1"}, nil
}

func (service *fakeDashboardService) Dialect(name string) (DialectView, string, error) {
	if service.dialectFn != nil {
		return service.dialectFn(name)
	}
	return DialectView{Name: name, Model: "model"}, "revision-1", nil
}

func (service *fakeDashboardService) CreateDialect(input DialectInput, revision string) (DialectMutationResult, error) {
	if service.createDialectFn != nil {
		return service.createDialectFn(input, revision)
	}
	return DialectMutationResult{Dialect: safeDialectView(input.Name, Dialect{Model: input.Model}), Created: true, Revision: "revision-2"}, nil
}

func (service *fakeDashboardService) UpdateDialect(input DialectInput, revision string) (DialectMutationResult, error) {
	if service.updateDialectFn != nil {
		return service.updateDialectFn(input, revision)
	}
	return DialectMutationResult{Dialect: safeDialectView(input.Name, Dialect{Model: input.Model}), Revision: "revision-2"}, nil
}

func (service *fakeDashboardService) RemoveDialect(name, revision string) error {
	if service.removeDialectFn != nil {
		return service.removeDialectFn(name, revision)
	}
	return nil
}

func (service *fakeDashboardService) StartDialect(name string) (RuntimeStatus, error) {
	if service.startDialectFn != nil {
		return service.startDialectFn(name)
	}
	return RuntimeStatus{State: RuntimeRunning}, nil
}

func (service *fakeDashboardService) StopDialect(name string) (RuntimeStatus, error) {
	if service.stopDialectFn != nil {
		return service.stopDialectFn(name)
	}
	return RuntimeStatus{State: RuntimeStopped}, nil
}

func (service *fakeDashboardService) RestartDialect(name string) (RuntimeStatus, error) {
	if service.restartDialectFn != nil {
		return service.restartDialectFn(name)
	}
	return RuntimeStatus{State: RuntimeRunning}, nil
}

func (service *fakeDashboardService) DialectStatus(name string) (RuntimeStatus, error) {
	if service.dialectStatusFn != nil {
		return service.dialectStatusFn(name)
	}
	return RuntimeStatus{State: RuntimeStopped}, nil
}

func (service *fakeDashboardService) CursorStatus() CursorRuntimeStatus {
	if service.cursorStatusFn != nil {
		return service.cursorStatusFn()
	}
	return CursorRuntimeStatus{RequiredVersion: cursorSDKVersion}
}

func (service *fakeDashboardService) InstallCursorRuntime() (CursorInstallResult, error) {
	if service.installCursorRuntimeFn != nil {
		return service.installCursorRuntimeFn()
	}
	return CursorInstallResult{InstalledVersion: cursorSDKVersion}, nil
}

func (service *fakeDashboardService) ListNativeLaunchers() ([]NativeLauncherView, string, error) {
	if service.listNativeLaunchersFn != nil {
		return service.listNativeLaunchersFn()
	}
	return []NativeLauncherView{}, "revision-1", nil
}

func (service *fakeDashboardService) InstallNativeLauncher(input NativeLauncherInput, revision string) (NativeLauncherResult, error) {
	if service.installNativeLauncherFn != nil {
		return service.installNativeLauncherFn(input, revision)
	}
	return NativeLauncherResult{Launcher: NativeLauncherView{Name: input.Name, Dangerous: input.Dangerous}, Revision: "revision-2"}, nil
}

func (service *fakeDashboardService) RemoveNativeLauncher(name, revision string) error {
	if service.removeNativeLauncherFn != nil {
		return service.removeNativeLauncherFn(name, revision)
	}
	return nil
}

func testDashboardHandler(t *testing.T, service dashboardService) http.Handler {
	t.Helper()
	handler, err := newDashboardHandler(service, "1.2.3", testDashboardAuthority, testDashboardURL, testDashboardCSRF)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func testDashboardRequest(method, path string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, testDashboardURL+strings.TrimPrefix(path, "/"), body)
	request.Host = testDashboardAuthority
	return request
}

func testDashboardMutation(method, path, body string) *http.Request {
	request := testDashboardRequest(method, path, strings.NewReader(body))
	request.Header.Set("Origin", strings.TrimSuffix(testDashboardURL, "/"))
	request.Header.Set("X-CC-Dialect-CSRF", testDashboardCSRF)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func serveDashboardRequest(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func dashboardErrorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope dashboardErrorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error response %q: %v", response.Body.String(), err)
	}
	return envelope.Error.Code
}

func TestDashboardCSRFTokenUsesAtLeast32RandomBytes(t *testing.T) {
	token, err := newDashboardCSRFToken()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("decode CSRF token: %v", err)
	}
	if len(decoded) < 32 {
		t.Fatalf("CSRF token contains %d bytes", len(decoded))
	}
}

func TestDashboardEmbeddedSPAAndFallback(t *testing.T) {
	handler := testDashboardHandler(t, &fakeDashboardService{})
	for _, path := range []string{"/", "/index.html", "/dialects/example"} {
		response := serveDashboardRequest(handler, testDashboardRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s returned %d: %s", path, response.Code, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), `data-dashboard-shell="cc-dialect"`) {
			t.Fatalf("GET %s did not serve embedded dashboard shell: %s", path, response.Body.String())
		}
		if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
			t.Fatalf("GET %s content type = %q", path, contentType)
		}
	}
}

func TestDashboardUnknownAPIRoutesReturnJSON404(t *testing.T) {
	handler := testDashboardHandler(t, &fakeDashboardService{})
	for _, path := range []string{"/api", "/api/v1", "/api/v1/missing", "/api/v1/dialects/name/missing"} {
		response := serveDashboardRequest(handler, testDashboardRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("GET %s returned %d: %s", path, response.Code, response.Body.String())
		}
		if code := dashboardErrorCode(t, response); code != "not_found" {
			t.Fatalf("GET %s error code = %q", path, code)
		}
		if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
			t.Fatalf("GET %s content type = %q", path, contentType)
		}
	}
}

func TestDashboardHostOriginAndCSRFProtection(t *testing.T) {
	createCalls := 0
	service := &fakeDashboardService{createDialectFn: func(input DialectInput, revision string) (DialectMutationResult, error) {
		createCalls++
		return DialectMutationResult{Dialect: DialectView{Name: input.Name}, Revision: "revision-2"}, nil
	}}
	handler := testDashboardHandler(t, service)

	wrongHost := testDashboardRequest(http.MethodGet, "/api/v1/bootstrap", nil)
	wrongHost.Host = "127.0.0.1"
	response := serveDashboardRequest(handler, wrongHost)
	if response.Code != http.StatusForbidden || dashboardErrorCode(t, response) != "invalid_host" {
		t.Fatalf("wrong host response = %d %s", response.Code, response.Body.String())
	}

	wrongOrigin := testDashboardRequest(http.MethodGet, "/api/v1/bootstrap", nil)
	wrongOrigin.Header.Set("Origin", "http://127.0.0.1:9999")
	response = serveDashboardRequest(handler, wrongOrigin)
	if response.Code != http.StatusForbidden || dashboardErrorCode(t, response) != "invalid_origin" {
		t.Fatalf("wrong origin response = %d %s", response.Code, response.Body.String())
	}

	missingOrigin := testDashboardRequest(http.MethodPost, "/api/v1/dialects", strings.NewReader(`{"name":"safe","model":"m"}`))
	missingOrigin.Header.Set("X-CC-Dialect-CSRF", testDashboardCSRF)
	missingOrigin.Header.Set("Content-Type", "application/json")
	response = serveDashboardRequest(handler, missingOrigin)
	if response.Code != http.StatusForbidden || dashboardErrorCode(t, response) != "invalid_origin" {
		t.Fatalf("missing origin response = %d %s", response.Code, response.Body.String())
	}

	missingCSRF := testDashboardRequest(http.MethodPost, "/api/v1/dialects", strings.NewReader(`{"name":"safe","model":"m"}`))
	missingCSRF.Header.Set("Origin", strings.TrimSuffix(testDashboardURL, "/"))
	missingCSRF.Header.Set("Content-Type", "application/json")
	response = serveDashboardRequest(handler, missingCSRF)
	if response.Code != http.StatusForbidden || dashboardErrorCode(t, response) != "invalid_csrf" {
		t.Fatalf("missing CSRF response = %d %s", response.Code, response.Body.String())
	}
	if createCalls != 0 {
		t.Fatalf("create called %d times despite security rejection", createCalls)
	}

	valid := testDashboardMutation(http.MethodPost, "/api/v1/dialects", `{"name":"safe","model":"m"}`)
	response = serveDashboardRequest(handler, valid)
	if response.Code != http.StatusCreated {
		t.Fatalf("valid mutation returned %d: %s", response.Code, response.Body.String())
	}
	if createCalls != 1 {
		t.Fatalf("create calls = %d", createCalls)
	}
}

func TestDashboardStrictJSONAndBodyLimit(t *testing.T) {
	handler := testDashboardHandler(t, &fakeDashboardService{})
	tests := []struct {
		name        string
		body        string
		contentType string
		status      int
		code        string
	}{
		{name: "content type", body: `{"name":"x","model":"m"}`, status: http.StatusUnsupportedMediaType, code: "unsupported_media_type"},
		{name: "unknown field", body: `{"name":"x","model":"m","secret":"value"}`, contentType: "application/json", status: http.StatusBadRequest, code: "invalid_json"},
		{name: "trailing value", body: `{"name":"x","model":"m"} {}`, contentType: "application/json", status: http.StatusBadRequest, code: "invalid_json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := testDashboardRequest(http.MethodPost, "/api/v1/dialects", strings.NewReader(test.body))
			request.Header.Set("Origin", strings.TrimSuffix(testDashboardURL, "/"))
			request.Header.Set("X-CC-Dialect-CSRF", testDashboardCSRF)
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			response := serveDashboardRequest(handler, request)
			if response.Code != test.status || dashboardErrorCode(t, response) != test.code {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}

	oversized := `{"name":"` + strings.Repeat("a", dashboardMaxBodyBytes) + `","model":"m"}`
	response := serveDashboardRequest(handler, testDashboardMutation(http.MethodPost, "/api/v1/dialects", oversized))
	if response.Code != http.StatusRequestEntityTooLarge || dashboardErrorCode(t, response) != "request_too_large" {
		t.Fatalf("oversized response = %d %s", response.Code, response.Body.String())
	}
}

func TestDashboardRequiresExactDestructiveConfirmation(t *testing.T) {
	dialectRemovals := 0
	launcherRemovals := 0
	service := &fakeDashboardService{
		removeDialectFn: func(name, revision string) error {
			dialectRemovals++
			if name != "demo" || revision != "revision-1" {
				t.Fatalf("remove dialect got %q %q", name, revision)
			}
			return nil
		},
		removeNativeLauncherFn: func(name, revision string) error {
			launcherRemovals++
			if name != "cc-native" || revision != "revision-1" {
				t.Fatalf("remove launcher got %q %q", name, revision)
			}
			return nil
		},
	}
	handler := testDashboardHandler(t, service)

	for _, test := range []struct {
		path    string
		wrong   string
		exact   string
		removed *int
	}{
		{path: "/api/v1/dialects/demo", wrong: `{"confirmation":"Demo"}`, exact: `{"confirmation":"demo"}`, removed: &dialectRemovals},
		{path: "/api/v1/native-launchers/cc-native", wrong: `{"confirmation":"cc-native "}`, exact: `{"confirmation":"cc-native"}`, removed: &launcherRemovals},
	} {
		request := testDashboardMutation(http.MethodDelete, test.path, test.wrong)
		request.Header.Set("If-Match", `"revision-1"`)
		response := serveDashboardRequest(handler, request)
		if response.Code != http.StatusBadRequest || dashboardErrorCode(t, response) != "confirmation_mismatch" {
			t.Fatalf("wrong confirmation response = %d %s", response.Code, response.Body.String())
		}
		if *test.removed != 0 {
			t.Fatalf("remove called after wrong confirmation")
		}

		request = testDashboardMutation(http.MethodDelete, test.path, test.exact)
		request.Header.Set("If-Match", `"revision-1"`)
		response = serveDashboardRequest(handler, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("exact confirmation response = %d %s", response.Code, response.Body.String())
		}
		if *test.removed != 1 {
			t.Fatalf("remove calls = %d", *test.removed)
		}
	}
}

func TestDashboardETagAndRevisionConflicts(t *testing.T) {
	service := &fakeDashboardService{
		listDialectsFn: func(includeStatus bool) (DialectListResult, error) {
			return DialectListResult{Dialects: []DialectView{}, Revision: "revision-1"}, nil
		},
		updateDialectFn: func(input DialectInput, revision string) (DialectMutationResult, error) {
			if revision != "revision-1" {
				t.Fatalf("expected revision = %q", revision)
			}
			return DialectMutationResult{}, operationError(ErrorRevisionConflict, "private-data-should-not-leak")
		},
	}
	handler := testDashboardHandler(t, service)

	response := serveDashboardRequest(handler, testDashboardRequest(http.MethodGet, "/api/v1/dialects", nil))
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"revision-1"` {
		t.Fatalf("list response = %d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body.String())
	}

	request := testDashboardMutation(http.MethodPut, "/api/v1/dialects/demo", `{"model":"m"}`)
	response = serveDashboardRequest(handler, request)
	if response.Code != http.StatusPreconditionRequired || dashboardErrorCode(t, response) != "precondition_required" {
		t.Fatalf("missing If-Match response = %d %s", response.Code, response.Body.String())
	}

	request = testDashboardMutation(http.MethodPut, "/api/v1/dialects/demo", `{"model":"m"}`)
	request.Header.Set("If-Match", "revision-1")
	response = serveDashboardRequest(handler, request)
	if response.Code != http.StatusBadRequest || dashboardErrorCode(t, response) != "invalid_if_match" {
		t.Fatalf("invalid If-Match response = %d %s", response.Code, response.Body.String())
	}

	request = testDashboardMutation(http.MethodPut, "/api/v1/dialects/demo", `{"model":"m"}`)
	request.Header.Set("If-Match", `"revision-1"`)
	response = serveDashboardRequest(handler, request)
	if response.Code != http.StatusPreconditionFailed || dashboardErrorCode(t, response) != string(ErrorRevisionConflict) {
		t.Fatalf("revision conflict response = %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "private-data") {
		t.Fatalf("revision response leaked service detail: %s", response.Body.String())
	}
}

func TestDashboardSafeResponsesAndErrors(t *testing.T) {
	service := &fakeDashboardService{
		listDialectsFn: func(includeStatus bool) (DialectListResult, error) {
			return DialectListResult{Dialects: []DialectView{{
				Name: "safe", Model: "model", BaseURL: "https://user:credential@example.com/v1?api_key=secret#credential",
				ExtraEnvKeys: []string{"SECRET_ENV"},
			}}, Revision: "revision-1"}, nil
		},
		installCursorRuntimeFn: func() (CursorInstallResult, error) {
			return CursorInstallResult{}, errors.New("npm output: CURSOR_API_KEY=super-secret ExtraEnv=value")
		},
	}
	handler := testDashboardHandler(t, service)

	response := serveDashboardRequest(handler, testDashboardRequest(http.MethodGet, "/api/v1/dialects", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("list response = %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, forbidden := range []string{"credential", "api_key", "secret#", "super-secret", `"apiKey"`, "ExtraEnv=value"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("safe response contains %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"extraEnvKeys":["SECRET_ENV"]`) {
		t.Fatalf("safe response omitted environment key names: %s", body)
	}

	request := testDashboardMutation(http.MethodPut, "/api/v1/cursor/runtime", `{}`)
	response = serveDashboardRequest(handler, request)
	if response.Code != http.StatusInternalServerError || dashboardErrorCode(t, response) != "internal_error" {
		t.Fatalf("cursor error response = %d %s", response.Code, response.Body.String())
	}
	for _, forbidden := range []string{"npm output", "CURSOR_API_KEY", "super-secret", "ExtraEnv=value"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("error response contains %q: %s", forbidden, response.Body.String())
		}
	}
}

func TestDashboardSecurityHeadersAndNoCORS(t *testing.T) {
	handler := testDashboardHandler(t, &fakeDashboardService{})
	for _, path := range []string{"/", "/api/v1/bootstrap"} {
		response := serveDashboardRequest(handler, testDashboardRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s returned %d", path, response.Code)
		}
		headers := response.Header()
		for name, expected := range map[string]string{
			"Cache-Control":          "no-store, no-cache, must-revalidate",
			"Pragma":                 "no-cache",
			"Expires":                "0",
			"X-Frame-Options":        "DENY",
			"Referrer-Policy":        "no-referrer",
			"X-Content-Type-Options": "nosniff",
		} {
			if value := headers.Get(name); value != expected {
				t.Fatalf("GET %s header %s = %q", path, name, value)
			}
		}
		if !strings.Contains(headers.Get("Content-Security-Policy"), "default-src 'self'") || !strings.Contains(headers.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatalf("GET %s CSP = %q", path, headers.Get("Content-Security-Policy"))
		}
		if value := headers.Get("Access-Control-Allow-Origin"); value != "" {
			t.Fatalf("GET %s unexpectedly enabled CORS: %q", path, value)
		}
	}
}

func TestDashboardBootstrapAndOperationRoutes(t *testing.T) {
	called := map[string]int{}
	service := &fakeDashboardService{
		dialectFn: func(name string) (DialectView, string, error) {
			called["detail"]++
			return DialectView{Name: name, Model: "model"}, "revision-1", nil
		},
		dialectStatusFn: func(name string) (RuntimeStatus, error) {
			called["status"]++
			return RuntimeStatus{State: RuntimeRunning}, nil
		},
		createDialectFn: func(input DialectInput, revision string) (DialectMutationResult, error) {
			called["create"]++
			if input.Name != "new" || input.Model != "model" {
				t.Fatalf("unexpected create input: %#v", input)
			}
			return DialectMutationResult{Dialect: DialectView{Name: input.Name, Model: input.Model}, Created: true, Revision: "revision-2"}, nil
		},
		updateDialectFn: func(input DialectInput, revision string) (DialectMutationResult, error) {
			called["update"]++
			return DialectMutationResult{Dialect: DialectView{Name: input.Name, Model: input.Model}, Revision: "revision-2"}, nil
		},
		startDialectFn: func(name string) (RuntimeStatus, error) {
			called["start"]++
			return RuntimeStatus{State: RuntimeRunning}, nil
		},
		stopDialectFn: func(name string) (RuntimeStatus, error) {
			called["stop"]++
			return RuntimeStatus{State: RuntimeStopped}, nil
		},
		restartDialectFn: func(name string) (RuntimeStatus, error) {
			called["restart"]++
			return RuntimeStatus{State: RuntimeRunning}, nil
		},
		cursorStatusFn: func() CursorRuntimeStatus {
			called["cursor-status"]++
			return CursorRuntimeStatus{RequiredVersion: cursorSDKVersion, RuntimeCurrent: true}
		},
		installCursorRuntimeFn: func() (CursorInstallResult, error) {
			called["cursor-update"]++
			return CursorInstallResult{InstalledVersion: cursorSDKVersion}, nil
		},
		listNativeLaunchersFn: func() ([]NativeLauncherView, string, error) {
			called["launcher-list"]++
			return []NativeLauncherView{{Name: "native", Path: "/tmp/native"}}, "revision-1", nil
		},
		installNativeLauncherFn: func(input NativeLauncherInput, revision string) (NativeLauncherResult, error) {
			called["launcher-update"]++
			return NativeLauncherResult{Launcher: NativeLauncherView{Name: input.Name}, Revision: "revision-2"}, nil
		},
	}
	handler := testDashboardHandler(t, service)

	bootstrap := serveDashboardRequest(handler, testDashboardRequest(http.MethodGet, "/api/v1/bootstrap", nil))
	if bootstrap.Code != http.StatusOK || !strings.Contains(bootstrap.Body.String(), `"version":"1.2.3"`) || !strings.Contains(bootstrap.Body.String(), `"csrfToken":"`+testDashboardCSRF+`"`) {
		t.Fatalf("bootstrap response = %d %s", bootstrap.Code, bootstrap.Body.String())
	}
	presets := serveDashboardRequest(handler, testDashboardRequest(http.MethodGet, "/api/v1/presets", nil))
	if presets.Code != http.StatusOK || !strings.Contains(presets.Body.String(), `"presets"`) || strings.Contains(presets.Body.String(), `"apiKey"`) {
		t.Fatalf("presets response = %d %s", presets.Code, presets.Body.String())
	}

	requests := []*http.Request{
		testDashboardRequest(http.MethodGet, "/api/v1/dialects/demo", nil),
		testDashboardMutation(http.MethodPost, "/api/v1/dialects", `{"name":"new","model":"model"}`),
		testDashboardMutation(http.MethodPost, "/api/v1/dialects/demo/start", `{}`),
		testDashboardMutation(http.MethodPost, "/api/v1/dialects/demo/stop", `{}`),
		testDashboardMutation(http.MethodPost, "/api/v1/dialects/demo/restart", `{}`),
		testDashboardRequest(http.MethodGet, "/api/v1/cursor/runtime", nil),
		testDashboardMutation(http.MethodPut, "/api/v1/cursor/runtime", `{}`),
		testDashboardRequest(http.MethodGet, "/api/v1/native-launchers", nil),
		testDashboardMutation(http.MethodPost, "/api/v1/native-launchers", `{"name":"fresh","directory":"/tmp"}`),
	}
	for _, request := range requests {
		response := serveDashboardRequest(handler, request)
		if response.Code < 200 || response.Code >= 300 {
			t.Fatalf("%s %s returned %d: %s", request.Method, request.URL.Path, response.Code, response.Body.String())
		}
	}

	update := testDashboardMutation(http.MethodPut, "/api/v1/dialects/demo", `{"model":"updated"}`)
	update.Header.Set("If-Match", `"revision-1"`)
	response := serveDashboardRequest(handler, update)
	if response.Code != http.StatusOK {
		t.Fatalf("dialect update returned %d: %s", response.Code, response.Body.String())
	}

	launcherUpdate := testDashboardMutation(http.MethodPut, "/api/v1/native-launchers/native", `{"dangerous":true}`)
	launcherUpdate.Header.Set("If-Match", `"revision-1"`)
	response = serveDashboardRequest(handler, launcherUpdate)
	if response.Code != http.StatusOK {
		t.Fatalf("launcher update returned %d: %s", response.Code, response.Body.String())
	}

	for _, name := range []string{"detail", "status", "create", "update", "start", "stop", "restart", "cursor-status", "cursor-update", "launcher-list", "launcher-update"} {
		if called[name] == 0 {
			t.Fatalf("route did not call %s operation", name)
		}
	}
}

func TestDashboardRejectsRelativeNativeLauncherDirectoriesBeforeServiceCalls(t *testing.T) {
	installCalls := 0
	listCalls := 0
	service := &fakeDashboardService{
		listNativeLaunchersFn: func() ([]NativeLauncherView, string, error) {
			listCalls++
			return []NativeLauncherView{{Name: "native", Path: "/tmp/native"}}, "revision-1", nil
		},
		installNativeLauncherFn: func(input NativeLauncherInput, revision string) (NativeLauncherResult, error) {
			installCalls++
			return NativeLauncherResult{}, nil
		},
	}
	handler := testDashboardHandler(t, service)
	requests := []*http.Request{
		testDashboardMutation(http.MethodPost, "/api/v1/native-launchers", `{"name":"fresh","directory":"~/.local/bin"}`),
		testDashboardMutation(http.MethodPut, "/api/v1/native-launchers/native", `{"directory":"relative/bin"}`),
	}
	requests[1].Header.Set("If-Match", `"revision-1"`)
	for _, request := range requests {
		response := serveDashboardRequest(handler, request)
		if response.Code != http.StatusBadRequest || dashboardErrorCode(t, response) != string(ErrorInvalidInput) {
			t.Fatalf("%s %s response = %d %s", request.Method, request.URL.Path, response.Code, response.Body.String())
		}
	}
	if installCalls != 0 || listCalls != 0 {
		t.Fatalf("relative directories reached service: install=%d list=%d", installCalls, listCalls)
	}
}

func TestDashboardInvalidCustomUpstreamEditFailsBeforeStop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	cfg := defaultConfig()
	cfg.Dialects["demo"] = Dialect{Preset: "codex", Model: "original", Port: 43171, APIKey: "private", Effort: true, Concurrency: 3}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	revision, err := configRevision(cfg)
	if err != nil {
		t.Fatal(err)
	}
	service := newAppService()
	stopCalls := 0
	service.stopRuntime = func(string, Dialect) error {
		stopCalls++
		return nil
	}
	handler := testDashboardHandler(t, service)

	for _, body := range []string{
		`{"model":"replacement","baseUrl":"https://api.example.com/v1"}`,
		`{"model":"replacement","baseUrl":"https://user:secret@api.example.com/v1","authTokenEnv":"EXAMPLE_API_KEY"}`,
		`{"model":"replacement","baseUrl":"https://api.example.com/v1","authTokenEnv":"9INVALID"}`,
	} {
		request := testDashboardMutation(http.MethodPut, "/api/v1/dialects/demo", body)
		request.Header.Set("If-Match", strconv.Quote(revision))
		response := serveDashboardRequest(handler, request)
		if response.Code != http.StatusBadRequest || dashboardErrorCode(t, response) != string(ErrorInvalidInput) {
			t.Fatalf("invalid custom upstream response = %d %s", response.Code, response.Body.String())
		}
	}
	if stopCalls != 0 {
		t.Fatalf("invalid API edits stopped runtime %d times", stopCalls)
	}
}

func TestDashboardCursorInstallReturnsEmptyStoppedDialectsArray(t *testing.T) {
	handler := testDashboardHandler(t, &fakeDashboardService{
		installCursorRuntimeFn: func() (CursorInstallResult, error) {
			return CursorInstallResult{InstalledVersion: cursorSDKVersion}, nil
		},
	})
	response := serveDashboardRequest(handler, testDashboardMutation(http.MethodPut, "/api/v1/cursor/runtime", `{}`))
	if response.Code != http.StatusOK {
		t.Fatalf("Cursor runtime update returned %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"stoppedDialects":[]`) {
		t.Fatalf("Cursor runtime update returned a nullable stoppedDialects field: %s", response.Body.String())
	}
}

func TestDashboardValidatesURLNamesBeforeServiceCalls(t *testing.T) {
	calls := 0
	service := &fakeDashboardService{dialectFn: func(name string) (DialectView, string, error) {
		calls++
		return DialectView{}, "", nil
	}}
	handler := testDashboardHandler(t, service)
	for _, path := range []string{"/api/v1/dialects/..", "/api/v1/dialects/BadName", "/api/v1/dialects/a%2Fb"} {
		response := serveDashboardRequest(handler, testDashboardRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest && response.Code != http.StatusNotFound {
			t.Fatalf("GET %s returned %d: %s", path, response.Code, response.Body.String())
		}
	}
	if calls != 0 {
		t.Fatalf("service was called %d times for invalid URL names", calls)
	}
}

func TestDashboardUsageIncludesWebCommand(t *testing.T) {
	if !strings.Contains(usage, "cc-dialect web [--listen 127.0.0.1:0] [--no-browser]") {
		t.Fatal("usage does not include the web command")
	}
}

func TestParseDashboardOptionsAndLoopbackValidation(t *testing.T) {
	opts, err := parseDashboardOptions([]string{"--listen", "127.0.0.1:4567", "--no-browser"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Listen != "127.0.0.1:4567" || !opts.NoBrowser {
		t.Fatalf("options = %#v", opts)
	}
	for _, address := range []string{"0.0.0.0:0", "192.168.1.5:8080", "localhost:0", ":8080"} {
		if err := validateDashboardListen(address); err == nil {
			t.Fatalf("validateDashboardListen(%q) succeeded", address)
		}
	}
	for _, address := range []string{"127.0.0.1:0", "[::1]:0"} {
		if err := validateDashboardListen(address); err != nil {
			t.Fatalf("validateDashboardListen(%q): %v", address, err)
		}
	}
}

type dashboardTestAddr string

func (address dashboardTestAddr) Network() string { return "tcp" }
func (address dashboardTestAddr) String() string  { return string(address) }

type dashboardTestListener struct {
	addr   net.Addr
	closed chan struct{}
	once   sync.Once
}

func (listener *dashboardTestListener) Accept() (net.Conn, error) {
	<-listener.closed
	return nil, net.ErrClosed
}

func (listener *dashboardTestListener) Close() error {
	listener.once.Do(func() { close(listener.closed) })
	return nil
}

func (listener *dashboardTestListener) Addr() net.Addr { return listener.addr }

type dashboardTestServer struct {
	started  chan struct{}
	shutdown chan struct{}
	once     sync.Once
}

func (server *dashboardTestServer) Serve(net.Listener) error {
	close(server.started)
	<-server.shutdown
	return http.ErrServerClosed
}

func (server *dashboardTestServer) Shutdown(context.Context) error {
	server.once.Do(func() { close(server.shutdown) })
	return nil
}

func TestServeDashboardBrowserSeamAndGracefulShutdown(t *testing.T) {
	for _, noBrowser := range []bool{false, true} {
		t.Run(map[bool]string{false: "opens browser", true: "no browser"}[noBrowser], func(t *testing.T) {
			listener := &dashboardTestListener{addr: dashboardTestAddr(testDashboardAuthority), closed: make(chan struct{})}
			server := &dashboardTestServer{started: make(chan struct{}), shutdown: make(chan struct{})}
			opened := make(chan string, 1)
			var output bytes.Buffer
			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() {
				result <- serveDashboard(ctx, dashboardOptions{Listen: defaultDashboardListen, NoBrowser: noBrowser}, &fakeDashboardService{}, "1.2.3", dashboardDependencies{
					listen: func(network, address string) (net.Listener, error) {
						if network != "tcp4" || address != defaultDashboardListen {
							t.Errorf("listen called with %q %q", network, address)
						}
						return listener, nil
					},
					openURL: func(rawURL string) error {
						opened <- rawURL
						return nil
					},
					newHTTPServer: func(http.Handler) dashboardHTTPServer { return server },
					stdout:        &output,
				})
			}()

			select {
			case <-server.started:
			case <-time.After(time.Second):
				t.Fatal("server did not start")
			}
			if noBrowser {
				select {
				case rawURL := <-opened:
					t.Fatalf("browser opened %q with --no-browser", rawURL)
				default:
				}
			} else {
				select {
				case rawURL := <-opened:
					if rawURL != testDashboardURL {
						t.Fatalf("opened URL = %q", rawURL)
					}
				case <-time.After(time.Second):
					t.Fatal("browser opener was not called")
				}
			}

			cancel()
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("dashboard did not shut down after cancellation")
			}
			select {
			case <-server.shutdown:
			default:
				t.Fatal("server Shutdown was not called")
			}
			if output.String() != testDashboardURL+"\n" {
				t.Fatalf("printed URL = %q", output.String())
			}
		})
	}
}

func TestServeDashboardOpenerErrorStopsServer(t *testing.T) {
	listener := &dashboardTestListener{addr: dashboardTestAddr(testDashboardAuthority), closed: make(chan struct{})}
	server := &dashboardTestServer{started: make(chan struct{}), shutdown: make(chan struct{})}
	err := serveDashboard(context.Background(), dashboardOptions{Listen: defaultDashboardListen}, &fakeDashboardService{}, "1.2.3", dashboardDependencies{
		listen:        func(string, string) (net.Listener, error) { return listener, nil },
		openURL:       func(string) error { return errors.New("open failed") },
		newHTTPServer: func(http.Handler) dashboardHTTPServer { return server },
		stdout:        io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "open dashboard") {
		t.Fatalf("serveDashboard error = %v", err)
	}
	select {
	case <-server.shutdown:
	default:
		t.Fatal("server was not shut down after opener failure")
	}
}
