import { useQuery } from '@tanstack/react-query'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import { api } from '../api'
import { QueryState } from '../components/ui'
import { dt } from '../format'

interface Result {
  valid: boolean
  holder_name?: string
  kind?: string
  detail?: string
  issued_at?: string
}

/** QR kodun açtığı herkese açık doğrulama sayfası. */
export default function VerifyCertificate() {
  const { id } = useParams()
  const [sp] = useSearchParams()
  const sig = sp.get('sig') ?? ''
  const q = useQuery({
    queryKey: ['verify', id, sig],
    queryFn: () => api<Result>(`/api/certificates/verify?id=${encodeURIComponent(id ?? '')}&sig=${encodeURIComponent(sig)}`),
  })
  return (
    <div className="login-wrap">
      <div className="card login">
        <h1 style={{ marginTop: 0 }}>Sertifika doğrulama</h1>
        <QueryState q={q}>
          {(r) =>
            r.valid ? (
              <div className="notice ok">
                <strong>✓ Geçerli sertifika</strong>
                <dl className="kv" style={{ marginTop: 8 }}>
                  <dt>Sahibi</dt>
                  <dd>{r.holder_name}</dd>
                  <dt>Tür</dt>
                  <dd>{r.kind === 'presenter' ? 'Sunum' : 'Katılım'}</dd>
                  <dt>Ayrıntı</dt>
                  <dd>{r.detail}</dd>
                  <dt>Düzenlenme</dt>
                  <dd>{dt(r.issued_at)}</dd>
                </dl>
              </div>
            ) : (
              <div className="notice err">
                <strong>✗ Doğrulanamadı.</strong> İmza geçersiz veya sertifika bulunamadı.
              </div>
            )
          }
        </QueryState>
        <p className="hint">İmza Ed25519 ile atılmıştır; içerik değiştirilirse doğrulama başarısız olur.</p>
        <Link to="/login">KongreHub'a git</Link>
      </div>
    </div>
  )
}
