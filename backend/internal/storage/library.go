package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"lyrics/internal/domain"
)

var ErrConflict = domain.ErrConflict
var ErrNotFound = domain.ErrRecordNotFound

func (store *Store) CreateUser(ctx context.Context, username, email, passwordHash string) (domain.User, error) {
	result, err := store.db.ExecContext(ctx, `INSERT INTO users (username, email, password_hash) VALUES (?, ?, ?)`, username, email, passwordHash)
	if err != nil {
		return domain.User{}, ErrConflict
	}
	userID, err := result.LastInsertId()
	if err != nil {
		return domain.User{}, err
	}
	if err := store.createSystemPlaylists(ctx, userID); err != nil {
		return domain.User{}, err
	}
	return domain.User{ID: userID, Username: username, Email: email}, nil
}

func (store *Store) FindUserByLogin(ctx context.Context, login string) (domain.UserRecord, error) {
	var user domain.UserRecord
	err := store.db.QueryRowContext(ctx, `SELECT id, username, email, password_hash FROM users WHERE username = ? OR email = ?`, login, login).
		Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.UserRecord{}, ErrNotFound
	}
	return user, err
}

func (store *Store) CreateSession(ctx context.Context, tokenHash string, userID int64) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id) VALUES (?, ?)`, tokenHash, userID)
	return err
}

func (store *Store) UserBySession(ctx context.Context, tokenHash string) (domain.User, error) {
	var user domain.User
	err := store.db.QueryRowContext(ctx, `SELECT u.id, u.username, u.email FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = ?`, tokenHash).
		Scan(&user.ID, &user.Username, &user.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, ErrNotFound
	}
	return user, err
}

func (store *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := store.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (store *Store) createSystemPlaylists(ctx context.Context, userID int64) error {
	for _, name := range []string{"Me gusta", "Guardadas"} {
		if _, err := store.db.ExecContext(ctx, `INSERT INTO playlists (user_id, name, kind) VALUES (?, ?, 'system')`, userID, name); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) UpsertTrack(ctx context.Context, input domain.TrackInput) (int64, error) {
	lyricsJSON := []byte("[]")
	if input.SyncedLyrics != nil {
		encoded, err := json.Marshal(input.SyncedLyrics)
		if err != nil {
			return 0, err
		}
		lyricsJSON = encoded
	}
	_, err := store.db.ExecContext(ctx, `
		INSERT INTO tracks (provider_id, artist, title, album, duration, youtube_video_id, plain_lyrics, synced_lyrics_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(artist, title) DO UPDATE SET provider_id = excluded.provider_id, album = excluded.album, duration = excluded.duration,
			youtube_video_id = CASE WHEN excluded.youtube_video_id <> '' THEN excluded.youtube_video_id ELSE tracks.youtube_video_id END,
			plain_lyrics = CASE WHEN excluded.plain_lyrics <> '' THEN excluded.plain_lyrics ELSE tracks.plain_lyrics END,
			synced_lyrics_json = CASE WHEN excluded.synced_lyrics_json <> '[]' THEN excluded.synced_lyrics_json ELSE tracks.synced_lyrics_json END,
			updated_at = CURRENT_TIMESTAMP`, input.ProviderID, input.Artist, input.Title, input.Album, input.Duration, input.YouTubeVideoID, input.PlainLyrics, lyricsJSON)
	if err != nil {
		return 0, err
	}
	var trackID int64
	err = store.db.QueryRowContext(ctx, `SELECT id FROM tracks WHERE artist = ? AND title = ?`, input.Artist, input.Title).Scan(&trackID)
	return trackID, err
}

func (store *Store) SaveTrack(ctx context.Context, userID, trackID int64, save bool) error {
	if save {
		_, err := store.db.ExecContext(ctx, `INSERT OR IGNORE INTO saved_tracks (user_id, track_id) VALUES (?, ?)`, userID, trackID)
		return err
	}
	_, err := store.db.ExecContext(ctx, `DELETE FROM saved_tracks WHERE user_id = ? AND track_id = ?`, userID, trackID)
	return err
}

func (store *Store) SetReaction(ctx context.Context, userID, trackID int64, reaction int) error {
	if reaction == 0 {
		_, err := store.db.ExecContext(ctx, `DELETE FROM track_reactions WHERE user_id = ? AND track_id = ?`, userID, trackID)
		return err
	}
	if reaction != 1 && reaction != -1 {
		return errors.New("reacción inválida")
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO track_reactions (user_id, track_id, reaction) VALUES (?, ?, ?) ON CONFLICT(user_id, track_id) DO UPDATE SET reaction = excluded.reaction, updated_at = CURRENT_TIMESTAMP`, userID, trackID, reaction)
	return err
}

func (store *Store) CreatePlaylist(ctx context.Context, userID int64, name string) (domain.Playlist, error) {
	result, err := store.db.ExecContext(ctx, `INSERT INTO playlists (user_id, name) VALUES (?, ?)`, userID, name)
	if err != nil {
		return domain.Playlist{}, ErrConflict
	}
	id, err := result.LastInsertId()
	return domain.Playlist{ID: id, Name: name, Kind: "custom"}, err
}

func (store *Store) ListPlaylists(ctx context.Context, userID int64) ([]domain.Playlist, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id, name, kind FROM playlists WHERE user_id = ? ORDER BY kind DESC, name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var playlists []domain.Playlist
	for rows.Next() {
		var playlist domain.Playlist
		if err := rows.Scan(&playlist.ID, &playlist.Name, &playlist.Kind); err != nil {
			return nil, err
		}
		playlists = append(playlists, playlist)
	}
	return playlists, rows.Err()
}

func (store *Store) AddToPlaylist(ctx context.Context, userID, playlistID, trackID int64) error {
	result, err := store.db.ExecContext(ctx, `INSERT OR IGNORE INTO playlist_tracks (playlist_id, track_id) SELECT id, ? FROM playlists WHERE id = ? AND user_id = ?`, trackID, playlistID, userID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return ErrNotFound
	}
	return err
}

func (store *Store) ListLibraryTracks(ctx context.Context, userID int64, collection string) ([]domain.LibraryTrack, error) {
	query := `SELECT t.id, t.artist, t.title, t.album, t.duration, t.youtube_video_id FROM tracks t JOIN saved_tracks s ON s.track_id = t.id WHERE s.user_id = ?`
	args := []any{userID}
	if collection == "liked" || collection == "disliked" {
		query = `SELECT t.id, t.artist, t.title, t.album, t.duration, t.youtube_video_id FROM tracks t JOIN track_reactions r ON r.track_id = t.id WHERE r.user_id = ? AND r.reaction = ?`
		reaction := 1
		if collection == "disliked" {
			reaction = -1
		}
		args = append(args, reaction)
	}
	rows, err := store.db.QueryContext(ctx, query+` ORDER BY t.title`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tracks []domain.LibraryTrack
	for rows.Next() {
		var track domain.LibraryTrack
		if err := rows.Scan(&track.ID, &track.Artist, &track.Title, &track.Album, &track.Duration, &track.YouTubeVideoID); err != nil {
			return nil, err
		}
		tracks = append(tracks, track)
	}
	return tracks, rows.Err()
}
