package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"lyrics/internal/domain"
)

type lrclibResponse struct {
	ID           int     `json:"id"`
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	AlbumName    string  `json:"albumName"`
	PlainLyrics  string  `json:"plainLyrics"`
	SyncedLyrics string  `json:"syncedLyrics"`
	Duration     float64 `json:"duration"`
}

type LRCLIB struct {
	client  *http.Client
	baseURL string
}

const (
	userAgent   = "lyrics-api/0.1 (personal project)"
	baseURL     = "https://lrclib.net"
	getPath     = "/api/get"
	searchPath  = "/api/search"
	artistParam = "artist_name"
	trackParam  = "track_name"
	searchParam = "q"
)

func NewLRCLIB(client *http.Client) *LRCLIB {
	if client == nil {
		client = http.DefaultClient
	}
	return &LRCLIB{client: client, baseURL: baseURL}
}

func (provider *LRCLIB) GetLyrics(ctx context.Context, artist, track string) (domain.Lyrics, error) {
	query := url.Values{}
	query.Set(artistParam, artist)
	query.Set(trackParam, track)

	url := fmt.Sprintf("%s%s?%s", provider.baseURL, getPath, query.Encode())

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return domain.Lyrics{}, err
	}
	request.Header.Set("User-Agent", userAgent)

	response, err := provider.client.Do(request)
	if err != nil {
		return domain.Lyrics{}, err
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotFound {
		return domain.Lyrics{}, domain.ErrNotFound
	}
	if response.StatusCode != http.StatusOK {
		return domain.Lyrics{}, fmt.Errorf("LRCLIB respondió con HTTP %d", response.StatusCode)
	}

	var result lrclibResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return domain.Lyrics{}, err
	}

	lyrics := domain.Lyrics{
		Track:       domain.Track{Title: result.TrackName, Artist: result.ArtistName, Album: result.AlbumName, Duration: result.Duration},
		PlainLyrics: result.PlainLyrics,
	}
	if result.SyncedLyrics != "" {
		lyrics.SyncedLyrics, err = parseLRC(result.SyncedLyrics, result.Duration)
		if err != nil {
			return domain.Lyrics{}, err
		}
		lyrics.Synchronized = true
	}
	return lyrics, nil
}

func (provider *LRCLIB) Search(ctx context.Context, artist, track string) ([]domain.SearchResult, error) {
	query := url.Values{}
	if strings.TrimSpace(track) == "" {
		query.Set(searchParam, artist)
	} else {
		query.Set(artistParam, artist)
		query.Set(trackParam, track)
	}

	url := fmt.Sprintf("%s%s?%s", provider.baseURL, searchPath, query.Encode())

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", userAgent)

	response, err := provider.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LRCLIB respondió con HTTP %d", response.StatusCode)
	}

	var records []lrclibResponse
	if err := json.NewDecoder(response.Body).Decode(&records); err != nil {
		return nil, err
	}

	results := make([]domain.SearchResult, 0, len(records))
	for _, record := range records {
		results = append(results, domain.SearchResult{
			ID:           record.ID,
			Track:        domain.Track{Title: record.TrackName, Artist: record.ArtistName, Album: record.AlbumName, Duration: record.Duration},
			Synchronized: record.SyncedLyrics != "",
		})
	}
	return results, nil
}

func parseLRC(content string, trackDuration float64) ([]domain.LyricLine, error) {
	var lines []domain.LyricLine
	var previousStart float64

	for _, rawLine := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		if len(rawLine) == 0 || rawLine[0] != '[' {
			continue
		}
		closingBracket := strings.IndexByte(rawLine, ']')
		if closingBracket < 0 {
			continue
		}
		parts := strings.SplitN(rawLine[1:closingBracket], ":", 2)
		if len(parts) != 2 {
			continue
		}
		minutes, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return nil, fmt.Errorf("timestamp inválido %q", rawLine)
		}
		seconds, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return nil, fmt.Errorf("timestamp inválido %q", rawLine)
		}

		start := minutes*60 + seconds
		if len(lines) > 0 {
			lines[len(lines)-1].End = start
		}
		lines = append(lines, domain.LyricLine{Text: strings.TrimSpace(rawLine[closingBracket+1:]), Start: start})
		previousStart = start
	}

	if len(lines) == 0 {
		return nil, errors.New("no se encontraron timestamps LRC")
	}
	lines[len(lines)-1].End = trackDuration
	if lines[len(lines)-1].End <= lines[len(lines)-1].Start {
		lines[len(lines)-1].End = previousStart
	}
	return lines, nil
}
