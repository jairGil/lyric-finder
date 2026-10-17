# Lyrics API

API personal en Go que consulta LRCLIB y devuelve la letra completa como JSON.

## Configuración HTTP y CORS

El listener usa `LYRICS_HTTP_ADDR` si está definido; en caso contrario escucha
en `0.0.0.0:$PORT`, con `8080` como valor local. `LYRICS_CORS_ORIGINS` acepta
orígenes separados por coma. Vacío permite `*` para desarrollo; en producción
define el dominio exacto del frontend. Al arrancar, el binario carga `.env` del
directorio actual o `backend/.env` si se ejecuta desde la raíz; las variables
ya definidas en el proceso tienen prioridad. [`.env.example`](.env.example)
enumera las variables de referencia.

Al iniciar, el backend emite un evento JSON `application_config` con los
listeners, directorios principales y el destino de persistencia efectivo. Para
PostgreSQL solo registra host, puerto, base y si TLS está activo; nunca imprime
la URL completa, el usuario ni la contraseña.

## Ejecutar

```powershell
go run .\cmd
```

Para enriquecer los resultados con videos de YouTube, configura una clave de
YouTube Data API v3 antes de iniciar el servidor:

```powershell
$env:YOUTUBE_API_KEY="tu-clave"
go run .\cmd
```

Sin esa variable, la API continúa funcionando y el frontend usa el karaoke
local y un enlace de búsqueda en YouTube como fallback.

## Persistencia y cache

El servidor crea SQLite en `data/lyrics.db` por defecto. Puedes cambiar la
ruta con `LYRICS_DB_PATH`:

```powershell
$env:LYRICS_DB_PATH="data/dev.db"
```

Para usar PostgreSQL configura `LYRICS_STORAGE_DRIVER=postgres` y una URL en
`LYRICS_DATABASE_URL` (o `DATABASE_URL`). El adaptador aplica al conectar las
migraciones versionadas de `internal/postgres/migrations/` automáticamente.
El esquema inicial también puede ejecutarse manualmente con `psql` desde
`internal/postgres/migrations/001_init.sql`. Cambiar de driver no copia los
datos existentes de SQLite.

Se cachean de forma persistente las respuestas de LRCLIB y las búsquedas de
videos de YouTube. Los registros no caducan y el cache se conserva entre
reinicios.

La migración también crea las tablas base para `users`, `tracks`, `playlists`,
`playlist_tracks`, `saved_tracks` y `track_reactions`.

Endpoints de cuenta disponibles:

```text
POST /api/auth/register
POST /api/auth/login
POST /api/auth/logout
GET  /api/me
GET  /api/playlists
POST /api/playlists
POST /api/playlists/{playlistId}/tracks
POST /api/library/tracks
GET  /api/library?collection=saved|liked|disliked
PUT  /api/tracks/{trackId}/save
PUT  /api/tracks/{trackId}/reaction
```

Las rutas protegidas reciben `Authorization: Bearer <token>`. Cada usuario
nuevo obtiene las playlists de sistema `Me gusta` y `Guardadas`.

## Consultar

```text
GET http://localhost:8080/api/lyrics?artist=DAAZ&track=GUAPÍSIMA%20BUENÍSIMA
```

Buscar canciones por artista:

```text
GET http://localhost:8080/api/search?artist=DAAZ
GET http://localhost:8080/api/search?artist=DAAZ&track=GUAPÍSIMA
```

Respuesta resumida:

```json
{
  "track": {
    "title": "...",
    "artist": "...",
    "album": "...",
    "duration": 233
  },
  "plainLyrics": "...",
  "lyrics": [
    { "text": "Primera línea", "start": 12.3, "end": 16.8 }
  ],
  "synchronized": true
}
```

El frontend debe sincronizar `lyrics` con el tiempo actual del reproductor. El
backend entrega datos; no envía caracteres por WebSocket.

## Exportar video karaoke

El servidor puede renderizar un MP4 con la letra sincronizada, un fondo de color,
imagen o degradado, y el medio que suba el usuario. El render es asíncrono: la API
encola el trabajo y responde de inmediato con un `id` para consultar el progreso.

El usuario puede recortar el resultado a un fragmento con `start` y `duration`, y
elegir la proporción de salida (vertical 9:16 y 4:5, cuadrado 1:1, horizontal
16:9). Al recortar, la letra se filtra a ese intervalo y sus tiempos se desplazan
para que el primer frame del clip coincida con el segundo 0.

No se puede exportar el video de YouTube del reproductor: el `iframe` no permite
leer ni descargar el contenido. El medio, si se quiere, se sube como archivo;
sin archivo el export es solo la letra sobre el fondo.

### Requisitos

- FFmpeg con `libass`, `libx264` y `aac`. Se espera en `tools/ffmpeg/bin/`
  relativo a la raíz del repositorio; Windows usa `.exe` y Linux nombres Unix.
- Fuentes TrueType en `backend/assets/fonts/`.
- `yt-dlp` en `tools/yt-dlp/yt-dlp[.exe]`, solo si se quiere usar el audio de un
  link de YouTube. Es opcional: sin él, esa fuente responde `501` y todo lo demás
  sigue igual.

Si falta el binario de ffmpeg, el servidor arranca igual pero responde
`400 ffmpeg no está disponible en el servidor` al intentar exportar; el resto de
la API sigue funcionando.

| Variable | Por defecto | Para qué sirve |
| --- | --- | --- |
| `LYRICS_FFMPEG_DIR` | `../tools` | Carpeta que contiene `ffmpeg/bin/ffmpeg[.exe]`. |
| `LYRICS_ASSETS_DIR` | `assets` | Carpeta de fuentes que recibe libass. |
| `LYRICS_RENDER_DIR` | `data/renders` | Salida de los MP4 y del ASS de cada job. |

```powershell
$env:LYRICS_FFMPEG_DIR="..\tools"
$env:LYRICS_ASSETS_DIR="assets"
$env:LYRICS_RENDER_DIR="data/renders"
go run .\cmd
```

### Endpoints

```text
POST /api/renders          -> 202 con el job creado
GET  /api/renders/{id}     -> estado y progreso
GET  /api/renders/{id}/file -> descarga el MP4
POST /api/renders/preview -> borrador del clip en video/mp4
GET  /api/renders/source   -> ficha de un link de YouTube (id, título, duración)
```

`POST` es `multipart/form-data`. Ningún archivo es obligatorio: sin `media` el
render es la letra sincronizada sobre el fondo, con una pista de audio en
silencio. Si se manda `media`, debe ser audio o video **con pista de audio**; si
no tiene audio, o no es un archivo multimedia legible, la API responde `400`.

| Campo | Obligatorio | Default | Notas |
| --- | --- | --- | --- |
| `media` | no | — | Archivo de audio o video con audio. Máx. 512 MB. Sin él, el video sale mudo. |
| `artist` | sí | — | Se usa para buscar la letra en LRCLIB. |
| `track` | sí | — | Igual que `artist`. |
| `background` | no | `color` | `color`, `image` o `gradient`. |
| `color` | no | `#111111` | Color de fondo. Con `gradient`, el color de partida. |
| `colorTwo` | no | `#0a0a14` | Con `background=gradient`, el color final del degradado diagonal. |
| `image` | si `image` | — | Foto (JPG, PNG, WebP, GIF) o video (MP4, MOV, WebM, MKV). Se recorta y escala al tamaño de salida. Una foto se repite; un video se reproduce una vez y **topa la duración del clip** a la suya. Su audio nunca se usa: el único sonido es el del medio. |
| `highlight` | no | `line` | `line` resalta la línea completa, `word` la palabra actual. |
| `colors` | no | `single` | `single` usa un solo color, `multi` reparte una paleta. |
| `colorOrder` | no | `word` | Con `colors=multi`, reparte la paleta por `line` o por `word`. |
| `font` | no | `inter` | `manrope`, `inter`, `mono`, `consolas`, `serif`, `slab` o `cursive`. |
| `align` | no | `center` | `left`, `center` o `right` (alineación vertical del texto). |
| `width` | no | `1920` | Limitado a 320–3840. |
| `height` | no | `1080` | Limitado a 320–3840. |
| `fps` | no | `30` | Limitado a 12–60. |
| `start` | no | `0` | Segundos desde el inicio del medio (o de la letra, sin medio) donde arranca el clip. |
| `duration` | no | hasta el final | Segundos de duración del clip. Se recorta al final del medio, del video de fondo o de la letra. |

Sin `media` la duración sale de la pista que reporta LRCLIB y, si no la hay, del
final de la última línea más 1,5 s. Si no hay ninguna de las dos, la API
responde `400`: un clip sin tope dejaría el fondo y el silencio codificando para
siempre.

Con `background=image` y un video de fondo, la duración del video es otro tope y
gana el más corto de los dos. El video no se repite: más allá de su final el
fondo quedaría congelado en el último fotograma, así que el clip se recorta en
vez de estirar. El modal lee la duración en el navegador para topar el selector de
fragmento antes de subir el archivo.

### Vista previa

`POST /api/renders/preview` recibe exactamente el mismo formulario y devuelve el
MP4 en el cuerpo de la respuesta, sin crear job ni dejar archivos. Es el render
real con dos cambios: la resolución baja a 540 px de lado largo y se corta a los
primeros 15 s, y no lleva audio. El resto, el grafo, el ASS, el recorte y las
proporciones, es idéntico, así que el `<video>` del menú de exportación muestra
lo que se va a obtener, solo que más chico.

Como se valida con el mismo camino que `POST /api/renders`, un fondo desconocido
o un fragmento imposible se rechazan igual. Si el servidor se compiló sin
soporte de borradores responde `501` y el render final sigue funcionando.

```powershell
curl.exe -X POST http://localhost:8080/api/renders/preview `
  -F "artist=Ed Sheeran" `
  -F "track=Shape of You" `
  -F "background=gradient" `
  -F "color=#0b1020" `
  -F "colorTwo=#3d1a5c" `
  -F "width=1080" `
  -F "height=1920" `
  -F "start=30" `
  -F "duration=10" `
  -o borrador.mp4
```

El tamaño de la letra se calcula sobre el lado corto del cuadro, así que los
formatos verticales no agrandan la fuente: los cuatro formatos recomendados son
`1080x1920` (9:16), `1080x1350` (4:5), `1080x1080` (1:1) y `1920x1080` (16:9).

### Audio de YouTube

En vez de subir un archivo, el formulario acepta `sourceUrl` con un link de
YouTube. El servidor baja **solo el audio** con `yt-dlp` y lo usa como pista del
render; el fondo sigue siendo el color, el degradado o la imagen elegidos. Si
llegan `media` y `sourceUrl` juntos, manda el archivo.

```powershell
curl.exe "http://localhost:8080/api/renders/source?url=https://www.youtube.com/watch?v=9bZkp7q19f0"
```

Esa ficha es la que consulta el menú de exportación al pegar el link, porque un
link no trae duración en el navegador y el selector de fragmento la necesita. No
baja el medio: solo lee los metadatos.

Requisitos y comportamiento:

- El binario va en `tools/yt-dlp/yt-dlp.exe`, junto al de ffmpeg. Si no está, el
  log dice `render=source-disabled` y pedir un link responde `501`; el resto de
  los renders sigue funcionando.
- Solo se aceptan links de YouTube (`youtube.com`, `youtu.be`, rutas `/shorts/`,
  `/embed/`, `/live/`) o el ID suelto. Cualquier otro host responde `400`, para
  que el endpoint no sea un descargador genérico.
- Lo descargado se cachea en `<render-dir>/youtube/<id>.m4a` y se reutiliza. Es lo
  que hace viable el preview: sin caché, cada movimiento del selector de
  fragmento bajaría el video otra vez.
- Un video privado, bloqueado en la región o en vivo responde `502`.

Usalo con contenido propio o con licencia que lo permita: descargar de YouTube
tiene sus propios términos de servicio.

Sobre `start` y `duration`:

- `start` no puede ser negativo ni empezar después de que termine el medio (o de
  la letra, en un render sin medio).
- Si `start + duration` se pasa del final, la duración se recorta en lugar de
  fallar, para que se pueda arrastrar el selector hasta el borde.
- Un fragmento sin ninguna línea de letra dentro devuelve `400`; conviene
  elegir una zona donde cante la canción.

Letra sobre un fondo, sin ningún archivo de audio o video. El largo sale de la
pista o del final de la letra:

```powershell
curl.exe -X POST http://localhost:8080/api/renders `
  -F "artist=Ed Sheeran" `
  -F "track=Shape of You" `
  -F "background=gradient" `
  -F "color=#0b1020" `
  -F "colorTwo=#3d1a5c" `
  -F "width=1080" `
  -F "height=1920" `
  -F "highlight=word"
```

```powershell
curl.exe -X POST http://localhost:8080/api/renders `
  -F "artist=Ed Sheeran" `
  -F "track=Shape of You" `
  -F "media=@cancion.mp3" `
  -F "background=color" `
  -F "color=#0b1020" `
  -F "highlight=word" `
  -F "colors=multi" `
  -F "colorOrder=line"
```

Fragmento vertical de 15 segundos, desde el segundo 30, con degradado:

```powershell
curl.exe -X POST http://localhost:8080/api/renders `
  -F "artist=Ed Sheeran" `
  -F "track=Shape of You" `
  -F "media=@cancion.mp3" `
  -F "background=gradient" `
  -F "color=#4da3ff" `
  -F "colorTwo=#f472b6" `
  -F "width=1080" `
  -F "height=1920" `
  -F "start=30" `
  -F "duration=15"
```

```json
{
  "id": "20260927-215946-f4e69f7d",
  "status": "queued",
  "progress": 0,
  "artist": "Ed Sheeran",
  "title": "Shape of You",
  "createdAt": "2026-09-27T21:59:46Z",
  "updatedAt": "2026-09-27T21:59:46Z"
}
```

Los estados son `queued`, `running`, `done` y `failed`. En `failed`, el campo
`error` trae la causa. `GET /api/renders/{id}/file` responde `404` mientras el
job no esté en `done`, y `200` con el MP4 cuando sí. La descarga llega como
`Content-Disposition: attachment` con el nombre `artista - titulo.mp4`: el
frontend se sirve en otro puerto que la API y, entre orígenes distintos, el
atributo `download` de una etiqueta `<a>` se ignora.

### Detalles del render

- Salida H.264 + AAC en MP4, con `-movflags +faststart` para reproducción
  progresiva en el navegador.
- La duración la impone el fragmento: sin `start` ni `duration` es la del medio,
  y con ellos es la del recorte. Una canción completa con `start=0` y
  `duration=20` produce un video de 20 s.
- El recorte del medio usa `-ss` antes del `-i`, que busca por keyframe y
  reinicia los tiempos en 0. Sobre esa base el ASS lleva solo las líneas del
  intervalo, restadas por `start`, así que la letra va sincronizada con el
  audio aunque el clip empiece a mitad de una frase.
- `-shortest` no acota un grafo cuyo fondo es una fuente infinita, así que la
  duración del clip se impone además con `-t`. Solo las fotos son infinitas
  (`-loop 1`); un video de fondo se lee una vez y su duración acota el clip.
- Un video de fondo entra como primer input y el medio como segundo, así que la
  letra se dibuja sobre la imagen y la única pista de audio mapeada es la del
  medio: el sonido propio del video de fondo se descarta.
- Cola de 32 jobs y un worker: los renders se procesan de uno en uno. Un job que
  supera los 20 minutos se cancela y queda en `failed`.
- Al arrancar, los jobs que quedaron en `queued` o `running` por un cierre
  inesperado se marcan `failed` para no bloquear la cola.
- La letra debe estar sincronizada. Sin tiempos, `POST` responde `400`.

## Estructura

- `cmd`: composición de dependencias y arranque del servidor.
- `internal/domain`: modelos y contratos del dominio.
- `internal/service`: casos de uso y validaciones.
- `internal/provider`: integración reemplazable con LRCLIB.
- `internal/render`: generación del ASS y ejecución de FFmpeg.
- `internal/storage`: SQLite, caché y cola de renders.
- `internal/httpapi`: handlers HTTP y serialización JSON.
