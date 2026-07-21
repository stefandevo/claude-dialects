package app

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

const dashboardMaxBodyBytes = 1 << 20

type dashboardService interface {
	ListDialects(includeStatus bool) (DialectListResult, error)
	Dialect(name string) (DialectView, string, error)
	CreateDialect(input DialectInput, expectedRevision string) (DialectMutationResult, error)
	UpdateDialect(input DialectInput, expectedRevision string) (DialectMutationResult, error)
	RemoveDialect(name, expectedRevision string) error
	StartDialect(name string) (RuntimeStatus, error)
	StopDialect(name string) (RuntimeStatus, error)
	RestartDialect(name string) (RuntimeStatus, error)
	DialectStatus(name string) (RuntimeStatus, error)
	CursorStatus() CursorRuntimeStatus
	InstallCursorRuntime() (CursorInstallResult, error)
	ListNativeLaunchers() ([]NativeLauncherView, string, error)
	InstallNativeLauncher(input NativeLauncherInput, expectedRevision string) (NativeLauncherResult, error)
	RemoveNativeLauncher(name, expectedRevision string) error
}

type dashboardAPI struct {
	service   dashboardService
	version   string
	url       string
	csrfToken string
}

type dashboardErrorEnvelope struct {
	Error dashboardError `json:"error"`
}

type dashboardError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type dashboardBootstrapResponse struct {
	Version   string `json:"version"`
	URL       string `json:"url"`
	CSRFToken string `json:"csrfToken"`
}

type dashboardPresetList struct {
	Presets []DialectView `json:"presets"`
}

type dialectRequest struct {
	Name          string `json:"name"`
	Preset        string `json:"preset"`
	Model         string `json:"model"`
	SubagentModel string `json:"subagentModel"`
	OpusModel     string `json:"opusModel"`
	SonnetModel   string `json:"sonnetModel"`
	HaikuModel    string `json:"haikuModel"`
	EffortLevel   string `json:"effortLevel"`
	Concurrency   int    `json:"concurrency"`
	Port          int    `json:"port"`
	BridgePort    int    `json:"bridgePort"`
	BaseURL       string `json:"baseUrl"`
	AuthTokenEnv  string `json:"authTokenEnv"`
	Effort        bool   `json:"effort"`
	ToolSearch    bool   `json:"toolSearch"`
}

type dashboardConfirmationRequest struct {
	Confirmation string `json:"confirmation"`
}

type dashboardActionRequest struct{}

type nativeLauncherRequest struct {
	Name      string `json:"name"`
	Directory string `json:"directory"`
	Dangerous bool   `json:"dangerous"`
}

type dialectStatusResponse struct {
	Name   string        `json:"name"`
	Status RuntimeStatus `json:"status"`
}

type nativeLauncherListResponse struct {
	Launchers []NativeLauncherView `json:"launchers"`
	Revision  string               `json:"revision"`
}

func newDashboardCSRFToken() (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("generate dashboard CSRF token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(secret), nil
}

func newDashboardHandler(service dashboardService, version, authority, rawURL, csrfToken string) (http.Handler, error) {
	if service == nil {
		return nil, fmt.Errorf("dashboard service is required")
	}
	if authority == "" || rawURL == "" || csrfToken == "" {
		return nil, fmt.Errorf("dashboard security configuration is incomplete")
	}
	spa, err := dashboardSPAHandler()
	if err != nil {
		return nil, err
	}
	api := &dashboardAPI{service: service, version: version, url: rawURL, csrfToken: csrfToken}
	origin := strings.TrimSuffix(rawURL, "/")
	return dashboardSecurityHandler(authority, origin, csrfToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isDashboardAPIPath(r.URL.Path) {
			api.ServeHTTP(w, r)
			return
		}
		spa.ServeHTTP(w, r)
	})), nil
}

func dashboardSecurityHandler(authority, origin, csrfToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setDashboardHeaders(w.Header())
		if r.Host != authority {
			writeDashboardError(w, http.StatusForbidden, "invalid_host", "request host is not allowed")
			return
		}
		requestOrigin := r.Header.Get("Origin")
		mutation := isDashboardAPIPath(r.URL.Path) && isDashboardMutation(r.Method)
		if (mutation && requestOrigin != origin) || (!mutation && requestOrigin != "" && requestOrigin != origin) {
			writeDashboardError(w, http.StatusForbidden, "invalid_origin", "request origin is not allowed")
			return
		}
		if mutation {
			provided := r.Header.Get("X-CC-Dialect-CSRF")
			if subtle.ConstantTimeCompare([]byte(provided), []byte(csrfToken)) != 1 {
				writeDashboardError(w, http.StatusForbidden, "invalid_csrf", "CSRF token is missing or invalid")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func setDashboardHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store, no-cache, must-revalidate")
	header.Set("Pragma", "no-cache")
	header.Set("Expires", "0")
	header.Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; connect-src 'self'; font-src 'self'; form-action 'self'; frame-ancestors 'none'; img-src 'self' data:; object-src 'none'; script-src 'self'; style-src 'self'")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
}

func isDashboardAPIPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}

func isDashboardMutation(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func (api *dashboardAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segments, err := dashboardAPISegments(r)
	if err != nil {
		writeDashboardError(w, http.StatusNotFound, "not_found", "API route not found")
		return
	}
	if len(segments) == 0 {
		writeDashboardError(w, http.StatusNotFound, "not_found", "API route not found")
		return
	}

	switch segments[0] {
	case "bootstrap":
		api.handleBootstrap(w, r, segments)
	case "presets":
		api.handlePresets(w, r, segments)
	case "dialects":
		api.handleDialects(w, r, segments)
	case "cursor":
		api.handleCursor(w, r, segments)
	case "native-launchers":
		api.handleNativeLaunchers(w, r, segments)
	default:
		writeDashboardError(w, http.StatusNotFound, "not_found", "API route not found")
	}
}

func dashboardAPISegments(r *http.Request) ([]string, error) {
	const prefix = "/api/v1/"
	escaped := r.URL.EscapedPath()
	if !strings.HasPrefix(escaped, prefix) {
		return nil, errors.New("not an API v1 path")
	}
	remainder := strings.TrimPrefix(escaped, prefix)
	if remainder == "" || strings.HasSuffix(remainder, "/") {
		return nil, errors.New("empty API path segment")
	}
	rawSegments := strings.Split(remainder, "/")
	segments := make([]string, len(rawSegments))
	for index, rawSegment := range rawSegments {
		segment, err := url.PathUnescape(rawSegment)
		if err != nil || segment == "" || strings.ContainsAny(segment, "/\\") {
			return nil, errors.New("invalid API path segment")
		}
		segments[index] = segment
	}
	return segments, nil
}

func (api *dashboardAPI) handleBootstrap(w http.ResponseWriter, r *http.Request, segments []string) {
	if len(segments) != 1 {
		writeDashboardError(w, http.StatusNotFound, "not_found", "API route not found")
		return
	}
	if !dashboardMethod(w, r, http.MethodGet) {
		return
	}
	writeDashboardJSON(w, http.StatusOK, dashboardBootstrapResponse{
		Version: api.version, URL: api.url, CSRFToken: api.csrfToken,
	})
}

func (api *dashboardAPI) handlePresets(w http.ResponseWriter, r *http.Request, segments []string) {
	if len(segments) != 1 {
		writeDashboardError(w, http.StatusNotFound, "not_found", "API route not found")
		return
	}
	if !dashboardMethod(w, r, http.MethodGet) {
		return
	}
	names := presetNames()
	views := make([]DialectView, 0, len(names))
	for _, name := range names {
		view := safeDialectView(name, presets[name])
		view.Name = name
		views = append(views, dashboardSafeDialectView(view))
	}
	writeDashboardJSON(w, http.StatusOK, dashboardPresetList{Presets: views})
}

func (api *dashboardAPI) handleDialects(w http.ResponseWriter, r *http.Request, segments []string) {
	if len(segments) == 1 {
		switch r.Method {
		case http.MethodGet:
			includeStatus := true
			if raw := r.URL.Query().Get("status"); raw != "" {
				parsed, err := strconv.ParseBool(raw)
				if err != nil {
					writeDashboardError(w, http.StatusBadRequest, "invalid_query", "status must be true or false")
					return
				}
				includeStatus = parsed
			}
			result, err := api.service.ListDialects(includeStatus)
			if err != nil {
				writeDashboardServiceError(w, err)
				return
			}
			for index := range result.Dialects {
				result.Dialects[index] = dashboardSafeDialectView(result.Dialects[index])
			}
			setDashboardETag(w.Header(), result.Revision)
			writeDashboardJSON(w, http.StatusOK, result)
		case http.MethodPost:
			var request dialectRequest
			if !decodeDashboardJSON(w, r, &request) {
				return
			}
			if !validName(request.Name) {
				writeDashboardError(w, http.StatusBadRequest, "invalid_name", "invalid dialect name")
				return
			}
			expected, ok := optionalDashboardIfMatch(w, r)
			if !ok {
				return
			}
			result, err := api.service.CreateDialect(request.dialectInput(request.Name), expected)
			if err != nil {
				writeDashboardServiceError(w, err)
				return
			}
			result.Dialect = dashboardSafeDialectView(result.Dialect)
			setDashboardETag(w.Header(), result.Revision)
			writeDashboardJSON(w, http.StatusCreated, result)
		default:
			dashboardMethod(w, r, http.MethodGet, http.MethodPost)
		}
		return
	}

	name := segments[1]
	if !validName(name) {
		writeDashboardError(w, http.StatusBadRequest, "invalid_name", "invalid dialect name")
		return
	}
	if len(segments) == 2 {
		switch r.Method {
		case http.MethodGet:
			view, revision, err := api.service.Dialect(name)
			if err != nil {
				writeDashboardServiceError(w, err)
				return
			}
			status, err := api.service.DialectStatus(name)
			if err != nil {
				writeDashboardServiceError(w, err)
				return
			}
			view.Status = &status
			view = dashboardSafeDialectView(view)
			setDashboardETag(w.Header(), revision)
			writeDashboardJSON(w, http.StatusOK, view)
		case http.MethodPut:
			var request dialectRequest
			if !decodeDashboardJSON(w, r, &request) {
				return
			}
			if request.Name != "" && request.Name != name {
				writeDashboardError(w, http.StatusBadRequest, "name_mismatch", "request name must match the URL")
				return
			}
			expected, ok := requireDashboardIfMatch(w, r)
			if !ok {
				return
			}
			result, err := api.service.UpdateDialect(request.dialectInput(name), expected)
			if err != nil {
				writeDashboardServiceError(w, err)
				return
			}
			result.Dialect = dashboardSafeDialectView(result.Dialect)
			setDashboardETag(w.Header(), result.Revision)
			writeDashboardJSON(w, http.StatusOK, result)
		case http.MethodDelete:
			var request dashboardConfirmationRequest
			if !decodeDashboardJSON(w, r, &request) {
				return
			}
			if request.Confirmation != name {
				writeDashboardError(w, http.StatusBadRequest, "confirmation_mismatch", "confirmation must exactly match the dialect name")
				return
			}
			expected, ok := requireDashboardIfMatch(w, r)
			if !ok {
				return
			}
			if err := api.service.RemoveDialect(name, expected); err != nil {
				writeDashboardServiceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			dashboardMethod(w, r, http.MethodGet, http.MethodPut, http.MethodDelete)
		}
		return
	}

	if len(segments) == 3 {
		action := segments[2]
		if action != "start" && action != "stop" && action != "restart" {
			writeDashboardError(w, http.StatusNotFound, "not_found", "API route not found")
			return
		}
		if !dashboardMethod(w, r, http.MethodPost) {
			return
		}
		var request dashboardActionRequest
		if !decodeDashboardJSON(w, r, &request) {
			return
		}
		var status RuntimeStatus
		var err error
		switch action {
		case "start":
			status, err = api.service.StartDialect(name)
		case "stop":
			status, err = api.service.StopDialect(name)
		case "restart":
			status, err = api.service.RestartDialect(name)
		}
		if err != nil {
			writeDashboardServiceError(w, err)
			return
		}
		writeDashboardJSON(w, http.StatusOK, dialectStatusResponse{Name: name, Status: status})
		return
	}

	writeDashboardError(w, http.StatusNotFound, "not_found", "API route not found")
}

func dashboardSafeDialectView(view DialectView) DialectView {
	if view.BaseURL == "" {
		return view
	}
	parsed, err := url.Parse(view.BaseURL)
	if err != nil {
		view.BaseURL = ""
		return view
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	view.BaseURL = parsed.String()
	return view
}

func (request dialectRequest) dialectInput(name string) DialectInput {
	return DialectInput{
		Name: name, Preset: request.Preset, Model: request.Model,
		SubagentModel: request.SubagentModel, OpusModel: request.OpusModel,
		SonnetModel: request.SonnetModel, HaikuModel: request.HaikuModel,
		EffortLevel: request.EffortLevel, Concurrency: request.Concurrency,
		Port: request.Port, BridgePort: request.BridgePort, BaseURL: request.BaseURL,
		AuthTokenEnv: request.AuthTokenEnv, Effort: request.Effort, ToolSearch: request.ToolSearch,
	}
}

func (api *dashboardAPI) handleCursor(w http.ResponseWriter, r *http.Request, segments []string) {
	if len(segments) != 2 || segments[1] != "runtime" {
		writeDashboardError(w, http.StatusNotFound, "not_found", "API route not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeDashboardJSON(w, http.StatusOK, api.service.CursorStatus())
	case http.MethodPut:
		var request dashboardActionRequest
		if !decodeDashboardJSON(w, r, &request) {
			return
		}
		result, err := api.service.InstallCursorRuntime()
		if err != nil {
			writeDashboardServiceError(w, err)
			return
		}
		if result.StoppedDialects == nil {
			result.StoppedDialects = []string{}
		}
		writeDashboardJSON(w, http.StatusOK, result)
	default:
		dashboardMethod(w, r, http.MethodGet, http.MethodPut)
	}
}

func (api *dashboardAPI) handleNativeLaunchers(w http.ResponseWriter, r *http.Request, segments []string) {
	if len(segments) == 1 {
		switch r.Method {
		case http.MethodGet:
			launchers, revision, err := api.service.ListNativeLaunchers()
			if err != nil {
				writeDashboardServiceError(w, err)
				return
			}
			setDashboardETag(w.Header(), revision)
			writeDashboardJSON(w, http.StatusOK, nativeLauncherListResponse{Launchers: launchers, Revision: revision})
		case http.MethodPost:
			var request nativeLauncherRequest
			if !decodeDashboardJSON(w, r, &request) {
				return
			}
			if !validName(request.Name) || request.Name == "claude" {
				writeDashboardError(w, http.StatusBadRequest, "invalid_name", "invalid native launcher name")
				return
			}
			if request.Directory != "" && !filepath.IsAbs(request.Directory) {
				writeDashboardError(w, http.StatusBadRequest, string(ErrorInvalidInput), "native launcher directory must be an absolute path or left blank")
				return
			}
			expected, ok := optionalDashboardIfMatch(w, r)
			if !ok {
				return
			}
			launchers, revision, err := api.service.ListNativeLaunchers()
			if err != nil {
				writeDashboardServiceError(w, err)
				return
			}
			if _, exists := dashboardNativeLauncher(launchers, request.Name); exists {
				writeDashboardError(w, http.StatusConflict, string(ErrorAlreadyExists), "native launcher already exists")
				return
			}
			if expected == "" {
				expected = revision
			}
			result, err := api.service.InstallNativeLauncher(NativeLauncherInput{
				Name: request.Name, Directory: request.Directory, Dangerous: request.Dangerous,
			}, expected)
			if err != nil {
				writeDashboardServiceError(w, err)
				return
			}
			setDashboardETag(w.Header(), result.Revision)
			writeDashboardJSON(w, http.StatusCreated, result)
		default:
			dashboardMethod(w, r, http.MethodGet, http.MethodPost)
		}
		return
	}

	if len(segments) != 2 {
		writeDashboardError(w, http.StatusNotFound, "not_found", "API route not found")
		return
	}
	name := segments[1]
	if !validName(name) || name == "claude" {
		writeDashboardError(w, http.StatusBadRequest, "invalid_name", "invalid native launcher name")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var request nativeLauncherRequest
		if !decodeDashboardJSON(w, r, &request) {
			return
		}
		if request.Name != "" && request.Name != name {
			writeDashboardError(w, http.StatusBadRequest, "name_mismatch", "request name must match the URL")
			return
		}
		if request.Directory != "" && !filepath.IsAbs(request.Directory) {
			writeDashboardError(w, http.StatusBadRequest, string(ErrorInvalidInput), "native launcher directory must be an absolute path or left blank")
			return
		}
		expected, ok := requireDashboardIfMatch(w, r)
		if !ok {
			return
		}
		launchers, _, err := api.service.ListNativeLaunchers()
		if err != nil {
			writeDashboardServiceError(w, err)
			return
		}
		existing, exists := dashboardNativeLauncher(launchers, name)
		if !exists {
			writeDashboardError(w, http.StatusNotFound, string(ErrorNotFound), "native launcher does not exist")
			return
		}
		directory := request.Directory
		if directory == "" && existing.Path != "" {
			directory = filepath.Dir(existing.Path)
		}
		result, err := api.service.InstallNativeLauncher(NativeLauncherInput{
			Name: name, Directory: directory, Dangerous: request.Dangerous,
		}, expected)
		if err != nil {
			writeDashboardServiceError(w, err)
			return
		}
		setDashboardETag(w.Header(), result.Revision)
		writeDashboardJSON(w, http.StatusOK, result)
	case http.MethodDelete:
		var request dashboardConfirmationRequest
		if !decodeDashboardJSON(w, r, &request) {
			return
		}
		if request.Confirmation != name {
			writeDashboardError(w, http.StatusBadRequest, "confirmation_mismatch", "confirmation must exactly match the native launcher name")
			return
		}
		expected, ok := requireDashboardIfMatch(w, r)
		if !ok {
			return
		}
		if err := api.service.RemoveNativeLauncher(name, expected); err != nil {
			writeDashboardServiceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		dashboardMethod(w, r, http.MethodPut, http.MethodDelete)
	}
}

func dashboardNativeLauncher(launchers []NativeLauncherView, name string) (NativeLauncherView, bool) {
	for _, launcher := range launchers {
		if launcher.Name == name {
			return launcher, true
		}
	}
	return NativeLauncherView{}, false
}

func dashboardMethod(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	for _, method := range allowed {
		if r.Method == method {
			return true
		}
	}
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	return false
}

func decodeDashboardJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeDashboardError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, dashboardMaxBodyBytes))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(destination); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeDashboardError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds 1 MiB")
			return false
		}
		writeDashboardError(w, http.StatusBadRequest, "invalid_json", "request body must contain one valid JSON object with only supported fields")
		return false
	}
	var trailing any
	if err = decoder.Decode(&trailing); err != io.EOF {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeDashboardError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds 1 MiB")
			return false
		}
		writeDashboardError(w, http.StatusBadRequest, "invalid_json", "request body must contain exactly one JSON value")
		return false
	}
	return true
}

func requireDashboardIfMatch(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.Header.Get("If-Match") == "" {
		writeDashboardError(w, http.StatusPreconditionRequired, "precondition_required", "If-Match is required")
		return "", false
	}
	return optionalDashboardIfMatch(w, r)
}

func optionalDashboardIfMatch(w http.ResponseWriter, r *http.Request) (string, bool) {
	value := r.Header.Get("If-Match")
	if value == "" {
		return "", true
	}
	if strings.Contains(value, ",") || strings.HasPrefix(value, "W/") {
		writeDashboardError(w, http.StatusBadRequest, "invalid_if_match", "If-Match must contain one strong ETag")
		return "", false
	}
	revision, err := strconv.Unquote(value)
	if err != nil || revision == "" {
		writeDashboardError(w, http.StatusBadRequest, "invalid_if_match", "If-Match must contain one quoted revision")
		return "", false
	}
	return revision, true
}

func setDashboardETag(header http.Header, revision string) {
	if revision != "" {
		header.Set("ETag", strconv.Quote(revision))
	}
}

func writeDashboardServiceError(w http.ResponseWriter, err error) {
	var operation *OperationError
	if errors.As(err, &operation) {
		switch operation.Code {
		case ErrorInvalidInput:
			writeDashboardError(w, http.StatusBadRequest, string(operation.Code), operation.Message)
		case ErrorNotFound:
			writeDashboardError(w, http.StatusNotFound, string(operation.Code), operation.Message)
		case ErrorAlreadyExists, ErrorExternallyModified:
			writeDashboardError(w, http.StatusConflict, string(operation.Code), operation.Message)
		case ErrorRevisionConflict:
			writeDashboardError(w, http.StatusPreconditionFailed, string(operation.Code), "configuration changed; reload and try again")
		default:
			writeDashboardError(w, http.StatusInternalServerError, "internal_error", "operation failed")
		}
		return
	}
	writeDashboardError(w, http.StatusInternalServerError, "internal_error", "operation failed")
}

func writeDashboardError(w http.ResponseWriter, status int, code, message string) {
	writeDashboardJSON(w, status, dashboardErrorEnvelope{Error: dashboardError{Code: code, Message: message}})
}

func writeDashboardJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
