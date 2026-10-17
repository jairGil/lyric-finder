package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log"
	"log/slog"
	"net/http"
	"time"
)

type requestIDKey struct{}

func NewJSONLogger(output io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level}))
}

func ConfigureStandardLogger(logger *slog.Logger) {
	slog.SetDefault(logger)
	bridge := slog.NewLogLogger(logger.Handler(), slog.LevelInfo)
	log.SetOutput(bridge.Writer())
	log.SetPrefix("")
	log.SetFlags(0)
}

func RequestID(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDKey{}).(string)
	return requestID
}

func Middleware(next http.Handler, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := newRequestID()
		writer.Header().Set("X-Request-ID", requestID)
		request = request.WithContext(context.WithValue(request.Context(), requestIDKey{}, requestID))

		startedAt := time.Now()
		responseWriter := &responseWriter{ResponseWriter: writer}
		next.ServeHTTP(responseWriter, request)

		status := responseWriter.status
		if status == 0 {
			status = http.StatusOK
		}
		route := request.Pattern
		if route == "" {
			route = "unmatched"
		}

		logger.InfoContext(request.Context(), "http_request",
			"request_id", requestID,
			"method", request.Method,
			"route", route,
			"status", status,
			"duration_ms", float64(time.Since(startedAt).Microseconds())/1000,
			"response_bytes", responseWriter.bytes,
		)
	})
}

func newRequestID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "unavailable"
	}
	return hex.EncodeToString(bytes[:])
}

type responseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (writer *responseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *responseWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}

	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *responseWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}

	bytes, err := writer.ResponseWriter.Write(body)
	writer.bytes += bytes
	return bytes, err
}
