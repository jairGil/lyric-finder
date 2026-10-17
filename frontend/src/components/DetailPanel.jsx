import React, { useEffect, useRef, useState } from 'react'
import { ExportModal } from './ExportModal'
import { KaraokePlayer } from './KaraokePlayer'
import { LyricsSettings } from './LyricsSettings'
import { useRenderJob } from '../hooks/useRenderJob'
import { getRenderFileUrl } from '../services/api'

function renderTrackKey(artist, title) {
  return `${String(artist || '').trim().toLowerCase()}::${String(title || '').trim().toLowerCase()}`
}

export function DetailPanel({ selected, lyrics, state, session, onTrackAction, settings, onSettingsChange }) {
  const [expanded, setExpanded] = useState(false)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [exportOpen, setExportOpen] = useState(false)
  const [completedRenders, setCompletedRenders] = useState({})
  const toolbarRef = useRef(null)
  const render = useRenderJob(session?.token)

  // El job pertenece a la pista: al cambiar de canción se descarta el anterior
  // para no ofrecer la descarga de un render que ya no corresponde.
  useEffect(() => {
    render.reset()
  }, [selected?.trackId, selected?.track?.title])

  useEffect(() => {
    const job = render.job
    if (job?.status !== 'done') return

    const key = renderTrackKey(job.artist, job.title)
    setCompletedRenders((current) => {
      const jobs = current[key] || []
      if (jobs.some((item) => item.id === job.id)) return current
      return { ...current, [key]: [...jobs, { id: job.id }] }
    })
  }, [render.job])

  useEffect(() => {
    if (!settingsOpen) return undefined

    function handlePointerDown(event) {
      if (toolbarRef.current && !toolbarRef.current.contains(event.target)) {
        setSettingsOpen(false)
      }
    }

    document.addEventListener('pointerdown', handlePointerDown, true)
    return () => document.removeEventListener('pointerdown', handlePointerDown, true)
  }, [settingsOpen])

  if (!selected) {
    return (
      <aside className="detail-panel">
        <div className="detail-placeholder">
          <span className="placeholder-line" />
          <span className="placeholder-line short" />
          <p>Selecciona una pista para ver su letra.</p>
        </div>
      </aside>
    )
  }

  if (state.loading) {
    return (
      <aside className="detail-panel">
        <DetailHeading
          title={selected.track.title}
          artist={selected.track.artist}
          eyebrow="Loading lyrics"
        />
        <div className="lyrics-loading">
          <span />
          <span />
          <span />
          <span />
        </div>
      </aside>
    )
  }

  if (state.error) {
    return (
      <aside className="detail-panel">
        <div className="detail-error">
          <p>No se pudo cargar la letra.</p>
          <span>{state.error}</span>
        </div>
      </aside>
    )
  }

  const hasKaraoke = lyrics.synchronized && lyrics.lyrics?.length
  const trackRenders = completedRenders[renderTrackKey(lyrics.track.artist, lyrics.track.title)] || []
  const youtubeUrl = `https://www.youtube.com/results?search_query=${encodeURIComponent(
    `${lyrics.track.artist} ${lyrics.track.title}`,
  )}`

  return (
    <aside className={`detail-panel ${expanded ? 'is-expanded' : ''}`}>
      <DetailHeading
        title={lyrics.track.title}
        artist={`${lyrics.track.artist}${lyrics.track.album ? ` · ${lyrics.track.album}` : ''}`}
        eyebrow={lyrics.synchronized ? 'Synchronized lyrics' : 'Lyrics'}
      />

      <div className="lyrics-content">
        <div className="lyrics-toolbar" ref={toolbarRef}>
          <a className="tool-button youtube-link" href={youtubeUrl} target="_blank" rel="noreferrer">
            <span aria-hidden="true">↗</span> YouTube
          </a>
          {lyrics.synchronized && (
            <button
              className="tool-button"
              type="button"
              onClick={() => setSettingsOpen(!settingsOpen)}
            >
              Estilo <span>☷</span>
            </button>
          )}
          {lyrics.synchronized && (
            <ExportControl
              render={render}
              completedRenders={trackRenders}
              onOpen={() => setExportOpen(true)}
            />
          )}
          <button
            className="tool-button expand-button"
            type="button"
            onClick={() => setExpanded(!expanded)}
          >
            {expanded ? 'Reducir' : 'Expandir'} <span>{expanded ? '↙' : '↗'}</span>
          </button>

          {settingsOpen && (
            <LyricsSettings
              mode="player"
              settings={settings}
              onChange={onSettingsChange}
              onClose={() => setSettingsOpen(false)}
              expanded={expanded}
            />
          )}
        </div>

        {hasKaraoke ? (
          <KaraokePlayer
            lyrics={lyrics}
            videoId={selected.youtubeVideoId}
            session={session}
            trackId={selected.trackId}
            onTrackAction={onTrackAction}
            settings={settings}
          />
        ) : (
          <p className="plain-lyrics">{lyrics.plainLyrics || 'No hay letra disponible.'}</p>
        )}
      </div>

      {exportOpen && (
        <ExportModal
          lyrics={lyrics}
          settings={settings}
          onSettingsChange={onSettingsChange}
          render={render}
          videoId={selected.youtubeVideoId}
          onClose={() => setExportOpen(false)}
        />
      )}
    </aside>
  )
}

// El estado del render vive en el panel y no en el modal: si quien exporta cierra
// el diálogo a mitad, el job sigue y la descarga queda a mano en la barra.
function ExportControl({ render, completedRenders, onOpen }) {
  const status = render.job?.status
  const isBusy = status === 'running' || status === 'queued' || render.submitting

  return (
    <>
      {completedRenders.length > 0 && (
        <details className="render-history">
          <summary className="tool-button" aria-label={`Descargas generadas: ${completedRenders.length}`}>
            MP4 ({completedRenders.length})
          </summary>
          <div className="render-history-list">
            {completedRenders.map((job, index) => (
              <a
                className="tool-button is-done"
                href={getRenderFileUrl(job.id)}
                download
                key={job.id}
              >
                <span aria-hidden="true">↓</span> Versión {index + 1}
              </a>
            ))}
          </div>
        </details>
      )}
      {isBusy ? (
        <span className="tool-button is-busy" role="status">
          Generando {Math.round((render.job?.progress || 0) * 100)}%
        </span>
      ) : (
        <button className="tool-button" type="button" onClick={onOpen}>
          {status === 'done' ? 'Exportar otra' : 'Exportar'} <span aria-hidden="true">⇩</span>
        </button>
      )}
    </>
  )
}

function DetailHeading({ eyebrow, title, artist }) {
  return (
    <div className="detail-heading">
      <p className="eyebrow">{eyebrow}</p>
      <h2>{title}</h2>
      <p>{artist}</p>
    </div>
  )
}