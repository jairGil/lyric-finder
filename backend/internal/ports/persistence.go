package ports

import (
	"context"

	"lyrics/internal/domain"
)

type CacheRepository interface {
	CacheGet(ctx context.Context, namespace, key string, destination any) (bool, error)
	CacheSet(ctx context.Context, namespace, key string, value any) error
}

type AuthRepository interface {
	CreateUser(ctx context.Context, username, email, passwordHash string) (domain.User, error)
	FindUserByLogin(ctx context.Context, login string) (domain.UserRecord, error)
	CreateSession(ctx context.Context, tokenHash string, userID int64) error
	UserBySession(ctx context.Context, tokenHash string) (domain.User, error)
	DeleteSession(ctx context.Context, tokenHash string) error
}

type LibraryRepository interface {
	UpsertTrack(ctx context.Context, input domain.TrackInput) (int64, error)
	SaveTrack(ctx context.Context, userID, trackID int64, save bool) error
	SetReaction(ctx context.Context, userID, trackID int64, reaction int) error
	CreatePlaylist(ctx context.Context, userID int64, name string) (domain.Playlist, error)
	ListPlaylists(ctx context.Context, userID int64) ([]domain.Playlist, error)
	AddToPlaylist(ctx context.Context, userID, playlistID, trackID int64) error
	ListLibraryTracks(ctx context.Context, userID int64, collection string) ([]domain.LibraryTrack, error)
}

type RenderRepository interface {
	CreateRenderJob(ctx context.Context, id string, userID int64, artist, title, mediaName string, spec domain.RenderSpecRecord) error
	MarkRenderRunning(ctx context.Context, id string) error
	MarkRenderProgress(ctx context.Context, id string, progress float64)
	MarkRenderDone(ctx context.Context, id, outputName string) error
	MarkRenderFailed(ctx context.Context, id, message string) error
	GetRenderJob(ctx context.Context, id string) (domain.RenderJob, domain.RenderSpecRecord, error)
	ResetStaleRenderJobs(ctx context.Context) (int64, error)
}

type Persistence interface {
	CacheRepository
	AuthRepository
	LibraryRepository
	RenderRepository
}
