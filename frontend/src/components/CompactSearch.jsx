import React from 'react'

export function CompactSearch({ visible, loading, artist, track, onArtistChange, onTrackChange, onSearch }) {
  if (!visible) return null

  function submit(event) {
    event.preventDefault()
    if (artist.trim()) onSearch(artist.trim(), track.trim())
  }

  return (
    <form className="compact-search" onSubmit={submit}>
      <input
        id="compact-artist"
        name="artist"
        className="compact-field"
        aria-label="Buscar artista"
        placeholder="Artista"
        value={artist}
        onChange={(event) => onArtistChange(event.target.value)}
      />
      <input
        id="compact-track"
        name="track"
        className="compact-field"
        aria-label="Buscar pista"
        placeholder="Pista"
        value={track}
        onChange={(event) => onTrackChange(event.target.value)}
      />
      <button className="compact-search-button" type="submit" disabled={loading} aria-label="Buscar">
        ↗
      </button>
    </form>
  )
}