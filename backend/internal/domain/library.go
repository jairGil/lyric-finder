package domain

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

type UserRecord struct {
	User
	PasswordHash string
}

type TrackInput struct {
	ProviderID     int
	Artist         string
	Title          string
	Album          string
	Duration       float64
	YouTubeVideoID string
	PlainLyrics    string
	SyncedLyrics   any
}

type LibraryTrack struct {
	ID             int64   `json:"id"`
	Artist         string  `json:"artist"`
	Title          string  `json:"title"`
	Album          string  `json:"album,omitempty"`
	Duration       float64 `json:"duration,omitempty"`
	YouTubeVideoID string  `json:"youtubeVideoId,omitempty"`
}

type Playlist struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}
