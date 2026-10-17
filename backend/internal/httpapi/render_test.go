package httpapi

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"lyrics/internal/domain"
	"lyrics/internal/render"
	"lyrics/internal/service"
	"lyrics/internal/storage"
)

func newRenderTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	renderDir := filepath.Join(t.TempDir(), "renders")
	engine := render.NewEngine(
		filepath.Join(root, "tools"),
		filepath.Join(root, "backend", "assets"),
		renderDir,
	)
	if err := engine.Available(); err != nil {
		t.Skipf("ffmpeg no disponible: %v", err)
	}

	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	lines := make([]domain.LyricLine, 0, 24)
	for i := 0; i < 24; i++ {
		start := float64(i) * 0.8
		lines = append(lines, domain.LyricLine{
			Text:  fmt.Sprintf("linea numero %d de prueba", i+1),
			Start: start,
			End:   start + 0.8,
		})
	}

	provider := &fakeLyricsProvider{lyrics: domain.Lyrics{
		Track:        domain.Track{Title: "Prueba", Artist: "Artista", Duration: 19.2},
		SyncedLyrics: lines,
		Synchronized: true,
	}}

	renderService, err := service.NewRenderService(provider, engine, engine, nil, store, renderDir)
	if err != nil {
		t.Fatal(err)
	}

	handler := NewRenderHandler(renderService)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/renders", handler.Create)
	mux.HandleFunc("POST /api/renders/preview", handler.Preview)
	mux.HandleFunc("GET /api/renders/source", handler.Source)
	mux.HandleFunc("GET /api/renders/{id}", handler.Status)
	mux.HandleFunc("GET /api/renders/{id}/file", handler.File)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, engine.Binary()
}

func makeTestMedia(t *testing.T, ffmpegPath, name string, args ...string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), name)
	full := append([]string{"-hide_banner", "-y"}, args...)
	full = append(full, target)
	command := exec.Command(ffmpegPath, full...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("no se pudo crear %s: %v: %s", name, err, output)
	}
	return target
}

func buildRenderBody(t *testing.T, fields map[string]string, files map[string]string) (*bytes.Buffer, string) {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	for field, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		part, err := writer.CreateFormFile(field, filepath.Base(path))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body, writer.FormDataContentType()
}

// postRenderStatus permite comprobar los rechazos (400) sin que el helper
// aborta el test, al contrario que postRender.
func postRenderStatus(t *testing.T, server *httptest.Server, fields map[string]string, files map[string]string) int {
	t.Helper()

	body, contentType := buildRenderBody(t, fields, files)
	response, err := http.Post(server.URL+"/api/renders", contentType, body)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)
	t.Logf("cuerpo de la respuesta: %s", payload)
	return response.StatusCode
}

func postRender(t *testing.T, server *httptest.Server, fields map[string]string, files map[string]string) map[string]any {
	t.Helper()

	body, contentType := buildRenderBody(t, fields, files)

	response, err := http.Post(server.URL+"/api/renders", contentType, body)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	payload, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /api/renders = %d, want 202: %s", response.StatusCode, payload)
	}

	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

// postPreview manda el mismo formulario que el render y devuelve la respuesta
// cruda, porque el preview responde con el MP4 y no con JSON.
func postPreview(t *testing.T, server *httptest.Server, fields map[string]string, files map[string]string) *http.Response {
	t.Helper()

	body, contentType := buildRenderBody(t, fields, files)
	response, err := http.Post(server.URL+"/api/renders/preview", contentType, body)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })

	if response.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("POST /api/renders/preview = %d, want 200: %s", response.StatusCode, payload)
	}
	return response
}

func postPreviewStatus(t *testing.T, server *httptest.Server, fields map[string]string, files map[string]string) int {
	t.Helper()

	body, contentType := buildRenderBody(t, fields, files)
	response, err := http.Post(server.URL+"/api/renders/preview", contentType, body)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)
	t.Logf("cuerpo de la respuesta: %s", payload)
	return response.StatusCode
}

func waitForRender(t *testing.T, server *httptest.Server, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		response, err := http.Get(server.URL + "/api/renders/" + id)
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := io.ReadAll(response.Body)
		response.Body.Close()

		var job map[string]any
		if err := json.Unmarshal(payload, &job); err != nil {
			t.Fatal(err)
		}
		switch job["status"] {
		case "done":
			return job
		case "failed":
			t.Fatalf("el render falló: %v", job["error"])
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("el render no terminó a tiempo")
	return nil
}

func TestRenderEndpointEndToEndWithColorBackground(t *testing.T) {
	server, ffmpegPath := newRenderTestServer(t)
	media := makeTestMedia(t, ffmpegPath, "audio.m4a",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=6", "-c:a", "aac")

	created := postRender(t, server, map[string]string{
		"artist":     "Artista",
		"track":      "Prueba",
		"background": "color",
		"color":      "#101820",
		"highlight":  "word",
		"colors":     "multi",
		"font":       "inter",
		"align":      "center",
		"width":      "640",
		"height":     "360",
		"fps":        "24",
	}, map[string]string{"media": media})

	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no se recibió id de job: %v", created)
	}

	job := waitForRender(t, server, id)
	if progress, _ := job["progress"].(float64); progress != 1 {
		t.Errorf("progress = %v, want 1", job["progress"])
	}

	response, err := http.Get(server.URL + "/api/renders/" + id + "/file")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET file = %d, want 200", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); got != "video/mp4" {
		t.Errorf("Content-Type = %q, want video/mp4", got)
	}
	// El frontend se sirve desde otro puerto, y ahí el atributo download se
	// ignora: solo attachment hace que el navegador guarde el archivo.
	if got := response.Header.Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") {
		t.Errorf("Content-Disposition = %q, want attachment para que la descarga funcione entre origenes", got)
	}
	if got := response.Header.Get("Content-Disposition"); !strings.Contains(got, "Artista - Prueba.mp4") {
		t.Errorf("Content-Disposition = %q, want un nombre con artista y titulo", got)
	}

	downloaded, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(downloaded) < 5<<10 {
		t.Fatalf("mp4 demasiado pequeño: %d bytes", len(downloaded))
	}
	if !bytes.Contains(downloaded[:32], []byte("ftyp")) {
		t.Errorf("el archivo descargado no parece un mp4")
	}
}

func TestRenderEndpointHonorsRequestedFrameRate(t *testing.T) {
	server, ffmpegPath := newRenderTestServer(t)
	media := makeTestMedia(t, ffmpegPath, "audio.m4a",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4", "-c:a", "aac")

	created := postRender(t, server, map[string]string{
		"artist":     "Artista",
		"track":      "Prueba",
		"background": "color",
		"color":      "#101820",
		"width":      "480",
		"height":     "270",
		"fps":        "24",
	}, map[string]string{"media": media})

	id, _ := created["id"].(string)
	waitForRender(t, server, id)

	output := t.TempDir() + "/out.mp4"
	download(t, server, id, output)

	probe := filepath.Join("..", "..", "..", "tools", "ffmpeg", "bin", "ffprobe.exe")
	result, err := exec.Command(probe, "-v", "error", "-select_streams", "v:0", "-count_frames",
		"-show_entries", "stream=r_frame_rate,nb_read_frames", "-of", "csv=p=0", output).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe falló: %v: %s", err, result)
	}

	fields := strings.Split(strings.TrimSpace(string(result)), ",")
	if len(fields) < 2 {
		t.Fatalf("salida inesperada de ffprobe: %q", result)
	}
	if fields[0] != "24/1" {
		t.Errorf("r_frame_rate = %q, want 24/1", fields[0])
	}
	frames, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatal(err)
	}
	if frames > 24*6 {
		t.Errorf("frames = %d, demasiado para 4s a 24fps (el grafo no está limitando el framerate)", frames)
	}
}

// Cubre el flujo que usa el modal de exportacion: fragmento recortado,
// formato vertical y fondo degradado, todo por el contrato multipart.
func TestRenderEndpointEndToEndWithFragmentAndVerticalGradient(t *testing.T) {
	server, ffmpegPath := newRenderTestServer(t)
	media := makeTestMedia(t, ffmpegPath, "audio.m4a",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=30", "-c:a", "aac")

	created := postRender(t, server, map[string]string{
		"artist":     "Artista",
		"track":      "Prueba",
		"background": "gradient",
		"color":      "#4da3ff",
		"colorTwo":   "#f472b6",
		"width":      "1080",
		"height":     "1920",
		"fps":        "30",
		"start":      "8.5",
		"duration":   "6",
	}, map[string]string{"media": media})

	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no se recibio id de job: %v", created)
	}
	waitForRender(t, server, id)

	output := t.TempDir() + "/vertical.mp4"
	download(t, server, id, output)

	probe := filepath.Join("..", "..", "..", "tools", "ffmpeg", "bin", "ffprobe.exe")
	result, err := exec.Command(probe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height:format=duration", "-of", "csv=p=0", output).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe fallo: %v: %s", err, result)
	}
	lines := strings.Split(strings.TrimSpace(string(result)), "\n")
	dimensions := strings.Split(strings.TrimSpace(lines[0]), ",")
	if len(dimensions) < 2 || dimensions[0] != "1080" || dimensions[1] != "1920" {
		t.Errorf("dimensiones = %q, want 1080,1920", lines[0])
	}
	if len(lines) > 1 {
		duration, parseErr := strconv.ParseFloat(strings.TrimSpace(lines[1]), 64)
		if parseErr != nil {
			t.Fatalf("duracion ilegible %q: %v", lines[1], parseErr)
		}
		if duration < 5.5 || duration > 6.5 {
			t.Errorf("duracion = %v, want ~6 (el fragmento pedido, no los 30s del medio)", duration)
		}
	}
}

func TestRenderRejectsFragmentStartingAfterMediaEnds(t *testing.T) {
	server, ffmpegPath := newRenderTestServer(t)
	media := makeTestMedia(t, ffmpegPath, "audio.m4a",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=5", "-c:a", "aac")

	response := postRenderStatus(t, server, map[string]string{
		"artist":   "Artista",
		"track":    "Prueba",
		"start":    "60",
		"duration": "5",
	}, map[string]string{"media": media})

	if response != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 para un inicio fuera del medio", response)
	}
}

func TestRenderRejectsUnknownBackgroundKind(t *testing.T) {
	server, ffmpegPath := newRenderTestServer(t)
	media := makeTestMedia(t, ffmpegPath, "audio.m4a",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4", "-c:a", "aac")

	response := postRenderStatus(t, server, map[string]string{
		"artist":     "Artista",
		"track":      "Prueba",
		"background": "gif",
	}, map[string]string{"media": media})

	if response != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 para un fondo desconocido", response)
	}
}

func download(t *testing.T, server *httptest.Server, id, target string) {
	t.Helper()
	response, err := http.Get(server.URL + "/api/renders/" + id + "/file")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET file = %d, want 200", response.StatusCode)
	}
	file, err := os.Create(target)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := io.Copy(file, response.Body); err != nil {
		t.Fatal(err)
	}
}

func TestRenderEndpointEndToEndWithImageBackground(t *testing.T) {
	server, ffmpegPath := newRenderTestServer(t)
	media := makeTestMedia(t, ffmpegPath, "video.mp4",
		"-f", "lavfi", "-i", "testsrc=size=640x480:rate=24:duration=6",
		"-f", "lavfi", "-i", "sine=frequency=330:duration=6",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest")
	image := makeTestMedia(t, ffmpegPath, "bg.png",
		"-f", "lavfi", "-i", "gradients=s=1280x720:d=1", "-frames:v", "1")

	created := postRender(t, server, map[string]string{
		"artist":     "Artista",
		"track":      "Prueba",
		"background": "image",
		"highlight":  "line",
		"font":       "consolas",
		"align":      "left",
	}, map[string]string{"media": media, "image": image})

	id, _ := created["id"].(string)
	waitForRender(t, server, id)
}

// Un video de fondo entra por el mismo campo `image` que una foto y topa el
// clip a su duración: el borrador dura los 4 s del video, no los 20 pedidos.
func TestRenderPreviewCapsClipToBackgroundVideo(t *testing.T) {
	server, ffmpegPath := newRenderTestServer(t)
	media := makeTestMedia(t, ffmpegPath, "video.mp4",
		"-f", "lavfi", "-i", "testsrc=size=640x480:rate=24:duration=8",
		"-f", "lavfi", "-i", "sine=frequency=330:duration=8",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest")
	background := makeTestMedia(t, ffmpegPath, "bg.mp4",
		"-f", "lavfi", "-i", "smptebars=size=1280x720:rate=24:duration=4",
		"-c:v", "libx264", "-pix_fmt", "yuv420p")

	response := postPreview(t, server, map[string]string{
		"artist":     "Artista",
		"track":      "Prueba",
		"background": "image",
		"highlight":  "line",
		"start":      "0",
		"duration":   "20",
	}, map[string]string{"media": media, "image": background})

	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	duration := probeFormatDuration(t, data)
	if duration > 5 || duration < 3 {
		t.Errorf("duración = %.2fs, want ~4s (la del video de fondo)", duration)
	}
}

// El mismo video de fondo llega bien por la cola y produce un job terminado.
func TestRenderEndpointEndToEndWithVideoBackground(t *testing.T) {
	server, ffmpegPath := newRenderTestServer(t)
	media := makeTestMedia(t, ffmpegPath, "video.mp4",
		"-f", "lavfi", "-i", "testsrc=size=640x480:rate=24:duration=6",
		"-f", "lavfi", "-i", "sine=frequency=330:duration=6",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest")
	background := makeTestMedia(t, ffmpegPath, "bg.mp4",
		"-f", "lavfi", "-i", "smptebars=size=1280x720:rate=24:duration=4",
		"-c:v", "libx264", "-pix_fmt", "yuv420p")

	created := postRender(t, server, map[string]string{
		"artist":     "Artista",
		"track":      "Prueba",
		"background": "image",
		"highlight":  "line",
		"start":      "0",
		"duration":   "20",
	}, map[string]string{"media": media, "image": background})

	id, _ := created["id"].(string)
	waitForRender(t, server, id)
}

// Un archivo grande viene de un temporal en disco, no de memoria: si el handler
// cerrara el multipart antes de renderizar, el video moría con "file already
// closed". Este test sube un archivo de más de maxMemoryForm y lo verifica.
func TestParseRenderUploadKeepsLargeFilesReadable(t *testing.T) {
	large := make([]byte, maxMemoryForm+(1<<20))
	if _, err := rand.Read(large); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "video-grande.mp4")
	if err := os.WriteFile(path, large, 0o600); err != nil {
		t.Fatal(err)
	}

	body, contentType := buildRenderBody(t, map[string]string{
		"artist": "Artista", "track": "Prueba", "background": "image",
		"start": "0", "duration": "5",
	}, map[string]string{"image": path})

	request := httptest.NewRequest(http.MethodPost, "/api/renders", body)
	request.Header.Set("Content-Type", contentType)

	upload, err := parseRenderUpload(request)
	if err != nil {
		t.Fatal(err)
	}
	if upload.Image == nil {
		t.Fatal("no se leyó el archivo de fondo")
	}

	// Lo que el render va a hacer es copiarlo al directorio del job, así que
	// tiene que poder leerse entero después de volver del parseo.
	written, err := io.Copy(io.Discard, upload.Image)
	if err != nil {
		t.Fatalf("no se pudo leer el archivo grande: %v", err)
	}
	if written != int64(len(large)) {
		t.Errorf("leídos %d bytes, want %d", written, len(large))
	}
}

// Un video de fondo de los que la gente sube de verdad pesa varios MB, y
// ParseMultipartForm los baja a un temporal en disco. Si el handler cerrara ese
// archivo antes de renderizar, el preview moría con "file already closed": por eso
// este test usa un video que supera a propósito el límite de memoria.
func TestRenderPreviewWithLargeBackgroundVideo(t *testing.T) {
	server, ffmpegPath := newRenderTestServer(t)
	media := makeTestMedia(t, ffmpegPath, "video.mp4",
		"-f", "lavfi", "-i", "testsrc=size=640x480:rate=24:duration=6",
		"-f", "lavfi", "-i", "sine=frequency=330:duration=6",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest")
	// testsrc2 y no smptebars: las barras de color comprimen a casi nada y el
	// archivo no llegaría al tamaño que esta prueba necesita.
	background := makeTestMedia(t, ffmpegPath, "bg-grande.mp4",
		"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=30:duration=5",
		"-c:v", "libx264", "-b:v", "12M", "-pix_fmt", "yuv420p")

	info, err := os.Stat(background)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= maxMemoryForm {
		t.Fatalf("el video de prueba pesa %d bytes, necesita pasar los %d de maxMemoryForm", info.Size(), maxMemoryForm)
	}

	response := postPreview(t, server, map[string]string{
		"artist":     "Artista",
		"track":      "Prueba",
		"background": "image",
		"highlight":  "line",
		"start":      "0",
		"duration":   "20",
	}, map[string]string{"media": media, "image": background})

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("HTTP %d: %s", response.StatusCode, body)
	}

	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if duration := probeFormatDuration(t, data); duration > 6 || duration < 4 {
		t.Errorf("duración = %.2fs, want ~5s (la del video de fondo)", duration)
	}
}

// Un archivo que no es foto ni video se rechaza con 400 en lugar de llegar a ffmpeg.
func TestRenderRejectsNonMediaBackground(t *testing.T) {
	server, _ := newRenderTestServer(t)

	notMedia := filepath.Join(t.TempDir(), "nota.txt")
	if err := os.WriteFile(notMedia, []byte("esto no es un archivo de medios"), 0o600); err != nil {
		t.Fatal(err)
	}

	if status := postRenderStatus(t, server, map[string]string{
		"artist": "Artista", "track": "Prueba", "background": "image",
		"start": "0", "duration": "10",
	}, map[string]string{"image": notMedia}); status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
}

// probeFormatDuration mide el largo de un MP4 en memoria, sin tocar el disco del
// servicio: la duración de la pista es del contenedor, no de un stream.
func probeFormatDuration(t *testing.T, mp4 []byte) float64 {
	t.Helper()

	path := filepath.Join(t.TempDir(), "draft.mp4")
	if err := os.WriteFile(path, mp4, 0o600); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join("..", "..", "..", "tools", "ffmpeg", "bin", "ffprobe.exe")
	result, err := exec.Command(probe, "-v", "error", "-show_entries", "format=duration",
		"-of", "csv=p=0", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe falló: %v: %s", err, result)
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(result)), 64)
	if err != nil {
		t.Fatalf("no pude interpretar la duración %q: %v", result, err)
	}
	return seconds
}

// El render solo de letra no lleva archivo: se acepta y produce un MP4 con
// video y una pista de audio en silencio.
func TestRenderEndpointEndToEndWithoutMedia(t *testing.T) {
	server, _ := newRenderTestServer(t)

	created := postRender(t, server, map[string]string{
		"artist":     "Artista",
		"track":      "Prueba",
		"background": "gradient",
		"color":      "#0b1020",
		"colorTwo":   "#3d1a5c",
		"width":      "1080",
		"height":     "1920",
		"fps":        "30",
		"start":      "0",
		"duration":   "5",
	}, nil)

	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no se recibio id de job: %v", created)
	}
	waitForRender(t, server, id)

	output := t.TempDir() + "/solo-letra.mp4"
	download(t, server, id, output)

	probe := filepath.Join("..", "..", "..", "tools", "ffmpeg", "bin", "ffprobe.exe")
	result, err := exec.Command(probe, "-v", "error",
		"-show_entries", "stream=codec_type,codec_name,width,height:format=duration",
		"-of", "default=nw=1", output).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe fallo: %v: %s", err, result)
	}
	text := string(result)
	t.Logf("ffprobe del render sin medio:\n%s", text)

	if !strings.Contains(text, "codec_name=h264") {
		t.Errorf("falta el stream de video h264")
	}
	if !strings.Contains(text, "width=1080") || !strings.Contains(text, "height=1920") {
		t.Errorf("dimensiones esperadas 1080x1920")
	}
	// Sin medio no hay de dónde sacar audio, pero el MP4 necesita la pista para
	// que los reproductores y las plataformas no lo rechacen.
	if !strings.Contains(text, "codec_name=aac") {
		t.Errorf("falta la pista de audio en silencio")
	}

	// La letra del servidor de pruebas acaba en 19.2s, así que duration=5
	// recorta: prueba de que -t manda sobre la línea de tiempo.
	const prefix = "duration="
	index := strings.LastIndex(text, prefix)
	if index < 0 {
		t.Fatalf("ffprobe no informo de la duracion")
	}
	value := text[index+len(prefix):]
	if end := strings.IndexAny(value, "\r\n"); end >= 0 {
		value = value[:end]
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		t.Fatalf("duracion ilegible %q: %v", value, err)
	}
	if duration < 4.5 || duration > 5.5 {
		t.Errorf("duracion = %v, want ~5 (el fragmento pedido)", duration)
	}
}

// El preview devuelve el MP4 en el cuerpo, sin job ni descarga, porque el
// frontend solo lo quiere para mostrar en un <video>.
// La lista de colores con varios modos viaja como "0,4,5". Un valor raro no puede
// tirar el pedido entero: se saltea y se renderiza con lo que sí se pudo leer.
func TestFormIntsReadsTheSelectedPalette(t *testing.T) {
	cases := []struct {
		raw  string
		want []int
	}{
		{raw: "0,4,5", want: []int{0, 4, 5}},
		{raw: " 1 , 3 ", want: []int{1, 3}},
		{raw: "", want: nil},
		{raw: "   ", want: nil},
		{raw: "0,x,5", want: []int{0, 5}},
		{raw: "nada", want: []int{}},
	}
	for _, testCase := range cases {
		body, contentType := buildRenderBody(t, map[string]string{"palette": testCase.raw}, nil)
		request := httptest.NewRequest(http.MethodPost, "/api/renders", body)
		request.Header.Set("Content-Type", contentType)
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("no se pudo leer el formulario: %v", err)
		}
		got := formInts(request, "palette")
		if len(got) != len(testCase.want) {
			t.Fatalf("palette=%q dio %v, quiero %v", testCase.raw, got, testCase.want)
		}
		for i, value := range testCase.want {
			if got[i] != value {
				t.Errorf("palette=%q en la posición %d dio %d, quiero %d", testCase.raw, i, got[i], value)
			}
		}
	}
}

func TestStatusForRenderErrorMapsSourceFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		// Un link mal escrito es un 400: si no, el frontend recibe un 500 y
		// parece que se rompió el servidor en vez de que hay que corregir el link.
		{"link invalido", fmt.Errorf("%w: no es un link de YouTube", domain.ErrInvalidSource), http.StatusBadRequest},
		{"video privado o caido", fmt.Errorf("%w: private video", domain.ErrSourceUnavailable), http.StatusBadGateway},
		{"sin soporte de fuentes", service.ErrSourceNotSupported, http.StatusNotImplemented},
	}
	for _, testCase := range cases {
		if got := statusForRenderError(testCase.err); got != testCase.want {
			t.Errorf("%s: statusForRenderError(%v) = %d, want %d", testCase.name, testCase.err, got, testCase.want)
		}
	}
}

func TestRenderSourceWithoutSupportReportsNotImplemented(t *testing.T) {
	server, _ := newRenderTestServer(t)

	// El servidor de prueba se arma sin fuente externa a propósito: es lo que
	// pasa cuando no está el binario de descarga, y la respuesta tiene que
	// decir que es una capacidad ausente y no un error de la petición.
	response, err := http.Get(server.URL + "/api/renders/source?url=https%3A%2F%2Fyoutu.be%2FdQw4w9WgXcQ")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotImplemented {
		t.Errorf("status = %d, want %d", response.StatusCode, http.StatusNotImplemented)
	}
}

func TestRenderSourceRequiresALink(t *testing.T) {
	server, _ := newRenderTestServer(t)

	response, err := http.Get(server.URL + "/api/renders/source")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
}

func TestRenderPreviewReturnsDraftVideo(t *testing.T) {
	server, _ := newRenderTestServer(t)

	response := postPreview(t, server, map[string]string{
		"artist":     "Artista",
		"track":      "Prueba",
		"background": "gradient",
		"color":      "#0b1020",
		"colorTwo":   "#3d1a5c",
		"width":      "1080",
		"height":     "1920",
		"fps":        "30",
		"start":      "1",
		"duration":   "4",
	}, nil)

	if contentType := response.Header.Get("Content-Type"); contentType != "video/mp4" {
		t.Errorf("Content-Type = %q, want video/mp4", contentType)
	}
	if cache := response.Header.Get("Cache-Control"); cache != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store: el borrador cambia con cada slider", cache)
	}

	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 1024 {
		t.Fatalf("el borrador pesa %d bytes, demasiado poco para ser un MP4", len(data))
	}

	// El borrador tiene que ser un MP4 reproducible, en pequeño y con audio: el
	// modal lo reproduce para juzgar el fragmento con sonido.
	target := filepath.Join(t.TempDir(), "preview.mp4")
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join("..", "..", "..", "tools", "ffmpeg", "bin", "ffprobe.exe")
	result, err := exec.Command(probe, "-v", "error",
		"-show_entries", "stream=codec_type,codec_name,width,height:format=duration",
		"-of", "default=nw=1", target).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe fallo: %v: %s", err, result)
	}
	text := string(result)
	t.Logf("ffprobe del borrador:\n%s", text)
	if !strings.Contains(text, "codec_name=h264") {
		t.Error("falta el stream de video")
	}
	if !strings.Contains(text, "codec_name=aac") {
		t.Error("falta el stream de audio, el modal no podría reproducir con sonido")
	}
	if !strings.Contains(text, "duration=4") && !strings.Contains(text, "duration=3.9") {
		t.Errorf("el borrador debería durar ~4s (el fragmento pedido), no la letra entera")
	}
}

// El preview valida lo mismo que el render, así que un fondo desconocido se
// rechaza con 400 en vez de devolver un video.
func TestRenderPreviewRejectsInvalidRequest(t *testing.T) {
	server, _ := newRenderTestServer(t)

	status := postPreviewStatus(t, server, map[string]string{
		"artist":     "Artista",
		"track":      "Prueba",
		"background": "plasma",
	}, nil)

	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
}

func TestRenderRejectsImageBackgroundWithoutImage(t *testing.T) {
	server, ffmpegPath := newRenderTestServer(t)
	media := makeTestMedia(t, ffmpegPath, "audio.m4a",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=3", "-c:a", "aac")

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("artist", "Artista")
	_ = writer.WriteField("track", "Prueba")
	_ = writer.WriteField("background", "image")
	content, _ := os.ReadFile(media)
	part, _ := writer.CreateFormFile("media", "audio.m4a")
	part.Write(content)
	writer.Close()

	response, err := http.Post(server.URL+"/api/renders", writer.FormDataContentType(), body)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", response.StatusCode)
	}
}

func TestRenderRejectsNonMediaUpload(t *testing.T) {
	server, _ := newRenderTestServer(t)

	path := filepath.Join(t.TempDir(), "notmedia.txt")
	if err := os.WriteFile(path, []byte("esto no es un archivo de audio"), 0o600); err != nil {
		t.Fatal(err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("artist", "Artista")
	_ = writer.WriteField("track", "Prueba")
	content, _ := os.ReadFile(path)
	part, _ := writer.CreateFormFile("media", "notmedia.txt")
	part.Write(content)
	writer.Close()

	response, err := http.Post(server.URL+"/api/renders", writer.FormDataContentType(), body)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", response.StatusCode)
	}
}

func TestRenderRejectsMissingArtist(t *testing.T) {
	server, ffmpegPath := newRenderTestServer(t)
	media := makeTestMedia(t, ffmpegPath, "audio.m4a",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=3", "-c:a", "aac")

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("track", "Prueba")
	content, _ := os.ReadFile(media)
	part, _ := writer.CreateFormFile("media", "audio.m4a")
	part.Write(content)
	writer.Close()

	response, err := http.Post(server.URL+"/api/renders", writer.FormDataContentType(), body)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", response.StatusCode)
	}
}

func TestRenderStatusUnknownJobIs404(t *testing.T) {
	server, _ := newRenderTestServer(t)

	response, err := http.Get(server.URL + "/api/renders/no-existe")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", response.StatusCode)
	}
}

func TestRenderFileBeforeCompletionIs404(t *testing.T) {
	server, _ := newRenderTestServer(t)

	response, err := http.Get(server.URL + "/api/renders/no-existe/file")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", response.StatusCode)
	}
}

func TestRenderFileNameIsSafeForTheHeader(t *testing.T) {
	cases := []struct {
		artist, title string
		want          string
	}{
		{"Artista", "Prueba", "Artista - Prueba.mp4"},
		{`Artista"`, "Prueba", "Artista- - Prueba.mp4"},
		{"AC/DC", "Back in Black", "AC-DC - Back in Black.mp4"},
		{"", "", "karaoke.mp4"},
		{"   ", "  ", "karaoke.mp4"},
		{"Artista", "Titulo\ncon salto", "Artista - Titulocon salto.mp4"},
	}
	for _, testCase := range cases {
		if got := renderFileName(testCase.artist, testCase.title); got != testCase.want {
			t.Errorf("renderFileName(%q, %q) = %q, want %q", testCase.artist, testCase.title, got, testCase.want)
		}
	}
}
