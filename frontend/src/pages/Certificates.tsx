import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, download } from '../api'
import { useAuth } from '../auth'
import { Card, ErrorBox, PageTitle, QueryState } from '../components/ui'
import { dt } from '../format'

interface Cert {
  id: string
  kind: string
  holder_name: string
  detail: string
  issued_at: string
  verify_url: string
}

export default function Certificates() {
  const { user } = useAuth()
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['certs'], queryFn: () => api<{ items: Cert[] }>('/api/certificates/mine') })
  const issue = useMutation({ mutationFn: (kind: string) => api('/api/certificates', { body: { kind }, idempotent: true }), onSuccess: () => void qc.invalidateQueries({ queryKey: ['certs'] }) })
  return (
    <>
      <PageTitle sub="Sertifikalar Ed25519 ile imzalanır; QR kod doğrulama sayfasını açar. Katılım sertifikası için ödenmiş kayıt gerekir.">Sertifikalar</PageTitle>
      <Card actions={
        <>
          <button className="btn" disabled={issue.isPending} onClick={() => issue.mutate('attendance')}>Katılım sertifikası oluştur</button>
          {user!.role === 'author' && <button className="btn secondary" disabled={issue.isPending} onClick={() => issue.mutate('presenter')}>Sunum sertifikası oluştur</button>}
        </>
      }>
        {issue.isError && <ErrorBox error={issue.error} />}
        <QueryState q={q} isEmpty={(d) => d.items.length === 0} empty="Henüz sertifikanız yok.">
          {(d) => (
            <table>
              <thead><tr><th>Tür</th><th>Ayrıntı</th><th>Düzenlenme</th><th /></tr></thead>
              <tbody>
                {d.items.map((c) => (
                  <tr key={c.id}>
                    <td>{c.kind === 'presenter' ? 'Sunum' : 'Katılım'}</td>
                    <td>{c.detail}</td>
                    <td>{dt(c.issued_at)}</td>
                    <td className="actions">
                      <button className="btn small" onClick={() => void download(`/api/certificates/${c.id}/pdf`, `sertifika-${c.id.slice(0, 8)}.pdf`)}>PDF</button>
                      <a className="btn ghost small" href={new URL(c.verify_url).pathname + new URL(c.verify_url).search} target="_blank" rel="noreferrer">Doğrula</a>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </QueryState>
      </Card>
    </>
  )
}
