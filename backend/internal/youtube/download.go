package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"lyrics/internal/domain"
	"lyrics/internal/platform"
)

// Los dos rechazos de una fuente se nombran en domain y no acÃ¡: la capa HTTP
// necesita distinguirlos para no contestar 500 a un link mal escrito, y no
// deberÃ­a tener que importar este paquete para hacerlo.
const (
	// downloadTimeout acota la espera de una descarga. Es holgado a propÃ³sito:
	// un audio de tres horas son unos 30MB, pero la red de YouTube es lenta y
	// variable. Lo que no puede pasar es que un job quede encolado para siempre.
	downloadTimeout = 5 * time.Minute
	// metadataTimeout es solo la ficha del video: no baja medios, asÃ­ que tiene
	// que ser rÃ¡pido para no hacerte esperar al pegar el link.
	metadataTimeout = 45 * time.Second

	// downloadSlots limita descargas simultÃ¡neas. Cada movimiento del selector
	// de fragmento lanza un borrador, y sin tope cada uno podrÃ­a bajar su copia.
	downloadSlots = 2

	// tailBytes acota lo que se guarda de stderr de yt-dlp para el mensaje de
	// error: su salida puede ser de varios KB con los warnings de formato.
	tailBytes = 2048
)

var ytdlpName = platform.ExecutableName("yt-dlp")

// videoIDPattern es el formato real de un ID de YouTube. Validarlo no es
// cosmÃ©tico: el ID termina en el nombre del archivo de la cachÃ©, asÃ­ que es lo
// Ãºnico que impide que un link pueda escribir fuera del directorio.
var videoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// Downloader baja el audio de un video de YouTube con yt-dlp. Guarda lo
// descargado en un directorio de cachÃ© compartido porque el borrador del modal
// se regenera en cada cambio del fragmento: sin cachÃ©, arrastrar el selector
// descargarÃ­a el mismo video una y otra vez.
type Downloader struct {
	binary    string
	ffmpegDir string
	cacheDir  string
	slots     chan struct{}
}

// NewDownloader deja yt-dlp junto a ffmpeg, en el mismo tools/ que ya usa el
// motor, y cachea en cacheDir.
func NewDownloader(toolsDir, cacheDir string) *Downloader {
	return &Downloader{
		binary:    filepath.Join(toolsDir, "yt-dlp", ytdlpName),
		ffmpegDir: filepath.Join(toolsDir, "ffmpeg", "bin"),
		cacheDir:  cacheDir,
		slots:     make(chan struct{}, downloadSlots),
	}
}

func (downloader *Downloader) Available() error {
	info, err := os.Stat(downloader.binary)
	if err != nil {
		return fmt.Errorf("%w: no se encontrÃ³ %s", domain.ErrSourceUnavailable, downloader.binary)
	}
	if info.IsDir() {
		return fmt.Errorf("%w: %s es un directorio", domain.ErrSourceUnavailable, downloader.binary)
	}
	return nil
}

// DownloadAudio devuelve la ruta de un archivo de audio con el del video pedido.
// Si ya estÃ¡ en cachÃ© devuelve la misma ruta sin volver a bajarlo.
func (downloader *Downloader) DownloadAudio(ctx context.Context, rawURL string) (string, error) {
	id, err := VideoID(rawURL)
	if err != nil {
		return "", err
	}
	if err := downloader.Available(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(downloader.cacheDir, 0o700); err != nil {
		return "", err
	}

	target := filepath.Join(downloader.cacheDir, id+".m4a")
	if _, err := os.Stat(target); err == nil {
		return target, nil
	}

	select {
	case downloader.slots <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-downloader.slots }()

	// Otro borrador pudo bajarlo mientras se esperaba el lugar.
	if _, err := os.Stat(target); err == nil {
		return target, nil
	}

	// La descarga va a un directorio propio y reciÃ©n al final se mueve a la
	// cachÃ©: si el proceso se corta a mitad, no queda un archivo truncado que
	// las prÃ³ximas descargas tomarÃ­an por bueno.
	attempt, err := os.MkdirTemp(downloader.cacheDir, "descarga-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(attempt)

	if err := downloader.run(ctx, downloadTimeout, attempt, id); err != nil {
		return "", err
	}

	produced, err := singleFile(attempt)
	if err != nil {
		return "", err
	}
	if err := os.Rename(produced, target); err != nil {
		// Si en paralelo otro borrador ganÃ³ la carrera, el que estÃ¡ en cachÃ©
		// sirve igual.
		if _, statErr := os.Stat(target); statErr == nil {
			return target, nil
		}
		return "", err
	}
	return target, nil
}

// Describe pide la ficha del video sin bajar el medio, para que el modal pueda
// mostrar el tÃ­tulo y ajustar el selector de fragmento a la duraciÃ³n real.
func (downloader *Downloader) Describe(ctx context.Context, rawURL string) (domain.SourceInfo, error) {
	id, err := VideoID(rawURL)
	if err != nil {
		return domain.SourceInfo{}, err
	}
	if err := downloader.Available(); err != nil {
		return domain.SourceInfo{}, err
	}

	metadataCtx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()

	output, err := exec.CommandContext(metadataCtx, downloader.binary,
		"--no-playlist", "--no-warnings", "--skip-download", "--dump-single-json",
		videoURL(id),
	).Output()
	if err != nil {
		return domain.SourceInfo{}, fmt.Errorf("%w: %s", domain.ErrSourceUnavailable, commandError(err, bytes.NewBuffer(output), nil))
	}

	return parseSourceInfo(output)
}

func parseSourceInfo(output []byte) (domain.SourceInfo, error) {
	var payload struct {
		ID           string  `json:"id"`
		Title        string  `json:"title"`
		Duration     float64 `json:"duration"`
		Width        int     `json:"width"`
		Height       int     `json:"height"`
		IsLive       bool    `json:"is_live"`
		LiveStatus   string  `json:"live_status"`
		Availability string  `json:"availability"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return domain.SourceInfo{}, fmt.Errorf("%w: no se pudo leer la ficha del video", domain.ErrSourceUnavailable)
	}
	if payload.IsLive || payload.LiveStatus == "is_live" {
		return domain.SourceInfo{}, fmt.Errorf("%w: el video es una transmisiÃ³n en vivo, sin duraciÃ³n fija", domain.ErrSourceUnavailable)
	}
	if payload.Availability != "" && payload.Availability != "public" {
		return domain.SourceInfo{}, fmt.Errorf("%w: el video no es pÃºblico (%s)", domain.ErrSourceUnavailable, payload.Availability)
	}

	if payload.Width <= 0 || payload.Height <= 0 {
		payload.Width, payload.Height = 0, 0
	}
	return domain.SourceInfo{
		ID:       payload.ID,
		Title:    payload.Title,
		Duration: payload.Duration,
		Width:    payload.Width,
		Height:   payload.Height,
	}, nil
}

// run baixa el audio en attempt. Usa -x --audio-format m4a para que la pista
// quede en un contenedor que ffmpeg y el navegador leen sin preguntar.
func (downloader *Downloader) run(ctx context.Context, timeout time.Duration, attempt, id string) error {
	downloadCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stderr bytes.Buffer
	command := exec.CommandContext(downloadCtx, downloader.binary,
		"--no-playlist", "--no-progress", "--no-warnings", "--newline",
		"--no-write-info-json", "--no-write-thumbnail", "--no-write-subs",
		"--no-overwrites",
		"-f", "bestaudio/best",
		"-x", "--audio-format", "m4a",
		// Sin ffmpeg no se puede recodificar a m4a, asÃ­ que se le pasa el
		// directorio donde el motor ya lo tiene.
		"--ffmpeg-location", downloader.ffmpegDir,
		"-o", filepath.Join(attempt, "medio.%(ext)s"),
		videoURL(id),
	)
	command.Stderr = &tailWriter{limit: tailBytes, data: &stderr}
	if err := command.Run(); err != nil {
		return fmt.Errorf("%w: %s", domain.ErrSourceUnavailable, commandError(err, nil, &stderr))
	}
	return nil
}

// VideoID acepta un link de YouTube o el ID suelto y devuelve el ID de once
// caracteres. La lista de hosts es cerrada a propÃ³sito: este endpoint ejecuta
// un binario con lo que le manden, y con cualquier host se convertirÃ­a en un
// descargador genÃ©rico.
func VideoID(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%w: el link estÃ¡ vacÃ­o", domain.ErrInvalidSource)
	}
	// El buscador de videos del app ya devuelve IDs, asÃ­ que pegarlos directo
	// tiene que funcionar sin armar la URL a mano.
	if videoIDPattern.MatchString(trimmed) {
		return trimmed, nil
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: %v", domain.ErrInvalidSource, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("%w: el link tiene que empezar con http", domain.ErrInvalidSource)
	}

	var id string
	switch host := strings.ToLower(parsed.Hostname()); host {
	case "youtu.be":
		id = pathSegment(parsed.Path, 0)
	case "youtube.com", "www.youtube.com", "m.youtube.com", "music.youtube.com":
		if fromQuery := parsed.Query().Get("v"); fromQuery != "" {
			id = fromQuery
			break
		}
		// /shorts/ID, /embed/ID y /live/ID no traen el ID en el query.
		if first := pathSegment(parsed.Path, 0); first == "shorts" || first == "embed" || first == "live" {
			id = pathSegment(parsed.Path, 1)
		}
	default:
		return "", fmt.Errorf("%w: %q no es un link de YouTube", domain.ErrInvalidSource, host)
	}

	if !videoIDPattern.MatchString(id) {
		return "", fmt.Errorf("%w: no se encontrÃ³ un ID de video en el link", domain.ErrInvalidSource)
	}
	return id, nil
}

func videoURL(id string) string {
	return "https://www.youtube.com/watch?v=" + id
}

func pathSegment(path string, index int) string {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if index >= len(segments) {
		return ""
	}
	return segments[index]
}

// singleFile encuentra lo que dejÃ³ la descarga. yt-dlp siempre escribe un solo
// medio cuando se le pide solo audio, asÃ­ que mÃ¡s de un archivo significa que
// algo se mezclÃ³ y mejor fallar que elegir uno al azar.
func singleFile(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	found := ""
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if found != "" {
			return "", fmt.Errorf("%w: la descarga dejÃ³ varios archivos", domain.ErrSourceUnavailable)
		}
		found = filepath.Join(dir, entry.Name())
	}
	if found == "" {
		return "", fmt.Errorf("%w: la descarga no produjo ningÃºn archivo", domain.ErrSourceUnavailable)
	}
	return found, nil
}

// commandError arma el mensaje a partir de la salida de yt-dlp. Sus errores
// vienen por stderr y son los que dicen por quÃ© fallÃ³ de verdad ("Video
// unavailable", "Private video"), asÃ­ que se muestran en vez del "exit status
// 1" que deja exec.
func commandError(err error, stdout *bytes.Buffer, stderr *bytes.Buffer) string {
	for _, buffer := range []*bytes.Buffer{stderr, stdout} {
		if buffer == nil {
			continue
		}
		if text := strings.TrimSpace(buffer.String()); text != "" {
			return lastLines(text, 2)
		}
	}
	return err.Error()
}

func lastLines(text string, count int) string {
	lines := strings.Split(text, "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.TrimSpace(strings.Join(lines, " "))
}

// tailWriter se queda solo con los Ãºltimos bytes escritos. yt-dlp puede escribir
// kilobytes de progreso, y stderr sin lÃ­mite en un proceso de red no es una
// buena idea para la memoria.
type tailWriter struct {
	limit int
	data  *bytes.Buffer
}

func (writer *tailWriter) Write(chunk []byte) (int, error) {
	writer.data.Write(chunk)
	if writer.data.Len() > writer.limit {
		trimmed := writer.data.Bytes()[writer.data.Len()-writer.limit:]
		writer.data.Reset()
		writer.data.Write(trimmed)
	}
	return len(chunk), nil
}
