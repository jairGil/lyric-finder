package cors

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddlewareHandlesPreflight(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		nextCalled = true
	})
	request := httptest.NewRequest(http.MethodOptions, "/api/search", nil)
	responseRecorder := httptest.NewRecorder()

	Middleware(next).ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", responseRecorder.Code, http.StatusNoContent)
	}
	if nextCalled {
		t.Fatal("next handler was called for preflight request")
	}
	if responseRecorder.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("missing Access-Control-Allow-Origin header")
	}
	if responseRecorder.Header().Get("Access-Control-Allow-Methods") != "GET, POST, PUT, PATCH, DELETE, OPTIONS" {
		t.Fatal("missing allowed methods header")
	}
	if responseRecorder.Header().Get("Access-Control-Allow-Headers") != "Content-Type, Authorization" {
		t.Fatal("missing allowed headers header")
	}
}

// El cliente del frontend envía POST con cabecera Authorization, lo que dispara
// preflight. Si el middleware no lo autorizara, el navegador bloquearía tanto el
// render como el guardado de la pista.
func TestMiddlewareAllowsPreflightForAuthenticatedWrites(t *testing.T) {
	request := httptest.NewRequest(http.MethodOptions, "/api/renders", nil)
	request.Header.Set("Origin", "http://localhost:5173")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	responseRecorder := httptest.NewRecorder()

	Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next handler should not run for preflight")
	})).ServeHTTP(responseRecorder, request)

	allowedMethods := responseRecorder.Header().Get("Access-Control-Allow-Methods")
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		if !strings.Contains(allowedMethods, method) {
			t.Errorf("Access-Control-Allow-Methods = %q, want it to include %s", allowedMethods, method)
		}
	}
	allowedHeaders := strings.ToLower(responseRecorder.Header().Get("Access-Control-Allow-Headers"))
	if !strings.Contains(allowedHeaders, "authorization") {
		t.Errorf("Access-Control-Allow-Headers = %q, want it to include authorization", allowedHeaders)
	}
}

func TestMiddlewareForwardsRegularRequests(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		responseWriter.WriteHeader(http.StatusOK)
	})
	request := httptest.NewRequest(http.MethodGet, "/api/search", nil)
	responseRecorder := httptest.NewRecorder()

	Middleware(next).ServeHTTP(responseRecorder, request)

	if !nextCalled {
		t.Fatal("next handler was not called for regular request")
	}
	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", responseRecorder.Code, http.StatusOK)
	}
	if responseRecorder.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("missing CORS header on regular request")
	}
}

func TestMiddlewareAllowsConfiguredOriginsOnly(t *testing.T) {
	handler := MiddlewareWithOrigins(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), []string{"https://lyrics.example.com", "https://preview.example.com"})

	allowed := httptest.NewRequest(http.MethodGet, "/api/search", nil)
	allowed.Header.Set("Origin", "https://lyrics.example.com")
	allowedResponse := httptest.NewRecorder()
	handler.ServeHTTP(allowedResponse, allowed)
	if got := allowedResponse.Header().Get("Access-Control-Allow-Origin"); got != "https://lyrics.example.com" {
		t.Errorf("allowed origin = %q, want the configured origin", got)
	}
	if got := allowedResponse.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin", got)
	}

	denied := httptest.NewRequest(http.MethodGet, "/api/search", nil)
	denied.Header.Set("Origin", "https://attacker.example")
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, denied)
	if got := deniedResponse.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("denied origin received CORS permission %q", got)
	}
}

func TestMiddlewareRejectsPreflightFromUnconfiguredOrigin(t *testing.T) {
	request := httptest.NewRequest(http.MethodOptions, "/api/search", nil)
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()
	MiddlewareWithOrigins(http.NotFoundHandler(), []string{"https://lyrics.example.com"}).ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.Code)
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unconfigured origin received CORS permission")
	}
}
