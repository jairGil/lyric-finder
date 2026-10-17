package youtube

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func item(videoID, title string) string {
	return fmt.Sprintf(`{"id":{"videoId":%q},"snippet":{"title":%q,"channelTitle":"Artist Canal"}}`, videoID, title)
}

func TestFindVideo(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if query.Get("key") != "test-key" || query.Get("type") != "video" || query.Get("maxResults") != "10" {
			t.Errorf("query = %v, want YouTube search parameters", query)
		}
		mu.Lock()
		queries = append(queries, query.Get("q"))
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		writer.Write(fmt.Appendf(nil, `{"items":[%s]}`, item("video-123", "Artist - Track (Official Audio)")))
	}))
	defer server.Close()

	provider := New(server.Client(), "test-key")
	provider.baseURL = server.URL

	videoID, err := provider.FindVideo(context.Background(), "Artist", "Track")

	if err != nil {
		t.Fatalf("FindVideo() error = %v, want nil", err)
	}
	if videoID != "video-123" {
		t.Fatalf("videoID = %q, want %q", videoID, "video-123")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(queries) != 1 || queries[0] != "Artist Track" {
		t.Fatalf("queries = %v, want a single query %q", queries, "Artist Track")
	}
}

func TestFindVideoFallsBackToBetterQuery(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query().Get("q")
		mu.Lock()
		queries = append(queries, query)
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		switch query {
		case "Artist Track":
			writer.Write(fmt.Appendf(nil, `{"items":[%s]}`, item("video-wrong", "Un video cualquiera")))
		default:
			writer.Write(fmt.Appendf(nil, `{"items":[%s]}`, item("video-off", "Artist - Track (Official Audio)")))
		}
	}))
	defer server.Close()

	provider := New(server.Client(), "test-key")
	provider.baseURL = server.URL

	videoID, err := provider.FindVideo(context.Background(), "Artist", "Track")

	if err != nil {
		t.Fatalf("FindVideo() error = %v, want nil", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if videoID != "video-off" {
		t.Fatalf("videoID = %q, want %q", videoID, "video-off")
	}
	if len(queries) != 2 {
		t.Fatalf("queries = %v, want two attempts", queries)
	}
}

func TestFindVideoWithoutAPIKeySkipsRequest(t *testing.T) {
	provider := New(nil, "")

	videoID, err := provider.FindVideo(context.Background(), "Artist", "Track")

	if err != nil || videoID != "" {
		t.Fatalf("FindVideo() = (%q, %v), want (empty, nil)", videoID, err)
	}
}

func TestFindVideoUsesCache(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests++
		writer.Write(fmt.Appendf(nil, `{"items":[%s]}`, item("video-123", "Artist - Track (Official Audio)")))
	}))
	defer server.Close()

	provider := New(server.Client(), "test-key")
	provider.baseURL = server.URL

	first, firstErr := provider.FindVideo(context.Background(), "Artist", "Track")
	second, secondErr := provider.FindVideo(context.Background(), " artist ", " track ")

	if firstErr != nil || secondErr != nil || first != second || requests != 1 {
		t.Fatalf("results = (%q, %v), (%q, %v), requests = %d; want one cached request", first, firstErr, second, secondErr, requests)
	}
}

func TestFindVideoDoesNotCacheEmptyResults(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests++
		writer.Write([]byte(`{"items":[]}`))
	}))
	defer server.Close()

	provider := New(server.Client(), "test-key")
	provider.baseURL = server.URL

	first, firstErr := provider.FindVideo(context.Background(), "Artist", "Track")
	second, secondErr := provider.FindVideo(context.Background(), "Artist", "Track")

	if firstErr != nil || secondErr != nil || first != "" || second != "" {
		t.Fatalf("results = (%q, %v), (%q, %v), want both empty without error", first, firstErr, second, secondErr)
	}
	if requests != 6 {
		t.Fatalf("requests = %d, want 6 (three queries per lookup, no cache for empty)", requests)
	}
}

func TestFindVideoReturnsProviderErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
		writer.Write([]byte(`{"error":{"message":"quota exceeded"}}`))
	}))
	defer server.Close()

	provider := New(server.Client(), "test-key")
	provider.baseURL = server.URL

	_, err := provider.FindVideo(context.Background(), "Artist", "Track")

	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("FindVideo() error = %v, want HTTP 403 error", err)
	}
}
