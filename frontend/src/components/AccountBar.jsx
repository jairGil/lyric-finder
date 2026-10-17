import React, { useState } from 'react'
import { AuthModal } from './AuthModal'

export function AccountBar({ session, error, onAuthenticate, onLogout }) {
  const [mode, setMode] = useState('login')
  const [open, setOpen] = useState(false)

  if (session) {
    return <div className="account-bar"><span>{session.user.username}</span><button type="button" onClick={onLogout}>Salir</button></div>
  }

  return (
    <><div className="account-bar"><button type="button" onClick={() => { setMode('login'); setOpen(true) }}>Entrar</button><button type="button" onClick={() => { setMode('register'); setOpen(true) }}>Registro</button></div>{open && <AuthModal mode={mode} error={error} onClose={() => setOpen(false)} onModeChange={setMode} onAuthenticate={async (...args) => { await onAuthenticate(...args); setOpen(false) }} />}</>
  )
}
