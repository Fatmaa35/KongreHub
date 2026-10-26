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
  ['attendee', 'attendee@kongrehub.test', 'Kaydolur, kendi programını oluşturur'],
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
    <div className="login-page">
      <header className="login-head">
        <div className="topbar-in">
          <span className="wordmark">Kongre<em>Hub</em></span>
          <span className="event">10–12 Mayıs 2027 · İstanbul</span>
        </div>
      </header>
      <div className="login-body">
        <section className="login-intro">
          <div className="poster">
            <small>Uluslararası Bilim Kongresi</small>
            <h1>KongreHub 2027</h1>
            <p>Bildiriden kürsüye, tek sistemde.</p>
            <dl>
              <div><dt>Bildiri son tarihi</dt><dd>15 Ocak 2027</dd></div>
              <div><dt>Hakem kararları</dt><dd>1 Mart 2027</dd></div>
              <div><dt>Kongre</dt><dd>10–12 Mayıs 2027</dd></div>
            </dl>
          </div>
          <p>
            Bildiri gönderimi, çift kör hakem değerlendirmesi, oturum programı, kayıt ve sertifika işlemleri. Aşağıdaki demo
            hesaplardan biriyle girebilirsiniz; hepsinin parolası <code>Demo123!</code>
          </p>
          <div className="card" style={{ padding: 0 }}>
            <table className="demo-table">
              <thead>
                <tr>
                  <th>Rol</th>
                  <th>Yetki</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {demos.map(([role, mail, desc]) => (
                  <tr key={role}>
                    <td>{roleLabel[role]}</td>
                    <td className="hint">{desc}</td>
                    <td className="num">
                      <button className="btn secondary small" disabled={busy !== null} onClick={() => void go(mail, 'Demo123!', role)}>
                        {busy === role ? 'Giriliyor…' : 'Giriş'}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
        <section className="card login-form">
          <h2>E-posta ile giriş</h2>
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
              {busy === 'form' ? 'Giriliyor…' : 'Giriş yap'}
            </button>
          </form>
        </section>
      </div>
      <footer className="login-foot">
        <div>KongreHub · Fatma Kaplan</div>
      </footer>
    </div>
  )
}
