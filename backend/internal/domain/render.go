package domain

import (
	"context"
	"errors"
	"time"
)

var ErrNoLyrics = errors.New("la pista no tiene letra sincronizada")

var ErrRenderUnavailable = errors.New("ffmpeg no está disponible")

type RenderStatus string

const (
	RenderQueued  RenderStatus = "queued"
	RenderRunning RenderStatus = "running"
	RenderDone    RenderStatus = "done"
	RenderFailed  RenderStatus = "failed"
)

type BackgroundKind string

const (
	BackgroundColor    BackgroundKind = "color"
	BackgroundImage    BackgroundKind = "image"
	BackgroundGradient BackgroundKind = "gradient"
)

type Style struct {
	Highlight   string
	Colors      string
	SingleColor int
	// Palette son los índices de la paleta que se eligieron para varios colores,
	// en el orden en que se recorren. Vacío significa que entra toda la paleta.
	Palette    []int
	ColorOrder string
	Font       string
	// SizePercent escala el cuerpo de la letra sobre el que sale por defecto para
	// el formato. Cero o menos significa el tamaño normal.
	SizePercent     int
	Align           string
	AlignHorizontal string
	AlignVertical   string
}

type RenderSpec struct {
	Artist     string
	Track      string
	MediaPath  string
	OutputPath string
	// MediaHasVideo distingue un archivo con video del que solo trae audio, que
	// se dibuja como fondo en lugar de recortarse.
	MediaHasVideo bool
	Background    BackgroundKind
	Color         string
	ColorTwo      string
	// BackgroundPath es el archivo que se elige como fondo: una foto o un video.
	// La diferencia está en BackgroundIsVideo, porque una foto se repite y un
	// video se reproduce una sola vez. BackgroundDuration es lo que dura el video
	// y tops el clip: repetirlo en loop no es lo que se pidió, y dejarlo correr
	// más allá del final deja un fotograma congelado.
	BackgroundPath     string
	BackgroundIsVideo  bool
	BackgroundDuration float64
	Style              Style
	Width              int
	Height             int
	FPS                int
	// ClipStart es el offset dentro del medio desde el que arranca el clip, y
	// ClipDuration su duración. ClipDuration <= 0 significa "hasta el final".
	ClipStart    float64
	ClipDuration float64
}

type RenderJob struct {
	ID         string       `json:"id"`
	Artist     string       `json:"artist"`
	Title      string       `json:"title"`
	Status     RenderStatus `json:"status"`
	Progress   float64      `json:"progress"`
	MediaName  string       `json:"mediaName,omitempty"`
	OutputName string       `json:"outputName,omitempty"`
	Error      string       `json:"error,omitempty"`
	CreatedAt  time.Time    `json:"createdAt"`
	UpdatedAt  time.Time    `json:"updatedAt"`
}

type RenderSpecRecord struct {
	MediaPath          string
	MediaHasVideo      bool
	BackgroundPath     string
	BackgroundIsVideo  bool
	BackgroundDuration float64
	Color              string
	ColorTwo           string
	Background         string
	Style              Style
	Width              int
	Height             int
	FPS                int
	ClipStart          float64
	ClipDuration       float64
}

type Renderer interface {
	Render(ctx context.Context, jobID string, lyrics Lyrics, spec RenderSpec, onProgress func(float64)) error
	Available() error
}

// DraftRenderer produce un borrador barato del mismo clip para el preview del
// menú de exportación: mismo grafo y mismo ASS, pero en baja resolución y sin
// audio. Es opcional a propósito, para que un Renderer de prueba no tenga que
// implementarlo; el service lo busca con una aserción de tipo y, si no está,
// dice que no hay preview en vez de fallar el render.
type DraftRenderer interface {
	RenderDraft(ctx context.Context, lyrics Lyrics, spec RenderSpec) error
}

type MediaInfo struct {
	HasVideo bool    `json:"hasVideo"`
	HasAudio bool    `json:"hasAudio"`
	Duration float64 `json:"duration"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
}

type MediaProbe interface {
	Probe(ctx context.Context, path string) (MediaInfo, error)
}

// SourceInfo es lo que el modal necesita saber de una fuente externa antes de
// renderizar: cuánto dura, para armar el selector de fragmento.
type SourceInfo struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Duration float64 `json:"duration"`
	Width    int     `json:"width,omitempty"`
	Height   int     `json:"height,omitempty"`
}

// ErrInvalidSource marca un link que no es de la fuente admitida. Vive en
// domain y no en el paquete que la implementa para que la capa HTTP pueda
// mapearlo a un 400 sin conocer de dónde salió.
var ErrInvalidSource = errors.New("el link de la fuente no es válido")

// ErrSourceUnavailable dice que el link era válido pero el audio no se pudo
// traer: la red falló, el video es privado o está bloqueado en la región.
var ErrSourceUnavailable = errors.New("no se pudo traer el audio de la fuente")

// AudioSource trae el audio de una fuente externa (hoy, un video de YouTube) para
// usarlo como pista del render sin que el navegador tenga que subir el archivo.
// Trae solo el audio: el fondo sigue siendo el color, el degradado o la imagen
// que se elige en el modal, que es lo que se pidió para esta fuente.
//
// Es opcional a propósito, igual que DraftRenderer: si no hay binario de
// descarga, pedir un link dice que la fuente no está disponible y el resto de
// los renders sigue funcionando.
type AudioSource interface {
	DownloadAudio(ctx context.Context, rawURL string) (string, error)
	Describe(ctx context.Context, rawURL string) (SourceInfo, error)
	Available() error
}
