import { useState, type FormEvent } from 'react'
import { Navigate, useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth'
import { ErrorBox } from '../components/ui'
import { roleLabel } from '../format'

const demos = [
  ['author', 'author@kongrehub.test'],
  ['reviewer', 'reviewer@kongrehub.test'],
  ['editor', 'editor@kongrehub.test'],
  ['registrar', 'registrar@kongrehub.test'],
  ['attendee', 'attendee@kongrehub.test'],
  ['admin', 'admin@kongrehub.test'],
] as const

export default function Login() {
  const { user, login } = useAuth()
  const nav = useNavigate()
  const loc = useLocation()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [err, setErr] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  if (user) return <Navigate to="/" replace />
  const go = async (e: string, p: string) => {
    setBusy(true)
    setErr(null)
    try {
      await login(e, p)
      nav((loc.state as { from?: string } | null)?.from ?? '/', { replace: true })
    } catch (x) {
      setErr(x)
    } finally {
      setBusy(false)
    }
  }
  const submit = (e: FormEvent) => {
    e.preventDefault()
    void go(email, password)
  }
  return (
    <div className="login-wrap">
      <div className="card login">
        <h1 style={{ marginTop: 0 }}>
          <span className="logo">K</span> KongreHub
        </h1>
        <p className="muted">Bilimsel kongre, bildiri ve program yönetimi</p>
        <form onSubmit={submit} style={{ marginTop: 16 }}>
          <div className="field">
            <label htmlFor="email">E-posta</label>
            <input id="email" type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required />
          </div>
          <div className="field">
            <label htmlFor="pw">Parola</label>
            <input id="pw" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
          </div>
          {err ? <ErrorBox error={err} /> : null}
          <button className="btn" disabled={busy} style={{ width: '100%' }}>
            {busy ? 'Giriş yapılıyor…' : 'Giriş yap'}
          </button>
        </form>
        <h3 style={{ marginBottom: 8 }}>Demo hesapları</h3>
        <p className="hint" style={{ marginTop: 0 }}>Tümünün parolası: <code>Demo123!</code></p>
        <div className="demo-grid">
          {demos.map(([role, mail]) => (
            <button key={role} className="btn secondary small" disabled={busy} onClick={() => void go(mail, 'Demo123!')}>
              {roleLabel[role]}
            </button>
          ))}
        </div>
      </div>
    </div>
  )
}
