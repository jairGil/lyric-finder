import React from 'react'
import { Message } from './Message'

export function ResultsPanel({ results, selected, heading, loading, error, hasSearched, onSelect }) {
  const count = `${results.length} ${results.length === 1 ? 'pista' : 'pistas'}`

  return (
    <div className="results-column">
      <div className="section-heading">
        <div>
          <p className="eyebrow">Library scan</p>
          <h2>{heading}</h2>
        </div>
        <span className="result-count">{error ? 'Error' : count}</span>
      </div>

      <div className="results-list">
        {loading && (
          <Message index=".." title="Buscando pistas" message="Consultando la API local..." />
        )}

        {!loading && error && (
          <Message index="!" title="No se pudo completar la búsqueda" message={error} error />
        )}

        {!loading && !error && !results.length && (
          <Message
            index={hasSearched ? '00' : '01'}
            title={hasSearched ? 'No encontramos coincidencias' : ''}
            message={
              hasSearched
                ? 'Prueba con otro artista o una pista más específica.'
                : 'Tu próxima canción empieza con una búsqueda.'
            }
          />
        )}

        {!loading &&
          !error &&
          results.map((result, index) => (
            <button
              className={`result-card ${selected === result ? 'is-selected' : ''}`}
              key={`${result.id}-${result.track.title}`}
              type="button"
              onClick={() => onSelect(result)}
            >
              <span className="result-number">{String(index + 1).padStart(2, '0')}</span>
              <span className="result-info">
                <strong>{result.track.title}</strong>
                <span>
                  {result.track.artist}
                  {result.track.album ? ` · ${result.track.album}` : ''}
                </span>
              </span>
              <span className={`sync-label ${result.synchronized ? 'is-synced' : ''}`}>
                {result.synchronized ? 'SYNC' : 'PLAIN'}
              </span>
              <span className="result-arrow" aria-hidden="true">↗</span>
            </button>
          ))}
      </div>
    </div>
  )
}