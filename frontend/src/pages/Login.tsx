import { useState, type FormEvent } from 'react'
import { Navigate, useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth'
import { ErrorBox } from '../components/ui'
import { roleLabel } from '../format'

const demos = [
  ['author', 'author@kongrehub.test', 'Bildiri gönderir, hakem yorumlarını görür'],
  ['reviewer', 'reviewer@kongrehub.test', 'Atanan bildirileri kör değerlendirir'],
  ['editor', 'editor@kongrehub.test', 'Hakem atar, karar verir, programı kurar'],
  ['registrar', 'registrar@kongrehub.test', 'Kayıt, ödeme ve faturaları yönetir'],
  ['attendee', 'attendee@kongrehub.test', 'Kaydolur, programını oluşturur'],
  ['admin', 'admin@kongrehub.test', 'Kullanıcılar ve denetim kaydı'],
] as const

export default function Login() {
  const { user, login } = useAuth()
  const nav = useNavigate()
  const loc = useLocation()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [err, setErr] = useState<unknown>(null)
  const [busy, setBusy] = useState<string | null>(null)
  if (user) return <Navigate to="/" replace />
  const go = async (e: string, p: string, key: string) => {
    setBusy(key)
    setErr(null)
    try {
      await login(e, p)
      nav((loc.state as { from?: string } | null)?.from ?? '/', { replace: true })
    } catch (x) {
      setErr(x)
    } finally {
      setBusy(null)
    }
  }
  const submit = (e: FormEvent) => {
    e.preventDefault()
    void go(email, password, 'form')
  }
  return (
    <div className="login-wrap">
      <section className="login-art">
        <div className="wordmark">KongreHub</div>
        <div>
          <h1>Bildiriden kürsüye tek yerden</h1>
          <p>Çift kör hakem değerlendirmesi, otomatik hakem ataması, çakışmasız oturum programı ve imzalı sertifikalar.</p>
        </div>
        <div className="dates">10–12 Mayıs 2027 · İstanbul</div>
      </section>
      <section className="login-panel">
        <div className="login">
          <h2>Yaka kartınızı seçin</h2>
          <p className="muted">Demo hesaplarla tek tıkla girin. Hepsinin parolası <code>Demo123!</code></p>
          <div className="roles">
            {demos.map(([role, mail, desc]) => (
              <button key={role} className={`role-pick role-${role}`} disabled={busy !== null} onClick={() => void go(mail, 'Demo123!', role)}>
                <span className="strap" aria-hidden />
                <span className="rp-body">
                  <strong>{busy === role ? 'Giriş yapılıyor…' : roleLabel[role]}</strong>
                  <small>{desc}</small>
                </span>
              </button>
            ))}
          </div>
          <div className="divider">ya da e-posta ile</div>
          <form onSubmit={submit}>
            <div className="field">
              <label htmlFor="email">E-posta</label>
              <input id="email" type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required />
            </div>
            <div className="field">
              <label htmlFor="pw">Parola</label>
              <input id="pw" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
            </div>
            {err ? <ErrorBox error={err} /> : null}
            <button className="btn" disabled={busy !== null} style={{ width: '100%', marginTop: 4 }}>
              {busy === 'form' ? 'Giriş yapılıyor…' : 'Giriş yap'}
            </button>
          </form>
        </div>
      </section>
    </div>
  )
}
