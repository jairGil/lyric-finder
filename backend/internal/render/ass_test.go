package render

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"lyrics/internal/domain"
)

func TestAssTime(t *testing.T) {
	cases := []struct {
		seconds float64
		want    string
	}{
		{0, "0:00:00.00"},
		{1.5, "0:00:01.50"},
		{61.25, "0:01:01.25"},
		{3661.999, "1:01:02.00"},
		{-4, "0:00:00.00"},
	}
	for _, testCase := range cases {
		if got := assTime(testCase.seconds); got != testCase.want {
			t.Errorf("assTime(%v) = %q, want %q", testCase.seconds, got, testCase.want)
		}
	}
}

func TestAssColorIsBGR(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"#4da3ff", "&H00FFA34D&"},
		{"4da3ff", "&H00FFA34D&"},
		{"0x4da3ff", "&H00FFA34D&"},
		{"#abc", "&H00CCBBAA&"},
		{"nope", "&H00FFFFFF&"},
	}
	for _, testCase := range cases {
		if got := assColor(testCase.input); got != testCase.want {
			t.Errorf("assColor(%q) = %q, want %q", testCase.input, got, testCase.want)
		}
	}
}

func TestBuildASSHeader(t *testing.T) {
	script := BuildASS(nil, domain.Style{Font: "manrope", Align: "center"}, 1920, 1080)

	for _, want := range []string{
		"ScriptType: v4.00+",
		"PlayResX: 1920",
		"PlayResY: 1080",
		"[V4+ Styles]",
		"[Events]",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q", want)
		}
	}
	if !strings.Contains(script, "Inter") {
		t.Errorf("manrope should map to Inter, got:\n%s", script)
	}
	if !strings.Contains(script, ",5,") {
		t.Errorf("center should map to alignment 5, got:\n%s", script)
	}
	if strings.Contains(script, "Dialogue:") {
		t.Errorf("no lines should produce no Dialogue events:\n%s", script)
	}
}

func TestBuildASSCombinesHorizontalAndVerticalAlignment(t *testing.T) {
	cases := []struct {
		name       string
		horizontal string
		vertical   string
		want       string
	}{
		{"arriba derecha", "right", "top", "9"},
		{"centro izquierda", "left", "center", "4"},
		{"abajo centro", "center", "bottom", "2"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			script := BuildASS(nil, domain.Style{
				Font:            "inter",
				AlignHorizontal: testCase.horizontal,
				AlignVertical:   testCase.vertical,
			}, 1080, 1920)
			for _, line := range strings.Split(script, "\n") {
				if !strings.HasPrefix(line, "Style: Karaoke,") {
					continue
				}
				fields := strings.Split(line, ",")
				if got := fields[18]; got != testCase.want {
					t.Errorf("ASS alignment = %q, want %q", got, testCase.want)
				}
				return
			}
			t.Fatal("ASS style line not found")
		})
	}
}

// dialogueColors saca los \c de cada palabra, en el orden en que salen del script,
// que es como se va a ver la letra.
func dialogueColors(script string) []string {
	var colors []string
	for _, line := range strings.Split(script, "\n") {
		if !strings.HasPrefix(line, "Dialogue:") {
			continue
		}
		for _, chunk := range strings.Split(line, "\\c") {
			// Cada trozo arranca con &H00 y el color termina en el & de cierre,
			// que es el primero después de la cabecera de seis caracteres.
			if !strings.HasPrefix(chunk, "&H00") {
				continue
			}
			end := strings.Index(chunk[5:], "&")
			if end < 0 {
				continue
			}
			colors = append(colors, chunk[:6+end])
		}
	}
	return colors
}

// El margen de cada lado se mide sobre el lado que tiene enfrente, no sobre el
// corto: con la letra arriba o abajo, el del alto es el que la despega del
// borde. En un 9:16 son 153px contra los 86 que daba antes.
// Con varios colores solo entra lo que se eligió, en el orden en que se eligió:
// el multiple select del cliente manda la lista y el render la respeta.
func TestMultiColorUsesOnlyTheSelectedPalette(t *testing.T) {
	lines := []domain.LyricLine{
		{Text: "uno dos tres", Start: 0, End: 3},
		{Text: "cuatro cinco", Start: 3, End: 6},
	}
	style := domain.Style{
		Highlight:   "word",
		Colors:      "multi",
		ColorOrder:  "word",
		Palette:     []int{4, 5, 0},
		Font:        "manrope",
		Align:       "center",
		SingleColor: 1,
	}
	script := BuildASS(lines, style, 1080, 1920)

	// ASS escribe el color al revés, byte a byte, así que el naranja 4fb923c va
	// como 3C92FB.
	want := []string{
		"&H003C92FB&", // naranja, el primero de la lista
		"&H00FFFFFF&", // blanco, el segundo
		"&H00FFA34D&", // azul, el tercero
		"&H003C92FB&", // y vuelve a arrancar el ciclo
	}
	colors := dialogueColors(script)
	if len(colors) != 5 {
		t.Fatalf("esperaba 5 palabras, saqué %d:\n%s", len(colors), script)
	}
	for i, expected := range want {
		if colors[i] != expected {
			t.Errorf("palabra %d = %s, quiero %s", i+1, colors[i], expected)
		}
	}
	for _, gone := range []string{"&H00EED322&", "&H00FB8BA7&", "&H00B672F4&"} {
		if strings.Contains(script, gone) {
			t.Errorf("el cian, el violeta y la rosa no se eligieron, así que no pueden aparecer: %s", gone)
		}
	}
}

// Por línea el color no se recalcula en cada palabra, así que dos palabras seguidas
// de la misma línea tienen que salir del mismo color.
func TestMultiColorByLineRepeatsTheColorWithinTheLine(t *testing.T) {
	lines := []domain.LyricLine{
		{Text: "uno dos", Start: 0, End: 2},
		{Text: "tres cuatro", Start: 2, End: 4},
	}
	script := BuildASS(lines, domain.Style{
		Highlight:  "word",
		Colors:     "multi",
		ColorOrder: "line",
		Palette:    []int{2, 3},
		Font:       "manrope",
		Align:      "center",
	}, 1080, 1920)

	colors := dialogueColors(script)
	if len(colors) != 4 {
		t.Fatalf("esperaba 4 palabras, saqué %d:\n%s", len(colors), script)
	}
	if colors[0] != colors[1] || colors[2] != colors[3] {
		t.Errorf("por línea cada renglón tiene un color: %v", colors)
	}
	if colors[0] == colors[2] {
		t.Errorf("las dos líneas deberían alternar de color: %v", colors)
	}
}

// Una lista vacía, con índices repetidos o fuera de rango no puede dejar la letra
// sin pintar: cae en la paleta completa.
func TestStylePaletteFallsBackToTheFullPalette(t *testing.T) {
	if got := stylePalette(domain.Style{Palette: []int{99, -3, 42}}); len(got) != len(palette) {
		t.Errorf("una lista inválida tiene que dar la paleta completa, saqué %d", len(got))
	}
	if got := stylePalette(domain.Style{Palette: []int{2, 2, 2}}); len(got) != 1 {
		t.Errorf("los repetidos se descartan, saqué %d", len(got))
	}
	if got := stylePalette(domain.Style{}); len(got) != len(palette) {
		t.Errorf("sin lista entra la paleta completa, saqué %d", len(got))
	}
}

func TestFrameMarginFollowsItsOwnSide(t *testing.T) {
	cases := map[string]int{"1920": 154, "1080": 86, "100": 24, "0": 0}
	for side, want := range cases {
		var value int
		fmt.Sscanf(side, "%d", &value)
		if got := frameMargin(value); got != want {
			t.Errorf("frameMargin(%d) = %d, want %d", value, got, want)
		}
	}

	tall := map[string]int{"1920": 307, "1080": 173, "100": 40, "0": 0}
	for side, want := range tall {
		var value int
		fmt.Sscanf(side, "%d", &value)
		if got := frameVerticalMargin(value); got != want {
			t.Errorf("frameVerticalMargin(%d) = %d, want %d", value, got, want)
		}
	}

	script := BuildASS(nil, domain.Style{Font: "manrope", Align: "top"}, 1080, 1920)
	if !strings.Contains(script, ",8,86,86,307,") {
		t.Errorf("un 9:16 con la letra arriba tiene que dejar 86px a los lados y 307 arriba:\n%s", script)
	}

	// El horizontal también usa el ancho, que antes tomaba el lado corto.
	script = BuildASS(nil, domain.Style{Font: "manrope", Align: "bottom"}, 1920, 1080)
	if !strings.Contains(script, ",2,154,154,173,") {
		t.Errorf("un 16:9 con la letra abajo tiene que dejar 154px a los lados y 173 abajo:\n%s", script)
	}
}

// El borde de la letra no puede ser un aro pesado: con 63px de cuerpo el 8% eran
// 5px de contorno más 3 de sombra, y las letras redondas se cerraban.
func TestLetterOutlineStaysLight(t *testing.T) {
	if got := letterOutline(63); got != 2 {
		t.Errorf("letterOutline(63) = %d, quiero 2", got)
	}
	if got := letterShadow(63); got != 1 {
		t.Errorf("letterShadow(63) = %d, quiero 1", got)
	}
	// Aunque la letra se achique, tiene que quedar algo de aire: sin borde no se
	// lee contra una foto clara.
	if got := letterOutline(10); got != 1 {
		t.Errorf("letterOutline(10) = %d, quiero el piso de 1", got)
	}
	script := BuildASS(nil, domain.Style{Font: "manrope", Align: "center"}, 1080, 1920)
	if !strings.Contains(script, ",1,2,1,5,") {
		t.Errorf("el estilo tiene que llevar 1 de sombra, 2 de contorno y alineación 5:\n%s", script)
	}
}

// El tamaño sale de un porcentaje sobre el cuerpo del formato, no de un alto fijo:
// el mismo 130% se ve igual en 16:9 que en 9:16.
func TestScaledFontSizeFollowsTheChosenPercent(t *testing.T) {
	base := fontSizeFor(1080)
	if got := scaledFontSize(1080, 0); got != base {
		t.Errorf("sin porcentaje tiene que quedar el tamaño normal: %d, quiero %d", got, base)
	}
	if got := scaledFontSize(1080, 130); got != int(math.Round(float64(base)*1.3)) {
		t.Errorf("scaledFontSize(1080, 130) = %d", got)
	}
	if got := scaledFontSize(1080, 50); got != int(math.Round(float64(base)*0.5)) {
		t.Errorf("scaledFontSize(1080, 50) = %d", got)
	}
	// Los valores disparados se frenan para que un campo mal llenado no termine
	// con una letra de un píxel o de un metro.
	if got := scaledFontSize(1080, 5000); got > int(math.Round(float64(base)*2.5)) {
		t.Errorf("scaledFontSize(1080, 5000) = %d, hay que frenarlo", got)
	}

	script := BuildASS(nil, domain.Style{Font: "manrope", Align: "center", SizePercent: 150}, 1080, 1920)
	if !strings.Contains(script, fmt.Sprintf("Karaoke,Inter,%d,", int(math.Round(float64(base)*1.5)))) {
		t.Errorf("al 150%% el cuerpo tiene que ser %d:\n%s", int(math.Round(float64(base)*1.5)), script)
	}
}

func TestBuildASSWordKaraokeDistributesLineDuration(t *testing.T) {
	lines := []domain.LyricLine{{Text: "one two three", Start: 10, End: 13}}

	script := BuildASS(lines, domain.Style{Highlight: "word", Font: "inter"}, 1920, 1080)

	if !strings.Contains(script, "Dialogue: 0,0:00:10.00,0:00:13.00,") {
		t.Fatalf("unexpected event header:\n%s", script)
	}
	for _, want := range []string{"{\\k100", "{\\k100", "{\\k100"} {
		if !strings.Contains(script, want) {
			t.Errorf("expected %q in:\n%s", want, script)
		}
	}
	if strings.Count(script, "\\k") != 3 {
		t.Errorf("expected 3 karaoke slices, got:\n%s", script)
	}
}

func TestBuildASSLineModeHasNoKaraoke(t *testing.T) {
	lines := []domain.LyricLine{{Text: "hola mundo", Start: 0, End: 2}}

	script := BuildASS(lines, domain.Style{Highlight: "line", Font: "inter"}, 1920, 1080)

	if strings.Contains(script, "\\k") {
		t.Errorf("line mode must not emit karaoke tags:\n%s", script)
	}
	if !strings.Contains(script, "{\\c&H00FFA34D&}hola mundo") {
		t.Errorf("line mode should color the whole line:\n%s", script)
	}
}

func TestBuildASSMultiColorVariesByLine(t *testing.T) {
	lines := []domain.LyricLine{
		{Text: "primera", Start: 0, End: 1},
		{Text: "segunda", Start: 1, End: 2},
		{Text: "tercera", Start: 2, End: 3},
	}

	script := BuildASS(lines, domain.Style{Highlight: "line", Colors: "multi", Font: "inter"}, 1920, 1080)

	for _, want := range []string{"&H00FFA34D&", "&H00EED322&", "&H00FA8BA7&"} {
		if !strings.Contains(eventsOnly(script), want) {
			t.Errorf("expected palette color %s in:\n%s", want, script)
		}
	}
}

func TestBuildASSColorOrderLineGivesEveryWordSameColor(t *testing.T) {
	lines := []domain.LyricLine{{Text: "uno dos tres", Start: 0, End: 3}}

	script := BuildASS(lines, domain.Style{
		Highlight:  "word",
		Colors:     "multi",
		ColorOrder: "line",
		Font:       "inter",
	}, 1920, 1080)

	events := eventsOnly(script)
	accent := strings.Count(events, "&H00FFA34D&")
	if accent != 3 {
		t.Errorf("colorOrder=line should repeat one color 3 times, got %d:\n%s", accent, events)
	}
	if strings.Contains(events, "&H00EED322&") {
		t.Errorf("colorOrder=line should not vary the color:\n%s", events)
	}
}

func TestBuildASSColorOrderWordVariesPerWord(t *testing.T) {
	lines := []domain.LyricLine{{Text: "uno dos tres", Start: 0, End: 3}}

	script := BuildASS(lines, domain.Style{
		Highlight:  "word",
		Colors:     "multi",
		ColorOrder: "word",
		Font:       "inter",
	}, 1920, 1080)

	events := eventsOnly(script)
	for _, want := range []string{"&H00FFA34D&", "&H00EED322&", "&H00FA8BA7&"} {
		if !strings.Contains(events, want) {
			t.Errorf("colorOrder=word should use %s in:\n%s", want, events)
		}
	}
}

func eventsOnly(script string) string {
	parts := strings.SplitN(script, "[Events]", 2)
	if len(parts) != 2 {
		return script
	}
	return parts[1]
}

func TestSanitizeText(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"plain", "plain"},
		{"  double  space ", "double space"},
		{"{braces}gone", "bracesgone"},
		{"line\nbreak", `line\Nbreak`},
		{"tab\there", "tab here"},
		{"", ""},
		{"\u00a0nbsp", "nbsp"},
	}
	for _, testCase := range cases {
		if got := sanitizeText(testCase.input); got != testCase.want {
			t.Errorf("sanitizeText(%q) = %q, want %q", testCase.input, got, testCase.want)
		}
	}
}

func TestFontFaceFallbacks(t *testing.T) {
	cases := map[string]string{
		"manrope":  "Inter",
		"inter":    "Inter",
		"mono":     "Consolas",
		"consolas": "Consolas",
		"serif":    "Arial",
		"slab":     "Arial",
		"cursive":  "Arial",
		"bogus":    "Inter",
		"":         "Inter",
	}
	for input, want := range cases {
		if got := fontFace(input); got != want {
			t.Errorf("fontFace(%q) = %q, want %q", input, got, want)
		}
	}
}

// La alineación es vertical, como la de Word o Excel: arriba, en el medio o
// abajo. Lo que no está en el mapa se dibuja en el medio, así que un ajuste
// guardado con los nombres horizontales viejos no queda torcido.
func TestAlignmentCodeFallback(t *testing.T) {
	cases := map[string]int{
		"top": 8, "center": 5, "bottom": 2,
		"TOP": 8, " Bottom ": 2,
		"left": 5, "right": 5, "": 5, "weird": 5,
	}
	for input, want := range cases {
		if got := alignmentCode(input); got != want {
			t.Errorf("alignmentCode(%q) = %d, want %d", input, got, want)
		}
	}
}

// La paleta cierra con blanco y negro, que son los que se eligen con fondos
// claros y oscuros. El negro necesita un contorno claro para no fundirse con el
// fondo y con la sombra de la letra.
func TestPaletteEndsWithWhiteAndBlack(t *testing.T) {
	if got := len(palette); got != 7 {
		t.Fatalf("la paleta tiene %d colores, want 7 (cinco acentos + blanco + negro)", got)
	}
	if palette[5] != "ffffff" || palette[6] != "000000" {
		t.Fatalf("los dos últimos colores son %q y %q, want ffffff y 000000", palette[5], palette[6])
	}

	// El índice que manda el formulario tiene que caer en el color pedido, y no
	// en uno cualquiera por el clamp.
	for index, want := range map[int]string{0: "4da3ff", 5: "ffffff", 6: "000000"} {
		script := BuildASS([]domain.LyricLine{{Text: "hola", Start: 0, End: 1}},
			domain.Style{Font: "inter", Highlight: "line", SingleColor: index}, 1080, 1920)
		if !strings.Contains(script, "{\\c&H00"+ffmpegBGR(want)+"&}hola") {
			t.Errorf("singleColor %d no eligió %q:\n%s", index, want, script)
		}
	}
}

// ffmpegBGR da el color que libass espera, que es BGR y no RGB.
func ffmpegBGR(hex string) string {
	red, green, blue, ok := parseHex(hex)
	if !ok {
		return hex
	}
	return fmt.Sprintf("%02X%02X%02X", blue, green, red)
}

func TestFFmpegColor(t *testing.T) {
	cases := map[string]string{
		"#4da3ff": "0x4DA3FF",
		"112233":  "0x112233",
		"bad":     "0xBBAADD",
		"zz":      "0x111111",
		"":        "0x111111",
	}
	for input, want := range cases {
		if got := ffmpegColor(input); got != want {
			t.Errorf("ffmpegColor(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestClipLyricsShiftsTimesIntoTheClip(t *testing.T) {
	lines := []domain.LyricLine{
		{Text: "antes", Start: 0, End: 4},
		{Text: "dentro", Start: 10, End: 13},
		{Text: "despues", Start: 18, End: 20},
	}
	clipped := ClipLyrics(lines, 9, 5)
	if len(clipped) != 1 {
		t.Fatalf("ClipLyrics devolvio %d lineas, want 1: %+v", len(clipped), clipped)
	}
	if clipped[0].Text != "dentro" {
		t.Errorf("linea = %q, want %q", clipped[0].Text, "dentro")
	}
	if clipped[0].Start != 1 || clipped[0].End != 4 {
		t.Errorf("tiempos = (%.1f, %.1f), want (1, 4) tras restar el inicio", clipped[0].Start, clipped[0].End)
	}
}

func TestClipLyricsKeepsLineSpanningTheStart(t *testing.T) {
	// El espectador entra a mitad de la frase: la linea debe verse desde el
	// segundo 0 del clip, no desaparecer.
	lines := []domain.LyricLine{{Text: "larga", Start: 2, End: 9}}
	clipped := ClipLyrics(lines, 7, 4)
	if len(clipped) != 1 {
		t.Fatalf("ClipLyrics devolvio %d lineas, want 1", len(clipped))
	}
	if clipped[0].Start != 0 {
		t.Errorf("inicio = %.1f, want 0", clipped[0].Start)
	}
	if clipped[0].End != 2 {
		t.Errorf("fin = %.1f, want 2 (9-7)", clipped[0].End)
	}
}

func TestClipLyricsCutsLineAtClipEnd(t *testing.T) {
	lines := []domain.LyricLine{{Text: "larga", Start: 10, End: 40}}
	clipped := ClipLyrics(lines, 10, 6)
	if len(clipped) != 1 {
		t.Fatalf("ClipLyrics devolvio %d lineas, want 1", len(clipped))
	}
	if clipped[0].End != 6 {
		t.Errorf("fin = %.1f, want 6 (recortada al final del clip)", clipped[0].End)
	}
}

func TestClipLyricsWithoutWindowReturnsInput(t *testing.T) {
	lines := []domain.LyricLine{{Text: "a", Start: 1, End: 2}}
	if got := ClipLyrics(lines, 0, 0); len(got) != 1 || got[0].Start != 1 {
		t.Errorf("sin ventana la letra deberia quedar intacta, obtuve %+v", got)
	}
}

func TestClipLyricsDropsLinesOutsideWindow(t *testing.T) {
	lines := []domain.LyricLine{
		{Text: "muy antes", Start: 0, End: 1},
		{Text: "muy despues", Start: 90, End: 95},
	}
	if got := ClipLyrics(lines, 50, 5); len(got) != 0 {
		t.Errorf("ClipLyrics = %+v, want vacio", got)
	}
}

func TestClipLyricsForRenderFillsEmptyWindow(t *testing.T) {
	lines := []domain.LyricLine{
		{Text: "coro tardio", Start: 25, End: 28},
	}
	got := ClipLyricsForRender(lines, 0, 5)
	if len(got) != 1 {
		t.Fatalf("ClipLyricsForRender = %+v, want 1 linea de relleno", got)
	}
	if got[0].Text != PlaceholderText {
		t.Errorf("texto = %q, want %q", got[0].Text, PlaceholderText)
	}
	if got[0].Start != 0 || got[0].End != 5 {
		t.Errorf("ventana = [%v, %v], want [0, 5]", got[0].Start, got[0].End)
	}
}

// Si hay letra, el relleno no aparece: el placeholder es solo para el hueco.
func TestClipLyricsForRenderKeepsRealLines(t *testing.T) {
	lines := []domain.LyricLine{
		{Text: "coro", Start: 0, End: 4},
	}
	got := ClipLyricsForRender(lines, 0, 5)
	if len(got) != 1 || got[0].Text != "coro" {
		t.Errorf("ClipLyricsForRender = %+v, want la linea real", got)
	}
}

// Sin ventana no se rellena: no hay largo con el que dibujar los puntos.
func TestClipLyricsForRenderWithoutWindowHasNoPlaceholder(t *testing.T) {
	lines := []domain.LyricLine{
		{Text: "coro tardio", Start: 25, End: 28},
	}
	if got := ClipLyricsForRender(lines, 0, 0); len(got) != 1 {
		t.Errorf("ClipLyricsForRender = %+v, want la letra intacta", got)
	}
}

// La cadena que usa el render: sin letra en la ventana, el ASS tiene que llevar
// los puntos durante todo el clip. Si esto falla, el video sale mudo.
func TestBuildASSEmitsPlaceholderForEmptyWindow(t *testing.T) {
	lines := []domain.LyricLine{
		{Text: "coro tardio", Start: 25, End: 28},
	}
	script := BuildASS(ClipLyricsForRender(lines, 0, 5), domain.Style{Font: "inter", Align: "center"}, 1080, 1920)
	if !strings.Contains(script, PlaceholderText) {
		t.Errorf("el ASS no tiene los puntos de relleno:\n%s", script)
	}
	// Y que duren todo el clip, no un instante.
	if !strings.Contains(script, "0:00:00.00,0:00:05.00") {
		t.Errorf("el dialogo del relleno no cubre los 5s del clip:\n%s", script)
	}
}
