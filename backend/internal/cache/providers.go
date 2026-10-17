package cache

import (
	"context"
	"strings"

	"lyrics/internal/domain"
	"lyrics/internal/ports"
)

type LyricsProvider struct {
	next  domain.LyricsProvider
	store ports.CacheRepository
}

func NewLyricsProvider(next domain.LyricsProvider, store ports.CacheRepository) *LyricsProvider {
	return &LyricsProvider{next: next, store: store}
}

func (provider *LyricsProvider) GetLyrics(ctx context.Context, artist, track string) (domain.Lyrics, error) {
	key := queryKey(artist, track)
	var cached domain.Lyrics
	if hit, err := provider.store.CacheGet(ctx, "lrclib:lyrics", key, &cached); err == nil && hit {
		return cached, nil
	}

	lyrics, err := provider.next.GetLyrics(ctx, artist, track)
	if err != nil {
		return domain.Lyrics{}, err
	}
	_ = provider.store.CacheSet(ctx, "lrclib:lyrics", key, lyrics)
	return lyrics, nil
}

func (provider *LyricsProvider) Search(ctx context.Context, artist, track string) ([]domain.SearchResult, error) {
	key := queryKey(artist, track)
	var cached []domain.SearchResult
	if hit, err := provider.store.CacheGet(ctx, "lrclib:search", key, &cached); err == nil && hit {
		return cached, nil
	}

	results, err := provider.next.Search(ctx, artist, track)
	if err != nil {
		return nil, err
	}
	_ = provider.store.CacheSet(ctx, "lrclib:search", key, results)
	return results, nil
}

type VideoSearcher struct {
	next  domain.VideoSearcher
	store ports.CacheRepository
}

func NewVideoSearcher(next domain.VideoSearcher, store ports.CacheRepository) *VideoSearcher {
	return &VideoSearcher{next: next, store: store}
}

func (searcher *VideoSearcher) FindVideo(ctx context.Context, artist, track string) (string, error) {
	key := queryKey(artist, track)
	var cached string
	if hit, err := searcher.store.CacheGet(ctx, "youtube:video", key, &cached); err == nil && hit && cached != "" {
		return cached, nil
	}

	videoID, err := searcher.next.FindVideo(ctx, artist, track)
	if err != nil {
		return "", err
	}
	if videoID != "" {
		_ = searcher.store.CacheSet(ctx, "youtube:video", key, videoID)
	}
	return videoID, nil
}

func queryKey(artist, track string) string {
	return strings.ToLower(strings.TrimSpace(artist) + "\x00" + strings.TrimSpace(track))
}
