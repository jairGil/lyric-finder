package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"lyrics/internal/domain"
)

func TestParseLRC(t *testing.T) {
	content := "[00:01.50]First line\r\n[00:03.25] Second line\n"

	lines, err := parseLRC(content, 5)

	if err != nil {
		t.Fatalf("parseLRC() error = %v, want nil", err)
	}
	want := []domain.LyricLine{
		{Text: "First line", Start: 1.5, End: 3.25},
		{Text: "Second line", Start: 3.25, End: 5},
	}
	if len(lines) != len(want) {
		t.Fatalf("parseLRC() returned %d lines, want %d", len(lines), len(want))
	}
	for index := range want {
		if lines[index] != want[index] {
			t.Errorf("line %d = %#v, want %#v", index, lines[index], want[index])
		}
	}
}

func TestParseLRCRejectsInvalidContent(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		content string
	}{
		{name: "empty", content: "plain text"},
		{name: "invalid minutes", content: "[invalid:00]line"},
		{name: "invalid seconds", content: "[01:invalid]line"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := parseLRC(testCase.content, 10); err == nil {
				t.Fatal("parseLRC() error = nil, want error")
			}
		})
	}
}

func TestParseLRCSkipsMalformedLines(t *testing.T) {
	content := "ignored\n[malformed]\n[00:01.00]line"

	lines, err := parseLRC(content, 2)

	if err != nil {
		t.Fatalf("parseLRC() error = %v, want nil", err)
	}
	if len(lines) != 1 || lines[0].Text != "line" {
		t.Fatalf("parseLRC() = %#v, want one valid line", lines)
	}
}

func TestParseLRCUsesPreviousStartWhenDurationHasPassed(t *testing.T) {
	lines, err := parseLRC("[00:01.00]line", 0)

	if err != nil {
		t.Fatalf("parseLRC() error = %v, want nil", err)
	}
	if lines[0].End != lines[0].Start {
		t.Fatalf("last line end = %v, want previous start %v", lines[0].End, lines[0].Start)
	}
}

func TestNewLRCLIBUsesDefaultClientWhenNil(t *testing.T) {
	lyricsProvider := NewLRCLIB(nil)

	if lyricsProvider.client != http.DefaultClient {
		t.Fatal("NewLRCLIB(nil) did not use http.DefaultClient")
	}
	if lyricsProvider.baseURL != baseURL {
		t.Fatalf("baseURL = %q, want %q", lyricsProvider.baseURL, baseURL)
	}
}

func TestLRCLIBGetLyrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path != getPath {
			t.Errorf("path = %q, want %q", request.URL.Path, getPath)
		}
		query := request.URL.Query()
		if query.Get(artistParam) != "Artist" || query.Get(trackParam) != "Track" {
			t.Errorf("query = %v, want artist and track parameters", query)
		}
		if request.Header.Get("User-Agent") != userAgent {
			t.Errorf("User-Agent = %q, want %q", request.Header.Get("User-Agent"), userAgent)
		}

		responseWriter.Header().Set("Content-Type", "application/json")
		responseWriter.WriteHeader(http.StatusOK)
		responseWriter.Write([]byte(`{"trackName":"Track","artistName":"Artist","albumName":"Album","plainLyrics":"Plain","syncedLyrics":"[00:01.00]Hello\n[00:02.00]World","duration":4}`))
	}))
	defer server.Close()

	lyricsProvider := NewLRCLIB(server.Client())
	lyricsProvider.baseURL = server.URL

	lyrics, err := lyricsProvider.GetLyrics(context.Background(), "Artist", "Track")

	if err != nil {
		t.Fatalf("GetLyrics() error = %v, want nil", err)
	}
	if lyrics.Track.Title != "Track" || lyrics.Track.Artist != "Artist" || lyrics.Track.Album != "Album" {
		t.Fatalf("track = %#v, want decoded track", lyrics.Track)
	}
	if lyrics.PlainLyrics != "Plain" || !lyrics.Synchronized || len(lyrics.SyncedLyrics) != 2 {
		t.Fatalf("lyrics = %#v, want plain and synchronized lyrics", lyrics)
	}
}

func TestLRCLIBGetLyricsMapsNotFound(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	lyricsProvider := NewLRCLIB(server.Client())
	lyricsProvider.baseURL = server.URL

	_, err := lyricsProvider.GetLyrics(context.Background(), "Artist", "Track")

	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetLyrics() error = %v, want %v", err, domain.ErrNotFound)
	}
}

func TestLRCLIBGetLyricsHandlesUnexpectedResponses(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		status    int
		body      string
		wantInErr string
	}{
		{name: "server error", status: http.StatusBadGateway, body: "upstream failure", wantInErr: "HTTP 502"},
		{name: "invalid JSON", status: http.StatusOK, body: "{invalid", wantInErr: "invalid character"},
		{name: "invalid synced lyrics", status: http.StatusOK, body: `{"syncedLyrics":"[00:invalid]line","duration":4}`, wantInErr: "timestamp inválido"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
				responseWriter.WriteHeader(testCase.status)
				responseWriter.Write([]byte(testCase.body))
			}))
			defer server.Close()

			lyricsProvider := NewLRCLIB(server.Client())
			lyricsProvider.baseURL = server.URL

			_, err := lyricsProvider.GetLyrics(context.Background(), "Artist", "Track")

			if err == nil || !strings.Contains(err.Error(), testCase.wantInErr) {
				t.Fatalf("GetLyrics() error = %v, want substring %q", err, testCase.wantInErr)
			}
		})
	}
}

func TestLRCLIBSearchUsesArtistQueryWhenTrackIsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path != searchPath {
			t.Errorf("path = %q, want %q", request.URL.Path, searchPath)
		}
		query := request.URL.Query()
		if query.Get(searchParam) != "Artist" || query.Get(artistParam) != "" || query.Get(trackParam) != "" {
			t.Errorf("query = %v, want only %s=Artist", query, searchParam)
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		responseWriter.Write([]byte(`[{"id":7,"trackName":"Track","artistName":"Artist","syncedLyrics":"[00:01.00]line"}]`))
	}))
	defer server.Close()

	lyricsProvider := NewLRCLIB(server.Client())
	lyricsProvider.baseURL = server.URL

	results, err := lyricsProvider.Search(context.Background(), "Artist", " ")

	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}
	if len(results) != 1 || results[0].ID != 7 || !results[0].Synchronized {
		t.Fatalf("Search() = %#v, want one synchronized result", results)
	}
}

func TestLRCLIBSearchEncodesQueryValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if query.Get(artistParam) != "Artist & Co" || query.Get(trackParam) != "Track/Live" {
			t.Errorf("query = %v, want decoded artist and track values", query)
		}
		if _, err := url.ParseQuery(request.URL.RawQuery); err != nil {
			t.Errorf("RawQuery = %q is not valid query data: %v", request.URL.RawQuery, err)
		}
		responseWriter.Write([]byte("[]"))
	}))
	defer server.Close()

	lyricsProvider := NewLRCLIB(server.Client())
	lyricsProvider.baseURL = server.URL

	if _, err := lyricsProvider.Search(context.Background(), "Artist & Co", "Track/Live"); err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}
}

func TestLRCLIBSearchHandlesUnexpectedResponses(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		status    int
		body      string
		wantError string
	}{
		{name: "server error", status: http.StatusServiceUnavailable, body: "unavailable", wantError: "HTTP 503"},
		{name: "invalid JSON", status: http.StatusOK, body: "not-json", wantError: "invalid character"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
				responseWriter.WriteHeader(testCase.status)
				responseWriter.Write([]byte(testCase.body))
			}))
			defer server.Close()

			lyricsProvider := NewLRCLIB(server.Client())
			lyricsProvider.baseURL = server.URL

			_, err := lyricsProvider.Search(context.Background(), "Artist", "Track")

			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("Search() error = %v, want substring %q", err, testCase.wantError)
			}
		})
	}
}
