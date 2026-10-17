package domain

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("letra no encontrada")

var ErrRecordNotFound = errors.New("recurso no encontrado")

var ErrConflict = errors.New("recurso ya existe")

type Track struct {
	Title    string  `json:"title"`
	Artist   string  `json:"artist"`
	Album    string  `json:"album,omitempty"`
	Duration float64 `json:"duration,omitempty"`
}

type SearchResult struct {
	ID             int    `json:"id"`
	Track          Track  `json:"track"`
	Synchronized   bool   `json:"synchronized"`
	YouTubeVideoID string `json:"youtubeVideoId,omitempty"`
}

type LyricLine struct {
	Text  string  `json:"text"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type Lyrics struct {
	Track        Track       `json:"track"`
	PlainLyrics  string      `json:"plainLyrics,omitempty"`
	SyncedLyrics []LyricLine `json:"lyrics,omitempty"`
	Synchronized bool        `json:"synchronized"`
}

type LyricsProvider interface {
	GetLyrics(ctx context.Context, artist, track string) (Lyrics, error)
	Search(ctx context.Context, artist, track string) ([]SearchResult, error)
}

type VideoSearcher interface {
	FindVideo(ctx context.Context, artist, track string) (string, error)
}
