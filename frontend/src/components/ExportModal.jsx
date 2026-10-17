import React, { useEffect, useMemo, useRef, useState } from 'react'
import { createRenderPreview, getRenderFileUrl, getRenderSource } from '../services/api'
import { LyricsSettings } from './LyricsSettings'

// Los cuatro formatos que cubre la app: los tres verticales para historias,
// reels y feeds, más el horizontal clásico.
const ASPECT_PRESETS = [
  { id: '9:16', label: 'Historia', hint: 'Reels, Stories, WhatsApp', width: 1080, height: 1920 },
  { id: '4:5', label: 'Feed', hint: 'Instagram y Facebook', width: 1080, height: 1350 },
  { id: '1:1', label: 'Cuadrado', hint: 'Feed clásico', width: 1080, height: 1080 },
  { id: '16:9', label: 'Horizontal', hint: 'YouTube y TV', width: 1920, height: 1080 },
]

const MIN_CLIP_SECONDS = 3
const DEFAULT_CLIP_SECONDS = 15
const STEP = 0.1

// Los campos del estilo que se aplican juntos: si alguno de estos cambió desde el
// último Aplicar hay algo sin confirmar, y en ese estado no se exporta.
const STYLE_KEYS = ['highlight', 'colors', 'singleColor', 'palette', 'colorOrder', 'font', 'sizePercent', 'alignHorizontal', 'alignVertical']

// El largo del clip se elige de a saltos, no con un slider libre: son valores que
// se piden enteros y con pasos de 5 no aparecen 13,7 s. El video de fondo puede
// obligar a uno más corto, y eso lo avisa el texto del fondo.
const CLIP_LENGTHS = [10, 15, 20, 30, 45, 60]

// Un video de 59,9s no es un video de 60s, pero para el usuario elegir 60 y
// quedarse con 59,9 es lo mismo que no tener la opción. Con este margen la
// opción queda habilitada; el largo real del clip lo sigue poniendo el tamaño
// del archivo, que es lo que se exporta.
const LENGTH_TOLERANCE = 1

// El servidor corta el borrador a 15s. El valor vive en dos lados a propósito:
// el de Go está en maxDraftSeconds y este solo decide qué texto se le muestra al
// usuario, así que conviene que se manifiesto si alguna vez cambia.
const MAX_PREVIEW_SECONDS = 15
// Cada cambio del fragmento lanza un ffmpeg en el servidor. El debounce ya evita
// pedir uno por cada pixel, pero arrastrar el tirador en varios tramos igual
// generaba una tanda de peticiones. Por eso, mientras se arrastra solo se mueve la
// ventana y el borrador se pide una sola vez, al soltar.
const PREVIEW_DEBOUNCE_MS = 550

// El degradado arranca oscuro a propósito. La paleta de la app son cinco colores
// brillantes, así que un fondo claro (o uno que empiece en el mismo accent-1)
// deja el texto apenas visible: solo se distingue por el contorno.
const DEFAULT_GRADIENT_FROM = '#0b1020'
const DEFAULT_GRADIENT_TO = '#3d1a5c'

// El audio sale siempre del video de YouTube que ya eligió la búsqueda de
// letras, así que el modal no pregunta de dónde viene: solo lo resuelve y lo
// muestra. El fondo sigue siendo el de abajo, el link no se edita y no hay
// alternativa sin audio, porque sin canción no hay video que valga.
function formatTime(seconds) {
  const total = Math.max(0, Math.round(seconds || 0))
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, '0')}`
}

// La duración de un archivo se lee en el navegador con un elemento de medios, sin
// subirlo. Es lo que permite topar el selector de fragmento antes de mandar los
// cientos de MB de un video. Las fotos no tienen duración real y no topan nada.
function readFileDuration(file) {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file)
    const element = document.createElement(file.type.startsWith('video') ? 'video' : 'audio')
    element.preload = 'metadata'

    element.onloadedmetadata = () => {
      URL.revokeObjectURL(url)
      const duration = element.duration
      if (!Number.isFinite(duration) || duration <= 0) {
        reject(new Error('No se pudo leer la duración del archivo.'))
        return
      }
      resolve(duration)
    }
    element.onerror = () => {
      URL.revokeObjectURL(url)
      reject(new Error('El navegador no pudo leer ese archivo. Probá con MP4, MOV, WebM, JPG o PNG.'))
    }
    element.src = url
  })
}

// El mismo criterio que timelineLength en el service del backend: la duración
// de la pista y, si no hay, hasta donde termina la última línea más un margen.
// Tiene que coincidir porque el backend vuelve a calcularla y recorta contra
// ese valor, así que mover una de las dos cambia el largo del video.
function timelineDuration(lyrics) {
  let total = lyrics?.track?.duration || 0
  for (const line of lyrics?.lyrics || []) {
    if ((line.end || 0) > total) total = line.end
  }
  return total > 0 ? total + 1.5 : 0
}

export function ExportModal({ lyrics, settings, onSettingsChange, render, videoId = '', onClose }) {
  const [sourceInfo, setSourceInfo] = useState(null)
  const [sourceLoading, setSourceLoading] = useState(false)
  const [sourceError, setSourceError] = useState('')
  const [clipStart, setClipStart] = useState(0)
  const [clipLength, setClipLength] = useState(DEFAULT_CLIP_SECONDS)
  // Lo que se está arrastrando ahora. Vive aparte del valor confirmado para que
  // mover el tirador no dispare un borrador: la ventana se dibuja con este, y
  // clipStart solo cambia al soltar, que es lo que pide el preview.
  const [dragStart, setDragStart] = useState(null)
  const [presetId, setPresetId] = useState('9:16')
  const [background, setBackground] = useState('gradient')
  const [color, setColor] = useState(DEFAULT_GRADIENT_FROM)
  const [colorTwo, setColorTwo] = useState(DEFAULT_GRADIENT_TO)
  const [imageFile, setImageFile] = useState(null)
  // Un video de fondo tops el largo del clip, así que su duración se lee en el
  // navegador para topar el doble slider antes de que el usuario llegue al final.
  const [backgroundVideo, setBackgroundVideo] = useState(0)
  const [backgroundError, setBackgroundError] = useState('')
  // El estilo se prueba en vivo pero no se manda solo: el borrador y el render
  // usan la última versión aplicada, que es la que se vio en la vista previa.
  // Probar cinco colores son cinco ffmpeg, y esperar por un video que se va a ver
  // igual es la parte lenta de exportar.
  const [appliedStyle, setAppliedStyle] = useState(settings)
  // La vista previa usa dos <video> que se turnan: el de adelante ya tiene un
  // cuadro y el de atrás carga escondido. Cuando el de atrás termina de
  // cargar pasa al frente. Remontar un solo elemento, o cambiarle el src de
  // golpe, lo deja un instante en readyState 0 y eso es el salto a negro que se
  // veía al mover el fragmento.
  const [previewSlots, setPreviewSlots] = useState(['', ''])
  const [frontSlot, setFrontSlot] = useState(0)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [previewError, setPreviewError] = useState('')
  // Los controles del fondo quedan ocultos detrás de inputs nativos, así que
  // hace falta guardarlos para poder abrir el diálogo al elegir la tarjeta y no
  // solo cuando se los toca a mano.
  const colorRef = useRef(null)
  const backgroundFileRef = useRef(null)
  const dialogRef = useRef(null)
  const trackRef = useRef(null)
  // Dónde se agarró la ventana y dónde estaba cuando se agarró. Va en un ref
  // porque solo lo usa el puntero: si fuera estado, cada movimiento volvería a
  // pintar el componente entero.
  const grabRef = useRef(null)
  // Los object URL de cada slot, en un ref y no en el estado: hay que revocarlos
  // en el momento exacto en que se sueltan, no cuando React decide volver a
  // correr el updater.
  const slotUrlsRef = useRef(['', ''])
  // Qué slot está al frente y cuál esperando cuadro. En refs porque los lee el
  // efecto del preview: si fueran estado, cada promoción volvería a disparar el
  // efecto y pediría un borrador que ya se está mostrando.
  const frontSlotRef = useRef(0)
  const pendingSlotRef = useRef(1)
  // Último link que se intentó resolver solo, para no reintentar en bucle el que
  // ya falló. Vive en un ref porque no tiene que provocar renders.
  const pendingRef = useRef('')

  const track = lyrics.track

  // El ID viene de la búsqueda de letras y no cambia mientras el modal está
  // abierto, así que la ficha resuelta vale siempre que sea del mismo video.
  const youtubeSource = sourceInfo?.id === videoId ? sourceInfo : null

  // La línea de tiempo que se muestra es la del video de YouTube: el fragmento se
  // recorta contra el audio, que es lo que manda en el render. Antes de resolverlo
  // se usa la de la letra para que el doble slider tenga algo que mostrar.
  const duration = youtubeSource?.duration || timelineDuration(lyrics)

  // El video de fondo no acorta la línea de tiempo: el fragmento se elige en toda
  // la canción y el server hace repetir el video para cubrirlo. Lo único que el
  // fondo tops es el largo del clip, para no exportar más segundos de fondo que los
  // que el video aporta.
  const lengthLimit = backgroundVideo > 0 ? Math.min(duration, backgroundVideo) : duration
  // El video de fondo solo topa el largo cuando es más corto que la canción; si
  // dura más, el tope sigue siendo el audio y no hay nada que avisar.
  const backgroundIsLimit = backgroundVideo > 0 && backgroundVideo < duration
  const preset = ASPECT_PRESETS.find((item) => item.id === presetId) || ASPECT_PRESETS[0]
  const minClip = Math.min(MIN_CLIP_SECONDS, lengthLimit)

  // La ventana del fragmento tiene siempre el mismo largo y se desplaza entera,
  // como las historias: no hay dos tiradores que la agranden, hay una que la mueve.
  // El largo sale de lo que pidas (o del video de fondo si es más corto) y el
  // punto de partida es lo único que se mueve, sobre toda la canción.
  const effectiveLength = Math.min(Math.max(clipLength, minClip), lengthLimit)
  const maxStart = Math.max(0, duration - effectiveLength)
  // Lo que se ve es el arrastre si lo hay, y lo confirmado si no. El formulario
  // que se manda al servidor usa siempre lo confirmado.
  const shownStart = dragStart === null ? clipStart : dragStart
  const clipEnd = Math.min(shownStart + effectiveLength, duration)
  const clipDuration = Math.max(0, clipEnd - shownStart)
  // El final de lo que se exportaría, que durante el arrastre es distinto del que
  // se ve en pantalla: lo que se manda al servidor es siempre el confirmado.
  const committedEnd = Math.min(clipStart + effectiveLength, duration)

  const status = render.job?.status
  const finished = status === 'done'
  const working = render.submitting || status === 'queued' || status === 'running'

  useEffect(() => {
    function handleKeyDown(event) {
      if (event.key === 'Escape' && !working) onClose()
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [onClose, working])

  useEffect(() => {
    if (!dialogRef.current) return
    const focusable = dialogRef.current.querySelector('button, input, a[href]')
    focusable?.focus()
  }, [])

  const range = useMemo(() => {
    if (!duration) return { startPercent: 0, lengthPercent: 0 }
    return {
      startPercent: (shownStart / duration) * 100,
      lengthPercent: Math.min((effectiveLength / duration) * 100, 100),
    }
  }, [shownStart, duration, effectiveLength])

  // La ventana tiene que quedar siempre dentro del tope, y el tope cambia solo:
  // la ficha de la pista y el archivo de fondo se resuelven por separado y el que
  // tarda recorta la ventana del otro. Por eso se corrige el estado y no se deja
  // que sea el input el que se recorte solo, que es cuando el readout miente.
  useEffect(() => {
    if (lengthLimit <= 0) return
    setClipLength((len) => (len > lengthLimit ? lengthLimit : len < minClip ? minClip : len))
    setClipStart((start) => Math.min(start, maxStart))
  }, [lengthLimit, minClip, maxStart])

  // El navegador no puede deducir cuánto dura un video, así que se le pregunta
  // al servidor apenas se abre el modal. Recién con esa duración el slider y el
  // borrador tienen sentido, y el error se muestra acá en vez de quedar enterrado
  // en el preview, que solo diría "borrador fallido" sin decir qué falló.
  //
  // El ID ya viene resuelto desde la búsqueda de letras, así que no hay nada que
  // elegir ni escribir: alcanza con resolverlo una vez.
  async function resolveSource(target) {
    if (!target || sourceLoading) return
    pendingRef.current = target
    setSourceLoading(true)
    setSourceError('')
    try {
      const info = await getRenderSource(target)
      if (!Number.isFinite(info.duration) || info.duration <= 0) {
        throw new Error('Ese video no tiene duración.')
      }
      setSourceInfo({ id: info.id, title: info.title, duration: info.duration })
      setClipStart(0)
      setDragStart(null)
      // Igual que con el video de fondo, el largo elegido manda: solo se acorta
      // si el video de la pista es más corto que lo que se pidió.
      setClipLength((len) => Math.min(Math.max(len, minClip), info.duration))
    } catch (error) {
      setSourceInfo(null)
      setSourceError(error.message)
    } finally {
      setSourceLoading(false)
    }
  }

  // Un video que falló no se reintenta solo en bucle, así que el intento queda
  // anotado. sourceLoading va en las dependencias a propósito: si el ID cambia
  // mientras había una consulta en vuelo, el efecto corre cuando esa termina y
  // el video nuevo queda sin resolver.
  useEffect(() => {
    if (!videoId) return
    if (sourceInfo?.id === videoId) return
    if (pendingRef.current === videoId) return
    resolveSource(videoId)
  }, [videoId, sourceInfo?.id, sourceLoading])

  // Un link nuevo se resuelve solo. Con el video de la pista ya viene precargado
  // al abrir la pestaña, así que un click alcanza para tener duración y borrador.
  // Al dejar el fondo de archivo se olvidan el archivo y su duración: el tope solo
  // existe mientras el video sea el fondo, y el input de archivos se desmonta al
  // cambiar de modo, así que dejarlo puesto escondería una selección que ya no se
  // ve en pantalla. Al elegir se abre directo lo que hay que tocar: el selector de
  // color para el color plano, el submenú con los dos tonos para el degradado y el
  // explorador para la foto o el video. Así no hay un paso extra de buscar dónde
  // quedó el control.
  function handleBackgroundSelect(option) {
    setBackground(option)
    if (option !== 'image') {
      setImageFile(null)
      setBackgroundVideo(0)
      setBackgroundError('')
    }
    if (option === 'image') {
      openBackgroundPicker(backgroundFileRef)
    } else if (option === 'color' || option === 'gradient') {
      // El degradado muestra los dos tonos abajo, así que se abre el primero: el
      // que se está elegindo es siempre el que estaba en el medio del degradado.
      openBackgroundPicker(colorRef)
    }
  }

  // showPicker() abre el diálogo nativo del input desde código. Necesita un gesto
  // del usuario, y el clic en la tarjeta lo es; esperar un frame es para cuando el
  // input recién se monta al cambiar de fondo. Si el navegador no lo soporta, se
  // cae al click() de siempre.
  function openBackgroundPicker(ref) {
    const input = ref.current
    if (!input || input.disabled) return
    window.requestAnimationFrame(() => {
      try {
        if (typeof input.showPicker === 'function') input.showPicker()
        else input.click()
      } catch {
        input.click()
      }
    })
  }

  // Un solo tirador: mueve la ventana entera sin cambiar su largo, como las
  // historias. El tope del input es hasta dónde puede arrancar la ventana, que es
  // el tope menos lo que mide.
  //
  // El arrastre se calcula sobre la pista y no sobre el input: el input reparte
  // su valor en todo el ancho, así que con la ventana de 15s dentro de una
  // canción de 3:41 el tirador caía en un lado y la ventana en el otro. Además,
  // cuando el video de fondo es más corto que el largo pedido la ventana se
  // queda sin recorrido y el input dead, mientras que la pista sí dice dónde
  // puede parar.
  // Un click soltaba la ventana en el segundo equivalente al punto tocado, y eso
  // se leía como un salto: aparecía en otro lado sin que el puntero se hubiera
  // movido. Ahora el click solo marca el punto de agarre y la ventana se mueve con
  // el desplazamiento del puntero, que es lo que se hace con cualquier control de
  // arrastre. Si el dedo no se mueve, la ventana no se mueve.
  function handleTrackPointerDown(event) {
    if (working) return
    const track = trackRef.current
    if (!track || !duration || maxStart <= 0) return
    const rect = track.getBoundingClientRect()
    if (rect.width <= 0) return
    event.currentTarget.setPointerCapture(event.pointerId)
    grabRef.current = { x: event.clientX, start: shownStart }
    setDragStart(shownStart)
  }

  function handleTrackPointerMove(event) {
    const grab = grabRef.current
    if (!grab) return
    const track = trackRef.current
    if (!track) return
    const rect = track.getBoundingClientRect()
    if (rect.width <= 0) return
    // Cuántos segundos vale un píxel del riel, y cuánto se corrió el puntero.
    const secondsPerPixel = duration / rect.width
    const delta = (event.clientX - grab.x) * secondsPerPixel
    setDragStart(Math.max(0, Math.min(grab.start + delta, maxStart)))
  }

  function handleTrackPointerUp() {
    grabRef.current = null
    handleStartCommit()
  }

  // Durante el arrastre solo se mueve el dibujo; al soltar se confirma y ahí sí
  // cambia el valor que se manda al render, que es el que dispara el borrador.
  function handleStartDrag(value) {
    setDragStart(Math.max(0, Math.min(value, maxStart)))
  }

  function handleStartCommit() {
    if (dragStart === null) return
    setClipStart(dragStart)
    setDragStart(null)
  }

  function handleLengthChange(value) {
    const next = Number(value)
    setClipLength(next)
    // Una ventana más larga deja menos recorrido, así que el punto de partida se
    // corre para que el final no se pase del tope.
    const nuevoMaxStart = Math.max(0, duration - Math.min(next, lengthLimit))
    setClipStart((start) => Math.min(start, nuevoMaxStart))
    setDragStart(null)
  }

  // El archivo de fondo se lee en el navegador para conocer su duración antes de
  // subirlo. Si es un video, esa duración topa el clip, así que el fragmento
  // arranca en 0 y no puede pasar de ahí.
  //
  // El archivo solo se guarda cuando ya se sabe la duración: así el estado cambia
  // una vez y se pide un único borrador, ya con el tope del video aplicado. Si se
  // guardara antes, la primera petición saldría sin recortar y habría que
  // descartar.
  async function handleBackgroundChange(event) {
    const nextFile = event.target.files?.[0] || null
    setBackgroundError('')
    if (!nextFile) {
      setImageFile(null)
      setBackgroundVideo(0)
      return
    }
    if (!nextFile.type.startsWith('video/')) {
      setImageFile(nextFile)
      setBackgroundVideo(0)
      return
    }
    try {
      const seconds = await readFileDuration(nextFile)
      setImageFile(nextFile)
      setBackgroundVideo(seconds)
      setClipStart(0)
      setDragStart(null)
      // El largo que eligió el usuario se respeta: solo se acorta si el video no
      // llega. Volverlo a 15 s cada vez que se sube un video perdería el ajuste.
      setClipLength((len) => Math.min(Math.max(len, minClip), seconds))
    } catch (error) {
      setBackgroundError(error.message)
      setImageFile(null)
      setBackgroundVideo(0)
    }
  }

  const imageMissing = background === 'image' && !imageFile
  const stylePending = STYLE_KEYS.some(
    (key) => String(settings[key] ?? '') !== String(appliedStyle[key] ?? ''),
  )
  // Sin el video resuelto no hay audio, y el render final no se arma sin medio:
  // por eso el botón espera a la ficha en vez de fallar al enviar.
  const canSubmit = Boolean(
    youtubeSource && clipDuration >= minClip - STEP && !imageMissing && !stylePending && !working,
  )

  // El borrador se arma con el mismo formulario que el render, así que el
  // preview muestra exactamente lo que se va a exportar.
  const buildForm = () => {
    const form = new FormData()
    form.set('artist', track.artist)
    form.set('track', track.title)
    // El ID solo, no el link: el backend lo resuelve al ID y lo usa de clave de
    // caché, así que no hace falta armar una URL.
    if (youtubeSource) form.set('sourceUrl', youtubeSource.id)
    form.set('background', background)
    form.set('color', color)
    form.set('colorTwo', colorTwo)
    form.set('width', String(preset.width))
    form.set('height', String(preset.height))
    form.set('fps', '30')
    form.set('start', clipStart.toFixed(3))
    form.set('duration', clipDuration.toFixed(3))
    form.set('highlight', appliedStyle.highlight || 'word')
    form.set('colors', appliedStyle.colors || 'single')
    form.set('singleColor', String(appliedStyle.singleColor ?? 0))
    // La lista de colores con varios modos viaja como "0,4,5": el render recorre
    // exactamente esos, en ese orden, y si no llega ninguno usa la paleta entera.
    form.set('palette', (appliedStyle.palette ?? []).join(','))
    form.set('colorOrder', appliedStyle.colorOrder || 'word')
    form.set('font', appliedStyle.font || 'manrope')
    form.set('sizePercent', String(appliedStyle.sizePercent || 100))
    form.set('alignHorizontal', appliedStyle.alignHorizontal || 'center')
    form.set('alignVertical', appliedStyle.alignVertical || appliedStyle.align || 'center')
    if (background === 'image' && imageFile) form.set('image', imageFile)
    return form
  }

  // Todo lo que cambia el resultado del render tiene que estar en esta firma:
  // es lo que dispara el borrador nuevo. Solo mira lo confirmado, nunca el
  // arrastre, que si no cada pixel movido pediría un ffmpeg. Del estilo mira lo
  // aplicado: cambiar un color no pide nada hasta que se presiona Aplicar.
  const previewKey = [
    youtubeSource?.id,
    imageFile?.name, imageFile?.size,
    clipStart.toFixed(2), committedEnd.toFixed(2),
    presetId, background, color, colorTwo,
    appliedStyle.highlight, appliedStyle.colors, appliedStyle.singleColor,
    (appliedStyle.palette ?? []).join(','),
    appliedStyle.colorOrder, appliedStyle.font, appliedStyle.sizePercent,
    appliedStyle.alignHorizontal, appliedStyle.alignVertical,
  ].join('|')

  // Con el video sin resolver no hay nada que previsualizar: el borrador saldría
  // mudo y con la línea de tiempo de la letra, que no es lo que se va a exportar.
  const previewReady = Boolean(youtubeSource) && !imageMissing && !working && !sourceLoading

  // El bloque se dibuja siempre que haya un video resuelto, aunque todavía falte
  // el archivo de fondo. Si se escondiera al elegir "Foto o video", el borrador
  // desaparecería de golpe y el modal quedaría con un hueco negro.
  // El marco no espera a que la fuente esté: si apareciera solo cuando ya hay un
  // video resuelto, el modal crecería de golpe en el momento de pedirlo. Con el
  // esqueleto dibujado desde el inicio el alto se mantiene siempre.
  const previewVisible = !working && (Boolean(youtubeSource) || sourceLoading)

  useEffect(() => {
    if (!previewReady) {
      setPreviewLoading(false)
      return undefined
    }

    const controller = new AbortController()
    const timer = setTimeout(async () => {
      setPreviewLoading(true)
      setPreviewError('')
      try {
        const url = await createRenderPreview(buildForm(), { signal: controller.signal })
        if (controller.signal.aborted) {
          URL.revokeObjectURL(url)
          return
        }
        // El borrador entra por el slot de atrás, que está escondido. El de
        // adelante sigue con su cuadro hasta que el nuevo tenga uno propio.
        const slot = frontSlotRef.current === 0 ? 1 : 0
        const replaced = slotUrlsRef.current[slot]
        if (replaced) URL.revokeObjectURL(replaced)
        slotUrlsRef.current[slot] = url
        pendingSlotRef.current = slot
        setPreviewSlots((prev) => {
          const next = [...prev]
          next[slot] = url
          return next
        })
      } catch (error) {
        // Un preview cancelado es lo normal al seguir moviendo un slider, no
        // un error que haya que mostrar.
        if (!controller.signal.aborted) setPreviewError(error.message)
      } finally {
        if (!controller.signal.aborted) setPreviewLoading(false)
      }
    }, PREVIEW_DEBOUNCE_MS)

    return () => {
      clearTimeout(timer)
      controller.abort()
    }
    // buildForm queda fuera a propósito: previewKey ya resume cada valor que
    // lee, y su identidad cambia con cada render del padre, lo que pediría un
    // borrador sin parar. Lo que se captura es un buildForm con los valores del
    // momento en que cambió previewKey, que es lo que importa.
  }, [previewKey, previewReady])

  // Al cerrar el modal quedan object URL sin revocar.
  useEffect(() => () => {
    slotUrlsRef.current.forEach((url) => url && URL.revokeObjectURL(url))
    slotUrlsRef.current = ['', '']
  }, [])

  // El slot de atrás ya tiene cuadro: pasa al frente y el otro queda libre para
  // el siguiente borrador. Se suelta el URL del que sale de la vista porque ya
  // no se va a volver a pintar.
  function promoteSlot(slot) {
    if (slot === frontSlotRef.current) return
    const stale = slotUrlsRef.current[slot === 0 ? 1 : 0]
    if (stale) URL.revokeObjectURL(stale)
    slotUrlsRef.current[slot === 0 ? 1 : 0] = ''
    frontSlotRef.current = slot
    setFrontSlot(slot)
    setPreviewSlots((prev) => {
      const next = ['', '']
      next[slot] = prev[slot]
      return next
    })
  }

  async function handleSubmit(event) {
    event.preventDefault()
    if (!canSubmit) return
    await render.submit(buildForm()).catch(() => {})
  }

  return (
    <div className="export-backdrop" onPointerDown={(event) => event.target === event.currentTarget && !working && onClose()}>
      <div className="export-modal" role="dialog" aria-modal="true" aria-label="Exportar video" ref={dialogRef}>
        <header className="export-header">
          <div>
            <p className="eyebrow">Exportar</p>
            <h3>{track.title}</h3>
            <span className="export-artist">{track.artist}</span>
          </div>
          <button type="button" className="export-close" onClick={onClose} disabled={working} aria-label="Cerrar">
            x
          </button>
        </header>

        <form className="export-body" onSubmit={handleSubmit}>
          {finished ? (
            <ResultPanel
              job={render.job}
              label={`Listo: ${track.artist} - ${track.title} · ${preset.width}×${preset.height}`}
              onReset={render.reset}
            />
          ) : (
            <>
              {!youtubeSource && !sourceLoading && !videoId && (
                <p className="export-error">
                  Esta pista no tiene un video de YouTube, así que no hay audio para exportar.
                </p>
              )}
              {sourceError && <p className="export-error">{sourceError}</p>}

              {duration > 0 && (
                <div className="export-field">
                  <span>Fragmento</span>
                  <p className="clip-readout">
                    <strong>
                      {formatTime(shownStart)} – {formatTime(clipEnd)}
                    </strong>
                    <span>{clipDuration.toFixed(1)}s de {formatTime(duration)}</span>
                  </p>
                  <div className="clip-range">
                    <div
                      className="clip-range-track"
                      ref={trackRef}
                      onPointerDown={handleTrackPointerDown}
                      onPointerMove={handleTrackPointerMove}
                      onPointerUp={handleTrackPointerUp}
                      onPointerCancel={handleTrackPointerUp}
                    >
                      <div
                        className="clip-range-window"
                        style={{
                          left: `${range.startPercent}%`,
                          width: `${range.lengthPercent}%`,
                        }}
                      />
                    </div>
                    <input
                      id="clip-start"
                      name="clipStart"
                      type="range"
                      className="clip-range-input"
                      min="0"
                      max={maxStart || 0}
                      step={STEP}
                      value={shownStart}
                      disabled={working}
                      aria-label="Mover el fragmento"
                      aria-valuetext={`${formatTime(shownStart)} a ${formatTime(clipEnd)}`}
                      onChange={(event) => handleStartDrag(Number(event.target.value))}
                      onKeyUp={handleStartCommit}
                      onBlur={handleStartCommit}
                    />
                  </div>
                  <div className="clip-length">
                    <label htmlFor="clip-length">Largo</label>
                    <select
                      id="clip-length"
                      name="clipLength"
                      value={clipLength}
                      disabled={working}
                      onChange={(event) => handleLengthChange(event.target.value)}
                    >
                      {CLIP_LENGTHS.map((seconds) => (
                        <option
                          key={seconds}
                          value={seconds}
                          disabled={seconds > lengthLimit + LENGTH_TOLERANCE}
                        >
                          {seconds}s
                        </option>
                      ))}
                    </select>
                    {backgroundIsLimit && (
                      <span className="clip-length-note">
                        el video dura {backgroundVideo.toFixed(1)}s y se repite
                      </span>
                    )}
                  </div>
                </div>
              )}

              {previewVisible && (
                <div className="export-field">
                  {/* La aclaración sobre la vista previa vive al lado del título, en
                      una línea de nota: antes ocupaba un párrafo entero debajo del
                      marco y empujaba todo lo que venía abajo. */}
                  <div className="export-legend">
                    <span>Vista previa</span>
                    <p className="export-legend-note">
                      {clipDuration > MAX_PREVIEW_SECONDS
                        ? `Muestra los primeros ${MAX_PREVIEW_SECONDS}s; el video sale entero.`
                        : 'Es el mismo render, en pequeño.'}
                    </p>
                  </div>
                  {/* El marco se dibuja siempre, tenga o no borrador, para que el
                      alto del modal no cambie entre uno y otro. */}
                  <div
                    className={`preview-frame ratio-${presetId.replace(':', '-')}`}
                    aria-busy={previewLoading}
                  >
                    {previewSlots.some(Boolean) ? (
                      <>
                        {[0, 1].map((slot) => {
                          const src = previewSlots[slot]
                          if (!src) return null
                          return (
                            <video
                              key={src}
                              className="preview-video"
                              style={{ display: slot === frontSlot ? 'block' : 'none' }}
                              src={src}
                              controls
                              muted
                              autoPlay
                              loop
                              playsInline
                              preload="auto"
                              onLoadedData={() => {
                                if (pendingSlotRef.current === slot) {
                                  pendingSlotRef.current = slot === 0 ? 1 : 0
                                  promoteSlot(slot)
                                }
                              }}
                            />
                          )
                        })}
                      </>
                    ) : (
                      <div className="preview-skeleton" aria-hidden="true" />
                    )}
                    <span className="sr-only" role="status">
                      {previewLoading ? 'Generando la vista previa' : 'Sin vista previa'}
                    </span>
                  </div>
                  {imageMissing ? (
                    <p className="export-warning">
                      Elegí una foto o un video: en cuanto lo cargues el borrador se rehace con ese fondo.
                    </p>
                  ) : null}
                  {previewError && (
                    <p className="export-warning">
                      No se pudo generar la vista previa: {previewError} Podés exportar igual.
                    </p>
                  )}
                </div>
              )}

{/* Los mismos controles que la barra de la canción, para no tener que
                  cerrar el modal a cada cambio de fuente o color. Sin la caja de
                  export-field alrededor: el panel ya es una caja y envolverlo
                  dibujaba dos bordes y dos títulos apilados. */}
              {onSettingsChange && (
                <LyricsSettings
                  mode="export"
                  settings={settings}
                  onChange={onSettingsChange}
                  onApply={() => setAppliedStyle(settings)}
                  pending={stylePending}
                  expanded
                  inline
                />
              )}

              <div className="export-field">
                <span>Formato</span>
                {/* Solo el dibujo de la proporción: el nombre va en el title y en
                    el aria-label, que es lo que queda para el lector de pantalla
                    y para el puntero. Cuatro tarjetas con tres textos cada una
                    tapaban el ancho del modal sin aportar nada. */}
                <div className="preset-grid" role="radiogroup" aria-label="Proporción del video">
                  {ASPECT_PRESETS.map((item) => (
                    <button
                      key={item.id}
                      type="button"
                      role="radio"
                      aria-checked={presetId === item.id}
                      aria-label={`${item.id} ${item.label}, ${item.hint}`}
                      title={`${item.id} · ${item.label}`}
                      className={`preset-option ${presetId === item.id ? 'is-selected' : ''}`}
                      onClick={() => setPresetId(item.id)}
                      disabled={working}
                    >
                      <span className={`preset-shape ratio-${item.id.replace(':', '-')}`} aria-hidden="true" />
                      {/* El identificador es lo único que dice qué sale. El nombre
                          largo y la pista de uso van en el aria-label, que es lo
                          que queda para el lector de pantalla y para el puntero. */}
                      <span className="preset-id" aria-hidden="true">{item.id}</span>
                    </button>
                  ))}
                </div>
              </div>

              {/* Tres tarjetas con su dibujo en vez de una fila de botones: se
                  elige el fondo mirando lo que va a pasar, y los controles que
                  importan aparecen abajo, solo los del fondo elegido. */}
              <div className="export-field">
                <span>Fondo</span>
                <div className="background-grid" role="radiogroup" aria-label="Tipo de fondo">
                  {[
                    { id: 'color', label: 'Color', art: <span className="background-art is-solid" /> },
                    { id: 'gradient', label: 'Degradado', art: <span className="background-art is-gradient" /> },
                    { id: 'image', label: 'Foto o video', art: <span className="background-art is-photo" /> },
                  ].map((option) => (
                    <button
                      key={option.id}
                      type="button"
                      role="radio"
                      aria-checked={background === option.id}
                      className={`background-option ${background === option.id ? 'is-selected' : ''}`}
                      onClick={() => handleBackgroundSelect(option.id)}
                      disabled={working}
                    >
                      {option.art}
                      <span>{option.label}</span>
                    </button>
                  ))}
                </div>

                {background === 'image' ? (
                  <div className="background-file">
                    <label className="background-drop" htmlFor="background-file">
                      <span className="background-drop-title">
                        {imageFile ? 'Cambiar el archivo' : 'Elegir foto o video'}
                      </span>
                      <span className="background-drop-hint">
                        {imageFile
                          ? `${imageFile.name}${backgroundVideo ? ` · video de ${formatTime(backgroundVideo)}` : ' · foto'}`
                          : 'Se repite para cubrir todo el clip'}
                      </span>
                    </label>
                    <input
                      id="background-file"
                      ref={backgroundFileRef}
                      name="background"
                      type="file"
                      accept="image/*,video/*"
                      disabled={working}
                      onChange={handleBackgroundChange}
                    />
                  </div>
                ) : (
                  <div className="color-row">
                    <label className="color-input">
                    <input
                      id="background-color"
                      ref={colorRef}
                      name="color"
                      type="color"
                        value={color}
                        disabled={working}
                        onChange={(event) => setColor(event.target.value)}
                      />
                      <span>Desde</span>
                    </label>
                    {background === 'gradient' && (
                      <label className="color-input">
                        <input
                          id="background-color-two"
                          name="colorTwo"
                          type="color"
                          value={colorTwo}
                          disabled={working}
                          onChange={(event) => setColorTwo(event.target.value)}
                        />
                        <span>Hasta</span>
                      </label>
                    )}
                  </div>
                )}

                {background === 'image' && backgroundIsLimit && (
                  <p className="export-hint">
                    Podés elegir el fragmento en toda la canción: el video se repite para cubrirlo. El clip
                    no puede durar más que los {formatTime(backgroundVideo)} que aporta el video. El sonido
                    sigue siendo el de la canción.
                  </p>
                )}

                {imageMissing && <p className="export-error">Elegí una foto o un video para el fondo.</p>}
                {backgroundError && <p className="export-error">{backgroundError}</p>}
              </div>

              {render.error && <p className="export-error">{render.error}</p>}

              {working && render.job && (
                <div className="export-progress">
                  <div className="export-progress-bar" style={{ width: `${Math.round((render.job.progress || 0) * 100)}%` }} />
                  <span>{Math.round((render.job.progress || 0) * 100)}%</span>
                </div>
              )}

              <footer className="export-footer">
                <p className="export-summary">
                  {preset.width}×{preset.height} · {formatTime(clipStart)}–{formatTime(committedEnd)}
                </p>
                <button type="submit" className="export-submit" disabled={!canSubmit}>
                  {working ? 'Generando…' : 'Generar video'}
                </button>
              </footer>
            </>
          )}
        </form>
      </div>
    </div>
  )
}

function ResultPanel({ job, label, onReset }) {
  const url = getRenderFileUrl(job.id)

  return (
    <div className="export-result">
      <video className="export-preview" src={url} controls playsInline />
      <p className="export-summary">{label}</p>
      <div className="export-result-actions">
        <a className="export-submit" href={url}>
          Descargar MP4
        </a>
        <button type="button" className="export-again" onClick={onReset}>
          Exportar otro
        </button>
      </div>
    </div>
  )
}
