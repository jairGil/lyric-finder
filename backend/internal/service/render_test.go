package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lyrics/internal/domain"
	"lyrics/internal/ports"
	"lyrics/internal/storage"
)

func syncedLyrics() domain.Lyrics {
	return domain.Lyrics{
		Track:        domain.Track{Title: "Prueba", Artist: "Artista", Duration: 10},
		SyncedLyrics: []domain.LyricLine{{Text: "hola mundo", Start: 0, End: 2}},
		Synchronized: true,
	}
}

// audioProbe simula un ffprobe que sí reconoce audio, como el real.
type audioProbe struct{ err error }

func (probe audioProbe) Probe(context.Context, string) (domain.MediaInfo, error) {
	if probe.err != nil {
		return domain.MediaInfo{}, probe.err
	}
	return domain.MediaInfo{HasAudio: true, Duration: 10}, nil
}

type stubProvider struct {
	lyrics domain.Lyrics
	err    error
}

func (provider stubProvider) GetLyrics(context.Context, string, string) (domain.Lyrics, error) {
	return provider.lyrics, provider.err
}

func (provider stubProvider) Search(context.Context, string, string) ([]domain.SearchResult, error) {
	return nil, nil
}

type stubRenderer struct {
	available  error
	renderErr  error
	specs      []domain.RenderSpec
	draftSpecs []domain.RenderSpec
	draftErr   error
	progressTo []float64
}

func (renderer *stubRenderer) Render(_ context.Context, _ string, _ domain.Lyrics, spec domain.RenderSpec, onProgress func(float64)) error {
	renderer.specs = append(renderer.specs, spec)
	for _, value := range renderer.progressTo {
		onProgress(value)
	}
	return renderer.renderErr
}

// RenderDraft imita al engine escribiendo un archivo mínimo, porque Preview
// devuelve los bytes del MP4 y no solo un error.
func (renderer *stubRenderer) RenderDraft(_ context.Context, _ domain.Lyrics, spec domain.RenderSpec) error {
	renderer.draftSpecs = append(renderer.draftSpecs, spec)
	if renderer.draftErr != nil {
		return renderer.draftErr
	}
	return os.WriteFile(spec.OutputPath, []byte("borrador"), 0o600)
}

func (renderer *stubRenderer) Available() error { return renderer.available }

type stubProbe struct {
	info domain.MediaInfo
	err  error
}

func (probe stubProbe) Probe(context.Context, string) (domain.MediaInfo, error) {
	return probe.info, probe.err
}

// mediaAndBackgroundProbe da duraciones distintas al medio y al video de fondo,
// que es lo que hace falta para probar que la ventana no se acorta con el fondo.
type mediaAndBackgroundProbe struct {
	song       float64
	background float64
}

func (probe mediaAndBackgroundProbe) Probe(_ context.Context, path string) (domain.MediaInfo, error) {
	if strings.Contains(filepath.Base(path), "bg") {
		return domain.MediaInfo{HasVideo: true, Duration: probe.background}, nil
	}
	return domain.MediaInfo{HasAudio: true, Duration: probe.song}, nil
}

// stubSource imita una fuente externa sin tocar la red: devuelve una ruta ya
// escrita y cuenta cuántas veces le pidieron el audio, que es lo que permite
// comprobar que la caché no se vuelve a pedir en cada borrador.
type stubSource struct {
	path      string
	info      domain.SourceInfo
	available error
	downloads int
	describes int
}

func (source *stubSource) DownloadAudio(context.Context, string) (string, error) {
	source.downloads++
	if source.path == "" {
		return "", errors.New("sin fuente")
	}
	return source.path, nil
}

func (source *stubSource) Describe(context.Context, string) (domain.SourceInfo, error) {
	source.describes++
	return source.info, source.available
}

func (source *stubSource) Available() error { return source.available }

func newTestService(t *testing.T, renderer domain.Renderer, provider stubProvider) *RenderService {
	t.Helper()
	return newTestServiceWithProbe(t, renderer, provider, audioProbe{})
}

func newTestServiceWithProbe(t *testing.T, renderer domain.Renderer, provider stubProvider, probe domain.MediaProbe) *RenderService {
	t.Helper()
	return newTestServiceWith(t, renderer, provider, probe, nil)
}

func newTestServiceWith(t *testing.T, renderer domain.Renderer, provider stubProvider, probe domain.MediaProbe, source domain.AudioSource) *RenderService {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	service, err := NewRenderService(
		provider,
		renderer,
		probe,
		source,
		store,
		filepath.Join(t.TempDir(), "renders"),
		log.New(io.Discard, "", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestNewRenderServiceValidatesDependencies(t *testing.T) {
	renderer := &stubRenderer{}
	probe := stubProbe{}
	store, err := storage.Open(filepath.Join(t.TempDir(), "deps.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	cases := []struct {
		name     string
		provider domain.LyricsProvider
		renderer domain.Renderer
		probe    domain.MediaProbe
		store    ports.RenderRepository
	}{
		{"sin provider", nil, renderer, probe, store},
		{"sin renderer", stubProvider{}, nil, probe, store},
		{"sin probe", stubProvider{}, renderer, nil, store},
		{"sin store", stubProvider{}, renderer, probe, nil},
	}
	for _, testCase := range cases {
		if _, err := NewRenderService(testCase.provider, testCase.renderer, testCase.probe, nil, testCase.store, t.TempDir(), log.New(io.Discard, "", 0)); err == nil {
			t.Errorf("%s: esperaba error", testCase.name)
		}
	}
}

func TestNewRenderServiceFailsWhenRendererUnavailable(t *testing.T) {
	renderer := &stubRenderer{available: domain.ErrRenderUnavailable}
	store, err := storage.Open(filepath.Join(t.TempDir(), "unavailable.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_, err = NewRenderService(stubProvider{}, renderer, stubProbe{}, nil, store, t.TempDir(), log.New(io.Discard, "", 0))
	if !errors.Is(err, domain.ErrRenderUnavailable) {
		t.Errorf("err = %v, want ErrRenderUnavailable", err)
	}
}

func TestQueueRejectsMissingArtistOrTrack(t *testing.T) {
	service := newTestService(t, &stubRenderer{}, stubProvider{lyrics: syncedLyrics()})

	cases := []struct {
		artist string
		track  string
	}{
		{"", "Prueba"},
		{"Artista", ""},
		{"   ", "Prueba"},
	}
	for _, testCase := range cases {
		_, err := service.Queue(context.Background(), RenderUpload{
			Artist: testCase.artist,
			Track:  testCase.track,
			Media:  bytes.NewReader([]byte("data")),
		})
		if !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("Queue(%q,%q) err = %v, want ErrInvalidQuery", testCase.artist, testCase.track, err)
		}
	}
}

// Sin medio el render es la letra sobre el fondo: debe aceptarse.
func TestQueueAcceptsRenderWithoutMedia(t *testing.T) {
	service := newTestService(t, &stubRenderer{}, stubProvider{lyrics: syncedLyrics()})

	job, err := service.Queue(context.Background(), RenderUpload{Artist: "Artista", Track: "Prueba"})
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != domain.RenderQueued {
		t.Errorf("status = %q, want %q", job.Status, domain.RenderQueued)
	}
}

func TestQueueUsesTheExternalSourceAsAudioTrack(t *testing.T) {
	audio := filepath.Join(t.TempDir(), "audio.m4a")
	if err := os.WriteFile(audio, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := &stubSource{path: audio}
	service := newTestServiceWith(t, &stubRenderer{}, stubProvider{lyrics: syncedLyrics()}, audioProbe{}, source)

	spec, _, err := service.prepareUpload(context.Background(), RenderUpload{
		Artist:    "Artista",
		Track:     "Prueba",
		SourceURL: "https://youtu.be/dQw4w9WgXcQ",
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if spec.MediaPath != audio {
		t.Errorf("MediaPath = %q, want %q", spec.MediaPath, audio)
	}
	// La fuente trae solo audio: el fondo elegido sigue siendo la escena y el
	// motor no tiene que hacer overlay de ningún video.
	if spec.MediaHasVideo {
		t.Error("MediaHasVideo = true, want false con una fuente de solo audio")
	}
	if source.downloads != 1 {
		t.Errorf("downloads = %d, want 1", source.downloads)
	}
	// Sin medio, la base del recorte es la línea de tiempo; con el audio de la
	// fuente, es la duración real del video.
	if spec.ClipDuration != 10 {
		t.Errorf("ClipDuration = %v, want 10 (duración del medio)", spec.ClipDuration)
	}
}

func TestPrepareUploadPrefersTheUploadedFileOverTheSource(t *testing.T) {
	audio := filepath.Join(t.TempDir(), "audio.m4a")
	if err := os.WriteFile(audio, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := &stubSource{path: filepath.Join(t.TempDir(), "otro.m4a")}
	service := newTestServiceWith(t, &stubRenderer{}, stubProvider{lyrics: syncedLyrics()}, audioProbe{}, source)

	spec, _, err := service.prepareUpload(context.Background(), RenderUpload{
		Artist:    "Artista",
		Track:     "Prueba",
		Media:     strings.NewReader("no soy un archivo de audio real"),
		SourceURL: "https://youtu.be/dQw4w9WgXcQ",
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if spec.MediaPath == source.path {
		t.Error("MediaPath = la ruta de la fuente, want el archivo subido")
	}
	if source.downloads != 0 {
		t.Errorf("downloads = %d, want 0: con archivo no se baja nada", source.downloads)
	}
}

func TestQueueWithoutSourceSupportRejectsOnlyTheLink(t *testing.T) {
	service := newTestService(t, &stubRenderer{}, stubProvider{lyrics: syncedLyrics()})

	_, err := service.Queue(context.Background(), RenderUpload{
		Artist:    "Artista",
		Track:     "Prueba",
		SourceURL: "https://youtu.be/dQw4w9WgXcQ",
	})
	if !errors.Is(err, ErrSourceNotSupported) {
		t.Errorf("con link: err = %v, want ErrSourceNotSupported", err)
	}

	// Sin link, el render de siempre tiene que seguir andando.
	if _, err := service.Queue(context.Background(), RenderUpload{Artist: "Artista", Track: "Prueba"}); err != nil {
		t.Errorf("sin link: err = %v, want nil", err)
	}
}

func TestMediaDisplayNamePrefersTheFileThenTheLink(t *testing.T) {
	if got := mediaDisplayName(RenderUpload{MediaName: "cancion.mp3"}); got != "cancion.mp3" {
		t.Errorf("con archivo: %q, want %q", got, "cancion.mp3")
	}
	if got := mediaDisplayName(RenderUpload{SourceURL: "  https://youtu.be/dQw4w9WgXcQ  "}); got != "https://youtu.be/dQw4w9WgXcQ" {
		t.Errorf("con link: %q, want el link recortado", got)
	}
	long := "https://youtu.be/" + strings.Repeat("a", 200)
	if got := mediaDisplayName(RenderUpload{SourceURL: long}); len([]rune(got)) != maxDisplayName+1 {
		t.Errorf("link largo: %d caracteres, want %d", len([]rune(got)), maxDisplayName+1)
	}
	if got := mediaDisplayName(RenderUpload{}); got != "" {
		t.Errorf("sin medio: %q, want vacío", got)
	}
}

func TestDescribeSourceDelegatesAndReportsMissingSupport(t *testing.T) {
	source := &stubSource{info: domain.SourceInfo{ID: "dQw4w9WgXcQ", Title: "Prueba", Duration: 252}}
	service := newTestServiceWith(t, &stubRenderer{}, stubProvider{lyrics: syncedLyrics()}, audioProbe{}, source)

	info, err := service.DescribeSource(context.Background(), "https://youtu.be/dQw4w9WgXcQ")
	if err != nil {
		t.Fatal(err)
	}
	if info.Duration != 252 || info.Title != "Prueba" {
		t.Errorf("info = %+v, want la ficha del stub", info)
	}

	sin := newTestService(t, &stubRenderer{}, stubProvider{lyrics: syncedLyrics()})
	if _, err := sin.DescribeSource(context.Background(), "https://youtu.be/dQw4w9WgXcQ"); !errors.Is(err, ErrSourceNotSupported) {
		t.Errorf("sin soporte: err = %v, want ErrSourceNotSupported", err)
	}
}

func TestTimelineLengthUsesTrackDuration(t *testing.T) {
	lyrics := domain.Lyrics{
		Track:        domain.Track{Duration: 30},
		Synchronized: true,
		SyncedLyrics: []domain.LyricLine{{Text: "corta", Start: 0, End: 4}},
	}
	if got := timelineLength(lyrics); got != 31.5 {
		t.Errorf("timelineLength = %v, want 31.5 (duración de la pista + margen)", got)
	}
}

func TestTimelineLengthFallsBackToLastLine(t *testing.T) {
	lyrics := domain.Lyrics{
		Synchronized: true,
		SyncedLyrics: []domain.LyricLine{{Text: "a", Start: 0, End: 6}, {Text: "b", Start: 6, End: 9.5}},
	}
	// La última línea acaba en 9.5, más el margen: gana sobre la pista sin duración.
	if got := timelineLength(lyrics); got != 11 {
		t.Errorf("timelineLength = %v, want 11 (última línea 9.5 + margen)", got)
	}
}

func TestTimelineLengthIsZeroWithoutAnyTiming(t *testing.T) {
	if got := timelineLength(domain.Lyrics{Synchronized: true}); got != 0 {
		t.Errorf("timelineLength = %v, want 0", got)
	}
}

// Sin medio y sin final de letra no hay nada que acotar el render, así que se
// rechaza en el service en vez de encolar un job que no terminaría nunca.
func TestQueueWithoutMediaRejectsClipWithNoLength(t *testing.T) {
	service := newTestService(t, &stubRenderer{}, stubProvider{lyrics: domain.Lyrics{
		Synchronized: true,
		SyncedLyrics: []domain.LyricLine{{Text: "sin final", Start: 0, End: 0}},
	}})

	_, err := service.Queue(context.Background(), RenderUpload{Artist: "Artista", Track: "Prueba"})
	if !errors.Is(err, ErrInvalidRender) {
		t.Errorf("err = %v, want ErrInvalidRender cuando no hay medio ni final de letra", err)
	}
}

// El borrador se corta a maxDraftSeconds: una canción de cuatro minutos se
// tardaría demasiado en verse, y lo que sirve del preview es el aspecto.
func TestPreviewCapsLongFragments(t *testing.T) {
	long := syncedLyrics()
	long.Track.Duration = 240
	renderer := &stubRenderer{}
	service := newTestService(t, renderer, stubProvider{lyrics: long})

	if _, err := service.Preview(context.Background(), RenderUpload{Artist: "Artista", Track: "Prueba"}); err != nil {
		t.Fatal(err)
	}

	spec := renderer.draftSpecs[0]
	if spec.ClipDuration != maxDraftSeconds {
		t.Errorf("ClipDuration = %v, want %v", spec.ClipDuration, maxDraftSeconds)
	}
	if spec.OutputPath == "" {
		t.Error("el borrador necesita OutputPath")
	}
	if spec.Artist != "Artista" || spec.Track != "Prueba" {
		t.Errorf("spec = %q - %q, want Artista - Prueba", spec.Artist, spec.Track)
	}
}

func TestPreviewKeepsShortFragment(t *testing.T) {
	renderer := &stubRenderer{}
	service := newTestService(t, renderer, stubProvider{lyrics: syncedLyrics()})

	if _, err := service.Preview(context.Background(), RenderUpload{
		Artist: "Artista", Track: "Prueba", Start: 3, Duration: 5,
	}); err != nil {
		t.Fatal(err)
	}

	spec := renderer.draftSpecs[0]
	if spec.ClipStart != 3 || spec.ClipDuration != 5 {
		t.Errorf("clip = %v+%v, want 3+5", spec.ClipStart, spec.ClipDuration)
	}
}

// El borrador se valida igual que el render: si el fondo está mal, tampoco hay
// preview, porque mostrar algo que el servidor no va a exportar engaña.
func TestPreviewRejectsSameMistakesAsQueue(t *testing.T) {
	service := newTestService(t, &stubRenderer{}, stubProvider{lyrics: syncedLyrics()})

	cases := map[string]struct {
		upload RenderUpload
		want   error
	}{
		"sin artista": {RenderUpload{Track: "Prueba"}, ErrInvalidQuery},
		"fondo raro": {
			RenderUpload{Artist: "Artista", Track: "Prueba", Background: "plasma"},
			ErrInvalidRender,
		},
		"imagen que falta": {
			RenderUpload{Artist: "Artista", Track: "Prueba", Background: domain.BackgroundImage},
			ErrImageRequired,
		},
		"fragmento al final": {
			RenderUpload{Artist: "Artista", Track: "Prueba", Start: 9999, Duration: 5},
			ErrInvalidRender,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := service.Preview(context.Background(), testCase.upload)
			if !errors.Is(err, testCase.want) {
				t.Errorf("err = %v, want %v", err, testCase.want)
			}
		})
	}
}

// Un renderer sin soporte de borradores no rompe el render final: el preview
// dice que no está disponible.
func TestPreviewReportsUnsupportedRenderer(t *testing.T) {
	service := newTestService(t, &plainRenderer{}, stubProvider{lyrics: syncedLyrics()})

	_, err := service.Preview(context.Background(), RenderUpload{Artist: "Artista", Track: "Prueba"})
	if !errors.Is(err, ErrDraftUnavailable) {
		t.Errorf("err = %v, want ErrDraftUnavailable", err)
	}
}

// El borrador no debe dejar directorios en el directorio de renders.
func TestPreviewCleansUpItsWorkspace(t *testing.T) {
	service := newTestService(t, &stubRenderer{}, stubProvider{lyrics: syncedLyrics()})

	if _, err := service.Preview(context.Background(), RenderUpload{Artist: "Artista", Track: "Prueba"}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(service.baseDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "borrador-") {
			t.Errorf("quedó %q en el directorio de renders", entry.Name())
		}
	}
}

type plainRenderer struct{}

func (plainRenderer) Render(context.Context, string, domain.Lyrics, domain.RenderSpec, func(float64)) error {
	return nil
}
func (plainRenderer) Available() error { return nil }

func TestQueueRejectsImageBackgroundWithoutImage(t *testing.T) {
	service := newTestService(t, &stubRenderer{}, stubProvider{lyrics: syncedLyrics()})

	_, err := service.Queue(context.Background(), RenderUpload{
		Artist:     "Artista",
		Track:      "Prueba",
		Background: domain.BackgroundImage,
		Media:      bytes.NewReader([]byte("data")),
	})
	if !errors.Is(err, ErrImageRequired) {
		t.Errorf("err = %v, want ErrImageRequired", err)
	}
}

func TestQueueRejectsUnknownBackground(t *testing.T) {
	service := newTestService(t, &stubRenderer{}, stubProvider{lyrics: syncedLyrics()})

	_, err := service.Queue(context.Background(), RenderUpload{
		Artist:     "Artista",
		Track:      "Prueba",
		Background: domain.BackgroundKind("gif"),
		Media:      bytes.NewReader([]byte("data")),
	})
	if !errors.Is(err, ErrInvalidRender) {
		t.Errorf("err = %v, want ErrInvalidRender con fondo desconocido", err)
	}
}

func TestQueueDefaultsEmptyBackgroundToColor(t *testing.T) {
	renderer := &stubRenderer{}
	service := newTestService(t, renderer, stubProvider{lyrics: syncedLyrics()})

	job, err := service.Queue(context.Background(), RenderUpload{
		Artist: "Artista",
		Track:  "Prueba",
		Media:  bytes.NewReader([]byte("ID3\x03\x00\x00\x00\x00\x00\x00data")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != domain.RenderQueued {
		t.Errorf("status = %q, want %q", job.Status, domain.RenderQueued)
	}
}

func TestQueueRejectsUnsynchronizedLyrics(t *testing.T) {
	provider := stubProvider{lyrics: domain.Lyrics{PlainLyrics: "sin tiempo"}}
	service := newTestService(t, &stubRenderer{}, provider)

	_, err := service.Queue(context.Background(), RenderUpload{
		Artist: "Artista",
		Track:  "Prueba",
		Media:  bytes.NewReader([]byte("data")),
	})
	if !errors.Is(err, domain.ErrNoLyrics) {
		t.Errorf("err = %v, want ErrNoLyrics", err)
	}
}

func TestQueueRejectsEmptyUpload(t *testing.T) {
	service := newTestService(t, &stubRenderer{}, stubProvider{lyrics: syncedLyrics()})

	_, err := service.Queue(context.Background(), RenderUpload{
		Artist: "Artista",
		Track:  "Prueba",
		Media:  bytes.NewReader(nil),
	})
	if !errors.Is(err, ErrUnsupportedMedia) {
		t.Errorf("err = %v, want ErrUnsupportedMedia para archivo vacío", err)
	}
}

func TestQueueRejectsMediaWithoutAudioAsClientError(t *testing.T) {
	// El ffprobe real acepta el archivo pero no encuentra pista de audio: eso es
	// un error del cliente y debe llegar envuelto en ErrUnsupportedMedia, o el
	// handler lo responde 500 en lugar de 400.
	service := newTestServiceWithProbe(t, &stubRenderer{}, stubProvider{lyrics: syncedLyrics()},
		stubProbe{info: domain.MediaInfo{HasVideo: true}})

	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13, 'I', 'H', 'D', 'R'}
	_, err := service.Queue(context.Background(), RenderUpload{
		Artist: "Artista",
		Track:  "Prueba",
		Media:  bytes.NewReader(png),
	})
	if !errors.Is(err, ErrUnsupportedMedia) {
		t.Errorf("err = %v, want ErrUnsupportedMedia cuando no hay pista de audio", err)
	}
}

func TestQueueRejectsMediaRejectedByProbe(t *testing.T) {
	service := newTestServiceWithProbe(t, &stubRenderer{}, stubProvider{lyrics: syncedLyrics()},
		stubProbe{err: errors.New("moov atom not found")})

	_, err := service.Queue(context.Background(), RenderUpload{
		Artist: "Artista",
		Track:  "Prueba",
		Media:  bytes.NewReader([]byte("esto es texto plano")),
	})
	if !errors.Is(err, ErrUnsupportedMedia) {
		t.Errorf("err = %v, want ErrUnsupportedMedia para texto plano", err)
	}
}

func TestQueueCleansUpJobDirWhenRejected(t *testing.T) {
	renderer := &stubRenderer{}
	service := newTestServiceWithProbe(t, renderer, stubProvider{lyrics: syncedLyrics()},
		stubProbe{info: domain.MediaInfo{HasVideo: true}})

	_, err := service.Queue(context.Background(), RenderUpload{
		Artist: "Artista",
		Track:  "Prueba",
		Media:  bytes.NewReader(bytes.Repeat([]byte{0x49, 0x44, 0x33}, 4096)),
	})
	if err == nil {
		t.Fatal("esperaba rechazo por falta de audio")
	}

	entries, readErr := os.ReadDir(service.baseDir)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("quedaron %d directorios de jobs tras un rechazo: %v", len(entries), names)
	}
}

func TestQueueCleansUpJobDirWhenProbeFails(t *testing.T) {
	service := newTestServiceWithProbe(t, &stubRenderer{}, stubProvider{lyrics: syncedLyrics()},
		stubProbe{err: errors.New("formato desconocido")})

	_, err := service.Queue(context.Background(), RenderUpload{
		Artist: "Artista",
		Track:  "Prueba",
		Media:  bytes.NewReader(bytes.Repeat([]byte{0x49, 0x44, 0x33}, 4096)),
	})
	if err == nil {
		t.Fatal("esperaba error del probe")
	}

	entries, readErr := os.ReadDir(service.baseDir)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Errorf("quedaron %d directorios huérfanos", len(entries))
	}
}

func TestNormalizeDimension(t *testing.T) {
	cases := map[int]int{0: defaultWidth, -5: defaultWidth, 100: 320, 1920: 1920, 9999: 3840}
	for input, want := range cases {
		if got := normalizeDimension(input, defaultWidth); got != want {
			t.Errorf("normalizeDimension(%d) = %d, want %d", input, got, want)
		}
	}
}

func TestNormalizeFPS(t *testing.T) {
	cases := map[int]int{0: defaultFPS, -1: defaultFPS, 5: 12, 24: 24, 30: 30, 60: 60, 999: 60}
	for input, want := range cases {
		if got := normalizeFPS(input); got != want {
			t.Errorf("normalizeFPS(%d) = %d, want %d", input, got, want)
		}
	}
}

// Un archivo vacío no es ni foto ni video, así que se rechaza antes de escribir.
func TestPersistBackgroundRejectsEmptyFile(t *testing.T) {
	service := newTestService(t, &stubRenderer{}, stubProvider{})
	_, _, err := service.persistBackground(bytes.NewReader(nil), t.TempDir())
	if !errors.Is(err, ErrUnsupportedMedia) {
		t.Errorf("err = %v, want ErrUnsupportedMedia", err)
	}
}

// El fondo decide foto o video por la cabecera, y esa decisión tiene que ser
// Ciega al contenedor: la mayoría de los MP4 reales se detectan como
// application/octet-stream, así que si el content type decidiera, el video de
// una cámara se trataría como foto y saldría congelado.
func TestPersistBackgroundTreatsUnsniffedFileAsVideo(t *testing.T) {
	service := newTestService(t, &stubRenderer{}, stubProvider{})

	// http.DetectContentType decide solo con la cabecera, así que alcanza con los
	// primeros bytes de cada formato y no hace falta un archivo real en disco.
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	mp4 := append([]byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}, make([]byte, 64)...)

	if _, isVideo, err := service.persistBackground(bytes.NewReader(png), t.TempDir()); err != nil {
		t.Fatalf("persistBackground(foto): %v", err)
	} else if isVideo {
		t.Error("una foto no debería tratarse como video")
	}

	// El mismo MP4 que http.DetectContentType no reconoce tiene que salir como
	// video, no como foto.
	if got := http.DetectContentType(mp4); strings.HasPrefix(got, "video/") {
		t.Skipf("esta versión de Go ya detecta %q, el test no aplica", got)
	}
	if _, isVideo, err := service.persistBackground(bytes.NewReader(mp4), t.TempDir()); err != nil {
		t.Fatalf("persistBackground(video): %v", err)
	} else if !isVideo {
		t.Error("un MP4 que la cabecera no reconoce igual tiene que tratarse como video")
	}
}

// Un fondo que el sonda no puede leer se rechaza con un error de formato, no con
// un fallo de ffmpeg más adelante.
func TestAttachBackgroundRejectsUnreadableFile(t *testing.T) {
	service := newTestServiceWithProbe(t, &stubRenderer{}, stubProvider{},
		stubProbe{err: errors.New("moov atom not found")})
	spec := domain.RenderSpec{BackgroundIsVideo: true}

	err := service.attachBackground(context.Background(), &spec, "bg.mp4")
	if !errors.Is(err, ErrUnsupportedMedia) {
		t.Errorf("err = %v, want ErrUnsupportedMedia", err)
	}
}

// El video de fondo tops el clip: sin su duración el ffmpeg se quedaría sin
// fondo a mitad del video.
func TestAttachBackgroundRejectsVideoWithoutDuration(t *testing.T) {
	service := newTestServiceWithProbe(t, &stubRenderer{}, stubProvider{},
		stubProbe{info: domain.MediaInfo{HasVideo: true}})
	spec := domain.RenderSpec{BackgroundIsVideo: true}

	if err := service.attachBackground(context.Background(), &spec, "bg.mp4"); !errors.Is(err, ErrUnsupportedMedia) {
		t.Errorf("err = %v, want ErrUnsupportedMedia", err)
	}
}

// Un video de fondo de 20s no puede acortar una canción de 200s: el fragmento se
// elige en toda la línea de tiempo y el fondo se repite para cubrirlo. Lo único que
// tops el video es el largo del clip.
func TestPrepareUploadLetsTheClipTravelTheWholeSongWithAShortVideo(t *testing.T) {
	service := newTestServiceWithProbe(t, &stubRenderer{}, stubProvider{lyrics: syncedLyrics()},
		mediaAndBackgroundProbe{song: 200, background: 20})

	spec, _, err := service.prepareUpload(context.Background(), RenderUpload{
		Artist:     "Artista",
		Track:      "Prueba",
		Background: domain.BackgroundImage,
		Image:      strings.NewReader("un video de fondo cualquiera"),
		Media:      strings.NewReader("audio"),
		Start:      100,
		Duration:   15,
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if spec.BackgroundDuration != 20 {
		t.Fatalf("BackgroundDuration = %v, want 20", spec.BackgroundDuration)
	}
	if spec.ClipStart != 100 {
		t.Errorf("ClipStart = %v, want 100: el video de fondo no tops dónde empieza el clip", spec.ClipStart)
	}
	if spec.ClipDuration != 15 {
		t.Errorf("ClipDuration = %v, want 15", spec.ClipDuration)
	}

	// Lo que sí tops el video es el largo: pedir 45s con un fondo de 20s sale de 20s.
	spec, _, err = service.prepareUpload(context.Background(), RenderUpload{
		Artist:     "Artista",
		Track:      "Prueba",
		Background: domain.BackgroundImage,
		Image:      strings.NewReader("un video de fondo cualquiera"),
		Media:      strings.NewReader("audio"),
		Start:      100,
		Duration:   45,
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if spec.ClipStart != 100 || spec.ClipDuration != 20 {
		t.Errorf("clip = (%.0f, %.0f), want (100, 20): el largo lo tops el video de fondo", spec.ClipStart, spec.ClipDuration)
	}
}

func TestPersistMediaKeepsEveryByteWhateverTheContainer(t *testing.T) {
	// La extensión solo se usa por comodidad: ffprobe y ffmpeg detectan el
	// contenedor por contenido, así que un .bin desconocido debe seguir siendo
	// reproducible. Lo que no se negocia es que el archivo se escriba completo.
	service := newTestService(t, &stubRenderer{}, stubProvider{})
	body := bytes.Repeat([]byte{0x11}, 4096)

	cases := []struct {
		name   string
		header []byte
	}{
		{"mp3 con cabecera ID3", []byte("ID3\x03\x00\x00\x00\x00\x00\x00")},
		{"wav con RIFF", append([]byte("RIFF\x24\x08\x00\x00WAVEfmt "), make([]byte, 16)...)},
		{"mp4 con caja ftyp", append([]byte{0, 0, 0, 0x20}, []byte("ftypisom\x00\x00\x02\x00")...)},
		{"contenedor desconocido", []byte{0x01, 0x02, 0x03, 0x04}},
	}

	seen := map[string]bool{}
	for _, testCase := range cases {
		dir := t.TempDir()
		payload := append(append([]byte{}, testCase.header...), body...)

		path, err := service.persistMedia(bytes.NewReader(payload), dir)
		if err != nil {
			t.Fatalf("%s: %v", testCase.name, err)
		}

		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s: %v", testCase.name, err)
		}
		if info.Size() != int64(len(payload)) {
			t.Errorf("%s: tamaño = %d, want %d (archivo truncado)", testCase.name, info.Size(), len(payload))
		}

		written, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(written, payload) {
			t.Errorf("%s: el contenido escrito difiere del enviado", testCase.name)
		}

		detected := http.DetectContentType(written)
		extension := unknownExtension
		if known, ok := mediaExtensions[detected]; ok {
			extension = known
		}
		if filepath.Ext(path) != extension {
			t.Errorf("%s: extensión = %q, want %q para %s", testCase.name, filepath.Ext(path), extension, detected)
		}
		seen[detected] = true
	}

	if len(seen) < 2 {
		t.Errorf("las fixtures no exercitan detecciones distintas: %v", seen)
	}
}

func TestUserFacingRenderErrorStripsFFmpegNoise(t *testing.T) {
	message := userFacingRenderError(errors.New("ffmpeg terminó con error: exit status 1: [Parsed_ass @ 0x1] algo"))
	if len(message) > 200 {
		t.Errorf("mensaje demasiado largo: %q", message)
	}
	if message == "" {
		t.Error("mensaje vacío")
	}
}

func TestUserFacingRenderErrorTranslatesUnavailable(t *testing.T) {
	message := userFacingRenderError(domain.ErrRenderUnavailable)
	if message != "ffmpeg no está disponible en el servidor" {
		t.Errorf("mensaje = %q", message)
	}
}

func TestNewJobIDIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		id := newJobID()
		if seen[id] {
			t.Fatalf("id duplicado: %s", id)
		}
		seen[id] = true
	}
}

func TestResolveClipKeepsWindowInsideMedia(t *testing.T) {
	start, duration, err := resolveClip(10, 5, 30)
	if err != nil {
		t.Fatal(err)
	}
	if start != 10 || duration != 5 {
		t.Errorf("resolveClip = (%.1f, %.1f), want (10, 5)", start, duration)
	}
}

func TestResolveClipDefaultsToWholeMedia(t *testing.T) {
	start, duration, err := resolveClip(0, 0, 30)
	if err != nil {
		t.Fatal(err)
	}
	if start != 0 || duration != 30 {
		t.Errorf("resolveClip = (%.1f, %.1f), want (0, 30)", start, duration)
	}
}

func TestResolveClipTrimsDurationAtEndOfMedia(t *testing.T) {
	// El slider puede dejar el tirador más allá del final; se recorta en vez de
	// fallar, que es lo que espera quien arrastra hasta el borde.
	start, duration, err := resolveClip(25, 20, 30)
	if err != nil {
		t.Fatal(err)
	}
	if start != 25 || duration != 5 {
		t.Errorf("resolveClip = (%.1f, %.1f), want (25, 5)", start, duration)
	}
}

func TestResolveClipRejectsInvalidWindows(t *testing.T) {
	cases := []struct {
		name                   string
		start, duration, media float64
	}{
		{"inicio negativo", -1, 5, 30},
		{"inicio fuera del medio", 40, 5, 30},
		{"fragmento ridiculamente corto", 29.95, 0.01, 30},
	}
	for _, testCase := range cases {
		if _, _, err := resolveClip(testCase.start, testCase.duration, testCase.media); !errors.Is(err, ErrInvalidRender) {
			t.Errorf("%s: err = %v, want ErrInvalidRender", testCase.name, err)
		}
	}
}

func TestResolveClipRejectsNonFiniteValues(t *testing.T) {
	for _, testCase := range [][2]float64{
		{math.NaN(), 5},
		{5, math.NaN()},
		{math.Inf(1), 5},
		{5, math.Inf(1)},
	} {
		if _, _, err := resolveClip(testCase[0], testCase[1], 30); !errors.Is(err, ErrInvalidRender) {
			t.Errorf("resolveClip(%v, %v): err = %v, want ErrInvalidRender", testCase[0], testCase[1], err)
		}
	}
}
