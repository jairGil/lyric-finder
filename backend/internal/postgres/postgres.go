package postgres

import (
	"context"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lyrics/internal/domain"
	"lyrics/internal/ports"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	db     *sql.DB
	logger *log.Logger
}

var _ ports.Persistence = (*Store)(nil)

func Open(ctx context.Context, databaseURL string, loggers ...*log.Logger) (*Store, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("postgres: falta la URL de conexión")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: abrir conexión: %w", err)
	}
	database.SetMaxOpenConns(10)
	database.SetMaxIdleConns(5)
	database.SetConnMaxLifetime(30 * time.Minute)

	logger := log.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	store := &Store{db: database, logger: logger}
	if err := database.PingContext(ctx); err != nil {
		database.Close()
		return nil, fmt.Errorf("postgres: conectar: %w", err)
	}
	if err := store.migrate(ctx); err != nil {
		database.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) Close() error {
	return store.db.Close()
}

func (store *Store) migrate(ctx context.Context) error {
	if _, err := store.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		return fmt.Errorf("postgres: crear tabla de migraciones: %w", err)
	}

	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("postgres: listar migraciones: %w", err)
	}
	for _, filename := range files {
		version, err := migrationVersion(filename)
		if err != nil {
			return err
		}
		var applied bool
		if err := store.db.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version,
		).Scan(&applied); err != nil {
			return fmt.Errorf("postgres: consultar migración %d: %w", version, err)
		}
		if applied {
			continue
		}

		script, err := migrations.ReadFile(filename)
		if err != nil {
			return fmt.Errorf("postgres: leer migración %s: %w", filename, err)
		}
		transaction, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("postgres: iniciar migración %d: %w", version, err)
		}
		if _, err = transaction.ExecContext(ctx, string(script)); err == nil {
			_, err = transaction.ExecContext(ctx,
				`INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT (version) DO NOTHING`, version,
			)
		}
		if err != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("postgres: aplicar migración %d: %w", version, err)
		}
		if err := transaction.Commit(); err != nil {
			return fmt.Errorf("postgres: confirmar migración %d: %w", version, err)
		}
	}
	return nil
}

func migrationVersion(filename string) (int, error) {
	base := filepath.Base(filename)
	prefix, _, ok := strings.Cut(base, "_")
	if !ok {
		return 0, fmt.Errorf("postgres: nombre de migración inválido %q", base)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil || version < 1 {
		return 0, fmt.Errorf("postgres: versión de migración inválida %q", base)
	}
	return version, nil
}

func (store *Store) CacheGet(ctx context.Context, namespace, key string, destination any) (bool, error) {
	var payload []byte
	encodedKey := encodeCacheKey(key)
	err := store.db.QueryRowContext(ctx,
		`SELECT payload FROM api_cache WHERE namespace = $1 AND cache_key = $2`, namespace, encodedKey,
	).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		store.logger.Printf("cache=miss namespace=%s", namespace)
		return false, nil
	}
	if err != nil {
		store.logger.Printf("cache=error operation=get namespace=%s error=%q", namespace, err)
		return false, err
	}
	if err := json.Unmarshal(payload, destination); err != nil {
		store.logger.Printf("cache=error operation=decode namespace=%s error=%q", namespace, err)
		return false, err
	}
	store.logger.Printf("cache=hit namespace=%s", namespace)
	return true, nil
}

func (store *Store) CacheSet(ctx context.Context, namespace, key string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	encodedKey := encodeCacheKey(key)
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO api_cache (namespace, cache_key, payload, created_at)
		VALUES ($1, $2, $3, CURRENT_TIMESTAMP)
		ON CONFLICT (namespace, cache_key) DO UPDATE SET
			payload = EXCLUDED.payload,
			created_at = EXCLUDED.created_at`, namespace, encodedKey, payload)
	return err
}

func encodeCacheKey(key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

func (store *Store) CreateUser(ctx context.Context, username, email, passwordHash string) (domain.User, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.User{}, err
	}
	var user domain.User
	err = transaction.QueryRowContext(ctx, `
		INSERT INTO users (username, email, password_hash)
		VALUES ($1, $2, $3)
		RETURNING id, username, email`, username, email, passwordHash,
	).Scan(&user.ID, &user.Username, &user.Email)
	if err != nil {
		_ = transaction.Rollback()
		return domain.User{}, mapError(err)
	}
	for _, name := range []string{"Me gusta", "Guardadas"} {
		if _, err := transaction.ExecContext(ctx,
			`INSERT INTO playlists (user_id, name, kind) VALUES ($1, $2, 'system')`, user.ID, name,
		); err != nil {
			_ = transaction.Rollback()
			return domain.User{}, mapError(err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return domain.User{}, mapError(err)
	}
	return user, nil
}

func (store *Store) FindUserByLogin(ctx context.Context, login string) (domain.UserRecord, error) {
	var user domain.UserRecord
	err := store.db.QueryRowContext(ctx, `
		SELECT id, username, email, password_hash
		FROM users WHERE username = $1 OR email = $1`, login,
	).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.UserRecord{}, domain.ErrRecordNotFound
	}
	return user, mapError(err)
}

func (store *Store) CreateSession(ctx context.Context, tokenHash string, userID int64) error {
	_, err := store.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id) VALUES ($1, $2)`, tokenHash, userID,
	)
	return mapError(err)
}

func (store *Store) UserBySession(ctx context.Context, tokenHash string) (domain.User, error) {
	var user domain.User
	err := store.db.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.email
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1`, tokenHash,
	).Scan(&user.ID, &user.Username, &user.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, domain.ErrRecordNotFound
	}
	return user, mapError(err)
}

func (store *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := store.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
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
	var trackID int64
	err := store.db.QueryRowContext(ctx, `
		INSERT INTO tracks (
			provider_id, artist, title, album, duration, youtube_video_id, plain_lyrics, synced_lyrics_json
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (artist, title) DO UPDATE SET
			provider_id = EXCLUDED.provider_id,
			album = EXCLUDED.album,
			duration = EXCLUDED.duration,
			youtube_video_id = CASE WHEN EXCLUDED.youtube_video_id <> '' THEN EXCLUDED.youtube_video_id ELSE tracks.youtube_video_id END,
			plain_lyrics = CASE WHEN EXCLUDED.plain_lyrics <> '' THEN EXCLUDED.plain_lyrics ELSE tracks.plain_lyrics END,
			synced_lyrics_json = CASE WHEN EXCLUDED.synced_lyrics_json <> '[]'::jsonb THEN EXCLUDED.synced_lyrics_json ELSE tracks.synced_lyrics_json END,
			updated_at = CURRENT_TIMESTAMP
		RETURNING id`,
		input.ProviderID, input.Artist, input.Title, input.Album, input.Duration,
		input.YouTubeVideoID, input.PlainLyrics, lyricsJSON,
	).Scan(&trackID)
	return trackID, mapError(err)
}

func (store *Store) SaveTrack(ctx context.Context, userID, trackID int64, save bool) error {
	if save {
		_, err := store.db.ExecContext(ctx, `
			INSERT INTO saved_tracks (user_id, track_id) VALUES ($1, $2)
			ON CONFLICT (user_id, track_id) DO NOTHING`, userID, trackID)
		return mapError(err)
	}
	_, err := store.db.ExecContext(ctx,
		`DELETE FROM saved_tracks WHERE user_id = $1 AND track_id = $2`, userID, trackID,
	)
	return err
}

func (store *Store) SetReaction(ctx context.Context, userID, trackID int64, reaction int) error {
	if reaction == 0 {
		_, err := store.db.ExecContext(ctx,
			`DELETE FROM track_reactions WHERE user_id = $1 AND track_id = $2`, userID, trackID,
		)
		return err
	}
	if reaction != 1 && reaction != -1 {
		return errors.New("reacción inválida")
	}
	_, err := store.db.ExecContext(ctx, `
		INSERT INTO track_reactions (user_id, track_id, reaction) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, track_id) DO UPDATE SET
			reaction = EXCLUDED.reaction,
			updated_at = CURRENT_TIMESTAMP`, userID, trackID, reaction)
	return mapError(err)
}

func (store *Store) CreatePlaylist(ctx context.Context, userID int64, name string) (domain.Playlist, error) {
	var playlist domain.Playlist
	err := store.db.QueryRowContext(ctx, `
		INSERT INTO playlists (user_id, name)
		VALUES ($1, $2)
		RETURNING id, name, kind`, userID, name,
	).Scan(&playlist.ID, &playlist.Name, &playlist.Kind)
	return playlist, mapError(err)
}

func (store *Store) ListPlaylists(ctx context.Context, userID int64) ([]domain.Playlist, error) {
	rows, err := store.db.QueryContext(ctx, `
		SELECT id, name, kind FROM playlists WHERE user_id = $1 ORDER BY kind DESC, name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	playlists := make([]domain.Playlist, 0)
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
	result, err := store.db.ExecContext(ctx, `
		INSERT INTO playlist_tracks (playlist_id, track_id)
		SELECT id, $2 FROM playlists WHERE id = $1 AND user_id = $3
		ON CONFLICT (playlist_id, track_id) DO NOTHING`, playlistID, trackID, userID)
	if err != nil {
		return mapError(err)
	}
	inserted, err := result.RowsAffected()
	if err != nil || inserted > 0 {
		return err
	}
	var owned bool
	if err := store.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM playlists WHERE id = $1 AND user_id = $2)`, playlistID, userID,
	).Scan(&owned); err != nil {
		return err
	}
	if !owned {
		return domain.ErrRecordNotFound
	}
	return nil
}

func (store *Store) ListLibraryTracks(ctx context.Context, userID int64, collection string) ([]domain.LibraryTrack, error) {
	query := `
		SELECT t.id, t.artist, t.title, t.album, t.duration, t.youtube_video_id
		FROM tracks t JOIN saved_tracks s ON s.track_id = t.id WHERE s.user_id = $1`
	args := []any{userID}
	if collection == "liked" || collection == "disliked" {
		query = `
			SELECT t.id, t.artist, t.title, t.album, t.duration, t.youtube_video_id
			FROM tracks t JOIN track_reactions r ON r.track_id = t.id
			WHERE r.user_id = $1 AND r.reaction = $2`
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

	tracks := make([]domain.LibraryTrack, 0)
	for rows.Next() {
		var track domain.LibraryTrack
		if err := rows.Scan(&track.ID, &track.Artist, &track.Title, &track.Album, &track.Duration, &track.YouTubeVideoID); err != nil {
			return nil, err
		}
		tracks = append(tracks, track)
	}
	return tracks, rows.Err()
}

func (store *Store) CreateRenderJob(ctx context.Context, id string, userID int64, artist, title, mediaName string, spec domain.RenderSpecRecord) error {
	payload, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO render_jobs (id, user_id, artist, title, status, media_name, spec_json)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, nullableUserID(userID), artist, title, string(domain.RenderQueued), mediaName, payload)
	return mapError(err)
}

func (store *Store) MarkRenderRunning(ctx context.Context, id string) error {
	return store.setRenderState(ctx, id, string(domain.RenderRunning), 0, "", "")
}

func (store *Store) MarkRenderProgress(ctx context.Context, id string, progress float64) {
	_, _ = store.db.ExecContext(ctx, `
		UPDATE render_jobs SET progress = $1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2 AND status = $3`, progress, id, string(domain.RenderRunning))
}

func (store *Store) MarkRenderDone(ctx context.Context, id, outputName string) error {
	return store.setRenderState(ctx, id, string(domain.RenderDone), 1, "", outputName)
}

func (store *Store) MarkRenderFailed(ctx context.Context, id, message string) error {
	return store.setRenderState(ctx, id, string(domain.RenderFailed), 0, message, "")
}

func (store *Store) setRenderState(ctx context.Context, id, status string, progress float64, message, outputName string) error {
	_, err := store.db.ExecContext(ctx, `
		UPDATE render_jobs
		SET status = $1, progress = $2, error = $3, output_name = $4, updated_at = CURRENT_TIMESTAMP
		WHERE id = $5`, status, progress, message, outputName, id)
	return err
}

func (store *Store) GetRenderJob(ctx context.Context, id string) (domain.RenderJob, domain.RenderSpecRecord, error) {
	var job domain.RenderJob
	var status string
	var payload []byte
	err := store.db.QueryRowContext(ctx, `
		SELECT id, artist, title, status, progress, media_name, output_name, error, spec_json, created_at, updated_at
		FROM render_jobs WHERE id = $1`, id,
	).Scan(&job.ID, &job.Artist, &job.Title, &status, &job.Progress,
		&job.MediaName, &job.OutputName, &job.Error, &payload, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RenderJob{}, domain.RenderSpecRecord{}, domain.ErrRecordNotFound
	}
	if err != nil {
		return domain.RenderJob{}, domain.RenderSpecRecord{}, err
	}
	job.Status = domain.RenderStatus(status)

	var spec domain.RenderSpecRecord
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &spec); err != nil {
			return domain.RenderJob{}, domain.RenderSpecRecord{}, err
		}
	}
	return job, spec, nil
}

func (store *Store) ResetStaleRenderJobs(ctx context.Context) (int64, error) {
	result, err := store.db.ExecContext(ctx, `
		UPDATE render_jobs
		SET status = $1, error = 'el servidor se reinició durante el render', updated_at = CURRENT_TIMESTAMP
		WHERE status IN ($2, $3)`, string(domain.RenderFailed), string(domain.RenderQueued), string(domain.RenderRunning))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func nullableUserID(userID int64) any {
	if userID <= 0 {
		return nil
	}
	return userID
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" {
		return fmt.Errorf("%w: %s", domain.ErrConflict, pgError.ConstraintName)
	}
	return err
}
