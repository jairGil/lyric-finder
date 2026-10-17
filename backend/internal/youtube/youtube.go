package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"unicode"
)

const searchURL = "https://www.googleapis.com/youtube/v3/search"

const (
	maxResults = 10
	idealScore = 1.0
)

type Provider struct {
	client  *http.Client
	apiKey  string
	baseURL string
	mu      sync.RWMutex
	cache   map[string]string
}

type searchItem struct {
	ID struct {
		VideoID string `json:"videoId"`
	} `json:"id"`
	Snippet struct {
		Title        string `json:"title"`
		ChannelTitle string `json:"channelTitle"`
	} `json:"snippet"`
}

type searchResponse struct {
	Items []searchItem `json:"items"`
}

func New(client *http.Client, apiKey string) *Provider {
	if client == nil {
		client = http.DefaultClient
	}
	return &Provider{client: client, apiKey: apiKey, baseURL: searchURL, cache: make(map[string]string)}
}

func (provider *Provider) FindVideo(ctx context.Context, artist, track string) (string, error) {
	if provider.apiKey == "" {
		return "", nil
	}

	cacheKey := strings.ToLower(strings.TrimSpace(artist) + "\x00" + strings.TrimSpace(track))
	provider.mu.RLock()
	videoID, cached := provider.cache[cacheKey]
	provider.mu.RUnlock()
	if cached {
		return videoID, nil
	}

	queries := []string{
		fmt.Sprintf("%s %s", artist, track),
		fmt.Sprintf("%s %s official audio", artist, track),
		fmt.Sprintf("%s %s audio oficial", artist, track),
	}

	var (
		bestVideoID string
		bestScore   = -1.0
	)
	for _, query := range queries {
		items, err := provider.search(ctx, query)
		if err != nil {
			return "", err
		}

		videoID, score := pickBest(items, artist, track)
		if score > bestScore {
			bestVideoID, bestScore = videoID, score
		}
		if bestScore >= idealScore {
			break
		}
	}

	if bestVideoID == "" {
		return "", nil
	}

	provider.mu.Lock()
	provider.cache[cacheKey] = bestVideoID
	provider.mu.Unlock()
	return bestVideoID, nil
}

func (provider *Provider) search(ctx context.Context, query string) ([]searchItem, error) {
	params := url.Values{}
	params.Set("part", "snippet")
	params.Set("type", "video")
	params.Set("maxResults", fmt.Sprintf("%d", maxResults))
	params.Set("q", query)
	params.Set("key", provider.apiKey)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.baseURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}

	response, err := provider.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 256))
		return nil, fmt.Errorf("YouTube respondió con HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var result searchResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Items, nil
}

func pickBest(items []searchItem, artist, track string) (string, float64) {
	var (
		bestVideoID string
		bestScore   = -1.0
		firstVideo  string
	)
	for _, item := range items {
		if item.ID.VideoID == "" {
			continue
		}
		if firstVideo == "" {
			firstVideo = item.ID.VideoID
		}
		if score := scoreVideo(item, artist, track); score > bestScore {
			bestVideoID, bestScore = item.ID.VideoID, score
		}
	}

	if bestVideoID == "" {
		return "", -1
	}
	if bestScore < 0.4 && firstVideo != "" {
		return firstVideo, bestScore
	}
	return bestVideoID, bestScore
}

func scoreVideo(item searchItem, artist, track string) float64 {
	title := normalize(item.Snippet.Title)
	channel := normalize(item.Snippet.ChannelTitle)

	trackTokens := tokenize(track)
	artistTokens := tokenize(artist)
	if len(trackTokens) == 0 {
		return 0
	}

	trackHits := 0
	for _, token := range trackTokens {
		if hasToken(title, token) {
			trackHits++
		}
	}
	artistHits := 0
	for _, token := range artistTokens {
		if hasToken(title, token) {
			artistHits++
		}
	}

	score := 0.6 * float64(trackHits) / float64(len(trackTokens))
	if len(artistTokens) > 0 {
		score += 0.2 * float64(artistHits) / float64(len(artistTokens))
	}
	if phrase := normalize(track); phrase != "" && hasToken(title, phrase) {
		score += 0.1
	}
	if phrase := normalize(artist); phrase != "" && hasToken(title, phrase) {
		score += 0.1
	}
	if strings.Contains(title, "official") || strings.Contains(title, "oficial") {
		score += 0.1
	}
	for _, word := range []string{"cover", "karaoke", "instrumental", "remix", "tribute", "mashup", "acapella"} {
		if hasToken(title, word) {
			score -= 0.4
		}
	}
	for _, token := range artistTokens {
		if hasToken(channel, token) {
			score += 0.1
			break
		}
	}

	return score
}

func tokenize(value string) []string {
	return strings.Fields(normalize(value))
}

var accentReplacer = strings.NewReplacer(
	"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u",
	"ü", "u", "ñ", "n",
	"Á", "a", "É", "e", "Í", "i", "Ó", "o", "Ú", "u",
	"Ü", "u", "Ñ", "n",
)

func normalize(value string) string {
	value = strings.ToLower(accentReplacer.Replace(value))
	var builder strings.Builder
	lastWasSpace := true
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			lastWasSpace = false
			continue
		}
		if !lastWasSpace {
			builder.WriteByte(' ')
			lastWasSpace = true
		}
	}
	return strings.TrimSpace(builder.String())
}

func hasToken(text, token string) bool {
	if token == "" {
		return false
	}
	for rest := 0; ; {
		index := strings.Index(text[rest:], token)
		if index < 0 {
			return false
		}
		position := rest + index
		beforeOK := position == 0 || !isWordChar(text[position-1])
		after := position + len(token)
		afterOK := after >= len(text) || !isWordChar(text[after])
		if beforeOK && afterOK {
			return true
		}
		rest = position + len(token)
		if rest >= len(text) {
			return false
		}
	}
}

func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}
