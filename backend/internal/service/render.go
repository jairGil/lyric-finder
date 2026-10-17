package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lyrics/internal/domain"
	"lyrics/internal/ports"
)

var ErrUnsupportedMedia = errors.New("formato de archivo no soportado")

var ErrImageRequired = errors.New("se requiere una imagen de fondo")

var ErrRenderFailed = errors.New("no se pudo completar el render")

// ErrInvalidRender marca un rechazo de la propia petición de render: un fondo
// desconocido o un fragmento imposible. Es un sentinel aparte de
// ErrInvalidQuery porque ese dice "artist y track son obligatorios", y
// envolverlo con el detalle del rechazo producía un mensaje contradictorio.
var ErrInvalidRender = errors.New("la petición de render no es válida")

// ErrDraftUnavailable dice que el servidor no sabe generar borradores. No es un
// error de la petición: el render final sigue funcionando sin preview.
var ErrDraftUnavailable = errors.New("el servidor no puede generar borradores")

// ErrSourceNotSupported dice que el servidor no puede traer el audio de un link
// externo porque no tiene el binario de descarga. Como ErrDraftUnavailable, es
// una capacidad ausente y no un error de la petición: los renders con archivo
// o solo letra no se ven afectados.
var ErrSourceNotSupported = errors.New("el servidor no puede traer el audio de un link externo")

const (
	defaultWidth   = 1920
	defaultHeight  = 1080
	defaultFPS     = 30
	maxUploadBytes = 512 << 20
	jobTimeout     = 20 * time.Minute
	queueDepth     = 32
	peekBytes      = 512
	// maxDraftSeconds recorta el borrador. Lo que sirve del preview es juzgar el
	// aspecto del fragmento, no verlo entero: cuatro minutos en borrador
	// tardarían lo mismo que el render final y dejarían el modal esperando.
	maxDraftSeconds = 15
	// draftSlots limita cuántos ffmpeg de borrador corren a la vez. Cada slider
	// movido lanza uno, y sin tope se acumularían hasta poner el worker de los
	// renders finales de rodillas.
	draftSlots = 2
	// maxDisplayName recorta el link que se guarda en el historial del job.
	maxDisplayName = 80
)

var mediaExtensions = map[string]string{
	"audio/mpeg":       ".mp3",
	"audio/wave":       ".wav",
	"audio/wav":        ".wav",
	"audio/ogg":        ".ogg",
	"video/mp4":        ".mp4",
	"video/webm":       ".webm",
	"video/quicktime":  ".mov",
	"video/x-matroska": ".mkv",
	"image/jpeg":       ".jpg",
	"image/png":        ".png",
	"image/webp":       ".webp",
	"image/gif":        ".gif",
}

const unknownExtension = ".bin"

type RenderService struct {
	provider domain.LyricsProvider
	renderer domain.Renderer
	probe    domain.MediaProbe
	source   domain.AudioSource
	store    ports.RenderRepository
	logger   *log.Logger
	queue    chan string
	drafts   chan struct{}
	baseDir  string
}

func NewRenderService(provider domain.LyricsProvider, renderer domain.Renderer, probe domain.MediaProbe, source domain.AudioSource, store ports.RenderRepository, baseDir string, loggers ...*log.Logger) (*RenderService, error) {
	if provider == nil {
		return nil, errors.New("NewRenderService: falta el proveedor de letras")
	}
	if renderer == nil {
		return nil, errors.New("NewRenderService: falta el renderizador")
	}
	if probe == nil {
		return nil, errors.New("NewRenderService: falta el sonda de medios")
	}
	if store == nil {
		return nil, errors.New("NewRenderService: falta el almacenamiento")
	}
	if err := renderer.Available(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return nil, err
	}

	logger := log.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}

	service := &RenderService{
		provider: provider,
		renderer: renderer,
		probe:    probe,
		source:   source,
		store:    store,
		logger:   logger,
		queue:    make(chan string, queueDepth),
		drafts:   make(chan struct{}, draftSlots),
		baseDir:  baseDir,
	}

	service.recoverStale()
	go service.worker()
	return service, nil
}

type RenderUpload struct {
	Artist     string
	Track      string
	Background domain.BackgroundKind
	Color      string
	ColorTwo   string
	Style      domain.Style
	Width      int
	Height     int
	FPS        int
	// Start y Duration recortan el clip. Start es un offset en segundos desde
	// el inicio y Duration la longitud; Duration <= 0 significa "hasta el
	// final", que es la duración del medio si se subió, o la de la línea de
	// tiempo de la letra si el render es solo letra con fondo.
	Start     float64
	Duration  float64
	MediaName string
	// Media es opcional: sin archivo el render es solo la letra sobre el fondo,
	// con una pista de audio en silencio.
	Media io.Reader
	// SourceURL trae el audio de un video de YouTube cuando no se sube archivo.
	// Es la alternativa a Media, no un complemento: si llegan los dos, manda el
	// archivo. Solo se usa el audio; el fondo sigue siendo el elegido arriba.
	SourceURL string
	ImageName string
	Image     io.Reader
	UserID    int64
}

func (service *RenderService) Queue(ctx context.Context, upload RenderUpload) (domain.RenderJob, error) {
	// El ID se genera antes de validar porque su directorio es donde se escriben
	// los archivos subidos: si algo falla después, el defer borra el todo.
	jobID := newJobID()
	jobDir := filepath.Join(service.baseDir, jobID)
	if err := os.MkdirAll(jobDir, 0o700); err != nil {
		return domain.RenderJob{}, err
	}

	// Si la subida no llega a encolarse, el directorio con el medio ya escrito
	// debe desaparecer; de lo contrario cada reintento deja basura en disco.
	accepted := false
	defer func() {
		if !accepted {
			os.RemoveAll(jobDir)
		}
	}()

	spec, _, err := service.prepareUpload(ctx, upload, jobDir)
	if err != nil {
		return domain.RenderJob{}, err
	}

	if err := service.store.CreateRenderJob(ctx, jobID, upload.UserID, spec.Artist, spec.Track, mediaDisplayName(upload), renderSpecRecord(spec)); err != nil {
		return domain.RenderJob{}, err
	}

	job, _, err := service.store.GetRenderJob(ctx, jobID)
	if err != nil {
		return domain.RenderJob{}, err
	}

	select {
	case service.queue <- jobID:
		service.logger.Printf("render=queued id=%s artist=%q track=%q video=%t background=%s size=%dx%d clip=%.2f+%.2f", jobID, spec.Artist, spec.Track, spec.MediaHasVideo, spec.Background, spec.Width, spec.Height, spec.ClipStart, spec.ClipDuration)
	default:
		_ = service.store.MarkRenderFailed(ctx, jobID, "la cola de renders está llena")
		return domain.RenderJob{}, ErrRenderFailed
	}
	accepted = true
	return job, nil
}

// prepareUpload valida la petición, busca la letra y deja los archivos
// escribidos en workDir. Lo comparten Queue y Preview a propósito: un borrador
// tiene que rechazarse exactamente igual que el render final, o el modal
// mostraría un fragmento que después el servidor no acepta.
func (service *RenderService) prepareUpload(ctx context.Context, upload RenderUpload, workDir string) (domain.RenderSpec, domain.Lyrics, error) {
	artist := strings.TrimSpace(upload.Artist)
	track := strings.TrimSpace(upload.Track)
	if artist == "" || track == "" {
		return domain.RenderSpec{}, domain.Lyrics{}, ErrInvalidQuery
	}
	if upload.Background == "" {
		upload.Background = domain.BackgroundColor
	}
	switch upload.Background {
	case domain.BackgroundColor, domain.BackgroundImage, domain.BackgroundGradient:
	default:
		return domain.RenderSpec{}, domain.Lyrics{}, fmt.Errorf("%w: el fondo debe ser %q, %q o %q", ErrInvalidRender,
			domain.BackgroundColor, domain.BackgroundImage, domain.BackgroundGradient)
	}
	if upload.Background == domain.BackgroundImage && upload.Image == nil {
		return domain.RenderSpec{}, domain.Lyrics{}, ErrImageRequired
	}

	lyrics, err := service.provider.GetLyrics(ctx, artist, track)
	if err != nil {
		return domain.RenderSpec{}, domain.Lyrics{}, err
	}
	if !lyrics.Synchronized || len(lyrics.SyncedLyrics) == 0 {
		return domain.RenderSpec{}, domain.Lyrics{}, domain.ErrNoLyrics
	}

	spec := domain.RenderSpec{
		Artist:     artist,
		Track:      track,
		Color:      upload.Color,
		ColorTwo:   upload.ColorTwo,
		Background: upload.Background,
		Style:      upload.Style,
		Width:      normalizeDimension(upload.Width, defaultWidth),
		Height:     normalizeDimension(upload.Height, defaultHeight),
		FPS:        normalizeFPS(upload.FPS),
	}

	// El medio es opcional y puede llegar de dos maneras: un archivo subido o
	// el audio de un link externo. Si vienen los dos, manda el archivo, que es
	// lo que el usuario eligió explícitamente. Sin ninguno, el render es la letra
	// sobre el fondo y su largo sale de la propia línea de tiempo.
	info := domain.MediaInfo{}
	switch {
	case upload.Media != nil:
		stored, err := service.persistMedia(upload.Media, workDir)
		if err != nil {
			return domain.RenderSpec{}, domain.Lyrics{}, err
		}
		if info, err = service.attachMedia(ctx, &spec, stored); err != nil {
			return domain.RenderSpec{}, domain.Lyrics{}, err
		}
	case strings.TrimSpace(upload.SourceURL) != "":
		stored, err := service.fetchSource(ctx, upload.SourceURL)
		if err != nil {
			return domain.RenderSpec{}, domain.Lyrics{}, err
		}
		if info, err = service.attachMedia(ctx, &spec, stored); err != nil {
			return domain.RenderSpec{}, domain.Lyrics{}, err
		}
	}

	if upload.Background == domain.BackgroundImage {
		backgroundPath, isVideo, err := service.persistBackground(upload.Image, workDir)
		if err != nil {
			return domain.RenderSpec{}, domain.Lyrics{}, err
		}
		spec.BackgroundPath = backgroundPath
		spec.BackgroundIsVideo = isVideo
		if isVideo {
			// Hay que conocer cuánto dura el video de fondo antes de cerrar el clip,
			// porque tops su largo.
			if err := service.attachBackground(ctx, &spec, backgroundPath); err != nil {
				return domain.RenderSpec{}, domain.Lyrics{}, err
			}
		}
	}

	basis := info.Duration
	if basis <= 0 {
		basis = timelineLength(lyrics)
	}
	clipStart, clipDuration, err := resolveClip(upload.Start, upload.Duration, basis)
	if err != nil {
		return domain.RenderSpec{}, domain.Lyrics{}, err
	}
	// El video de fondo se repite para cubrir la ventana, así que no limita dónde
	// puede empezar el fragmento: eso lo acota la canción. Lo único que tops es el
	// largo, para no exportar más segundos de fondo que los que el video aporta.
	if spec.BackgroundIsVideo && spec.BackgroundDuration > 0 && clipDuration > spec.BackgroundDuration {
		clipDuration = spec.BackgroundDuration
	}
	if spec.MediaPath == "" && clipDuration <= 0 {
		// Sin medio, el fondo y el silencio son fuentes infinitas: sin una
		// duración el job codificaría hasta agotar el tiempo de espera.
		return domain.RenderSpec{}, domain.Lyrics{}, fmt.Errorf("%w: no hay medio ni una letra con final para medir la duración", ErrInvalidRender)
	}
	spec.ClipStart = clipStart
	spec.ClipDuration = clipDuration

	return spec, lyrics, nil
}

// Preview devuelve los bytes de un borrador en MP4 del fragmento pedido. Usa el
// mismo pipeline que el render final, así que el modal muestra el resultado real
// en baja resolución en vez de una aproximación hecha en el navegador.
func (service *RenderService) Preview(ctx context.Context, upload RenderUpload) ([]byte, error) {
	draft, ok := service.renderer.(domain.DraftRenderer)
	if !ok {
		return nil, ErrDraftUnavailable
	}

	// Cada cambio en el modal lanza un borrador. Se espera por un lugar y no se
	// abandona: si el navegador corta la petición, el ctx cancela la espera.
	select {
	case service.drafts <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-service.drafts }()

	workDir, err := os.MkdirTemp(service.baseDir, "borrador-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workDir)

	spec, lyrics, err := service.prepareUpload(ctx, upload, workDir)
	if err != nil {
		return nil, err
	}
	if spec.ClipDuration > maxDraftSeconds {
		spec.ClipDuration = maxDraftSeconds
	}
	if spec.ClipDuration <= 0 {
		return nil, fmt.Errorf("%w: el fragmento no tiene duración para previsualizar", ErrInvalidRender)
	}

	outputPath := filepath.Join(workDir, "borrador.mp4")
	spec.OutputPath = outputPath

	started := time.Now()
	if err := draft.RenderDraft(ctx, lyrics, spec); err != nil {
		return nil, err
	}
	service.logger.Printf("render=draft artist=%q track=%q size=%dx%d clip=%.2f+%.2f elapsed=%s",
		spec.Artist, spec.Track, spec.Width, spec.Height, spec.ClipStart, spec.ClipDuration,
		time.Since(started).Round(time.Millisecond))

	return os.ReadFile(outputPath)
}

// mediaDisplayName es lo que queda en el historial del job. Con un link externo
// no hay nombre de archivo, así que se guarda el link recortado: identifica la
// fuente de una vista y no obliga a una llamada de red extra por el título.
func mediaDisplayName(upload RenderUpload) string {
	if name := strings.TrimSpace(upload.MediaName); name != "" {
		return name
	}
	source := strings.TrimSpace(upload.SourceURL)
	if len(source) > maxDisplayName {
		source = source[:maxDisplayName] + "…"
	}
	return source
}

// DescribeSource pide los metadatos de un link externo sin bajar el medio. El
// modal lo usa para mostrar el título y ajustar el selector de fragmento a la
// duración real antes de que haya que renderizar nada.
func (service *RenderService) DescribeSource(ctx context.Context, rawURL string) (domain.SourceInfo, error) {
	if service.source == nil {
		return domain.SourceInfo{}, ErrSourceNotSupported
	}
	return service.source.Describe(ctx, rawURL)
}

// renderSpecRecord baja la spec a lo que se persiste en el job. Los archivos
// van por separado: en la base solo queda la ruta.
func renderSpecRecord(spec domain.RenderSpec) domain.RenderSpecRecord {
	return domain.RenderSpecRecord{
		MediaPath:          spec.MediaPath,
		MediaHasVideo:      spec.MediaHasVideo,
		BackgroundPath:     spec.BackgroundPath,
		BackgroundIsVideo:  spec.BackgroundIsVideo,
		BackgroundDuration: spec.BackgroundDuration,
		Color:              spec.Color,
		ColorTwo:           spec.ColorTwo,
		Background:         string(spec.Background),
		Style:              spec.Style,
		Width:              spec.Width,
		Height:             spec.Height,
		FPS:                spec.FPS,
		ClipStart:          spec.ClipStart,
		ClipDuration:       spec.ClipDuration,
	}
}

func (service *RenderService) Status(ctx context.Context, jobID string) (domain.RenderJob, error) {
	job, _, err := service.store.GetRenderJob(ctx, jobID)
	return job, err
}

func (service *RenderService) OutputPath(ctx context.Context, jobID string) (string, error) {
	_, path, err := service.Output(ctx, jobID)
	return path, err
}

// Output devuelve el job junto con la ruta del archivo ya terminado, para que el
// handler pueda nombrar la descarga con artista y título.
func (service *RenderService) Output(ctx context.Context, jobID string) (domain.RenderJob, string, error) {
	job, _, err := service.store.GetRenderJob(ctx, jobID)
	if err != nil {
		return domain.RenderJob{}, "", err
	}
	if job.Status != domain.RenderDone {
		return domain.RenderJob{}, "", domain.ErrRecordNotFound
	}
	path := filepath.Join(service.baseDir, jobID+".mp4")
	if _, err := os.Stat(path); err != nil {
		return domain.RenderJob{}, "", domain.ErrRecordNotFound
	}
	return job, path, nil
}

func (service *RenderService) worker() {
	for jobID := range service.queue {
		service.process(jobID)
	}
}

func (service *RenderService) process(jobID string) {
	ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
	defer cancel()

	job, spec, err := service.store.GetRenderJob(ctx, jobID)
	if err != nil {
		service.logger.Printf("render=error id=%s stage=load error=%q", jobID, err)
		return
	}
	if job.Status == domain.RenderDone {
		return
	}

	if err := service.store.MarkRenderRunning(ctx, jobID); err != nil {
		service.logger.Printf("render=error id=%s stage=mark_running error=%q", jobID, err)
		return
	}

	lyrics, err := service.provider.GetLyrics(ctx, job.Artist, job.Title)
	if err != nil {
		service.fail(ctx, jobID, "no se pudo volver a leer la letra")
		return
	}
	if !lyrics.Synchronized || len(lyrics.SyncedLyrics) == 0 {
		service.fail(ctx, jobID, domain.ErrNoLyrics.Error())
		return
	}

	outputName := jobID + ".mp4"
	outputPath := filepath.Join(service.baseDir, outputName)
	renderSpec := domain.RenderSpec{
		Artist:             job.Artist,
		Track:              job.Title,
		MediaPath:          spec.MediaPath,
		OutputPath:         outputPath,
		MediaHasVideo:      spec.MediaHasVideo,
		Background:         domain.BackgroundKind(spec.Background),
		Color:              spec.Color,
		ColorTwo:           spec.ColorTwo,
		BackgroundPath:     spec.BackgroundPath,
		BackgroundIsVideo:  spec.BackgroundIsVideo,
		BackgroundDuration: spec.BackgroundDuration,
		Style:              spec.Style,
		Width:              spec.Width,
		Height:             spec.Height,
		FPS:                spec.FPS,
		ClipStart:          spec.ClipStart,
		ClipDuration:       spec.ClipDuration,
	}

	started := time.Now()
	err = service.renderer.Render(ctx, jobID, lyrics, renderSpec, func(progress float64) {
		service.store.MarkRenderProgress(ctx, jobID, progress)
	})

	if err != nil {
		service.logger.Printf("render=failed id=%s error=%q", jobID, err)
		service.fail(ctx, jobID, userFacingRenderError(err))
		return
	}

	if err := service.store.MarkRenderDone(ctx, jobID, outputName); err != nil {
		service.logger.Printf("render=error id=%s stage=mark_done error=%q", jobID, err)
		return
	}
	service.logger.Printf("render=done id=%s elapsed=%s", jobID, time.Since(started).Round(time.Millisecond))
}

func (service *RenderService) fail(ctx context.Context, jobID, message string) {
	if err := service.store.MarkRenderFailed(ctx, jobID, message); err != nil {
		service.logger.Printf("render=error id=%s stage=mark_failed error=%q", jobID, err)
	}
}

func (service *RenderService) recoverStale() {
	count, err := service.store.ResetStaleRenderJobs(context.Background())
	if err != nil {
		service.logger.Printf("render=error stage=recover error=%q", err)
		return
	}
	if count > 0 {
		service.logger.Printf("render=recovered count=%d", count)
	}
}

// persistBackground escribe el archivo elegido como fondo y dice si hay que
// tratarlo como video.
//
// La cabecera solo sirve para reconocer fotos: http.DetectContentType las ve
// todas (png, jpeg, webp, gif), pero no los MP4 de verdad: un ftypisom cae en
// application/octet-stream, así que decidir por el content type dejaría afuera
// el video de cualquier cámara. Por eso lo que no es foto se acepta sin mirar la
// cabecera y lo decide el sonda, que sí sabe qué se puede decodificar.
func (service *RenderService) persistBackground(source io.Reader, jobDir string) (string, bool, error) {
	header, contentType, err := peek(source)
	if err != nil {
		return "", false, err
	}
	extension, known := mediaExtensions[contentType]
	if !known {
		// La extensión es solo comodidad: ffprobe y ffmpeg detectan el contenedor
		// por contenido, así que un .bin desconocido no impide el render.
		extension = unknownExtension
	}
	isVideo := !strings.HasPrefix(contentType, "image/")
	path, err := service.write(jobDir, "bg", extension, header, source)
	return path, isVideo, err
}

// attachBackground confirma que el archivo de fondo sea un video usable y deja su
// duración en la spec, que es el tope del clip. Las fotos no pasan por acá: se
// repiden indefinidas y no tienen nada que medir.
func (service *RenderService) attachBackground(ctx context.Context, spec *domain.RenderSpec, path string) error {
	info, err := service.probe.Probe(ctx, path)
	if err != nil {
		return fmt.Errorf("%w: no se pudo leer el video de fondo: %s", ErrUnsupportedMedia, err.Error())
	}
	if !info.HasVideo {
		return fmt.Errorf("%w: el fondo tiene que ser una foto o un video con imagen", ErrUnsupportedMedia)
	}
	if info.Duration <= 0 {
		return fmt.Errorf("%w: el video de fondo no tiene duración", ErrUnsupportedMedia)
	}
	spec.BackgroundDuration = info.Duration
	return nil
}

func (service *RenderService) persistMedia(source io.Reader, jobDir string) (string, error) {
	header, contentType, err := peek(source)
	if err != nil {
		return "", err
	}
	extension, known := mediaExtensions[contentType]
	if !known {
		extension = unknownExtension
	}
	return service.write(jobDir, "media", extension, header, source)
}

// attachMedia sondea el medio ya escrito en disco y lo deja apuntado en la spec.
// Un archivo sin pista de audio no sirve: el render quedaría mudo, que es
// justo lo que el usuario no quiere al elegir una fuente.
func (service *RenderService) attachMedia(ctx context.Context, spec *domain.RenderSpec, path string) (domain.MediaInfo, error) {
	info, err := service.probe.Probe(ctx, path)
	if err != nil {
		return domain.MediaInfo{}, fmt.Errorf("%w: %s", ErrUnsupportedMedia, err.Error())
	}
	if !info.HasAudio {
		return domain.MediaInfo{}, fmt.Errorf("%w: el archivo no tiene pista de audio", ErrUnsupportedMedia)
	}
	spec.MediaPath = path
	spec.MediaHasVideo = info.HasVideo
	return info, nil
}

// fetchSource trae el audio de un link externo. No escribe nada en el
// directorio del job: la fuente decide dónde dejar el archivo, porque el
// borrador del modal pide el mismo audio en cada cambio del fragmento y
// bajarlo una vez por ajuste sería insostenible.
func (service *RenderService) fetchSource(ctx context.Context, rawURL string) (string, error) {
	if service.source == nil {
		return "", ErrSourceNotSupported
	}
	path, err := service.source.DownloadAudio(ctx, rawURL)
	if err != nil {
		return "", err
	}
	return path, nil
}

func peek(source io.Reader) ([]byte, string, error) {
	header := make([]byte, peekBytes)
	read, err := io.ReadFull(source, header)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, "", err
	}
	header = header[:read]
	if len(header) == 0 {
		return nil, "", fmt.Errorf("%w: el archivo está vacío", ErrUnsupportedMedia)
	}
	return header, http.DetectContentType(header), nil
}

func (service *RenderService) write(jobDir, prefix, extension string, header []byte, source io.Reader) (string, error) {
	if extension == "" {
		extension = unknownExtension
	}
	target := filepath.Join(jobDir, prefix+extension)
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	defer file.Close()

	if _, err := file.Write(header); err != nil {
		return "", err
	}
	if _, err := io.Copy(file, io.LimitReader(source, maxUploadBytes)); err != nil {
		return "", err
	}
	return target, nil
}

func normalizeDimension(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	if value < 320 {
		return 320
	}
	if value > 3840 {
		return 3840
	}
	return value
}

func normalizeFPS(value int) int {
	if value <= 0 {
		return defaultFPS
	}
	if value < 12 {
		return 12
	}
	if value > 60 {
		return 60
	}
	return value
}

// timelineLength es la duración de un render sin medio: la de la pista según
// LRCLIB o, si no la hay, hasta donde termina la última línea. Sin ella el
// grafo sería una fuente infinita y el job no terminaría nunca.
func timelineLength(lyrics domain.Lyrics) float64 {
	length := lyrics.Track.Duration
	for _, line := range lyrics.SyncedLyrics {
		if line.End > length {
			length = line.End
		}
	}
	if length <= 0 {
		return 0
	}
	// Un pequeño margen para que la última línea no muera justo en el corte.
	return length + 1.5
}

// resolveClip acota el fragmento pedido al medio real. Un start negativo o más
// allá del final no tiene sentido y se rechaza; una duración que se pasa del
// final se recorta en lugar de fallar, que es lo que espera quien arrastra el
// tirador hasta el final de la línea de tiempo.
func resolveClip(start, duration, mediaDuration float64) (float64, float64, error) {
	if math.IsNaN(start) || math.IsNaN(duration) || math.IsInf(start, 0) || math.IsInf(duration, 0) {
		return 0, 0, fmt.Errorf("%w: el inicio y la duración del fragmento no son válidos", ErrInvalidRender)
	}
	if start < 0 {
		return 0, 0, fmt.Errorf("%w: el inicio del fragmento no puede ser negativo", ErrInvalidRender)
	}
	if mediaDuration > 0 && start >= mediaDuration {
		return 0, 0, fmt.Errorf("%w: el inicio del fragmento (%.1fs) está fuera del medio (%.1fs)", ErrInvalidRender, start, mediaDuration)
	}
	if duration <= 0 {
		if mediaDuration > 0 {
			duration = mediaDuration - start
		}
		return start, duration, nil
	}
	if mediaDuration > 0 && start+duration > mediaDuration {
		duration = mediaDuration - start
	}
	// Un fragmento de menos de un frame no se puede codificar.
	if duration < 0.1 {
		return 0, 0, fmt.Errorf("%w: el fragmento es demasiado corto para renderizar", ErrInvalidRender)
	}
	return start, duration, nil
}

func userFacingRenderError(err error) string {
	if errors.Is(err, domain.ErrRenderUnavailable) {
		return "ffmpeg no está disponible en el servidor"
	}
	if errors.Is(err, domain.ErrNoLyrics) {
		return domain.ErrNoLyrics.Error()
	}
	message := err.Error()
	if index := strings.Index(message, ": ["); index > 0 {
		message = message[:index]
	}
	if len(message) > 400 {
		message = message[:400]
	}
	return message
}

func newJobID() string {
	buffer := make([]byte, 4)
	if _, err := rand.Read(buffer); err != nil {
		return time.Now().UTC().Format("20060102-150405")
	}
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(buffer)
}
