import { type FormEvent, useState } from 'react'

export default function Login({ onLogin }: { onLogin: (username: string, password: string) => Promise<void> }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setPending(true)
    setError('')
    try {
      await onLogin(username, password)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'login failed')
      setPending(false)
    }
  }

  return (
    <main className="login-shell">
      <form className="login-form" onSubmit={submit}>
        <div className="login-mark" aria-hidden="true">S</div>
        <div>
          <h1>SZX Gateway</h1>
          <p>Панель управления</p>
        </div>
        <label>
          <span>Логин</span>
          <input autoComplete="username" autoFocus value={username} onChange={(event) => setUsername(event.target.value)} required />
        </label>
        <label>
          <span>Пароль</span>
          <input autoComplete="current-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} required />
        </label>
        {error && <p className="login-error" role="alert">{error}</p>}
        <button type="submit" disabled={pending}>{pending ? 'Вход...' : 'Войти'}</button>
      </form>
    </main>
  )
}
