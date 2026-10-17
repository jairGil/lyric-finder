import { useState } from 'react'
import { loginAccount, logoutAccount, registerAccount } from '../services/api'

const STORAGE_KEY = 'lyrics-session'

function readSession() {
  try {
    return JSON.parse(localStorage.getItem(STORAGE_KEY) || 'null')
  } catch {
    return null
  }
}

export function useSession() {
  const [session, setSession] = useState(readSession)
  const [error, setError] = useState('')

  async function authenticate(mode, form) {
    setError('')
    try {
      const nextSession = mode === 'login' ? await loginAccount(form) : await registerAccount(form)
      localStorage.setItem(STORAGE_KEY, JSON.stringify(nextSession))
      setSession(nextSession)
      return nextSession
    } catch (requestError) {
      setError(requestError.message)
      throw requestError
    }
  }

  async function logout() {
    if (session?.token) await logoutAccount(session.token).catch(() => {})
    localStorage.removeItem(STORAGE_KEY)
    setSession(null)
  }

  return { session, error, authenticate, logout }
}
