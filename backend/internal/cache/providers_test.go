package cache

import (
	"context"
	"testing"

	"lyrics/internal/domain"
	"lyrics/internal/storage"
)

type fakeProvider struct {
	lyricsCalls int
	searchCalls int
	videoCalls  int
}

func (provider *fakeProvider) GetLyrics(context.Context, string, string) (domain.Lyrics, error) {
	provider.lyricsCalls++
	return domain.Lyrics{PlainLyrics: "cached lyrics"}, nil
}

func (provider *fakeProvider) Search(context.Context, string, string) ([]domain.SearchResult, error) {
	provider.searchCalls++
	return []domain.SearchResult{{ID: 1}}, nil
}

func (provider *fakeProvider) FindVideo(context.Context, string, string) (string, error) {
	provider.videoCalls++
	return "video-123", nil
}

func TestProvidersUsePersistentCache(t *testing.T) {
	store, err := storage.Open(t.TempDir() + "/lyrics.db")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	fake := &fakeProvider{}
	lyricsProvider := NewLyricsProvider(fake, store)
	videoSearcher := NewVideoSearcher(fake, store)

	if _, err := lyricsProvider.GetLyrics(context.Background(), "Artist", "Track"); err != nil {
		t.Fatal(err)
	}
	if _, err := lyricsProvider.GetLyrics(context.Background(), " artist ", " track "); err != nil {
		t.Fatal(err)
	}
	if _, err := lyricsProvider.Search(context.Background(), "Artist", "Track"); err != nil {
		t.Fatal(err)
	}
	if _, err := lyricsProvider.Search(context.Background(), "Artist", "Track"); err != nil {
		t.Fatal(err)
	}
	if _, err := videoSearcher.FindVideo(context.Background(), "Artist", "Track"); err != nil {
		t.Fatal(err)
	}
	if _, err := videoSearcher.FindVideo(context.Background(), "artist", "track"); err != nil {
		t.Fatal(err)
	}

	if fake.lyricsCalls != 1 || fake.searchCalls != 1 || fake.videoCalls != 1 {
		t.Fatalf("provider calls = lyrics:%d search:%d video:%d, want one each", fake.lyricsCalls, fake.searchCalls, fake.videoCalls)
	}
}
