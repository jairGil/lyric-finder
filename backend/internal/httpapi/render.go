package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"lyrics/internal/auth"
	"lyrics/internal/domain"
	"lyrics/internal/service"
)

type RenderHandler struct {
	renders *service.RenderService
}

func NewRenderHandler(renders *service.RenderService) *RenderHandler {
	return &RenderHandler{renders: renders}
}

const maxMemoryForm = 4 << 20

func (handler *RenderHandler) Create(writer http.ResponseWriter, request *http.Request) {
	upload, err := parseRenderUpload(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}

	job, err := handler.renders.Queue(request.Context(), upload)
	if err != nil {
		writeError(writer, statusForRenderError(err), err.Error())
		return
	}

	writeJSON(writer, http.StatusAccepted, job)
}

// Preview devuelve un borrador del clip en video/mp4. No crea job ni deja nada
// en disco: el frontend solo lo muestra en un <video> mientras elige el
// fragmento, así que el MP4 va directo en el cuerpo de la respuesta.
func (handler *RenderHandler) Preview(writer http.ResponseWriter, request *http.Request) {
	upload, err := parseRenderUpload(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}

	data, err := handler.renders.Preview(request.Context(), upload)
	if err != nil {
		writeError(writer, statusForRenderError(err), err.Error())
		return
	}

	writer.Header().Set("Content-Type", "video/mp4")
	// El borrador cambia con cada slider, así que no debe quedar cacheado.
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Length", strconv.Itoa(len(data)))
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(data)
}

// Source devuelve la ficha de un link externo: cuánto dura y cómo se llama. El
// modal la pide al pegar el link, antes de renderizar, porque el selector de
// fragmento necesita una duración real y el navegador no puede deducirla de un
// link. No baja el medio, solo lee los metadatos.
func (handler *RenderHandler) Source(writer http.ResponseWriter, request *http.Request) {
	rawURL := strings.TrimSpace(request.URL.Query().Get("url"))
	if rawURL == "" {
		writeError(writer, http.StatusBadRequest, "falta el link en el parámetro url")
		return
	}

	info, err := handler.renders.DescribeSource(request.Context(), rawURL)
	if err != nil {
		writeError(writer, statusForRenderError(err), err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, info)
}

// parseRenderUpload lee el multipart de creación y de preview. Es el mismo
// formulario a propósito: el borrador tiene que ser el render menos la calidad.
//
// Ojo con cerrar los archivos acá: los readers que devuelve son los del multipart
// y el render los consume después de que esta función volvió, dentro de la misma
// petición. Por eso no se cierran aquí; los cierra net/http cuando termina el
// handler. Cerrarlos temprano rompía solo los archivos grandes, que es lo que
// hacía el bug difícil de ver: ParseMultipartForm mantiene en memoria lo que
// entra en maxMemoryForm (4 MB) y baja el resto a un temporal en disco. Una foto
// chica pasaba entera en memoria, así que el Close no molestaba y andaba todo; un
// video de verdad ya venía por disco y moría con "file already closed" antes de
// llegar al render.
func parseRenderUpload(request *http.Request) (service.RenderUpload, error) {
	if err := request.ParseMultipartForm(maxMemoryForm); err != nil {
		return service.RenderUpload{}, errors.New("no se pudo leer el formulario: " + err.Error())
	}

	// El medio es opcional: sin él el render es la letra sincronizada sobre el
	// fondo, con una pista de audio en silencio.
	var media io.Reader
	mediaName := ""
	mediaFile, mediaHeader, mediaErr := request.FormFile("media")
	switch {
	case mediaErr == nil:
		media = mediaFile
		mediaName = mediaHeader.Filename
	case errors.Is(mediaErr, http.ErrMissingFile):
		// Sin archivo, todo bien.
	default:
		return service.RenderUpload{}, errors.New("no se pudo leer el archivo del campo media: " + mediaErr.Error())
	}

	background := domain.BackgroundKind(strings.ToLower(request.FormValue("background")))
	if background == "" {
		background = domain.BackgroundColor
	}
	if background != domain.BackgroundColor && background != domain.BackgroundImage && background != domain.BackgroundGradient {
		return service.RenderUpload{}, errors.New("el fondo debe ser color, image o gradient")
	}

	var (
		image       io.Reader
		imageHeader = ""
	)
	if background == domain.BackgroundImage {
		upload, header, err := request.FormFile("image")
		if err != nil {
			// El campo se llama `image` por compatibilidad, pero `background=image`
			// acepta una foto o un video.
			return service.RenderUpload{}, errors.New("se requiere una foto o un video de fondo en el campo image")
		}
		image = upload
		if header != nil {
			imageHeader = header.Filename
		}
	}

	alignVertical := formValueOr(request, "alignVertical", formValueOr(request, "align", "center"))
	upload := service.RenderUpload{
		Artist:     request.FormValue("artist"),
		Track:      request.FormValue("track"),
		Background: background,
		Color:      request.FormValue("color"),
		ColorTwo:   request.FormValue("colorTwo"),
		MediaName:  mediaName,
		Media:      media,
		SourceURL:  strings.TrimSpace(request.FormValue("sourceUrl")),
		ImageName:  imageHeader,
		Image:      image,
		Style: domain.Style{
			Highlight:       formValueOr(request, "highlight", "word"),
			Colors:          formValueOr(request, "colors", "single"),
			SingleColor:     formInt(request, "singleColor", 0),
			Palette:         formInts(request, "palette"),
			ColorOrder:      formValueOr(request, "colorOrder", "word"),
			Font:            formValueOr(request, "font", "inter"),
			SizePercent:     formInt(request, "sizePercent", 0),
			Align:           alignVertical,
			AlignHorizontal: formValueOr(request, "alignHorizontal", "center"),
			AlignVertical:   alignVertical,
		},
		Width:    formInt(request, "width", 0),
		Height:   formInt(request, "height", 0),
		FPS:      formInt(request, "fps", 0),
		Start:    formFloat(request, "start", 0),
		Duration: formFloat(request, "duration", 0),
	}

	if user, ok := auth.UserFromContext(request.Context()); ok {
		upload.UserID = user.ID
	}
	return upload, nil
}

func (handler *RenderHandler) Status(writer http.ResponseWriter, request *http.Request) {
	job, err := handler.renders.Status(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, statusForRenderError(err), err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, job)
}

func (handler *RenderHandler) File(writer http.ResponseWriter, request *http.Request) {
	job, path, err := handler.renders.Output(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, statusForRenderError(err), err.Error())
		return
	}
	writer.Header().Set("Content-Type", "video/mp4")
	// attachment, no inline: el frontend corre en otro puerto que la API, y el
	// atributo download de la etiqueta <a> se ignora entre orígenes distintos.
	// Con attachment el navegador guarda el archivo aunque el enlace apunte a
	// localhost:8080. Un <video src> sigue reproduciéndose igual.
	writer.Header().Set("Content-Disposition", `attachment; filename="`+renderFileName(job.Artist, job.Title)+`"`)
	http.ServeFile(writer, request, path)
}

// renderFileName arma un nombre legible y seguro para la cabecera. Las comillas
// y los saltos de línea romperían la cabecera, así que se descartan.
func renderFileName(artist, title string) string {
	clean := func(value string) string {
		var builder strings.Builder
		for _, character := range value {
			switch {
			case character == '"' || character == '\\' || character == '/':
				builder.WriteRune('-')
			case character < 0x20:
			default:
				builder.WriteRune(character)
			}
		}
		return strings.TrimSpace(builder.String())
	}
	name := strings.TrimSpace(clean(artist) + " - " + clean(title))
	if name == "" || name == "-" {
		name = "karaoke"
	}
	return name + ".mp4"
}

func formValueOr(request *http.Request, key, fallback string) string {
	if value := strings.TrimSpace(request.FormValue(key)); value != "" {
		return value
	}
	return fallback
}

func formInt(request *http.Request, key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(request.FormValue(key)))
	if err != nil {
		return fallback
	}
	return value
}

// formInts lee la lista de colores elegidos para varios colores, que viaja como
// "0,2,4". Se ignoran las partes que no sean números en vez de tirar el pedido
// entero: un valor raro de la lista no puede dejar de renderizar el video.
func formInts(request *http.Request, key string) []int {
	raw := strings.TrimSpace(request.FormValue(key))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]int, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		values = append(values, value)
	}
	return values
}

// formFloat acepta el texto decimal que envía el selector de fragmento del
// cliente. Cualquier valor no numérico se trata como ausente para que lo
// resuelva el service con sus propios valores por defecto.
func formFloat(request *http.Request, key string, fallback float64) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(request.FormValue(key)), 64)
	if err != nil {
		return fallback
	}
	return value
}

func statusForRenderError(err error) int {
	switch {
	case errors.Is(err, domain.ErrRecordNotFound), errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, service.ErrInvalidQuery),
		errors.Is(err, service.ErrInvalidRender),
		errors.Is(err, service.ErrUnsupportedMedia),
		errors.Is(err, service.ErrImageRequired),
		errors.Is(err, domain.ErrInvalidSource),
		errors.Is(err, domain.ErrNoLyrics),
		errors.Is(err, domain.ErrRenderUnavailable):
		return http.StatusBadRequest
	case errors.Is(err, domain.ErrSourceUnavailable):
		// El link era válido: falló el servidor al ir a buscarlo a YouTube, así
		// que reintentar tiene sentido.
		return http.StatusBadGateway
	case errors.Is(err, service.ErrDraftUnavailable),
		errors.Is(err, service.ErrSourceNotSupported):
		// No es culpa de la petición: el servidor fue compilado sin esa
		// capacidad, así que el cliente puede seguir exportando.
		return http.StatusNotImplemented
	default:
		return http.StatusInternalServerError
	}
}
