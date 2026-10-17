package render

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"lyrics/internal/domain"
)

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(filepath.Join(root, "tools"), filepath.Join(root, "backend", "assets"), filepath.Join(t.TempDir(), "out"))
	if err := engine.Available(); err != nil {
		t.Skipf("ffmpeg no disponible: %v", err)
	}
	if err := os.MkdirAll(engine.workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	return engine
}

func makeFixture(t *testing.T, engine *Engine, name string, args ...string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), name)
	full := append([]string{"-hide_banner", "-y"}, args...)
	full = append(full, target)
	command := exec.Command(engine.binary, full...)
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		t.Fatalf("no se pudo crear el fixture %s: %v", name, err)
	}
	return target
}

func testLyrics() domain.Lyrics {
	return domain.Lyrics{
		Track: domain.Track{Title: "Prueba", Artist: "Artista"},
		SyncedLyrics: []domain.LyricLine{
			{Text: "primera linea de prueba", Start: 0.5, End: 2.5},
			{Text: "segunda linea {con} llaves", Start: 2.5, End: 4.5},
		},
		Synchronized: true,
	}
}

func probeStreamCount(t *testing.T, engine *Engine, path, entry string) int {
	t.Helper()
	output, err := exec.Command(engine.probe,
		"-v", "error",
		"-select_streams", entry,
		"-show_entries", "stream=codec_type",
		"-of", "csv=p=0",
		path,
	).Output()
	if err != nil {
		t.Fatalf("ffprobe falló: %v", err)
	}
	return len(strings.Fields(strings.TrimSpace(string(output))))
}

func TestRenderAudioOnlyWithColorBackground(t *testing.T) {
	engine := newTestEngine(t)
	media := makeFixture(t, engine, "audio.m4a",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=5", "-c:a", "aac", "-b:a", "128k",
	)

	spec := domain.RenderSpec{
		Artist:        "Artista",
		Track:         "Prueba",
		MediaPath:     media,
		MediaHasVideo: false,
		Background:    domain.BackgroundColor,
		Color:         "#4da3ff",
		Style:         domain.Style{Highlight: "word", Font: "inter", Align: "center"},
		Width:         640,
		Height:        360,
		FPS:           24,
	}

	var seen []float64
	err := engine.Render(context.Background(), "job-audio", testLyrics(), spec, func(p float64) {
		seen = append(seen, p)
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	output := filepath.Join(engine.workspace, "job-audio.mp4")
	info, err := os.Stat(output)
	if err != nil {
		t.Fatalf("no se creó el mp4: %v", err)
	}
	if info.Size() < 10<<10 {
		t.Errorf("mp4 sospechosamente pequeño: %d bytes", info.Size())
	}
	if got := probeStreamCount(t, engine, output, "v:0"); got != 1 {
		t.Errorf("esperaba 1 stream de video, obtuve %d", got)
	}
	if got := probeStreamCount(t, engine, output, "a:0"); got != 1 {
		t.Errorf("esperaba 1 stream de audio, obtuve %d", got)
	}
	if len(seen) == 0 {
		t.Error("no se reportó progreso")
	}
	if last := seen[len(seen)-1]; last != 1 {
		t.Errorf("el progreso final debería ser 1, obtuve %v", last)
	}
}

func TestRenderVideoWithImageBackground(t *testing.T) {
	engine := newTestEngine(t)
	video := makeFixture(t, engine, "video.mp4",
		"-f", "lavfi", "-i", "testsrc=size=640x480:rate=24:duration=5",
		"-f", "lavfi", "-i", "sine=frequency=330:duration=5",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest",
	)
	image := makeFixture(t, engine, "bg.png",
		"-f", "lavfi", "-i", "color=c=0x203040:s=1920x1080", "-frames:v", "1",
	)

	spec := domain.RenderSpec{
		Artist:         "Artista",
		Track:          "Prueba",
		MediaPath:      video,
		MediaHasVideo:  true,
		Background:     domain.BackgroundImage,
		BackgroundPath: image,
		Style:          domain.Style{Highlight: "line", Colors: "multi", Font: "consolas", Align: "left"},
		Width:          640,
		Height:         360,
		FPS:            24,
	}

	if err := engine.Render(context.Background(), "job-video", testLyrics(), spec, nil); err != nil {
		t.Fatalf("Render: %v", err)
	}

	output := filepath.Join(engine.workspace, "job-video.mp4")
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("no se creó el mp4: %v", err)
	}
	if got := probeStreamCount(t, engine, output, "a:0"); got != 1 {
		t.Errorf("esperaba 1 stream de audio, obtuve %d", got)
	}
}

// El fondo de imagen es un input más que el color, así que el índice del audio
// en silencio tiene que seguir siendo el correcto cuando no hay medio.
func TestRenderWithoutMediaUsesImageBackground(t *testing.T) {
	engine := newTestEngine(t)
	image := makeFixture(t, engine, "bg-solo.png",
		"-f", "lavfi", "-i", "color=c=0x203040:s=1920x1080", "-frames:v", "1",
	)

	spec := domain.RenderSpec{
		Artist:         "Artista",
		Track:          "Prueba",
		Background:     domain.BackgroundImage,
		BackgroundPath: image,
		Style:          domain.Style{Highlight: "line", Colors: "single", Font: "consolas", Align: "left"},
		Width:          640,
		Height:         360,
		FPS:            24,
		// El fondo y el silencio son infinitos: sin este tope no terminaría.
		ClipDuration: 4,
	}

	if err := engine.Render(context.Background(), "job-solo-imagen", testLyrics(), spec, nil); err != nil {
		t.Fatalf("Render: %v", err)
	}

	output := filepath.Join(engine.workspace, "job-solo-imagen.mp4")
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("no se creó el mp4: %v", err)
	}
	if got := probeStreamCount(t, engine, output, "v:0"); got != 1 {
		t.Errorf("esperaba 1 stream de video, obtuve %d", got)
	}
	if got := probeStreamCount(t, engine, output, "a:0"); got != 1 {
		t.Errorf("esperaba 1 stream de audio en silencio, obtuve %d", got)
	}
}

// Sin medio y sin tope de duración el grafo es infinito, así que el engine
// tiene que rechazarlo en vez de codificar hasta que lo cancelen.
func TestRenderWithoutMediaRejectsUnboundedClip(t *testing.T) {
	engine := newTestEngine(t)
	spec := domain.RenderSpec{
		Artist: "Artista",
		Track:  "Prueba",
		Style:  domain.Style{Highlight: "line", Colors: "single", Font: "consolas", Align: "left"},
		Width:  640,
		Height: 360,
		FPS:    24,
	}

	err := engine.Render(context.Background(), "job-solo-infinito", testLyrics(), spec, nil)
	if !errors.Is(err, domain.ErrNoLyrics) {
		t.Errorf("err = %v, want ErrNoLyrics por falta de duración", err)
	}
}

// El borrador tiene que verse igual que el final, solo que más chico: por eso se
// baja la resolución conservando la proporción. El audio no se descarta, porque
// el modal lo reproduce para juzgar el fragmento con sonido.
func TestRenderDraftShrinksAndKeepsAudio(t *testing.T) {
	engine := newTestEngine(t)
	media := makeFixture(t, engine, "audio.m4a",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=6",
		"-c:a", "aac", "-b:a", "192k",
	)
	spec := domain.RenderSpec{
		Artist:        "Artista",
		Track:         "Prueba",
		MediaPath:     media,
		MediaHasVideo: false,
		Background:    domain.BackgroundGradient,
		Color:         "#0b1020",
		ColorTwo:      "#3d1a5c",
		Style:         domain.Style{Highlight: "word", Colors: "multi", Font: "inter", Align: "center"},
		Width:         1080,
		Height:        1920,
		FPS:           30,
		ClipStart:     2,
		ClipDuration:  4,
		OutputPath:    filepath.Join(engine.workspace, "borrador.mp4"),
	}

	if err := engine.RenderDraft(context.Background(), testLyrics(), spec); err != nil {
		t.Fatalf("RenderDraft: %v", err)
	}

	if _, err := os.Stat(spec.OutputPath); err != nil {
		t.Fatalf("no se creó el borrador: %v", err)
	}
	// 1920 de lado largo contra el tope de 540 deja el ancho en 303.75, que al
	// truncar queda en 303 y hay que hacerlo par porque yuv420p no admite impar.
	dimensions := probeDimensions(t, engine, spec.OutputPath)
	if dimensions.width != 302 || dimensions.height != 540 {
		t.Errorf("dimensiones = %dx%d, want 302x540", dimensions.width, dimensions.height)
	}
	if got := probeStreamCount(t, engine, spec.OutputPath, "a:0"); got != 1 {
		t.Fatalf("el borrador debería llevar audio, hay %d streams", got)
	}
	// Que exista el stream no alcanza: si el borrador saliera mudo el modal
	// reproduciría algo sin sonido, que es justo lo que hay que evitar.
	if volume := meanVolume(t, engine, spec.OutputPath); volume < -50 {
		t.Errorf("el audio del borrador es silencio (mean_volume = %.1f dB)", volume)
	}
}

// Sin medio el borrador igual lleva pista de audio, en silencio, para no
// cambiar la forma del MP4 entre preview y render final.
func TestRenderDraftWithoutMediaKeepsSilentTrack(t *testing.T) {
	engine := newTestEngine(t)
	spec := domain.RenderSpec{
		Artist:       "Artista",
		Track:        "Prueba",
		Background:   domain.BackgroundColor,
		Color:        "#101018",
		Style:        domain.Style{Highlight: "line", Colors: "single", Font: "inter", Align: "center"},
		Width:        320,
		Height:       240,
		FPS:          24,
		ClipDuration: 2,
		OutputPath:   filepath.Join(engine.workspace, "borrador-sin-medio.mp4"),
	}

	if err := engine.RenderDraft(context.Background(), testLyrics(), spec); err != nil {
		t.Fatalf("RenderDraft: %v", err)
	}

	if got := probeStreamCount(t, engine, spec.OutputPath, "a:0"); got != 1 {
		t.Errorf("esperaba 1 stream de audio, obtuve %d", got)
	}
	if volume := meanVolume(t, engine, spec.OutputPath); volume > -50 {
		t.Errorf("sin medio el audio debería ser silencio, mean_volume = %.1f dB", volume)
	}
}

// meanVolume devuelve el volumen promedio en dB del primer flujo de audio, o un
// valor muy bajo si el archivo no tiene ninguno.
func meanVolume(t *testing.T, engine *Engine, path string) float64 {
	t.Helper()
	command := exec.Command(engine.binary,
		"-hide_banner", "-i", path,
		"-map", "a:0", "-af", "volumedetect", "-f", "null", "-",
	)
	output, err := command.CombinedOutput()
	if err != nil && len(output) == 0 {
		t.Fatalf("volumedetect falló: %v", err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.Contains(line, "mean_volume:") {
			continue
		}
		_, value, found := strings.Cut(line, "mean_volume:")
		if !found {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		number := strings.TrimSuffix(fields[0], "dB")
		parsed, convErr := strconv.ParseFloat(number, 64)
		if convErr != nil {
			t.Fatalf("no se pudo leer el volumen %q: %v", value, convErr)
		}
		return parsed
	}
	return -120
}

// Un fragmento chico no se agranda: el borrador sale del tamaño pedido.
func TestRenderDraftKeepsSmallDimensions(t *testing.T) {
	engine := newTestEngine(t)
	spec := domain.RenderSpec{
		Artist:       "Artista",
		Track:        "Prueba",
		Background:   domain.BackgroundColor,
		Style:        domain.Style{Highlight: "line", Colors: "single", Font: "inter", Align: "center"},
		Width:        320,
		Height:       240,
		FPS:          24,
		ClipDuration: 2,
		OutputPath:   filepath.Join(engine.workspace, "borrador-chico.mp4"),
	}

	if err := engine.RenderDraft(context.Background(), testLyrics(), spec); err != nil {
		t.Fatalf("RenderDraft: %v", err)
	}

	dimensions := probeDimensions(t, engine, spec.OutputPath)
	if dimensions.width != 320 || dimensions.height != 240 {
		t.Errorf("dimensiones = %dx%d, want 320x240 sin tocar", dimensions.width, dimensions.height)
	}
}

func TestRenderDraftRequiresOutputPath(t *testing.T) {
	engine := newTestEngine(t)
	err := engine.RenderDraft(context.Background(), testLyrics(), domain.RenderSpec{})
	if err == nil {
		t.Fatal("esperaba error por falta de OutputPath")
	}
}

type streamDimensions struct {
	width  int
	height int
}

func probeDimensions(t *testing.T, engine *Engine, path string) streamDimensions {
	t.Helper()
	output, err := exec.Command(engine.probe,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height",
		"-of", "csv=p=0",
		path,
	).Output()
	if err != nil {
		t.Fatalf("ffprobe falló: %v", err)
	}
	fields := strings.Split(strings.TrimSpace(string(output)), ",")
	if len(fields) < 2 {
		t.Fatalf("ffprobe no devolvió dimensiones: %q", output)
	}
	width, err := strconv.Atoi(strings.TrimSpace(fields[0]))
	if err != nil {
		t.Fatalf("ancho ilegible %q: %v", fields[0], err)
	}
	height, err := strconv.Atoi(strings.TrimSpace(fields[1]))
	if err != nil {
		t.Fatalf("alto ilegible %q: %v", fields[1], err)
	}
	return streamDimensions{width: width, height: height}
}

func TestRenderOutputIsExactlyRequestedResolution(t *testing.T) {
	engine := newTestEngine(t)
	media := makeFixture(t, engine, "audio.m4a",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=3", "-c:a", "aac",
	)

	spec := domain.RenderSpec{
		MediaPath:  media,
		Background: domain.BackgroundColor,
		Color:      "000000",
		Style:      domain.Style{Font: "inter"},
		Width:      1280,
		Height:     720,
		FPS:        30,
	}

	if err := engine.Render(context.Background(), "job-res", testLyrics(), spec, nil); err != nil {
		t.Fatalf("Render: %v", err)
	}

	output := filepath.Join(engine.workspace, "job-res.mp4")
	raw, err := exec.Command(engine.probe,
		"-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height",
		"-of", "csv=p=0", output,
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(raw)); got != "1280,720" {
		t.Errorf("resolución = %q, want 1280,720", got)
	}
}

func TestRenderRejectsMissingLyrics(t *testing.T) {
	engine := newTestEngine(t)
	media := makeFixture(t, engine, "audio.m4a",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-c:a", "aac",
	)

	spec := domain.RenderSpec{
		MediaPath:  media,
		Background: domain.BackgroundColor,
		Color:      "000000",
		Width:      320,
		Height:     180,
		FPS:        24,
	}

	err := engine.Render(context.Background(), "job-empty", domain.Lyrics{}, spec, nil)
	if err == nil || !strings.Contains(err.Error(), "letra sincronizada") {
		t.Errorf("esperaba ErrNoLyrics, obtuve %v", err)
	}
}

func TestProbeDuration(t *testing.T) {
	engine := newTestEngine(t)
	media := makeFixture(t, engine, "audio.m4a",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=7.5", "-c:a", "aac",
	)

	got, err := engine.probeDuration(context.Background(), media)
	if err != nil {
		t.Fatal(err)
	}
	if delta := got - 7.5; delta > 0.3 || delta < -0.3 {
		t.Errorf("duración = %v, want ~7.5", got)
	}
}

func TestRenderOutputLengthMatchesMediaNotLyrics(t *testing.T) {
	// El ASS se genera desde la letra completa, que suele ser mucho más larga
	// que el medio recortado. El video debe terminar con el medio, no con la
	// letra ni unos segundos más por culpa de -shortest.
	engine := newTestEngine(t)
	media := makeFixture(t, engine, "audio.m4a",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=6", "-c:a", "aac",
	)

	lyrics := domain.Lyrics{Synchronized: true, SyncedLyrics: []domain.LyricLine{
		{Text: "primera linea", Start: 0, End: 3},
		{Text: "segunda linea", Start: 3, End: 6},
		{Text: "linea muy posterior", Start: 600, End: 620},
	}}

	spec := domain.RenderSpec{
		MediaPath:  media,
		Background: domain.BackgroundColor,
		Color:      "101820",
		Width:      480,
		Height:     270,
		FPS:        30,
	}
	output := filepath.Join(t.TempDir(), "out.mp4")
	spec.OutputPath = output
	if err := engine.Render(context.Background(), "job", lyrics, spec, nil); err != nil {
		t.Fatal(err)
	}

	duration := probeStreamValue(t, engine, output, "v:0", "duration")
	if duration < 5.5 || duration > 6.5 {
		t.Errorf("duración del video = %v, want ~6 (la duración del medio)", duration)
	}
}

func TestRenderOutputUsesRequestedFrameRate(t *testing.T) {
	engine := newTestEngine(t)
	media := makeFixture(t, engine, "audio.m4a",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4", "-c:a", "aac",
	)
	lyrics := domain.Lyrics{Synchronized: true, SyncedLyrics: []domain.LyricLine{
		{Text: "hola", Start: 0, End: 4},
	}}

	spec := domain.RenderSpec{
		MediaPath:  media,
		Background: domain.BackgroundColor,
		Color:      "101820",
		Width:      480,
		Height:     270,
		FPS:        24,
	}
	output := filepath.Join(t.TempDir(), "out.mp4")
	spec.OutputPath = output
	if err := engine.Render(context.Background(), "job", lyrics, spec, nil); err != nil {
		t.Fatal(err)
	}

	if got := probeFrameRate(t, engine, output); math.Abs(got-24) > 0.01 {
		t.Errorf("r_frame_rate = %v, want 24", got)
	}
	frames := probeStreamValue(t, engine, output, "v:0", "nb_read_frames")
	if frames > 24*6 {
		t.Errorf("frames = %v, demasiado para 4s a 24fps", frames)
	}
}

// probeFrameRate lee r_frame_rate, que ffprobe reporta como fracción.
func probeFrameRate(t *testing.T, engine *Engine, path string) float64 {
	t.Helper()
	probe := strings.TrimSuffix(engine.Binary(), "ffmpeg.exe") + "ffprobe.exe"
	output, err := exec.Command(probe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=r_frame_rate", "-of", "csv=p=0", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe r_frame_rate: %v: %s", err, output)
	}
	raw := strings.TrimSpace(string(output))
	numerator, denominator, found := strings.Cut(raw, "/")
	if !found {
		t.Fatalf("r_frame_rate inesperado: %q", raw)
	}
	top, err := strconv.ParseFloat(numerator, 64)
	if err != nil {
		t.Fatal(err)
	}
	bottom, err := strconv.ParseFloat(denominator, 64)
	if err != nil {
		t.Fatal(err)
	}
	if bottom == 0 {
		t.Fatalf("r_frame_rate con denominador cero: %q", raw)
	}
	return top / bottom
}

func probeStreamValue(t *testing.T, engine *Engine, path, stream, field string) float64 {
	t.Helper()
	probe := strings.TrimSuffix(engine.Binary(), "ffmpeg.exe") + "ffprobe.exe"
	arguments := []string{"-v", "error", "-select_streams", stream, "-count_frames",
		"-show_entries", "stream=" + field, "-of", "csv=p=0", path}
	output, err := exec.Command(probe, arguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe %s: %v: %s", field, err, output)
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		t.Fatalf("no pude interpretar %s = %q: %v", field, output, err)
	}
	return value
}

func TestAvailableReportsMissingBinary(t *testing.T) {
	engine := NewEngine(filepath.Join(t.TempDir(), "nope"), t.TempDir(), t.TempDir())
	if err := engine.Available(); err == nil {
		t.Fatal("esperaba error cuando ffmpeg no existe")
	} else if !strings.Contains(err.Error(), "ffmpeg") {
		t.Errorf("el error debería mencionar ffmpeg, obtuve %v", err)
	}
}

func TestScanProgressParsesFfmpegTime(t *testing.T) {
	sample := strings.Join([]string{
		"frame=  100 fps=25 q=28.0 size=     256KiB time=00:00:04.00 bitrate=524.3kbits/s speed=1.0x",
		"frame=  250 fps=25 q=28.0 size=     640KiB time=00:01:02.50 bitrate=524.3kbits/s speed=1.0x",
		"frame=  251 fps=25 q=28.0 size=     640KiB time=01:02:03.50 bitrate=524.3kbits/s speed=1.0x",
		"video:0kB audio:123kB subtitle:0kB global headers:0kB",
	}, "\n")

	var got []float64
	if err := scanProgress(strings.NewReader(sample), func(s float64) {
		got = append(got, s)
	}); err != nil {
		t.Fatal(err)
	}
	want := []float64{4, 62.5, 3723.5}
	if len(got) != len(want) {
		t.Fatalf("parseé %v, want %v", got, want)
	}
	for i := range want {
		if diff := got[i] - want[i]; diff > 0.01 || diff < -0.01 {
			t.Errorf("got[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestRenderHonorsContextCancellation(t *testing.T) {
	engine := newTestEngine(t)
	media := makeFixture(t, engine, "audio.m4a",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=30", "-c:a", "aac",
	)

	spec := domain.RenderSpec{
		MediaPath:  media,
		Background: domain.BackgroundColor,
		Color:      "000000",
		Width:      1920,
		Height:     1080,
		FPS:        30,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := engine.Render(ctx, "job-cancel", testLyrics(), spec, nil)
	if err == nil {
		t.Fatal("esperaba error por cancelación")
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Errorf("la cancelación tardó demasiado: %v", elapsed)
	}
	if !strings.Contains(err.Error(), "ffmpeg") {
		t.Errorf("el error debería venir de ffmpeg, obtuve %v", err)
	}
}

func TestFontSizeScalesWithHeight(t *testing.T) {
	small := fontSizeFor(360)
	large := fontSizeFor(1080)
	if small <= 0 || large <= small {
		t.Errorf("tamaños inesperados: 360->%d, 1080->%d", small, large)
	}
	if str := strconv.Itoa(large); len(str) < 2 {
		t.Errorf("tamaño 1080p debería ser de dos dígitos, obtuve %d", large)
	}
}

// El tamaño se decide sobre el lado corto: si se usara el alto, un 1080x1920
// pediría 111px de fuente sobre un cuadro de 1080px de ancho y la letra no
// cabría. Todos los formatos deben compartir el mismo tamaño para 1080.
func TestBuildASSUsesShortSideForTypeScale(t *testing.T) {
	style := domain.Style{Colors: "single", SingleColor: 0, Font: "inter", Align: "center"}
	formatters := []struct {
		name          string
		width, height int
	}{
		{"vertical 9:16", 1080, 1920},
		{"feed 4:5", 1080, 1350},
		{"cuadrado 1:1", 1080, 1080},
		{"horizontal 16:9", 1920, 1080},
	}
	var reference string
	for _, format := range formatters {
		script := BuildASS([]domain.LyricLine{{Text: "hola", Start: 0, End: 2}}, style, format.width, format.height)
		line := assLineFor(script, "Style: Karaoke")
		if line == "" {
			t.Fatalf("no se encontró el estilo en el ASS de %s", format.name)
		}
		fields := strings.Split(line, ",")
		if len(fields) < 3 {
			t.Fatalf("estilo incompleto en %s: %q", format.name, line)
		}
		size := fields[2]
		if reference == "" {
			reference = size
			continue
		}
		if size != reference {
			t.Errorf("%s usa tamaño %s, pero 9:16 usa %s; debe depender del lado corto", format.name, size, reference)
		}
	}
	if reference != "63" {
		t.Errorf("tamaño sobre lado corto 1080 = %s, want 63", reference)
	}
}

func TestBuildASSEnablesWordWrapping(t *testing.T) {
	script := BuildASS(nil, domain.Style{Font: "inter", Align: "center"}, 1080, 1920)
	if !strings.Contains(script, "WrapStyle: 0") {
		t.Error("con WrapStyle 2 las frases largas se salen del cuadro en vertical")
	}
}

func assLineFor(script, prefix string) string {
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

func TestRenderGradientBackground(t *testing.T) {
	engine := newTestEngine(t)
	media := makeFixture(t, engine, "audio.m4a",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=3", "-c:a", "aac",
	)
	spec := domain.RenderSpec{
		MediaPath:  media,
		Background: domain.BackgroundGradient,
		Color:      "4da3ff",
		ColorTwo:   "f472b6",
		Width:      320,
		Height:     320,
		FPS:        24,
		OutputPath: filepath.Join(t.TempDir(), "gradiente.mp4"),
	}
	if err := engine.Render(context.Background(), "gradiente", testLyrics(), spec, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(spec.OutputPath)
	if err != nil || info.Size() == 0 {
		t.Fatalf("no se generó el archivo con degradado: %v", err)
	}

	inputs := gradientInputs(spec)
	filter := inputs[len(inputs)-1]
	if !strings.Contains(filter, "c0=0x4DA3FF") || !strings.Contains(filter, "c1=0xF472B6") {
		t.Errorf("el degradado no lleva los dos colores pedidos: %q", filter)
	}
}

func TestGradientInputsFallBackToDarkSecondColor(t *testing.T) {
	inputs := gradientInputs(domain.RenderSpec{Color: "4da3ff", Width: 100, Height: 200})
	filter := inputs[len(inputs)-1]
	if !strings.Contains(filter, "c1=0x0A0A14") {
		t.Errorf("sin colorTwo el degradado debería oscurecerse, obtuve %q", filter)
	}
}

func TestRenderClipsMediaToRequestedFragment(t *testing.T) {
	engine := newTestEngine(t)
	media := makeFixture(t, engine, "audio.m4a",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=20", "-c:a", "aac",
	)
	lyrics := domain.Lyrics{Synchronized: true, SyncedLyrics: []domain.LyricLine{
		{Text: "antes del corte", Start: 0, End: 4},
		{Text: "dentro del clip", Start: 10, End: 13},
		{Text: "tambien dentro", Start: 13.5, End: 16},
		{Text: "despues del clip", Start: 18, End: 20},
	}}
	output := filepath.Join(t.TempDir(), "clip.mp4")
	spec := domain.RenderSpec{
		MediaPath:    media,
		Background:   domain.BackgroundColor,
		Color:        "101820",
		Width:        320,
		Height:       320,
		FPS:          30,
		ClipStart:    9,
		ClipDuration: 5,
		OutputPath:   output,
	}
	if err := engine.Render(context.Background(), "clip", lyrics, spec, nil); err != nil {
		t.Fatal(err)
	}

	duration := probeStreamValue(t, engine, output, "v:0", "duration")
	if duration < 4.5 || duration > 5.5 {
		t.Errorf("duración del clip = %v, want ~5 (la duración del fragmento)", duration)
	}
}

func TestClipWindowClampsToMediaDuration(t *testing.T) {
	start, duration := clipWindow(domain.RenderSpec{ClipStart: 8, ClipDuration: 60}, 10)
	if start != 8 || duration != 2 {
		t.Errorf("clipWindow = (%.1f, %.1f), want (8, 2) recortado al final del medio", start, duration)
	}

	start, duration = clipWindow(domain.RenderSpec{ClipStart: 3}, 10)
	if start != 3 || duration != 7 {
		t.Errorf("clipWindow sin duración = (%.1f, %.1f), want (3, 7) hasta el final", start, duration)
	}

	start, duration = clipWindow(domain.RenderSpec{ClipStart: 99}, 10)
	if start != 10 || duration != 0 {
		t.Errorf("clipWindow con inicio fuera = (%.1f, %.1f), want (10, 0)", start, duration)
	}
}

// El video de fondo es un tope más: si el clip lo pasara, el fondo se quedaría
// sin video y saldría con el último fotograma congelado.
// El video de fondo se repite, así que no tops la ventana: el fragmento puede
// empezar más allá de donde termina el archivo y el largo lo acota el medio.
func TestClipWindowIgnoresBackgroundVideoDuration(t *testing.T) {
	spec := domain.RenderSpec{
		ClipStart:          2,
		ClipDuration:       12,
		BackgroundIsVideo:  true,
		BackgroundDuration: 8,
	}
	start, duration := clipWindow(spec, 300)
	if start != 2 || duration != 12 {
		t.Errorf("clipWindow = (%.1f, %.1f), want (2, 12): el fondo se repite y no acota", start, duration)
	}

	// Un fragmento que arranca después del final del video también vale.
	start, duration = clipWindow(domain.RenderSpec{
		ClipStart: 100, ClipDuration: 20, BackgroundIsVideo: true, BackgroundDuration: 8,
	}, 300)
	if start != 100 || duration != 20 {
		t.Errorf("clipWindow pasado el video = (%.1f, %.1f), want (100, 20)", start, duration)
	}

	// Cuando el medio es más corto que el fondo, manda el medio.
	start, duration = clipWindow(domain.RenderSpec{
		ClipStart: 1, ClipDuration: 7, BackgroundIsVideo: true, BackgroundDuration: 60,
	}, 5)
	if start != 1 || duration != 4 {
		t.Errorf("clipWindow con medio más corto = (%.1f, %.1f), want (1, 4)", start, duration)
	}
}

// Foto y video de fondo se repiten: el -loop alcanza para la foto y el video usa
// -stream_loop -1, que además arranca en el punto que le toca de la canción.
func TestBackgroundInputsLoopsPhotosAndVideos(t *testing.T) {
	engine := newTestEngine(t)
	video := makeFixture(t, engine, "fondo.mp4",
		"-f", "lavfi", "-i", "testsrc=size=640x480:rate=24:duration=5",
		"-f", "lavfi", "-i", "sine=frequency=330:duration=5",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest",
	)
	base := domain.RenderSpec{
		Background:         domain.BackgroundImage,
		BackgroundPath:     video,
		BackgroundIsVideo:  true,
		BackgroundDuration: 5,
		Width:              640,
		Height:             360,
		FPS:                24,
	}

	inputs, count, err := engine.backgroundInputs(base, 0)
	if err != nil {
		t.Fatalf("backgroundInputs(video): %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
	joined := strings.Join(inputs, " ")
	if !strings.Contains(joined, "-stream_loop -1") {
		t.Errorf("un video de fondo se tiene que repetir con -stream_loop -1: %v", inputs)
	}
	if strings.Contains(joined, "-ss") {
		t.Errorf("arrancando en 0 no hace falta -ss: %v", inputs)
	}
	// El audio del video de fondo nunca se mapea, así que la canción queda de
	// largo. Que sea un input más tampoco lo cambia: solo se mapea el medio.
	if !strings.Contains(joined, video) {
		t.Errorf("falta el archivo de fondo en los inputs: %v", inputs)
	}

	// 12s de canción con un video de 5s: el fondo arranca en el minuto 2 del video,
	// que es donde cae el fragmento, y sigue dando imagen más allá de su final.
	inputs, _, err = engine.backgroundInputs(base, 12)
	if err != nil {
		t.Fatalf("backgroundInputs(video, 12): %v", err)
	}
	joined = strings.Join(inputs, " ")
	if !strings.Contains(joined, "-ss 2.000") {
		t.Errorf("el video de fondo tiene que arrancar en 12 mod 5 = 2s: %v", inputs)
	}

	image := makeFixture(t, engine, "fondo.png",
		"-f", "lavfi", "-i", "color=c=0x203040:s=640x480", "-frames:v", "1",
	)
	photoSpec := base
	photoSpec.BackgroundPath = image
	photoSpec.BackgroundIsVideo = false
	inputs, _, err = engine.backgroundInputs(photoSpec, 0)
	if err != nil {
		t.Fatalf("backgroundInputs(foto): %v", err)
	}
	if !strings.Contains(strings.Join(inputs, " "), "-loop 1") {
		t.Errorf("una foto de fondo tiene que repetirse con -loop 1: %v", inputs)
	}
}

// Con video de fondo el render sigue aunque el clip pase el final del video: el
// fondo se repite, así que el audio no se corta y el video no queda mudo.
func TestRenderKeepsGoingPastBackgroundVideoEnd(t *testing.T) {
	engine := newTestEngine(t)
	media := makeFixture(t, engine, "audio.m4a",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=30", "-c:a", "aac",
	)
	video := makeFixture(t, engine, "fondo.mp4",
		"-f", "lavfi", "-i", "testsrc=size=640x480:rate=24:duration=4",
		"-c:v", "libx264", "-pix_fmt", "yuv420p",
	)
	lyrics := domain.Lyrics{Synchronized: true, SyncedLyrics: []domain.LyricLine{
		{Text: "primera", Start: 10.2, End: 12},
		{Text: "segunda", Start: 12, End: 13.8},
	}}
	spec := domain.RenderSpec{
		MediaPath:          media,
		Background:         domain.BackgroundImage,
		BackgroundPath:     video,
		BackgroundIsVideo:  true,
		BackgroundDuration: 4,
		Color:              "#0b1020",
		Style:              domain.Style{Highlight: "line", Colors: "single", Font: "inter", Align: "center"},
		Width:              640,
		Height:             360,
		FPS:                24,
		// Un fragmento que arranca en 10s, bien después de que el video de fondo
		// haya terminado, tiene que salir entero.
		ClipStart:    10,
		ClipDuration: 4,
		OutputPath:   filepath.Join(engine.workspace, "job-bg-video.mp4"),
	}

	if err := engine.Render(context.Background(), "job-bg-video", lyrics, spec, nil); err != nil {
		t.Fatalf("Render: %v", err)
	}

	if got := probeStreamValue(t, engine, spec.OutputPath, "v:0", "duration"); got > 4.5 || got < 3.5 {
		t.Errorf("duración = %.2fs, want ~4s (el largo pedido, más allá del video de fondo)", got)
	}
	if got := probeStreamCount(t, engine, spec.OutputPath, "v:0"); got != 1 {
		t.Errorf("esperaba 1 stream de video, obtuve %d", got)
	}
	// El fondo aporta imagen, el medio aporta el sonido: un solo stream de audio.
	if got := probeStreamCount(t, engine, spec.OutputPath, "a:0"); got != 1 {
		t.Errorf("esperaba 1 stream de audio, obtuve %d", got)
	}
}

// Un fragmento que cae en un tramo sin letra igual se exporta: antes se
// rechazaba y el usuario se quedaba sin video. Ahora sale el clip completo con
// los puntos de PlaceholderText en el medio.
func TestRenderKeepsFragmentWithoutLyrics(t *testing.T) {
	engine := newTestEngine(t)
	media := makeFixture(t, engine, "audio.m4a",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=30", "-c:a", "aac",
	)
	lyrics := domain.Lyrics{Synchronized: true, SyncedLyrics: []domain.LyricLine{
		{Text: "coro tardio", Start: 25, End: 28},
	}}
	spec := domain.RenderSpec{
		MediaPath:    media,
		Background:   domain.BackgroundColor,
		Color:        "101820",
		Width:        320,
		Height:       320,
		FPS:          30,
		ClipStart:    0,
		ClipDuration: 5,
		OutputPath:   filepath.Join(t.TempDir(), "vacio.mp4"),
	}
	if err := engine.Render(context.Background(), "vacio", lyrics, spec, nil); err != nil {
		t.Fatalf("Render: %v", err)
	}

	if got := probeStreamValue(t, engine, spec.OutputPath, "v:0", "duration"); got > 5.5 || got < 4.5 {
		t.Errorf("duración = %.2fs, want ~5s (el fragmento pedido)", got)
	}
	if got := probeStreamCount(t, engine, spec.OutputPath, "v:0"); got != 1 {
		t.Errorf("esperaba 1 stream de video, obtuve %d", got)
	}
	// Que los puntos se dibujen lo cubre ClipLyricsForRender en ass_test: desde
	// acá alcanza con que el archivo exista y tenga el largo pedido.
}
