import React from 'react'

export function SearchForm({ loading, artist, track, onArtistChange, onTrackChange, onSearch }) {
  function submit(event) {
    event.preventDefault()
    onSearch(artist.trim(), track.trim())
  }

  return (
    <>
      <form className="search-form" onSubmit={submit}>
        <label className="field">
          <span>Artista</span>
          <input
            id="search-artist"
            name="artist"
            value={artist}
            onChange={(event) => onArtistChange(event.target.value)}
            type="text"
            placeholder="Ej. DAAZ"
            autoComplete="off"
            required
          />
        </label>

        <label className="field field-track">
          <span>
            Pista <small>opcional</small>
          </span>
          <input
            id="search-track"
            name="track"
            value={track}
            onChange={(event) => onTrackChange(event.target.value)}
            type="text"
            placeholder="Ej. GUAPÍSIMA BUENÍSIMA"
            autoComplete="off"
          />
        </label>

        <button className="search-button" type="submit" disabled={loading}>
          <span>{loading ? 'Buscando...' : 'Buscar'}</span>
          <span className="button-arrow" aria-hidden="true">↗</span>
        </button>
      </form>

      <p className="form-hint">Pulsa Enter para buscar rápidamente.</p>
    </>
  )
}