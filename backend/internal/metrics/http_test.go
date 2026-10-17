package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddlewareUsesRoutePatternAndDoesNotExposeQueryValues(t *testing.T) {
	metrics := NewHTTP()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/renders/{id}", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
	})

	request := httptest.NewRequest(http.MethodGet, "/api/renders/job-secret?artist=private", nil)
	response := httptest.NewRecorder()
	metrics.Middleware(mux).ServeHTTP(response, request)

	metricsResponse := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsResponse.Body.String()
	for _, expected := range []string{
		`lyrics_http_requests_total{method="GET",route="GET /api/renders/{id}",status="202"} 1`,
		`lyrics_http_request_duration_seconds_count{method="GET",route="GET /api/renders/{id}"} 1`,
		`lyrics_http_requests_in_flight 0`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("metrics body missing %q:\n%s", expected, body)
		}
	}
	if strings.Contains(body, "job-secret") || strings.Contains(body, "private") {
		t.Fatalf("metric labels leaked request path/query values:\n%s", body)
	}
}

func TestMetricsMiddlewareDefaultsUnmatchedRouteAndStatus(t *testing.T) {
	metrics := NewHTTP()
	handler := metrics.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/missing", nil))

	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(response.Body.String(), `route="unmatched",status="200"`) {
		t.Fatalf("missing fallback labels in metrics:\n%s", response.Body.String())
	}
}
