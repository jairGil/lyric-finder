import React, { useEffect, useState } from 'react'
import { AccountBar } from './components/AccountBar'
import { CompactSearch } from './components/CompactSearch'
import { DetailPanel } from './components/DetailPanel'
import { ResultsPanel } from './components/ResultsPanel'
import { SearchForm } from './components/SearchForm'
import { useSession } from './hooks/useSession'
import { findVideo, getLyrics, reactTrack, registerTrack, saveTrack, searchLyrics } from './services/api'
import './style.css'

const DEFAULT_SETTINGS =
  '{"highlight":"line","font":"manrope","sizePercent":100,"colors":"single","colorOrder":"word","singleColor":0,"palette":[0,1,2],"lines":2,"alignHorizontal":"center","alignVertical":"center"}'

function App() {
  const { session, error: accountError, authenticate, logout } = useSession()

  const [results, setResults] = useState([])
  const [selected, setSelected] = useState(null)
  const [lyrics, setLyrics] = useState(null)
  const [searchState, setSearchState] = useState({ loading: false, error: '' })
  const [detailState, setDetailState] = useState({ loading: false, error: '' })
  const [searchedFor, setSearchedFor] = useState({ artist: '', track: '' })
  const [compactSearch, setCompactSearch] = useState(false)
  const [artist, setArtist] = useState('')
  const [track, setTrack] = useState('')

  const [theme, setTheme] = useState(() => localStorage.getItem('lyrics-theme') || 'dark')
  const [settings, setSettings] = useState(() =>
    JSON.parse(localStorage.getItem('lyrics-settings') || DEFAULT_SETTINGS),
  )

  useEffect(() => {
    document.body.dataset.theme = theme
    return () => {
      delete document.body.dataset.theme
    }
  }, [theme])

  useEffect(() => {
    const onScroll = () => setCompactSearch(window.scrollY > 760)
    window.addEventListener('scroll', onScroll, { passive: true })
    onScroll()
    return () => window.removeEventListener('scroll', onScroll)
  }, [])

  async function handleSearch(nextArtist, nextTrack) {
    if (!nextArtist) return
    setSelected(null)
    setLyrics(null)
    setSearchState({ loading: true, error: '' })

    try {
      const nextResults = await searchLyrics(nextArtist, nextTrack)
      setResults(nextResults)
      setSearchedFor({ artist: nextArtist, track: nextTrack })
      setSearchState({ loading: false, error: '' })
    } catch (error) {
      setResults([])
      setSearchedFor({ artist: nextArtist, track: nextTrack })
      setSearchState({ loading: false, error: error.message })
    }
  }

  async function handleSelect(result) {
    setSelected(result)
    setLyrics(null)
    setDetailState({ loading: true, error: '' })

    try {
      const nextLyrics = await getLyrics(result.track.artist, result.track.title)
      const video = nextLyrics.synchronized
        ? await findVideo(result.track.artist, result.track.title)
        : { videoId: '' }

      const stored = session?.token
        ? await registerTrack(session.token, {
            ...result.track,
            youtubeVideoId: video.videoId || '',
            plainLyrics: nextLyrics.plainLyrics,
            syncedLyrics: nextLyrics.lyrics || [],
          })
        : null

      setSelected({ ...result, trackId: stored?.trackId, youtubeVideoId: video.videoId || '' })
      setLyrics(nextLyrics)
      setDetailState({ loading: false, error: '' })
    } catch (error) {
      setDetailState({ loading: false, error: error.message })
    }
  }

  async function handleTrackAction(action) {
    if (!session?.token || !selected?.trackId) return
    if (action === 'save') await saveTrack(session.token, selected.trackId, true)
    else await reactTrack(session.token, selected.trackId, action)
  }

  function updateTheme(nextTheme) {
    setTheme(nextTheme)
    localStorage.setItem('lyrics-theme', nextTheme)
  }

  function updateSettings(nextSettings) {
    setSettings(nextSettings)
    localStorage.setItem('lyrics-settings', JSON.stringify(nextSettings))
  }

  const heading = searchedFor.track
    ? `${searchedFor.artist} / ${searchedFor.track}`
    : searchedFor.artist || 'Resultados'

  return (
    <main className={`shell theme-${theme}`}>
      <header className="topbar">
        <a className="brand" href="/" aria-label="Lyrics Finder, inicio">
          <span className="brand-mark">L/</span>
          <span>Lyrics Finder</span>
        </a>

        <CompactSearch
          visible={compactSearch}
          loading={searchState.loading}
          artist={artist}
          track={track}
          onArtistChange={setArtist}
          onTrackChange={setTrack}
          onSearch={handleSearch}
        />

        <div className="topbar-actions">
          <button
            className="theme-button"
            type="button"
            onClick={() => updateTheme(theme === 'dark' ? 'light' : 'dark')}
            aria-label="Cambiar tema"
          >
            {theme === 'dark' ? '☼' : '◐'}
          </button>
        </div>
      </header>

      <section className="hero">
        <p className="eyebrow">Search the words</p>
        <h1>
          Encuentra la canción.
          <br />
          <em>Vuelve a escucharla.</em>
        </h1>
        <p className="intro">
          Busca por artista y pista para descubrir letras, versiones sincronizadas y nuevos favoritos.
        </p>
        <SearchForm
          loading={searchState.loading}
          artist={artist}
          track={track}
          onArtistChange={setArtist}
          onTrackChange={setTrack}
          onSearch={handleSearch}
        />
      </section>

      <section className="workspace" aria-live="polite">
        <ResultsPanel
          results={results}
          selected={selected}
          heading={heading}
          loading={searchState.loading}
          error={searchState.error}
          hasSearched={Boolean(searchedFor.artist)}
          onSelect={handleSelect}
        />
        <DetailPanel
          selected={selected}
          lyrics={lyrics}
          state={detailState}
          session={session}
          onTrackAction={handleTrackAction}
          settings={settings}
          onSettingsChange={updateSettings}
        />
      </section>

      <footer className="footer">
        <span>Lyrics Finder / personal project</span>
        <span>Powered by LRCLIB</span>
      </footer>
    </main>
  )
}

export default App