package main

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"fmt"
	"lyrics/internal/storage"
	"strings"
)

func TestNewServerBuildsConfiguredServer(t *testing.T) {
	t.Setenv("LYRICS_HTTP_ADDR", "")
	t.Setenv("PORT", "")
	server := newServer()

	// Escucha en todas las interfaces, no solo en loopback: el teléfono entra al
	// mismo servidor que la compu. El test pedía ":8080" desde antes de ese cambio
	// y venía fallando sin que nadie lo mirara.
	if server.Addr != "0.0.0.0:8080" {
		t.Fatalf("server address = %q, want %q", server.Addr, "0.0.0.0:8080")
	}
	if server.Handler == nil {
		t.Fatal("server handler is nil")
	}
}

func TestNewServerReadsHTTPAddressFromEnvironment(t *testing.T) {
	t.Setenv("LYRICS_HTTP_ADDR", "127.0.0.1:8181")
	server := newServer()
	if server.Addr != "127.0.0.1:8181" {
		t.Fatalf("server address = %q, want %q", server.Addr, "127.0.0.1:8181")
	}
}

func TestNewServerUsesPlatformPortWhenAddressIsUnset(t *testing.T) {
	t.Setenv("LYRICS_HTTP_ADDR", "")
	t.Setenv("PORT", "10000")
	server := newServer()
	if server.Addr != "0.0.0.0:10000" {
		t.Fatalf("server address = %q, want %q", server.Addr, "0.0.0.0:10000")
	}
}

func TestHealthEndpoint(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/healthz", nil)
	newServer().Handler.ServeHTTP(response, request)
	if response.Code != 204 {
		t.Fatalf("health status = %d, want 204", response.Code)
	}
}

func TestOpenPersistenceDefaultsToSQLite(t *testing.T) {
	t.Setenv("LYRICS_STORAGE_DRIVER", "")
	t.Setenv("LYRICS_DB_PATH", filepath.Join(t.TempDir(), "lyrics.db"))
	persistence, closeStore, err := openPersistence(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer closeStore()
	if _, ok := persistence.(*storage.Store); !ok {
		t.Fatalf("persistence type = %T, want SQLite storage.Store", persistence)
	}
}

func TestOpenPersistenceRequiresPostgresURL(t *testing.T) {
	t.Setenv("LYRICS_STORAGE_DRIVER", "postgres")
	t.Setenv("LYRICS_DATABASE_URL", "")
	t.Setenv("DATABASE_URL", "")
	if _, _, err := openPersistence(context.Background()); err == nil {
		t.Fatal("expected missing PostgreSQL URL error")
	}
}

func TestPersistenceLogAttrsRedactsPostgresCredentials(t *testing.T) {
	t.Setenv("LYRICS_STORAGE_DRIVER", "postgres")
	t.Setenv("LYRICS_DATABASE_URL", "postgres://lyrics_user:super-secret@db.example.test:5432/lyrics?sslmode=require")
	t.Setenv("DATABASE_URL", "")

	attributes := fmt.Sprint(persistenceLogAttrs())
	for _, expected := range []string{"postgres", "db.example.test", "5432", "lyrics", "true"} {
		if !strings.Contains(attributes, expected) {
			t.Errorf("log attributes %q do not include %q", attributes, expected)
		}
	}
	if strings.Contains(attributes, "super-secret") || strings.Contains(attributes, "lyrics_user") {
		t.Fatalf("PostgreSQL credentials leaked in startup attributes: %q", attributes)
	}
}

func TestLoadEnvironmentFilePreservesProcessVariables(t *testing.T) {
	const fileKey = "LYRICS_DOTENV_TEST_FILE_VALUE"
	const processKey = "LYRICS_DOTENV_TEST_PROCESS_VALUE"

	previousFileValue, hadFileValue := os.LookupEnv(fileKey)
	if err := os.Unsetenv(fileKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadFileValue {
			_ = os.Setenv(fileKey, previousFileValue)
		} else {
			_ = os.Unsetenv(fileKey)
		}
	})
	t.Setenv(processKey, "from-process")

	environmentFile := filepath.Join(t.TempDir(), ".env")
	contents := fileKey + "='from-file'\n" + processKey + "=from-file\n"
	if err := os.WriteFile(environmentFile, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadEnvironmentFile(environmentFile); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(fileKey); got != "from-file" {
		t.Errorf("file variable = %q, want from-file", got)
	}
	if got := os.Getenv(processKey); got != "from-process" {
		t.Errorf("process variable = %q, want from-process", got)
	}
}
