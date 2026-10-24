import { useState } from 'react'
import { useInfiniteQuery, useMutation } from '@tanstack/react-query'
import { api, type Page } from '../api'
import { Badge, Card, ErrorBox, Notice, PageTitle, QueryState } from '../components/ui'
import { dt, money, statusLabel } from '../format'
import type { Registration } from './Registration'

type Reg = Registration & { user_name: string; group_id?: number }

export function Registrations() {
  const [status, setStatus] = useState('')
  const q = useInfiniteQuery({
    queryKey: ['registrations', status],
    initialPageParam: '',
    queryFn: ({ pageParam }) => api<Page<Reg>>(`/api/registrations?limit=50&status=${status}${pageParam ? `&cursor=${pageParam}` : ''}`),
    getNextPageParam: (l) => l.next_cursor || undefined,
  })
  const items = q.data?.pages.flatMap((p) => p.items) ?? []
  return (
    <>
      <PageTitle sub="Simülatörün ürettiği kayıt ve ödemeler canlı olarak eklenir">Kayıtlar</PageTitle>
      <Card actions={<button className="btn secondary small" onClick={() => void q.refetch()}>Yenile</button>}>
        <select value={status} onChange={(e) => setStatus(e.target.value)} style={{ maxWidth: 240, marginBottom: 12 }} aria-label="Durum">
          <option value="">Tüm durumlar</option>
          {['pending', 'paid', 'refunded', 'cancelled'].map((s) => <option key={s} value={s}>{statusLabel[s]}</option>)}
        </select>
        <QueryState q={q} isEmpty={() => items.length === 0}>
          {() => (
            <div className="table-wrap">
              <table>
                <thead><tr><th>#</th><th>Katılımcı</th><th>Kademe</th><th>Kategori</th><th className="num">Tutar</th><th>Kur</th><th>Durum</th><th>Fatura</th><th>Tarih</th></tr></thead>
                <tbody>
                  {items.map((r) => (
                    <tr key={r.id}>
                      <td>{r.id}</td><td>{r.user_name}{r.group_id && <span className="chip">grup</span>}</td><td>{r.tier}</td>
                      <td>{r.category}{r.is_member && ' · üye'}</td><td className="num">{money(r.total_cents, r.currency)}</td>
                      <td>{r.currency === 'TRY' ? r.fx_rate : '—'}</td><td><Badge status={r.status} /></td><td>{r.invoice_number ?? '—'}</td><td>{dt(r.created_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </QueryState>
        {q.hasNextPage && <button className="btn secondary" onClick={() => void q.fetchNextPage()}>Daha fazla</button>}
      </Card>
    </>
  )
}

export function GroupRegistration() {
  const [name, setName] = useState('')
  const [currency, setCurrency] = useState('EUR')
  const [emails, setEmails] = useState('')
  const m = useMutation({
    mutationFn: () => api<{ group_id: number; registrations: Reg[] }>('/api/registrations/group', {
      body: { name, currency, emails: emails.split(/[\s,;]+/).map((e) => e.trim()).filter(Boolean) },
      idempotent: true,
    }),
  })
  return (
    <>
      <PageTitle sub="5 ve üzeri katılımcıda her üyeye %10 grup indirimi uygulanır. Tüm grup tek işlemde kaydedilir; bir üye hatalıysa hiçbiri kaydedilmez.">Grup kaydı</PageTitle>
      <Card>
        <form onSubmit={(e) => { e.preventDefault(); m.mutate() }}>
          <div className="row">
            <div className="field"><label>Grup adı</label><input value={name} onChange={(e) => setName(e.target.value)} required /></div>
            <div className="field"><label>Para birimi</label><select value={currency} onChange={(e) => setCurrency(e.target.value)}><option>EUR</option><option>TRY</option></select></div>
          </div>
          <div className="field"><label>Katılımcı e-postaları</label><textarea value={emails} onChange={(e) => setEmails(e.target.value)} placeholder="her satıra bir e-posta" required /><div className="hint">Katılımcıların sistemde hesabı olmalıdır.</div></div>
          {m.isError && <ErrorBox error={m.error} />}
          <button className="btn" disabled={m.isPending}>Grubu kaydet</button>
        </form>
        {m.data && (
          <Notice>
            Grup #{m.data.group_id}: {m.data.registrations.length} kayıt oluşturuldu.
            <ul>{m.data.registrations.map((r) => <li key={r.id}>{r.user_name}: {money(r.total_cents, r.currency)} ({r.breakdown.map((b) => b.code).join(', ')})</li>)}</ul>
          </Notice>
        )}
      </Card>
    </>
  )
}

export function Invoices() {
  const q = useInfiniteQuery({
    queryKey: ['invoices'],
    initialPageParam: '',
    queryFn: ({ pageParam }) => api<{ items: { id: number; number: string; seq: number; holder: string; amount_cents: number; currency: string; issued_at: string }[]; next_cursor?: string; gap_count: number }>(`/api/invoices?limit=50${pageParam ? `&cursor=${pageParam}` : ''}`),
    getNextPageParam: (l) => l.next_cursor || undefined,
  })
  const items = q.data?.pages.flatMap((p) => p.items) ?? []
  const gaps = q.data?.pages[0]?.gap_count
  return (
    <>
      <PageTitle sub="Fatura numaraları ödeme ile aynı transaction'da, kilitli sayaçtan boşluksuz üretilir">Faturalar</PageTitle>
      <Card>
        {gaps !== undefined && <Notice kind={gaps === 0 ? 'ok' : 'err'}>Numara boşluğu: {gaps}</Notice>}
        <QueryState q={q} isEmpty={() => items.length === 0}>
          {() => (
            <table>
              <thead><tr><th>Numara</th><th>Sahibi</th><th className="num">Tutar</th><th>Tarih</th></tr></thead>
              <tbody>{items.map((i) => <tr key={i.id}><td><code>{i.number}</code></td><td>{i.holder}</td><td className="num">{money(i.amount_cents, i.currency)}</td><td>{dt(i.issued_at)}</td></tr>)}</tbody>
            </table>
          )}
        </QueryState>
        {q.hasNextPage && <button className="btn secondary" onClick={() => void q.fetchNextPage()}>Daha fazla</button>}
      </Card>
    </>
  )
}
