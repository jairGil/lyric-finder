import React, { useState } from 'react'

const emptyForm = { login: '', username: '', email: '', password: '' }

export function AuthModal({ mode, error, onClose, onAuthenticate, onModeChange }) {
  const [form, setForm] = useState(emptyForm)
  const update = (field) => (event) => setForm({ ...form, [field]: event.target.value })

  async function submit(event) {
    event.preventDefault()
    await onAuthenticate(mode, form)
  }

  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
      <section className="auth-modal" role="dialog" aria-modal="true" aria-labelledby="auth-title">
        <button className="modal-close" type="button" onClick={onClose} aria-label="Cerrar">×</button>
        <p className="eyebrow">Your library</p>
        <h2 id="auth-title">{mode === 'login' ? 'Bienvenido de vuelta' : 'Crea tu espacio'}</h2>
        <p className="modal-copy">Guarda canciones, crea playlists y conserva tus preferencias.</p>
        <form className="auth-form" onSubmit={submit}>
          {mode === 'login' ? <label><span>Usuario o email</span><input id="auth-login" name="login" value={form.login} onChange={update('login')} autoComplete="username" required /></label> : <><label><span>Usuario</span><input id="auth-username" name="username" value={form.username} onChange={update('username')} autoComplete="username" required /></label><label><span>Email</span><input id="auth-email" name="email" value={form.email} onChange={update('email')} type="email" autoComplete="email" required /></label></>}
          <label><span>Contraseña</span><input id="auth-password" name="password" value={form.password} onChange={update('password')} type="password" autoComplete={mode === 'login' ? 'current-password' : 'new-password'} required /></label>
          {error && <p className="modal-error">{error}</p>}
          <button className="modal-submit" type="submit">{mode === 'login' ? 'Entrar' : 'Crear cuenta'} <span>↗</span></button>
        </form>
        <button className="modal-switch" type="button" onClick={() => onModeChange(mode === 'login' ? 'register' : 'login')}>{mode === 'login' ? '¿Aún no tienes cuenta? Regístrate' : 'Ya tengo una cuenta'}</button>
      </section>
    </div>
  )
}
