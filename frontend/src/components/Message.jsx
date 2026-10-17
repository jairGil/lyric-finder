import React from 'react'

export function Message({ index, title, message, error = false }) {
  return <div className={`empty-state ${error ? 'is-error' : ''}`}><span className="empty-index">{index}</span><p>{title && <><strong>{title}</strong><br /></>}{message}</p></div>
}
