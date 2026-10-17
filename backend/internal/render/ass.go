package render

import (
	"fmt"
	"math"
	"strings"

	"lyrics/internal/domain"
)

// palette son los colores que se pueden elegir para la letra. Los cinco primeros
// son los acentos de la app y los dos últimos los neutros, que al final van juntos
// porque son los que se usan con fondos claros y oscuros.
var palette = []string{"4da3ff", "22d3ee", "a78bfa", "f472b6", "fb923c", "ffffff", "000000"}

const dimColor = "777670"

const (
	outlineColor = "0a0a0a"
	backColor    = "000000"
	styleName    = "Karaoke"
	// minLineDuration es la duración mínima de una línea, para que una línea
	// con marca de tiempo degenerada siga siendo visible al menos un instante.
	minLineDuration = 0.2
)

var fontFaces = map[string]string{
	"manrope":  "Inter",
	"inter":    "Inter",
	"mono":     "Consolas",
	"consolas": "Consolas",
	"serif":    "Arial",
	"slab":     "Arial",
	"cursive":  "Arial",
}

// alignmentCodes son los códigos de alineación de ASS/SSA para la columna del
// centro, que es la que el selector de la app ofrece: 8 arriba, 5 en el medio y
// 2 abajo. Lo que no está en el mapa cae en 5, así que un ajuste viejo que
// guarde izquierda o derecha se dibuja centrado en vez de quedar torcido.
var alignmentCodes = map[string]int{
	"top":    8,
	"center": 5,
	"bottom": 2,
}

func fontFace(name string) string {
	if face, ok := fontFaces[strings.ToLower(strings.TrimSpace(name))]; ok {
		return face
	}
	return "Inter"
}

func alignmentCode(name string) int {
	if code, ok := alignmentCodes[strings.ToLower(strings.TrimSpace(name))]; ok {
		return code
	}
	return 5
}

func combinedAlignmentCode(horizontal, vertical string) int {
	rowStart := 4
	switch alignmentCode(vertical) {
	case 8:
		rowStart = 7
	case 2:
		rowStart = 1
	}

	switch strings.ToLower(strings.TrimSpace(horizontal)) {
	case "left":
		return rowStart
	case "right":
		return rowStart + 2
	default:
		return rowStart + 1
	}
}

// ClipLyrics deja solo las líneas que caen dentro de [start, start+duration] y
// las desplaza para que el tiempo 0 sea el primer frame del clip. Una línea que
// empezaba antes del corte se muestra desde el segundo 0, porque el espectador
// entra a mitad de la frase; una que se extiende más allá del final se corta
// ahí. Un clip sin límites (start 0 y duration <= 0) devuelve la letra intacta.
func ClipLyrics(lines []domain.LyricLine, start, duration float64) []domain.LyricLine {
	if start <= 0 && duration <= 0 {
		return lines
	}
	end := math.Inf(1)
	if duration > 0 {
		end = start + duration
	}

	clipped := make([]domain.LyricLine, 0, len(lines))
	for _, line := range lines {
		lineStart := line.Start
		lineEnd := math.Max(line.End, lineStart+minLineDuration)
		if lineEnd <= start || lineStart >= end {
			continue
		}
		shifted := line
		shifted.Start = math.Max(lineStart-start, 0)
		shifted.End = math.Min(lineEnd, end) - start
		if shifted.End < shifted.Start+minLineDuration {
			shifted.End = shifted.Start + minLineDuration
		}
		clipped = append(clipped, shifted)
	}
	return clipped
}

// PlaceholderText es lo que se dibuja cuando el fragmento elegido cae en un
// tramo sin letra. Antes el render se rechazaba y el usuario se quedaba sin
// video; ahora sale el clip con estos puntos en el medio del cuadro.
const PlaceholderText = "..."

// ClipLyricsForRender es ClipLyrics con el caso vacío resuelto: si ninguna
// línea cae dentro de la ventana, devuelve una sola línea con los puntos que
// duran todo el clip. Así el video se genera siempre y el espectador ve que ahí
// no había letra en vez de un cuadro mudo.
func ClipLyricsForRender(lines []domain.LyricLine, start, duration float64) []domain.LyricLine {
	clipped := ClipLyrics(lines, start, duration)
	if len(clipped) > 0 || duration <= 0 {
		return clipped
	}
	return []domain.LyricLine{{Text: PlaceholderText, Start: 0, End: duration}}
}

func BuildASS(lyrics []domain.LyricLine, style domain.Style, width, height int) string {
	face := fontFace(style.Font)
	verticalAlignment := style.AlignVertical
	if verticalAlignment == "" {
		verticalAlignment = style.Align
	}
	primary := palette[clampIndex(style.SingleColor, len(palette))]
	multi := strings.EqualFold(style.Colors, "multi")
	wordMode := strings.EqualFold(style.Highlight, "word")
	// Con varios colores entra solo lo que se eligió, en ese orden; con la paleta
	// completa o con un solo color, la lista es la de siempre.
	colors := stylePalette(style)
	// El color de la cabecera no decide nada porque cada línea lleva su propio \c,
	// pero si quedara algún trozo sin colorear tiene que salir del mismo juego que
	// el resto y no del tono suelto que quedó guardado.
	if multi {
		primary = colors[0]
	}

	script := strings.Builder{}
	script.WriteString("[Script Info]\n")
	script.WriteString("ScriptType: v4.00+\n")
	fmt.Fprintf(&script, "PlayResX: %d\n", width)
	fmt.Fprintf(&script, "PlayResY: %d\n", height)
	// WrapStyle 0 deja que libass ajuste las líneas largas al ancho disponible.
	// Con 2 (sin ajuste) una frase de 40 caracteres se salía del cuadro en 9:16.
	script.WriteString("WrapStyle: 0\n")
	script.WriteString("ScaledBorderAndShadow: yes\n")
	script.WriteString("YCbCr Matrix: TV.709\n")
	script.WriteString("\n[V4+ Styles]\n")
	script.WriteString("Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n")

	// La escala se calcula sobre el lado corto para que la letra se vea igual
	// en todos los formatos: en 1080x1920 usar el alto daría 111px, que desborda
	// los 1080px de ancho. Todos los presets se apoyan en 1080 como lado corto.
	shortSide := width
	if height > 0 && (shortSide <= 0 || height < shortSide) {
		shortSide = height
	}
	fontSize := scaledFontSize(shortSide, style.SizePercent)
	// El margen se mide sobre el lado que tiene enfrente cada borde, no sobre el
	// corto: con la letra arriba o abajo, un margen del lado corto en un 9:16
	// quedan a un 4% del alto y la frase va pegada al borde, que es justo lo que
	// tapa el fondo. Cada lado usa su propio.
	marginH := frameMargin(width)
	marginV := frameVerticalMargin(height)
	fmt.Fprintf(&script, "Style: %s,%s,%d,%s,%s,%s,%s,0,0,0,0,100,100,0,0,1,%d,%d,%d,%d,%d,%d,1\n",
		styleName,
		face,
		fontSize,
		assColor(primary),
		assColor(dimColor),
		assColor(outlineColor),
		assColor(backColor),
		letterOutline(fontSize),
		letterShadow(fontSize),
		combinedAlignmentCode(style.AlignHorizontal, verticalAlignment),
		marginH,
		marginH,
		marginV,
	)

	script.WriteString("\n[Events]\n")
	script.WriteString("Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")

	for index, line := range lyrics {
		text := sanitizeText(line.Text)
		if text == "" {
			continue
		}
		start := line.Start
		end := math.Max(line.End, start+0.2)

		var body string
		if wordMode {
			body = karaokeBody(text, start, end, index, style, multi, primary)
		} else {
			color := primary
			if multi {
				color = colors[index%len(colors)]
			}
			body = "{\\c" + assColor(color) + "}" + text
		}

		fmt.Fprintf(&script, "Dialogue: 0,%s,%s,%s,,0,0,0,,%s\n", assTime(start), assTime(end), styleName, body)
	}

	return script.String()
}

// stylePalette son los colores que se recorren con la letra en varios colores.
// Con una lista elegida entra solo esa, en el orden en que se eligió, que es lo
// que hace el multiple select del cliente; sin lista, o si no quedó ningún color
// válido, entra la paleta completa para que siempre haya con qué pintar.
func stylePalette(style domain.Style) []string {
	if len(style.Palette) == 0 {
		return palette
	}
	colors := make([]string, 0, len(style.Palette))
	seen := make(map[int]bool, len(style.Palette))
	for _, index := range style.Palette {
		if index < 0 || index >= len(palette) || seen[index] {
			continue
		}
		seen[index] = true
		colors = append(colors, palette[index])
	}
	if len(colors) == 0 {
		return palette
	}
	return colors
}

func karaokeBody(text string, start, end float64, lineIndex int, style domain.Style, multi bool, primary string) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}

	byLine := multi && strings.EqualFold(style.ColorOrder, "line")
	colors := stylePalette(style)
	total := math.Max(end-start, 0.1)
	boundaries := make([]int, len(words)+1)
	for i := range boundaries {
		boundaries[i] = int(math.Round((start + total*float64(i)/float64(len(words))) * 100))
	}

	builder := strings.Builder{}
	for index, word := range words {
		slice := boundaries[index+1] - boundaries[index]
		if slice < 1 {
			slice = 1
		}
		color := primary
		if multi {
			position := index
			if byLine {
				position = lineIndex
			}
			color = colors[position%len(colors)]
		}
		fmt.Fprintf(&builder, "{\\k%d\\c%s}%s", slice, assColor(color), word)
		if index < len(words)-1 {
			builder.WriteByte(' ')
		}
	}
	return builder.String()
}

func fontSizeFor(height int) int {
	size := int(math.Round(float64(height) * 0.058))
	if size < 24 {
		return 24
	}
	return size
}

// scaledFontSize aplica el tamaño que eligió el usuario sobre el que sale por
// defecto para el formato. Es un porcentaje y no un alto fijo para que la letra
// crezca o baje en la misma proporción en 16:9 que en 9:16.
func scaledFontSize(shortSide int, percent int) int {
	if percent <= 0 {
		percent = 100
	}
	if percent < 40 {
		percent = 40
	}
	if percent > 250 {
		percent = 250
	}
	size := int(math.Round(float64(fontSizeFor(shortSide)) * float64(percent) / 100))
	if size < 12 {
		return 12
	}
	return size
}

// letterOutline y letterShadow son el borde de la letra. Con un 8% del cuerpo la
// contorno tapaba los huecos de las letras redondas y la sombra sumaba un segundo
// borde, así que la palabra quedaba con un aro pesado detrás. Ahora es apenas lo
// justo para que la letra se despegue de un fondo con imagen.
func letterOutline(fontSize int) int {
	outline := int(math.Round(float64(fontSize) * 0.035))
	if outline < 1 {
		return 1
	}
	return outline
}

func letterShadow(fontSize int) int {
	shadow := int(math.Round(float64(fontSize) * 0.018))
	if shadow < 1 {
		return 1
	}
	return shadow
}

// frameMargin es el aire que la letra deja contra el borde del cuadro. El horizontal
// es un 8% del ancho, que es lo que evita que el renglón toque los costados; el
// vertical es el doble, un 16% del alto, porque arriba y abajo la línea queda
// pegada al borde contra el fondo y con un 8% seguía viéndose cortada. En ASS
// MarginV cuenta desde arriba con la letra alineada arriba y desde abajo cuando
// está alineada abajo, así que el mismo valor sirve para las dos.
func frameMargin(side int) int {
	return frameSide(side, 0.08, 24)
}

func frameVerticalMargin(side int) int {
	return frameSide(side, 0.16, 40)
}

func frameSide(side int, factor float64, floor int) int {
	if side <= 0 {
		return 0
	}
	margin := int(math.Round(float64(side) * factor))
	if margin < floor {
		margin = floor
	}
	return margin
}

func clampIndex(value, length int) int {
	if length <= 0 {
		return 0
	}
	index := value % length
	if index < 0 {
		index += length
	}
	return index
}

func sanitizeText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")

	cleaned := strings.Builder{}
	pendingBreak := false
	pendingSpace := false
	for _, r := range value {
		switch {
		case r == '\n':
			pendingSpace = false
			pendingBreak = true
		case r == ' ' || r == '\t' || r == '\u00a0':
			pendingSpace = true
		case r == '{' || r == '}' || r < 0x20:
			continue
		default:
			if pendingBreak {
				cleaned.WriteString(`\N`)
				pendingBreak = false
				pendingSpace = false
			} else if pendingSpace && cleaned.Len() > 0 {
				cleaned.WriteByte(' ')
			}
			pendingSpace = false
			cleaned.WriteRune(r)
		}
	}
	return cleaned.String()
}

func assTime(seconds float64) string {
	if seconds < 0 || math.IsNaN(seconds) {
		seconds = 0
	}
	total := int(math.Round(seconds * 100))
	hours := total / 360000
	total -= hours * 360000
	minutes := total / 6000
	total -= minutes * 6000
	secondsPart := total / 100
	centis := total % 100
	return fmt.Sprintf("%d:%02d:%02d.%02d", hours, minutes, secondsPart, centis)
}

func assColor(hex string) string {
	red, green, blue, ok := parseHex(hex)
	if !ok {
		red, green, blue = 0xFF, 0xFF, 0xFF
	}
	return fmt.Sprintf("&H00%02X%02X%02X&", blue, green, red)
}

func parseHex(hex string) (uint8, uint8, uint8, bool) {
	hex = strings.TrimPrefix(strings.TrimSpace(hex), "#")
	hex = strings.TrimPrefix(hex, "0x")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return 0, 0, 0, false
	}
	var red, green, blue uint64
	if _, err := fmt.Sscanf(hex, "%02x%02x%02x", &red, &green, &blue); err != nil {
		return 0, 0, 0, false
	}
	return uint8(red), uint8(green), uint8(blue), true
}
