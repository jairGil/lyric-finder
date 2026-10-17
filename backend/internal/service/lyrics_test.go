package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"lyrics/internal/domain"
)

type fakeLyricsProvider struct {
	lyrics       domain.Lyrics
	searchResult []domain.SearchResult
	err          error
	findCalls    int
	searchCalls  int
	artist       string
	track        string
}

type fakeVideoSearcher struct {
	videoID string
	artist  string
	track   string
}

func (searcher *fakeVideoSearcher) FindVideo(_ context.Context, artist, track string) (string, error) {
	searcher.artist = artist
	searcher.track = track
	return searcher.videoID, nil
}

func (provider *fakeLyricsProvider) GetLyrics(_ context.Context, artist, track string) (domain.Lyrics, error) {
	provider.findCalls++
	provider.artist = artist
	provider.track = track
	return provider.lyrics, provider.err
}

func (provider *fakeLyricsProvider) Search(_ context.Context, artist, track string) ([]domain.SearchResult, error) {
	provider.searchCalls++
	provider.artist = artist
	provider.track = track
	return provider.searchResult, provider.err
}

func TestLyricsServiceFindRejectsIncompleteQuery(t *testing.T) {
	provider := &fakeLyricsProvider{}
	lyricsService := NewLyricsService(provider)

	for _, testCase := range []struct {
		name   string
		artist string
		track  string
	}{
		{name: "missing artist", artist: "", track: "Track"},
		{name: "missing track", artist: "Artist", track: "  "},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := lyricsService.Find(context.Background(), testCase.artist, testCase.track)

			if !errors.Is(err, ErrInvalidQuery) {
				t.Fatalf("Find() error = %v, want %v", err, ErrInvalidQuery)
			}
			if provider.findCalls != 0 {
				t.Fatalf("provider was called %d times, want 0", provider.findCalls)
			}
		})
	}
}

func TestLyricsServiceFindDelegatesValidQuery(t *testing.T) {
	expected := domain.Lyrics{Track: domain.Track{Title: "Track", Artist: "Artist"}}
	provider := &fakeLyricsProvider{lyrics: expected}
	lyricsService := NewLyricsService(provider)

	lyrics, err := lyricsService.Find(context.Background(), " Artist ", " Track ")

	if err != nil {
		t.Fatalf("Find() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(lyrics, expected) {
		t.Fatalf("Find() = %#v, want %#v", lyrics, expected)
	}
	if provider.findCalls != 1 || provider.artist != " Artist " || provider.track != " Track " {
		t.Fatalf("provider call = (%d, %q, %q), want (1, %q, %q)", provider.findCalls, provider.artist, provider.track, " Artist ", " Track ")
	}
}

func TestLyricsServiceSearchRejectsMissingArtist(t *testing.T) {
	provider := &fakeLyricsProvider{}
	lyricsService := NewLyricsService(provider)

	_, err := lyricsService.Search(context.Background(), "  ", "Track")

	if err == nil || err.Error() != "artist es obligatorio" {
		t.Fatalf("Search() error = %v, want artist validation error", err)
	}
	if provider.searchCalls != 0 {
		t.Fatalf("provider was called %d times, want 0", provider.searchCalls)
	}
}

func TestLyricsServiceSearchDelegatesValidQuery(t *testing.T) {
	expected := []domain.SearchResult{{ID: 1, Track: domain.Track{Title: "Track"}}}
	provider := &fakeLyricsProvider{searchResult: expected}
	lyricsService := NewLyricsService(provider)

	results, err := lyricsService.Search(context.Background(), "Artist", "Track")

	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}
	if len(results) != len(expected) || results[0] != expected[0] {
		t.Fatalf("Search() = %#v, want %#v", results, expected)
	}
	if provider.searchCalls != 1 || provider.artist != "Artist" || provider.track != "Track" {
		t.Fatalf("provider call = (%d, %q, %q), want (1, %q, %q)", provider.searchCalls, provider.artist, provider.track, "Artist", "Track")
	}
}

func TestLyricsServiceFindVideo(t *testing.T) {
	videoSearcher := &fakeVideoSearcher{videoID: "video-123"}
	lyricsService := NewLyricsService(&fakeLyricsProvider{}, videoSearcher)

	videoID, err := lyricsService.FindVideo(context.Background(), "Artist", "Track")

	if err != nil {
		t.Fatalf("FindVideo() error = %v, want nil", err)
	}
	if videoID != "video-123" {
		t.Fatalf("videoID = %q, want %q", videoID, "video-123")
	}
	if videoSearcher.artist != "Artist" || videoSearcher.track != "Track" {
		t.Fatalf("video search query = (%q, %q), want (Artist, Track)", videoSearcher.artist, videoSearcher.track)
	}
}
