package youtube

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"lyrics/internal/domain"
)

func TestVideoIDAcceptsLinksAndBareIDs(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"watch clasico", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"sin www", "https://youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"mobile", "https://m.youtube.com/watch?v=dQw4w9WgXcQ&t=42s", "dQw4w9WgXcQ"},
		{"music", "https://music.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"short", "https://youtu.be/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"short con query", "https://youtu.be/dQw4w9WgXcQ?si=abc", "dQw4w9WgXcQ"},
		{"shorts", "https://www.youtube.com/shorts/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"embed", "https://www.youtube.com/embed/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"live", "https://www.youtube.com/live/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"id suelto", "dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"id suelto con espacios", "  dQw4w9WgXcQ  ", "dQw4w9WgXcQ"},
		{"http", "http://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
	}
	for _, testCase := range cases {
		got, err := VideoID(testCase.raw)
		if err != nil {
			t.Errorf("%s: err = %v, want nil", testCase.name, err)
			continue
		}
		if got != testCase.want {
			t.Errorf("%s: VideoID(%q) = %q, want %q", testCase.name, testCase.raw, got, testCase.want)
		}
	}
}

// El ID se vuelve nombre de archivo en la caché, así que un link construido a
// mano no puede escribir fuera del directorio. También es lo que evita que este
// endpoint sea un descargador genérico de cualquier host.
func TestVideoIDRejectsAnythingElse(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"vacio", ""},
		{"solo espacios", "   "},
		{"host raro", "https://example.com/video.mp4"},
		{"host que termina en youtube", "https://notyoutube.com/watch?v=dQw4w9WgXcQ"},
		{"youtube sin punto", "https://youtubee.com/watch?v=dQw4w9WgXcQ"},
		{"subdominio de youtube", "https://evil.youtube.com.attacker.net/watch?v=dQw4w9WgXcQ"},
		{"file", "file:///etc/passwd"},
		{"ftp", "ftp://youtube.com/watch?v=dQw4w9WgXcQ"},
		{"javascript", "javascript:alert(1)"},
		{"watch sin v", "https://www.youtube.com/watch"},
		{"watch con v invalido", "https://www.youtube.com/watch?v=corto"},
		{"v con ruta", "https://www.youtube.com/watch?v=../../etc/passwd"},
		{"id demasiado largo", "dQw4w9WgXcQdQw4w9WgXcQ"},
		{"id con simbolos", "dQw4w9WgXc$"},
		{"shorts con id invalido", "https://www.youtube.com/shorts/xx"},
	}
	for _, testCase := range cases {
		if _, err := VideoID(testCase.raw); !errors.Is(err, domain.ErrInvalidSource) {
			t.Errorf("%s: VideoID(%q) err = %v, want ErrInvalidSource", testCase.name, testCase.raw, err)
		}
	}
}

func TestDownloaderAvailableNeedsTheBinary(t *testing.T) {
	tools := t.TempDir()
	if err := NewDownloader(tools, t.TempDir()).Available(); !errors.Is(err, domain.ErrSourceUnavailable) {
		t.Fatalf("sin binario: err = %v, want ErrSourceUnavailable", err)
	}

	installFakeYTDLP(t, tools)
	if err := NewDownloader(tools, t.TempDir()).Available(); err != nil {
		t.Errorf("con binario: err = %v, want nil", err)
	}
}

// El borrador del modal se regenera en cada movimiento del selector de
// fragmento. Si la descarga no se cachea, arrastrar el cursor bajaría el mismo
// video una y otra vez, así que la segunda llamada tiene que devolver la ruta
// sin tocar el binario. El falso no es ejecutable a propósito: si se intentara
// correr, la llamada fallaría.
func TestDownloadAudioServesTheCache(t *testing.T) {
	tools := t.TempDir()
	cache := t.TempDir()
	installFakeYTDLP(t, tools)

	cached := filepath.Join(cache, "dQw4w9WgXcQ.m4a")
	if err := os.WriteFile(cached, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}

	path, err := NewDownloader(tools, cache).DownloadAudio(context.Background(), "https://youtu.be/dQw4w9WgXcQ")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if path != cached {
		t.Errorf("path = %q, want la ruta de la caché %q", path, cached)
	}
}

func TestDownloadAudioRejectsBadLinkBeforeUsingTheBinary(t *testing.T) {
	tools := t.TempDir()
	// Sin binario instalado: si el link se validara después, el error sería
	// ErrSourceUnavailable y no ErrInvalidSource.
	_, err := NewDownloader(tools, t.TempDir()).DownloadAudio(context.Background(), "https://example.com/x")
	if !errors.Is(err, domain.ErrInvalidSource) {
		t.Errorf("err = %v, want ErrInvalidSource", err)
	}
}

func TestDownloadAudioNeedsTheBinaryForACacheMiss(t *testing.T) {
	_, err := NewDownloader(t.TempDir(), t.TempDir()).DownloadAudio(context.Background(), "dQw4w9WgXcQ")
	if !errors.Is(err, domain.ErrSourceUnavailable) {
		t.Errorf("err = %v, want ErrSourceUnavailable", err)
	}
}

func TestDescribeRejectsBadLink(t *testing.T) {
	_, err := NewDownloader(t.TempDir(), t.TempDir()).Describe(context.Background(), "https://example.com/x")
	if !errors.Is(err, domain.ErrInvalidSource) {
		t.Errorf("err = %v, want ErrInvalidSource", err)
	}
}

func TestParseSourceInfoIncludesDimensions(t *testing.T) {
	info, err := parseSourceInfo([]byte(`{"id":"dQw4w9WgXcQ","title":"Video","duration":123,"width":1920,"height":1080}`))
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 1920 || info.Height != 1080 {
		t.Errorf("dimensions = %dx%d, want 1920x1080", info.Width, info.Height)
	}
}

func TestSingleFileRejectsEmptyAndMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := singleFile(dir); !errors.Is(err, domain.ErrSourceUnavailable) {
		t.Errorf("vacío: err = %v, want ErrSourceUnavailable", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "medio.m4a"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := singleFile(dir); err != nil {
		t.Errorf("un archivo: err = %v, want nil", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "otro.m4a"), []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := singleFile(dir); !errors.Is(err, domain.ErrSourceUnavailable) {
		t.Errorf("dos archivos: err = %v, want ErrSourceUnavailable", err)
	}
}

func TestTailWriterKeepsTheEnd(t *testing.T) {
	var data bytes.Buffer
	writer := tailWriter{limit: 4, data: &data}
	if _, err := writer.Write([]byte("abcdefgh")); err != nil {
		t.Fatal(err)
	}
	if data.String() != "efgh" {
		t.Errorf("data = %q, want %q", data.String(), "efgh")
	}
}

func installFakeYTDLP(t *testing.T, toolsDir string) {
	t.Helper()
	dir := filepath.Join(toolsDir, "yt-dlp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ytdlpName), []byte("no ejecutable"), 0o700); err != nil {
		t.Fatal(err)
	}
}
