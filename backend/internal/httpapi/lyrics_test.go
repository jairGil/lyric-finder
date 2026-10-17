package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"lyrics/internal/domain"
	"lyrics/internal/service"
)

type fakeLyricsProvider struct {
	lyrics       domain.Lyrics
	searchResult []domain.SearchResult
	err          error
}

func (provider *fakeLyricsProvider) GetLyrics(context.Context, string, string) (domain.Lyrics, error) {
	return provider.lyrics, provider.err
}

func (provider *fakeLyricsProvider) Search(context.Context, string, string) ([]domain.SearchResult, error) {
	return provider.searchResult, provider.err
}

func TestLyricsHandlerGetReturnsJSON(t *testing.T) {
	expected := domain.Lyrics{
		Track:       domain.Track{Title: "Track", Artist: "Artist"},
		PlainLyrics: "lyrics",
	}
	lyricsHandler := NewLyricsHandler(service.NewLyricsService(&fakeLyricsProvider{lyrics: expected}))
	request := httptest.NewRequest(http.MethodGet, "/api/lyrics?artist=Artist&track=Track", nil)
	responseRecorder := httptest.NewRecorder()

	lyricsHandler.Get(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", responseRecorder.Code, http.StatusOK)
	}
	if responseRecorder.Header().Get("Content-Type") != contentTypeJSON {
		t.Fatalf("Content-Type = %q, want %q", responseRecorder.Header().Get("Content-Type"), contentTypeJSON)
	}
	var actual domain.Lyrics
	if err := json.NewDecoder(responseRecorder.Body).Decode(&actual); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if actual.Track != expected.Track || actual.PlainLyrics != expected.PlainLyrics {
		t.Fatalf("response = %#v, want %#v", actual, expected)
	}
}

func TestLyricsHandlerGetMapsErrors(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid query", err: service.ErrInvalidQuery, wantStatus: http.StatusBadRequest},
		{name: "not found", err: domain.ErrNotFound, wantStatus: http.StatusNotFound},
		{name: "provider failure", err: errors.New("provider failure"), wantStatus: http.StatusInternalServerError},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			lyricsHandler := NewLyricsHandler(service.NewLyricsService(&fakeLyricsProvider{err: testCase.err}))
			request := httptest.NewRequest(http.MethodGet, "/api/lyrics?artist=Artist&track=Track", nil)
			responseRecorder := httptest.NewRecorder()

			lyricsHandler.Get(responseRecorder, request)

			if responseRecorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d", responseRecorder.Code, testCase.wantStatus)
			}
			var body map[string]string
			if err := json.NewDecoder(responseRecorder.Body).Decode(&body); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if body["error"] == "" {
				t.Fatal("error response does not contain an error message")
			}
		})
	}
}

func TestLyricsHandlerRejectsNonGet(t *testing.T) {
	lyricsHandler := NewLyricsHandler(service.NewLyricsService(&fakeLyricsProvider{}))
	request := httptest.NewRequest(http.MethodPost, "/api/lyrics", nil)
	responseRecorder := httptest.NewRecorder()

	lyricsHandler.Get(responseRecorder, request)

	if responseRecorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", responseRecorder.Code, http.StatusMethodNotAllowed)
	}
}

func TestLyricsHandlerSearchReturnsResults(t *testing.T) {
	expected := []domain.SearchResult{{ID: 1, Track: domain.Track{Title: "Track"}}}
	lyricsHandler := NewLyricsHandler(service.NewLyricsService(&fakeLyricsProvider{searchResult: expected}))
	request := httptest.NewRequest(http.MethodGet, "/api/search?artist=Artist", nil)
	responseRecorder := httptest.NewRecorder()

	lyricsHandler.Search(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", responseRecorder.Code, http.StatusOK)
	}
	var actual []domain.SearchResult
	if err := json.NewDecoder(responseRecorder.Body).Decode(&actual); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(actual) != 1 || actual[0] != expected[0] {
		t.Fatalf("response = %#v, want %#v", actual, expected)
	}
}

func TestLyricsHandlerSearchRejectsProviderError(t *testing.T) {
	lyricsHandler := NewLyricsHandler(service.NewLyricsService(&fakeLyricsProvider{err: errors.New("provider failure")}))
	request := httptest.NewRequest(http.MethodGet, "/api/search?artist=Artist", nil)
	responseRecorder := httptest.NewRecorder()

	lyricsHandler.Search(responseRecorder, request)

	if responseRecorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", responseRecorder.Code, http.StatusBadRequest)
	}
}

func TestLyricsHandlerSearchRejectsMissingArtist(t *testing.T) {
	lyricsHandler := NewLyricsHandler(service.NewLyricsService(&fakeLyricsProvider{}))
	request := httptest.NewRequest(http.MethodGet, "/api/search?artist=", nil)
	responseRecorder := httptest.NewRecorder()

	lyricsHandler.Search(responseRecorder, request)

	if responseRecorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", responseRecorder.Code, http.StatusBadRequest)
	}
}
