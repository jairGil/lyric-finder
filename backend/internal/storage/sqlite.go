package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Store struct {
	db     *sql.DB
	logger *log.Logger
}

func Open(path string, loggers ...*log.Logger) (*Store, error) {
	if directory := filepath.Dir(path); directory != "." {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, err
		}
	}

	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	logger := log.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	store := &Store{db: database, logger: logger}
	if _, err := database.Exec(`PRAGMA busy_timeout = 5000; PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;`); err != nil {
		database.Close()
		return nil, err
	}
	if err := store.migrate(); err != nil {
		database.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) Close() error {
	return store.db.Close()
}

func (store *Store) CacheGet(ctx context.Context, namespace, key string, destination any) (bool, error) {
	var payload []byte
	err := store.db.QueryRowContext(ctx, `
		SELECT payload
		FROM api_cache
		WHERE namespace = ? AND cache_key = ?`, namespace, key).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		store.logger.Printf("cache=miss namespace=%s key_hash=%s", namespace, keyHash(key))
		return false, nil
	}
	if err != nil {
		store.logger.Printf("cache=error operation=get namespace=%s key_hash=%s error=%q", namespace, keyHash(key), err)
		return false, err
	}
	if err := json.Unmarshal(payload, destination); err != nil {
		store.logger.Printf("cache=error operation=decode namespace=%s key_hash=%s error=%q", namespace, keyHash(key), err)
		return false, err
	}
	store.logger.Printf("cache=hit namespace=%s key_hash=%s", namespace, keyHash(key))
	return true, nil
}

func (store *Store) CacheSet(ctx context.Context, namespace, key string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		store.logger.Printf("cache=error operation=encode namespace=%s key_hash=%s error=%q", namespace, keyHash(key), err)
		return err
	}
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO api_cache (namespace, cache_key, payload, created_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(namespace, cache_key) DO UPDATE SET
			payload = excluded.payload,
			created_at = excluded.created_at`, namespace, key, payload)
	if err != nil {
		store.logger.Printf("cache=error operation=set namespace=%s key_hash=%s error=%q", namespace, keyHash(key), err)
		return err
	}
	store.logger.Printf("cache=store namespace=%s key_hash=%s bytes=%d", namespace, keyHash(key), len(payload))
	return nil
}

func keyHash(key string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(key)))[:12]
}

func (store *Store) migrate() error {
	_, err := store.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS api_cache (
			namespace TEXT NOT NULL,
			cache_key TEXT NOT NULL,
			payload BLOB NOT NULL,
			created_at DATETIME NOT NULL,
			PRIMARY KEY (namespace, cache_key)
		);

		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS sessions (
			token_hash TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS tracks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			provider_id INTEGER,
			artist TEXT NOT NULL,
			title TEXT NOT NULL,
			album TEXT NOT NULL DEFAULT '',
			duration REAL NOT NULL DEFAULT 0,
			youtube_video_id TEXT NOT NULL DEFAULT '',
			plain_lyrics TEXT NOT NULL DEFAULT '',
			synced_lyrics_json BLOB NOT NULL DEFAULT '[]',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (artist, title)
		);

		CREATE TABLE IF NOT EXISTS playlists (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT 'custom' CHECK (kind IN ('custom', 'system')),
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (user_id, name)
		);

		CREATE TABLE IF NOT EXISTS playlist_tracks (
			playlist_id INTEGER NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
			track_id INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
			position INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (playlist_id, track_id)
		);

		CREATE TABLE IF NOT EXISTS saved_tracks (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			track_id INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, track_id)
		);

		CREATE TABLE IF NOT EXISTS track_reactions (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			track_id INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
			reaction INTEGER NOT NULL CHECK (reaction IN (-1, 1)),
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, track_id)
		);

		CREATE TABLE IF NOT EXISTS render_jobs (
			id TEXT PRIMARY KEY,
			user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
			artist TEXT NOT NULL,
			title TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'done', 'failed')),
			progress REAL NOT NULL DEFAULT 0,
			media_name TEXT NOT NULL DEFAULT '',
			output_name TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			spec_json BLOB NOT NULL DEFAULT '{}',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`)
	return err
}
