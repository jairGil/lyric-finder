package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"lyrics/internal/domain"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestMapErrorMapsUniqueConstraint(t *testing.T) {
	got := mapError(&pgconn.PgError{Code: "23505", ConstraintName: "users_email_key"})
	if !errors.Is(got, domain.ErrConflict) {
		t.Fatalf("mapError() = %v, want ErrConflict", got)
	}
}

func TestPersistenceIntegration(t *testing.T) {
	databaseURL := os.Getenv("LYRICS_TEST_POSTGRES_DSN")
	if databaseURL == "" {
		t.Skip("set LYRICS_TEST_POSTGRES_DSN to run PostgreSQL integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	user, err := store.CreateUser(ctx, "user-"+suffix, "user-"+suffix+"@example.test", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindUserByLogin(ctx, user.Email); err != nil {
		t.Fatalf("FindUserByLogin: %v", err)
	}
	if err := store.CreateSession(ctx, "token-"+suffix, user.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := store.UserBySession(ctx, "token-"+suffix); err != nil || got.ID != user.ID {
		t.Fatalf("UserBySession = (%+v, %v), want user %d", got, err, user.ID)
	}

	trackID, err := store.UpsertTrack(ctx, domain.TrackInput{
		Artist: "Artist " + suffix,
		Title:  "Song " + suffix,
		SyncedLyrics: []domain.LyricLine{
			{Text: "line", Start: 0, End: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTrack(ctx, user.ID, trackID, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetReaction(ctx, user.ID, trackID, 1); err != nil {
		t.Fatal(err)
	}
	tracks, err := store.ListLibraryTracks(ctx, user.ID, "liked")
	if err != nil || len(tracks) != 1 || tracks[0].ID != trackID {
		t.Fatalf("ListLibraryTracks = (%+v, %v), want saved track %d", tracks, err, trackID)
	}

	playlist, err := store.CreatePlaylist(ctx, user.ID, "Playlist "+suffix)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddToPlaylist(ctx, user.ID, playlist.ID, trackID); err != nil {
		t.Fatal(err)
	}
	if err := store.AddToPlaylist(ctx, user.ID, playlist.ID, trackID); err != nil {
		t.Fatalf("adding duplicate track should be idempotent: %v", err)
	}

	var cached struct {
		Value string `json:"value"`
	}
	cacheKey := "artist\x00track-" + suffix
	if err := store.CacheSet(ctx, "integration", cacheKey, map[string]string{"value": "cached"}); err != nil {
		t.Fatal(err)
	}
	if hit, err := store.CacheGet(ctx, "integration", cacheKey, &cached); err != nil || !hit || cached.Value != "cached" {
		t.Fatalf("CacheGet = (%t, %+v, %v), want cached value", hit, cached, err)
	}

	renderID := "job-" + suffix
	if err := store.CreateRenderJob(ctx, renderID, user.ID, "Artist", "Song", "audio.mp3", domain.RenderSpecRecord{
		Background: "color",
		Style:      domain.Style{AlignHorizontal: "center", AlignVertical: "bottom"},
		Width:      1080,
		Height:     1920,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkRenderRunning(ctx, renderID); err != nil {
		t.Fatal(err)
	}
	store.MarkRenderProgress(ctx, renderID, 0.5)
	if err := store.MarkRenderDone(ctx, renderID, renderID+".mp4"); err != nil {
		t.Fatal(err)
	}
	job, spec, err := store.GetRenderJob(ctx, renderID)
	if err != nil || job.Status != domain.RenderDone || spec.Width != 1080 || spec.Style.AlignVertical != "bottom" {
		t.Fatalf("GetRenderJob = (%+v, %+v, %v), want completed job/spec", job, spec, err)
	}
}
