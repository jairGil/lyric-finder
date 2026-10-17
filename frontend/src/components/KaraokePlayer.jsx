import React, { useEffect, useMemo, useRef, useState } from 'react'
import { getRenderSource } from '../services/api'

let youtubeAPI
const DEFAULT_VIDEO_DIMENSIONS = { width: 16, height: 9 }

const PALETTE = [
  'var(--accent-1)',
  'var(--accent-2)',
  'var(--accent-3)',
  'var(--accent-4)',
  'var(--accent-5)',
]

function loadYouTubeAPI() {
  if (window.YT?.Player) return Promise.resolve(window.YT)
  if (youtubeAPI) return youtubeAPI

  youtubeAPI = new Promise((resolve, reject) => {
    const previousCallback = window.onYouTubeIframeAPIReady
    window.onYouTubeIframeAPIReady = () => {
      previousCallback?.()
      resolve(window.YT)
    }

    const script = document.createElement('script')
    script.src = 'https://www.youtube.com/iframe_api'
    script.onerror = () => reject(new Error('No se pudo cargar YouTube'))
    document.head.appendChild(script)
  })

  return youtubeAPI
}

export function KaraokePlayer({ lyrics, videoId, session, trackId, onTrackAction, settings }) {
  const [isPlaying, setIsPlaying] = useState(false)
  const [currentTime, setCurrentTime] = useState(0)
  const [youtubeReady, setYoutubeReady] = useState(false)
  const [videoDimensions, setVideoDimensions] = useState(DEFAULT_VIDEO_DIMENSIONS)

  const youtubeContainer = useRef(null)
  const player = useRef(null)
  const karaokeLinesRef = useRef(null)
  const activeLine = useRef(null)

  const duration = useMemo(() => {
    const lastLine = lyrics.lyrics[lyrics.lyrics.length - 1]
    return Math.max(lastLine?.end || 0, lyrics.track.duration || 0)
  }, [lyrics])

  useEffect(() => {
    setIsPlaying(false)
    setCurrentTime(0)
  }, [lyrics])

  useEffect(() => {
    let cancelled = false
    setVideoDimensions(DEFAULT_VIDEO_DIMENSIONS)
    if (!videoId) return undefined

    getRenderSource(videoId)
      .then((info) => {
        const width = Number(info.width)
        const height = Number(info.height)
        const ratio = width / height
        if (cancelled || !Number.isFinite(ratio) || ratio < 0.25 || ratio > 4) return
        setVideoDimensions({ width, height })
      })
      .catch(() => {})

    return () => {
      cancelled = true
    }
  }, [videoId])

  useEffect(() => {
    if (!videoId || !youtubeContainer.current) return undefined

    let cancelled = false

    loadYouTubeAPI()
      .then((YouTube) => {
        if (cancelled || !youtubeContainer.current) return

        player.current = new YouTube.Player(youtubeContainer.current, {
          videoId,
          playerVars: { playsinline: 1, rel: 0 },
          events: {
            onReady: () => setYoutubeReady(true),
            onStateChange: (event) => {
              if (event.data === YouTube.PlayerState.PLAYING) setIsPlaying(true)
              if (event.data === YouTube.PlayerState.PAUSED) setIsPlaying(false)
              if (event.data === YouTube.PlayerState.ENDED) {
                setCurrentTime(duration)
                setIsPlaying(false)
              }
            },
          },
        })
      })
      .catch(() => setYoutubeReady(false))

    return () => {
      cancelled = true
      player.current?.destroy()
      player.current = null
      setYoutubeReady(false)
    }
  }, [duration, videoId])

  useEffect(() => {
    if (videoId || !isPlaying) return undefined

    let animationFrame
    let lastFrame = performance.now()

    const tick = (now) => {
      const elapsed = (now - lastFrame) / 1000
      lastFrame = now

      setCurrentTime((time) => {
        const next = Math.min(time + elapsed, duration)
        if (next >= duration) setIsPlaying(false)
        return next
      })

      animationFrame = requestAnimationFrame(tick)
    }

    animationFrame = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(animationFrame)
  }, [duration, isPlaying, videoId])

  useEffect(() => {
    if (!videoId || !isPlaying || !player.current) return undefined

    const timer = window.setInterval(() => {
      setCurrentTime(player.current?.getCurrentTime?.() || 0)
    }, 100)

    return () => window.clearInterval(timer)
  }, [isPlaying, videoId, youtubeReady])

  const highlight = settings?.highlight || 'line'
  const configuredLines = Number(settings?.lines)
  const visibleLines = Number.isFinite(configuredLines)
    ? Math.max(0, Math.min(5, Math.round(configuredLines)))
    : 2

  const activeIndex = lyrics.lyrics.findIndex(
    (line, index) =>
      currentTime >= line.start &&
      (index === lyrics.lyrics.length - 1 || currentTime < line.end),
  )

  let windowIndex = activeIndex
  if (windowIndex < 0 && lyrics.lyrics.length) {
    if (currentTime <= 0) {
      windowIndex = 0
    } else if (currentTime >= duration) {
      windowIndex = lyrics.lyrics.length - 1
    } else {
      for (let index = 0; index < lyrics.lyrics.length; index++) {
        if (currentTime >= lyrics.lyrics[index].start) windowIndex = index
      }
    }
  }

  const firstVisible = Math.max(windowIndex, 0) - visibleLines
  const visibleSlots = Array.from({ length: visibleLines * 2 + 1 }, (_, slot) => {
    const index = firstVisible + slot
    return { index, line: lyrics.lyrics[index] }
  })

  const progress = duration ? Math.min((currentTime / duration) * 100, 100) : 0

  function toggle() {
    if (videoId && player.current && youtubeReady) {
      isPlaying ? player.current.pauseVideo() : player.current.playVideo()
      return
    }
    if (currentTime >= duration) setCurrentTime(0)
    setIsPlaying((value) => !value)
  }

  function reset() {
    if (videoId && player.current && youtubeReady) {
      player.current.pauseVideo()
      player.current.seekTo(0, true)
    }
    setIsPlaying(false)
    setCurrentTime(0)
  }

  function seek(event) {
    const next = Number(event.target.value)
    if (videoId && player.current && youtubeReady) player.current.seekTo(next, true)
    setCurrentTime(next)
  }

  function renderLine(line, originalIndex) {
    const isActive = originalIndex === activeIndex
    const isMultiColors = settings?.colors === 'multi'
    const colorOrder = settings?.colorOrder || 'word'

    if (highlight !== 'word') {
      const lineColor = isMultiColors
        ? PALETTE[originalIndex % PALETTE.length]
        : PALETTE[Number(settings?.singleColor || 0) % PALETTE.length]

      return (
        <p
          key={`${line.start}-${originalIndex}`}
          ref={isActive ? activeLine : null}
          className={isActive ? 'is-active' : ''}
          style={isActive && lineColor ? { '--line-color': lineColor } : undefined}
        >
          {line.text || '\u00a0'}
        </p>
      )
    }

    const words = line.text.split(/(\s+)/)
    const realWords = words.filter((word) => word.trim())
    const lineDuration = Math.max((line.end || line.start + 1) - line.start, 0.1)
    const elapsedInLine = isActive
      ? Math.max(0, Math.min((currentTime - line.start) / lineDuration, 1))
      : 0

    let wordPosition = 0

    return (
      <p
        key={`${line.start}-${originalIndex}`}
        ref={isActive ? activeLine : null}
        className={isActive ? 'is-active word-mode' : 'word-mode'}
      >
        {words.map((word, wordPartIndex) => {
          if (!word.trim()) return word

          const position = wordPosition++
          const wordProgress = Math.max(
            0,
            Math.min(elapsedInLine * realWords.length - position, 1),
          )
          const colorIndex = isMultiColors
            ? colorOrder === 'line'
              ? originalIndex
              : position
            : Number(settings?.singleColor || 0)
          const wordColor = PALETTE[colorIndex % PALETTE.length]

          return (
            <span
              key={wordPartIndex}
              className={wordProgress > 0 ? 'word-part is-colored' : 'word-part'}
              style={{
                '--word-progress': `${(wordProgress * 100).toFixed(2)}%`,
                '--word-color': wordColor,
              }}
            >
              {word}
            </span>
          )
        })}
      </p>
    )
  }

  const fontClass = `lyrics-font-${settings?.font || 'manrope'}`
  const highlightClass = `lyrics-highlight-${highlight}`
  const legacyHorizontal = ['left', 'right'].includes(settings?.align) ? settings.align : 'center'
  const horizontalAlign = ['left', 'center', 'right'].includes(settings?.alignHorizontal)
    ? settings.alignHorizontal
    : legacyHorizontal
  const alignmentClasses = `lyrics-horizontal-${horizontalAlign} lyrics-vertical-center`

  return (
    <div
      className={`karaoke-player ${fontClass} ${highlightClass} ${alignmentClasses}`}
      style={{ '--visible-lines': visibleLines }}
    >
      {videoId && (
        <div
          className="youtube-frame"
          ref={youtubeContainer}
          style={{ aspectRatio: `${videoDimensions.width} / ${videoDimensions.height}` }}
        />
      )}

      <div className="lyrics-side">
        {session && trackId && (
          <div className="library-actions">
            <button type="button" onClick={() => onTrackAction('save')}>Guardar</button>
            <button type="button" onClick={() => onTrackAction(1)}>Me gusta</button>
            <button type="button" onClick={() => onTrackAction(-1)}>No me gusta</button>
          </div>
        )}

        {videoId && !youtubeReady && (
          <p className="player-note">Preparando el reproductor de YouTube...</p>
        )}
      </div>

      <div className="lyrics-stage">
        <div className="karaoke-lines" ref={karaokeLinesRef}>
          {visibleSlots.map(({ index, line }) =>
            line ? renderLine(line, index) : (
              <p key={`blank-${index}`} aria-hidden="true">{'\u00a0'}</p>
            ),
          )}
        </div>
      </div>

      <div className="player-foot">
        <div className="progress-row">
          <span>{formatTime(currentTime)}</span>
          <input
            id="player-position"
            name="position"
            className="progress-range"
            type="range"
            min="0"
            max={duration || 1}
            step="0.01"
            value={currentTime}
            onChange={seek}
            aria-label="Posición de la letra"
            style={{ '--progress': `${progress}%` }}
          />
          <span>{formatTime(duration)}</span>
        </div>

        <div className="transport">
          <button
            className="play-button"
            type="button"
            onClick={toggle}
            title={isPlaying ? 'Pausar' : 'Reproducir'}
            aria-label={isPlaying ? 'Pausar' : 'Reproducir'}
          >
            <span aria-hidden="true">
              {isPlaying ? (
                <svg viewBox="0 0 24 24" width="15" height="17" fill="currentColor">
                  <rect x="5" y="4" width="5" height="16" rx="1.5" />
                  <rect x="14" y="4" width="5" height="16" rx="1.5" />
                </svg>
              ) : (
                '▶'
              )}
            </span>
          </button>
          <button
            className="reset-button"
            type="button"
            onClick={reset}
            title="Reiniciar karaoke"
            aria-label="Reiniciar karaoke"
          >
            ↺
          </button>
        </div>
      </div>
    </div>
  )
}

function formatTime(seconds) {
  return `${Math.floor(seconds / 60)}:${Math.floor(seconds % 60).toString().padStart(2, '0')}`
}