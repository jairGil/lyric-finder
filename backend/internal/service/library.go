package service

import (
	"context"

	"lyrics/internal/domain"
	"lyrics/internal/ports"
)

type LibraryService struct {
	repository ports.LibraryRepository
}

func NewLibraryService(repository ports.LibraryRepository) *LibraryService {
	return &LibraryService{repository: repository}
}

func (service *LibraryService) ListPlaylists(ctx context.Context, userID int64) ([]domain.Playlist, error) {
	return service.repository.ListPlaylists(ctx, userID)
}

func (service *LibraryService) ListTracks(ctx context.Context, userID int64, collection string) ([]domain.LibraryTrack, error) {
	return service.repository.ListLibraryTracks(ctx, userID, collection)
}

func (service *LibraryService) CreatePlaylist(ctx context.Context, userID int64, name string) (domain.Playlist, error) {
	return service.repository.CreatePlaylist(ctx, userID, name)
}

func (service *LibraryService) RegisterTrack(ctx context.Context, track domain.TrackInput) (int64, error) {
	return service.repository.UpsertTrack(ctx, track)
}

func (service *LibraryService) SaveTrack(ctx context.Context, userID, trackID int64, save bool) error {
	return service.repository.SaveTrack(ctx, userID, trackID, save)
}

func (service *LibraryService) SetReaction(ctx context.Context, userID, trackID int64, reaction int) error {
	return service.repository.SetReaction(ctx, userID, trackID, reaction)
}

func (service *LibraryService) AddToPlaylist(ctx context.Context, userID, playlistID, trackID int64) error {
	return service.repository.AddToPlaylist(ctx, userID, playlistID, trackID)
}
