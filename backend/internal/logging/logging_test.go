package logging

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddlewareRecordsResponseDetails(t *testing.T) {
	var logOutput bytes.Buffer
	logger := NewJSONLogger(&logOutput, slog.LevelInfo)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/test", func(responseWriter http.ResponseWriter, _ *http.Request) {
		responseWriter.WriteHeader(http.StatusCreated)
		responseWriter.Write([]byte("created"))
	})
	request := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	responseRecorder := httptest.NewRecorder()

	Middleware(mux, logger).ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", responseRecorder.Code, http.StatusCreated)
	}
	message := logOutput.String()
	for _, expected := range []string{
		`"msg":"http_request"`,
		`"method":"POST"`,
		`"route":"POST /api/test"`,
		`"status":201`,
		`"response_bytes":7`,
		`"duration_ms":`,
	} {
		if !strings.Contains(message, expected) {
			t.Errorf("log = %q, missing %q", message, expected)
		}
	}
	requestID := responseRecorder.Header().Get("X-Request-ID")
	if requestID == "" || !strings.Contains(message, `"request_id":"`+requestID+`"`) {
		t.Errorf("request ID header/log mismatch: header=%q log=%q", requestID, message)
	}
}

func TestMiddlewareUsesDefaultStatusWhenHandlerWritesNothing(t *testing.T) {
	var logOutput bytes.Buffer
	logger := NewJSONLogger(&logOutput, slog.LevelInfo)
	request := httptest.NewRequest(http.MethodGet, "/empty", nil)
	responseRecorder := httptest.NewRecorder()

	Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), logger).
		ServeHTTP(responseRecorder, request)

	if !strings.Contains(logOutput.String(), `"status":200`) {
		t.Fatalf("log = %q, want status=200", logOutput.String())
	}
}

func TestResponseWriterDefaultsStatusAndCountsBytes(t *testing.T) {
	responseRecorder := httptest.NewRecorder()
	responseWriter := &responseWriter{ResponseWriter: responseRecorder}

	bytesWritten, err := responseWriter.Write([]byte("ok"))
	responseWriter.WriteHeader(http.StatusCreated)

	if err != nil {
		t.Fatalf("Write() error = %v, want nil", err)
	}
	if bytesWritten != 2 || responseWriter.bytes != 2 {
		t.Fatalf("bytes written = (%d, %d), want (2, 2)", bytesWritten, responseWriter.bytes)
	}
	if responseWriter.status != http.StatusOK || responseRecorder.Code != http.StatusOK {
		t.Fatalf("status = (%d, %d), want (200, 200)", responseWriter.status, responseRecorder.Code)
	}
}

func TestMiddlewareUsesDefaultLoggerWhenNil(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	responseRecorder := httptest.NewRecorder()

	Middleware(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		responseWriter.WriteHeader(http.StatusNoContent)
	}), nil).ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", responseRecorder.Code, http.StatusNoContent)
	}
}
