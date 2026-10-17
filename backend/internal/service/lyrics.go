package service

import (
	"context"
	"errors"
	"strings"

	"lyrics/internal/domain"
)

var ErrInvalidQuery = errors.New("artist y track son obligatorios")

type LyricsService struct {
	provider      domain.LyricsProvider
	videoSearcher domain.VideoSearcher
}

func NewLyricsService(provider domain.LyricsProvider, videoSearchers ...domain.VideoSearcher) *LyricsService {
	var videoSearcher domain.VideoSearcher
	if len(videoSearchers) > 0 {
		videoSearcher = videoSearchers[0]
	}
	return &LyricsService{provider: provider, videoSearcher: videoSearcher}
}

func (service *LyricsService) Find(ctx context.Context, artist, track string) (domain.Lyrics, error) {
	if strings.TrimSpace(artist) == "" || strings.TrimSpace(track) == "" {
		return domain.Lyrics{}, ErrInvalidQuery
	}
	return service.provider.GetLyrics(ctx, artist, track)
}

func (service *LyricsService) Search(ctx context.Context, artist, track string) ([]domain.SearchResult, error) {
	if strings.TrimSpace(artist) == "" {
		return nil, errors.New("artist es obligatorio")
	}
	return service.provider.Search(ctx, artist, track)
}

func (service *LyricsService) FindVideo(ctx context.Context, artist, track string) (string, error) {
	if strings.TrimSpace(artist) == "" || strings.TrimSpace(track) == "" {
		return "", ErrInvalidQuery
	}
	if service.videoSearcher == nil {
		return "", nil
	}
	return service.videoSearcher.FindVideo(ctx, artist, track)
}
