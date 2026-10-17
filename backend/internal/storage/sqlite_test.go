package storage

import (
	"bytes"
	"context"
	"log"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCreatesSchemaAndCacheRoundTrip(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "lyrics.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	if err := store.CacheSet(context.Background(), "test", "key", map[string]string{"value": "cached"}); err != nil {
		t.Fatalf("CacheSet() error = %v", err)
	}

	var value map[string]string
	hit, err := store.CacheGet(context.Background(), "test", "key", &value)
	if err != nil || !hit || value["value"] != "cached" {
		t.Fatalf("CacheGet() = (%t, %v, %#v), want hit with cached value", hit, err, value)
	}

	rows, err := store.db.Query(`PRAGMA table_info(api_cache)`)
	if err != nil {
		t.Fatalf("inspect api_cache schema: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan api_cache schema: %v", err)
		}
		if name == "expires_at" {
			t.Fatal("api_cache must not contain expires_at")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate api_cache schema: %v", err)
	}
}

func TestCacheEntriesDoNotExpire(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "lyrics.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	if err := store.CacheSet(context.Background(), "test", "persistent", "value"); err != nil {
		t.Fatalf("CacheSet() error = %v", err)
	}

	var value string
	hit, err := store.CacheGet(context.Background(), "test", "persistent", &value)
	if err != nil || !hit || value != "value" {
		t.Fatalf("CacheGet() = (%t, %v, %q), want persistent value", hit, err, value)
	}
}

func TestCacheLogsHitMissAndStoreWithoutRawKey(t *testing.T) {
	var output bytes.Buffer
	store, err := Open(filepath.Join(t.TempDir(), "lyrics.db"), log.New(&output, "", 0))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	key := "Artist\x00Track"
	var value string
	if hit, err := store.CacheGet(context.Background(), "test", key, &value); err != nil || hit {
		t.Fatalf("initial CacheGet() = (%t, %v), want miss", hit, err)
	}
	if err := store.CacheSet(context.Background(), "test", key, "value"); err != nil {
		t.Fatalf("CacheSet() error = %v", err)
	}
	if hit, err := store.CacheGet(context.Background(), "test", key, &value); err != nil || !hit {
		t.Fatalf("cached CacheGet() = (%t, %v), want hit", hit, err)
	}

	logs := output.String()
	for _, expected := range []string{"cache=miss", "cache=store", "cache=hit", "namespace=test"} {
		if !strings.Contains(logs, expected) {
			t.Errorf("logs = %q, missing %q", logs, expected)
		}
	}
	if strings.Contains(logs, key) {
		t.Fatalf("logs contain raw cache key: %q", logs)
	}
}
