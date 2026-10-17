package render

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"lyrics/internal/domain"
	"lyrics/internal/platform"
)

const (
	ffprobeTimeout = 30
	// El borrador se ve en el modal mientras se arrastran los selectores, así
	// que tiene que salir en décimas de segundo: resolución y fps bajos. El lado
	// largo de 540px mantiene la proporción y, como el tamaño de la letra se
	// calcula sobre el lado corto, el texto queda escalado igual que en el render
	// final. El audio no se sacrifica: es lo que se escucha en el modal, y como
	// el borrador ya trae la letra quemada, el sonido sale sincronizado solo.
	draftMaxLongEdge = 540
	draftFPS         = 15
)

// quality agrupa lo que distingue un borrador de un render final. El grafo, el
// ASS, el recorte y el audio son los mismos en los dos casos: solo cambia cuánto
// cuesta codificar, que es justo lo que hace viable el preview.
type quality struct {
	preset string
	crf    string
}

var (
	finalQuality = quality{preset: "medium", crf: "20"}
	draftQuality = quality{preset: "ultrafast", crf: "30"}
)

var progressRe = regexp.MustCompile(`time=(\d+):(\d+):(\d+\.?\d*)`)

type Engine struct {
	binary    string
	probe     string
	fontsDir  string
	workspace string
	mu        sync.Mutex
	ffmpegOK  error
}

func NewEngine(toolsDir, assetsDir, workspace string) *Engine {
	return &Engine{
		binary:    filepath.Join(toolsDir, "ffmpeg", "bin", platform.ExecutableName("ffmpeg")),
		probe:     filepath.Join(toolsDir, "ffmpeg", "bin", platform.ExecutableName("ffprobe")),
		fontsDir:  filepath.Join(assetsDir, "fonts"),
		workspace: workspace,
	}
}

func (engine *Engine) Binary() string { return engine.binary }

func (engine *Engine) Available() error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.ffmpegOK != nil {
		return engine.ffmpegOK
	}
	info, err := os.Stat(engine.binary)
	if err != nil {
		engine.ffmpegOK = fmt.Errorf("%w: no se encontró %s", domain.ErrRenderUnavailable, engine.binary)
		return engine.ffmpegOK
	}
	if info.IsDir() {
		engine.ffmpegOK = fmt.Errorf("%w: %s es un directorio", domain.ErrRenderUnavailable, engine.binary)
		return engine.ffmpegOK
	}
	if _, err := os.Stat(engine.fontsDir); err != nil {
		engine.ffmpegOK = fmt.Errorf("%w: no se encontró el directorio de fuentes %s", domain.ErrRenderUnavailable, engine.fontsDir)
		return engine.ffmpegOK
	}
	engine.ffmpegOK = nil
	return nil
}

func (engine *Engine) Render(ctx context.Context, jobID string, lyrics domain.Lyrics, spec domain.RenderSpec, onProgress func(float64)) error {
	return engine.render(ctx, jobID, lyrics, spec, onProgress, finalQuality)
}

// RenderDraft escribe en spec.OutputPath un borrador del clip. Comparte todo el
// pipeline con Render salvo la resolución y el preset, así que lo que se ve y
// se escucha en el preview es el resultado final en pequeño.
func (engine *Engine) RenderDraft(ctx context.Context, lyrics domain.Lyrics, spec domain.RenderSpec) error {
	if spec.OutputPath == "" {
		return errors.New("RenderDraft: la spec necesita OutputPath")
	}
	return engine.render(ctx, "borrador", lyrics, shrinkForDraft(spec), nil, draftQuality)
}

// shrinkForDraft baja la resolución manteniendo la proporción y las dimensiones
// pares, que yuv420p exige para que el chroma no se descentrable.
func shrinkForDraft(spec domain.RenderSpec) domain.RenderSpec {
	longEdge := spec.Width
	if spec.Height > longEdge {
		longEdge = spec.Height
	}
	if longEdge > draftMaxLongEdge {
		scale := float64(draftMaxLongEdge) / float64(longEdge)
		spec.Width = evenDimension(int(float64(spec.Width) * scale))
		spec.Height = evenDimension(int(float64(spec.Height) * scale))
	}
	spec.FPS = draftFPS
	return spec
}

func evenDimension(value int) int {
	if value < 2 {
		return 2
	}
	if value%2 != 0 {
		return value - 1
	}
	return value
}

func (engine *Engine) render(ctx context.Context, jobID string, lyrics domain.Lyrics, spec domain.RenderSpec, onProgress func(float64), q quality) error {
	if err := engine.Available(); err != nil {
		return err
	}
	if len(lyrics.SyncedLyrics) == 0 {
		return domain.ErrNoLyrics
	}

	outputPath := spec.OutputPath
	if outputPath == "" {
		outputPath = filepath.Join(engine.workspace, jobID+".mp4")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		return err
	}

	workDir := filepath.Join(filepath.Dir(outputPath), jobID)
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return err
	}

	// La duración real del medio es lo que acota el clip, así que hay que
	// sondearla antes de decidir la ventana y antes de generar la letra: el ASS
	// debe contener solo los tiempos que existen dentro del recorte. Sin medio
	// no hay nada que sondear y manda ClipDuration, que el service ya calculó
	// desde la línea de tiempo de la letra.
	var mediaDuration float64
	if spec.MediaPath != "" {
		probed, err := engine.probeDuration(ctx, spec.MediaPath)
		if err != nil {
			return err
		}
		mediaDuration = probed
	}
	clipStart, clipDuration := clipWindow(spec, mediaDuration)
	if spec.MediaPath == "" && clipDuration <= 0 {
		// El fondo y el silencio son fuentes infinitas: sin este tope el job
		// codificaría hasta que se cancelara por tiempo.
		return fmt.Errorf("%w: el render sin medio necesita una duración", domain.ErrNoLyrics)
	}
	// Un fragmento que cae en un tramo sin letra no se rechaza: sale con los
	// puntos de PlaceholderText. Rechazarlo dejaba al usuario sin video y sin
	// una pista audible, cuando lo que quiere es justo ese trozo del video.
	clipLyrics := ClipLyricsForRender(lyrics.SyncedLyrics, clipStart, clipDuration)

	assPath := filepath.Join(workDir, "lyrics.ass")
	assScript := BuildASS(clipLyrics, spec.Style, spec.Width, spec.Height)
	if err := os.WriteFile(assPath, []byte(assScript), 0o600); err != nil {
		return err
	}

	args := engine.buildArgs(outputPath, assPath, spec, clipStart, clipDuration, q)

	// -shortest no acota de forma fiable un grafo cuyo fondo es una fuente
	// infinita: el video puede arrastrar un par de segundos de más. La duración
	// del clip ya está sonada, así que se impone como límite explícito.
	if clipDuration > 0 {
		args = insertBeforeOutput(args, "-t", strconv.FormatFloat(clipDuration, 'f', 3, 64))
	}

	command := exec.CommandContext(ctx, engine.binary, args...)
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		return err
	}

	if err := command.Start(); err != nil {
		return err
	}

	tail := &tailBuffer{limit: 12 << 10}
	var last float64
	scanErr := scanProgress(io.TeeReader(stderrPipe, tail), func(seconds float64) {
		if clipDuration <= 0 {
			return
		}
		progress := seconds / clipDuration
		if progress > 1 {
			progress = 1
		}
		if progress-last < 0.005 {
			return
		}
		last = progress
		if onProgress != nil {
			onProgress(progress)
		}
	})

	waitErr := command.Wait()
	if waitErr != nil {
		if scanErr != nil {
			return fmt.Errorf("ffmpeg terminó con error: %w", waitErr)
		}
		return fmt.Errorf("ffmpeg terminó con error: %w: %s", waitErr, tail.String())
	}
	if onProgress != nil {
		onProgress(1)
	}
	return nil
}

func (engine *Engine) buildArgs(outputPath, assPath string, spec domain.RenderSpec, clipStart, clipDuration float64, q quality) []string {
	inputs, inputCount, err := engine.backgroundInputs(spec, clipStart)
	if err != nil {
		inputs = solidColorInputs(spec)
		inputCount = 1
	}

	args := []string{"-hide_banner", "-y"}
	args = append(args, inputs...)

	mediaIndex := -1
	audioIndex := -1
	if spec.MediaPath != "" {
		// -ss va justo antes del -i al que aplica: así ffmpeg busca rápido por
		// keyframe y reinicia los tiempos en 0, que es la base del ASS recortado.
		mediaIndex = inputCount
		audioIndex = mediaIndex
		if clipStart > 0 {
			args = append(args, "-ss", strconv.FormatFloat(clipStart, 'f', 3, 64))
		}
		args = append(args, "-i", spec.MediaPath)
	} else {
		// Render solo de letra: se añade una pista en silencio porque muchos
		// reproductores y las plataformas de video esperan audio en el MP4.
		audioIndex = inputCount
		args = append(args, silentAudioInputs()...)
	}

	args = append(args, "-filter_complex", engine.buildGraph(mediaIndex, assPath, spec))
	args = append(args, "-map", "[out]")
	if mediaIndex >= 0 {
		// El ? deja pasar el caso de un archivo sin pista de audio en vez de
		// cortar el render, que es el mismo criterio del render final.
		args = append(args, "-map", fmt.Sprintf("%d:a?", mediaIndex))
	} else {
		args = append(args, "-map", fmt.Sprintf("%d:a", audioIndex))
	}

	args = append(args,
		"-c:v", "libx264",
		"-preset", q.preset,
		"-crf", q.crf,
		"-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "192k",
	)
	args = append(args, "-movflags", "+faststart", "-shortest", outputPath)
	return args
}

// silentAudioInputs produce un audio estéreo mudo e infinito; el largo lo impone
// el -t de salida.
func silentAudioInputs() []string {
	return []string{"-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=44100"}
}

// insertBeforeOutput coloca un par de opciones globales justo antes de la ruta
// de salida, que es el último argumento, y la deja como última.
func insertBeforeOutput(args []string, name, value string) []string {
	out := make([]string, 0, len(args)+2)
	out = append(out, args[:len(args)-1]...)
	out = append(out, name, value, args[len(args)-1])
	return out
}

// clipWindow acota el recorte pedido al medio real. Devolver los valores ya
// validados evita que ffmpeg reciba una ventana imposible.
func clipWindow(spec domain.RenderSpec, mediaDuration float64) (float64, float64) {
	start := spec.ClipStart
	if start < 0 {
		start = 0
	}
	// Solo el medio tops la ventana. El video de fondo se repite para cubrirla, así
	// que su duración no acota nada: el service ya recortó el largo contra el fondo
	// cuando hizo falta, y acá conviene que las dos puntadas coincidan.
	bound := mediaDuration
	if bound > 0 && start > bound {
		start = bound
	}
	duration := spec.ClipDuration
	switch {
	case duration <= 0:
		if bound > 0 {
			duration = bound - start
		}
	case bound > 0 && start+duration > bound:
		duration = bound - start
	}
	return start, duration
}

func solidColorInputs(spec domain.RenderSpec) []string {
	return []string{
		"-f", "lavfi",
		"-i", fmt.Sprintf("color=c=%s:s=%dx%d:r=%d", ffmpegColor(spec.Color), spec.Width, spec.Height, spec.FPS),
	}
}

func (engine *Engine) backgroundInputs(spec domain.RenderSpec, clipStart float64) ([]string, int, error) {
	switch spec.Background {
	case domain.BackgroundImage:
		if spec.BackgroundPath != "" {
			if _, err := os.Stat(spec.BackgroundPath); err != nil {
				return nil, 0, fmt.Errorf("no se encontró el archivo de fondo: %w", err)
			}
			// Foto y video se repiten para cubrir el clip entero: el fondo tiene que
			// dar imagen durante toda la ventana, que puede empezar más allá del
			// final del archivo. El video entra con -stream_loop -1 y arrancando en
			// el punto que le toca de la canción, para que al mover el fragmento el
			// fondo avance con la música en vez de reiniciarse. Su audio nunca se
			// mapea, que es lo que deja la canción como único sonido del video.
			if spec.BackgroundIsVideo {
				args := []string{"-stream_loop", "-1"}
				if spec.BackgroundDuration > 0 {
					offset := math.Mod(clipStart, spec.BackgroundDuration)
					if offset > 0 {
						args = append(args, "-ss", strconv.FormatFloat(offset, 'f', 3, 64))
					}
				}
				return append(args, "-i", spec.BackgroundPath), 1, nil
			}
			return []string{"-loop", "1", "-framerate", strconv.Itoa(spec.FPS), "-i", spec.BackgroundPath}, 1, nil
		}
	case domain.BackgroundGradient:
		return gradientInputs(spec), 1, nil
	}
	return []string{
		"-f", "lavfi",
		"-i", fmt.Sprintf("color=c=%s:s=%dx%d:r=%d", ffmpegColor(spec.Color), spec.Width, spec.Height, spec.FPS),
	}, 1, nil
}

// gradientInputs usa el filtro gradients en diagonal, de un color al otro. Si
// solo se envía un color, el degradado parte de un tono oscuro para que el
// texto siga teniendo contraste.
func gradientInputs(spec domain.RenderSpec) []string {
	from := ffmpegColor(spec.Color)
	to := ffmpegColor(spec.ColorTwo)
	if strings.TrimSpace(spec.ColorTwo) == "" {
		to = ffmpegColor("0a0a14")
	}
	source := fmt.Sprintf("gradients=s=%dx%d:c0=%s:c1=%s:x0=0:y0=0:x1=%d:y1=%d:type=linear",
		spec.Width, spec.Height, from, to, spec.Width, spec.Height)
	return []string{"-f", "lavfi", "-i", source}
}

func (engine *Engine) buildGraph(mediaIndex int, assPath string, spec domain.RenderSpec) string {
	chains := []string{
		fmt.Sprintf("[0:v]scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,setsar=1,fps=%d[bg]",
			spec.Width, spec.Height, spec.Width, spec.Height, spec.FPS),
	}

	// mediaIndex < 0 significa render solo de letra: el fondo es la escena.
	if mediaIndex >= 0 && spec.MediaHasVideo {
		chains = append(chains, fmt.Sprintf("[%d:v]scale=%d:%d:force_original_aspect_ratio=decrease[media]", mediaIndex, spec.Width, spec.Height))
		chains = append(chains, "[bg][media]overlay=x=(W-w)/2:y=(H-h)/2:shortest=1[scene]")
	} else {
		chains = append(chains, "[bg]null[scene]")
	}

	assFilter := fmt.Sprintf("ass=filename='%s':fontsdir='%s'", escapeFilterPath(assPath), escapeFilterPath(engine.fontsDir))
	chains = append(chains, fmt.Sprintf("[scene]%s[out]", assFilter))
	return strings.Join(chains, ";")
}

func (engine *Engine) probeDuration(ctx context.Context, mediaPath string) (float64, error) {
	probeCtx, cancel := context.WithTimeout(ctx, ffprobeTimeout*time.Second)
	defer cancel()

	command := exec.CommandContext(probeCtx, engine.probe,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		mediaPath,
	)
	output, err := command.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe no pudo leer el medio: %w", err)
	}
	text := strings.TrimSpace(string(output))
	if text == "" || text == "N/A" {
		return 0, nil
	}
	return strconv.ParseFloat(text, 64)
}

func scanProgress(reader io.Reader, onTime func(float64)) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		match := progressRe.FindStringSubmatch(scanner.Text())
		if match == nil {
			continue
		}
		hours, _ := strconv.ParseFloat(match[1], 64)
		minutes, _ := strconv.ParseFloat(match[2], 64)
		seconds, _ := strconv.ParseFloat(match[3], 64)
		onTime(hours*3600 + minutes*60 + seconds)
	}
	return scanner.Err()
}

type tailBuffer struct {
	limit int
	data  []byte
}

func (buffer *tailBuffer) Write(chunk []byte) (int, error) {
	buffer.data = append(buffer.data, chunk...)
	if len(buffer.data) > buffer.limit {
		buffer.data = buffer.data[len(buffer.data)-buffer.limit:]
	}
	return len(chunk), nil
}

func (buffer *tailBuffer) String() string {
	trimmed := strings.TrimSpace(string(buffer.data))
	if trimmed == "" {
		return "ffmpeg no produjo salida de error"
	}
	return trimmed
}

func escapeFilterPath(path string) string {
	replacer := strings.NewReplacer(`\`, `/`, `:`, `\:`, `'`, `\'`)
	return replacer.Replace(path)
}

func ffmpegColor(hex string) string {
	red, green, blue, ok := parseHex(hex)
	if !ok {
		red, green, blue = 0x11, 0x11, 0x11
	}
	return fmt.Sprintf("0x%02X%02X%02X", red, green, blue)
}
