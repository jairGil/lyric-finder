const API_BASE_URL = (import.meta.env.VITE_API_URL || 'http://localhost:8080').replace(/\/+$/, '')

async function request(path, params, options = {}) {
  const query = params ? new URLSearchParams(params) : ''
  const response = await fetch(`${API_BASE_URL}${path}${query ? `?${query}` : ''}`, {
    ...options,
    headers: { 'Content-Type': 'application/json', ...(options.headers || {}) },
  })

  if (!response.ok) throw new Error(`La API respondió con ${response.status}`)
  return response.json()
}

export const searchLyrics = (artist, track = '') => request('/api/search', { artist, ...(track && { track }) })
export const getLyrics = (artist, track) => request('/api/lyrics', { artist, track })
export const findVideo = (artist, track) => request('/api/video', { artist, track })
export const registerAccount = (input) => request('/api/auth/register', null, { method: 'POST', body: JSON.stringify(input) })
export const loginAccount = (input) => request('/api/auth/login', null, { method: 'POST', body: JSON.stringify(input) })
export const logoutAccount = (token) => request('/api/auth/logout', null, { method: 'POST', headers: { Authorization: `Bearer ${token}` } })
export const registerTrack = (token, track) => request('/api/library/tracks', null, { method: 'POST', headers: { Authorization: `Bearer ${token}` }, body: JSON.stringify(track) })
export const saveTrack = (token, trackId, save) => request(`/api/tracks/${trackId}/save`, null, { method: 'PUT', headers: { Authorization: `Bearer ${token}` }, body: JSON.stringify({ save }) })
export const reactTrack = (token, trackId, reaction) => request(`/api/tracks/${trackId}/reaction`, null, { method: 'PUT', headers: { Authorization: `Bearer ${token}` }, body: JSON.stringify({ reaction }) })

// El render va aparte porque es multipart con archivos: si se fija el
// Content-Type a mano, el navegador deja de poner el boundary y el servidor no
// puede leer el formulario. Solo se declara Authorization.
async function renderRequest(path, { token, form } = {}) {
  const response = await fetch(`${API_BASE_URL}${path}`, {
    method: form ? 'POST' : 'GET',
    headers: token ? { Authorization: `Bearer ${token}` } : {},
    ...(form ? { body: form } : {}),
  })

  const payload = await response.json().catch(() => null)
  if (!response.ok) {
    // El backend explica el rechazo (fondo inválido, fragmento fuera del
    // medio, letra no sincronizada) y ese texto es más útil que el status.
    throw new Error(payload?.error || `La API respondió con ${response.status}`)
  }
  return payload
}

export const createRender = (form, token) => renderRequest('/api/renders', { form, token })
export const getRenderJob = (id, token) => renderRequest(`/api/renders/${id}`, { token })
export const getRenderFileUrl = (id) => `${API_BASE_URL}/api/renders/${id}/file`

// Ficha de un link de YouTube: duración y título. Es una lectura, no un render,
// así que va por renderRequest y no necesita token. El modal la pide antes de
// renderizar porque el doble slider necesita una duración real y un link no la
// trae en el navegador.
export const getRenderSource = (url) => renderRequest(`/api/renders/source?url=${encodeURIComponent(url)}`)

// El preview responde con el MP4 del borrador y no con JSON, así que no puede
// pasar por renderRequest. Devuelve un object URL para ponerlo en un <video>,
// que el llamador tiene que revocar cuando lo cambie.
export async function createRenderPreview(form, { signal } = {}) {
  let response
  try {
    response = await fetch(`${API_BASE_URL}/api/renders/preview`, {
      method: 'POST',
      body: form,
      signal,
    })
  } catch (error) {
    // fetch solo lanza cuando no hubo respuesta (API caída o CORS). El mensaje
    // crudo ("Failed to fetch") no dice nada útil, y como el preview es
    // opcional tiene que quedar claro que el export sigue disponible.
    if (error.name === 'AbortError') throw error
    throw new Error('no se pudo contactar al servidor.')
  }

  if (!response.ok) {
    const payload = await response.json().catch(() => null)
    throw new Error(payload?.error || `La API respondió con ${response.status}`)
  }
  return URL.createObjectURL(await response.blob())
}
