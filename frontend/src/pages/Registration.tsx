import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useFeedback } from '../components/feedback'
import { api, newIdempotencyKey, type Page } from '../api'
import { Badge, Card, ErrorBox, Notice, PageTitle, QueryState } from '../components/ui'
import { dt, money } from '../format'

export interface Registration {
  id: number
  tier: string
  category: string
  is_member: boolean
  currency: string
  fx_rate: string
  base_cents: number
  discount_cents: number
  total_cents: number
  breakdown: { code: string; label: string; amount_cents: number }[]
  status: string
  created_at: string
  paid_at?: string
  quote_valid_until: string
  invoice_number?: string
  refund_cents?: number
}

interface Quote {
  tier: string
  currency: string
  fx_rate: string
  total_cents: number
  lines: { code: string; label: string; amount_cents: number }[]
}

const tierLabel: Record<string, string> = { early: 'Erken', normal: 'Normal', late: 'Geç' }

function Breakdown({ lines, currency }: { lines: { label: string; amount_cents: number }[]; currency: string }) {
  return (
    <table>
      <tbody>
        {lines.map((l, i) => <tr key={i}><td>{l.label}</td><td className="num">{money(l.amount_cents, currency)}</td></tr>)}
      </tbody>
    </table>
  )
}

export default function RegistrationPage() {
  const qc = useQueryClient()
  const { toast } = useFeedback()
  const [currency, setCurrency] = useState('EUR')
  const mine = useQuery({ queryKey: ['my-registrations'], queryFn: () => api<Page<Registration>>('/api/registrations/mine') })
  const quote = useQuery({ queryKey: ['quote', currency], queryFn: () => api<Quote>('/api/registrations/quote', { body: { currency } }) })
  const fx = useQuery({ queryKey: ['fx'], queryFn: () => api<{ rate: string; observed_at: string }>('/api/fx/current'), refetchInterval: 10000 })
  const refresh = () => void qc.invalidateQueries({ queryKey: ['my-registrations'] })
  const register = useMutation({ mutationFn: () => api('/api/registrations', { body: { currency }, idempotent: true }), onSuccess: () => { refresh(); toast(currency === 'TRY' ? 'Kaydınız oluşturuldu, kur 24 saatliğine sabitlendi' : 'Kaydınız oluşturuldu') } })
  const active = mine.data?.items.find((r) => r.status === 'pending' || r.status === 'paid')
  return (
    <>
      <PageTitle sub="Kademeli ücret (erken/normal/geç), öğrenci ve üye indirimi, 5+ kişilik grup indirimi. TRY ödemelerde kur kayıt anında 24 saatliğine dondurulur.">Kayıt ve ödeme</PageTitle>
      <QueryState q={mine}>
        {() => active ? <ActiveRegistration r={active} onChange={refresh} liveRate={fx.data?.rate} /> : (
          <Card title="Yeni kayıt">
            <div className="row">
              <div className="field">
                <label htmlFor="cur">Para birimi</label>
                <select id="cur" value={currency} onChange={(e) => setCurrency(e.target.value)}><option value="EUR">EUR</option><option value="TRY">TRY</option></select>
                {currency === 'TRY' && fx.data && <div className="hint">Güncel kur: 1 EUR = {fx.data.rate} TRY ({dt(fx.data.observed_at)})</div>}
              </div>
            </div>
            <QueryState q={quote}>
              {(q) => (
                <>
                  <p><strong>{tierLabel[q.tier]} kayıt</strong> · ödenecek: <strong>{money(q.total_cents, q.currency)}</strong></p>
                  <Breakdown lines={q.lines} currency={q.currency} />
                </>
              )}
            </QueryState>
            {register.isError && <ErrorBox error={register.error} />}
            <p><button className="btn" disabled={register.isPending} onClick={() => register.mutate()}>Kaydı oluştur{currency === 'TRY' ? ' ve kuru dondur' : ''}</button></p>
          </Card>
        )}
      </QueryState>
      {mine.data && mine.data.items.length > 0 && (
        <Card title="Kayıt geçmişi">
          <table>
            <thead><tr><th>#</th><th>Kademe</th><th>Tutar</th><th>Durum</th><th>Fatura</th><th>İade</th></tr></thead>
            <tbody>
              {mine.data.items.map((r) => (
                <tr key={r.id}><td>{r.id}</td><td>{tierLabel[r.tier]}</td><td>{money(r.total_cents, r.currency)}</td><td><Badge status={r.status} /></td><td>{r.invoice_number ?? '—'}</td><td>{r.refund_cents != null ? money(r.refund_cents, r.currency) : '—'}</td></tr>
              ))}
            </tbody>
          </table>
        </Card>
      )}
    </>
  )
}

function ActiveRegistration({ r, onChange, liveRate }: { r: Registration; onChange: () => void; liveRate?: string }) {
  const { toast, confirm } = useFeedback()
  // aynı ödeme denemesi için sabit Idempotency-Key: çift tıklama çift ödeme yaratmaz
  const [payKey, setPayKey] = useState(newIdempotencyKey)
  const pay = useMutation({
    mutationFn: (outcome: string) => api<{ outcome: string }>(`/api/registrations/${r.id}/payments`, { body: { outcome }, idempotent: payKey }),
    onSuccess: (d) => {
      setPayKey(newIdempotencyKey())
      onChange()
      if (d.outcome === 'paid') toast('Ödeme alındı, faturanız kesildi')
      else toast('Ödeme reddedildi, tekrar deneyebilirsiniz', 'err')
    },
  })
  const requote = useMutation({ mutationFn: () => api(`/api/registrations/${r.id}/requote`, { body: {} }), onSuccess: onChange })
  const refundQ = useQuery({ queryKey: ['refund-quote', r.id], queryFn: () => api<{ rule: { label: string; percent: number }; refund_cents: number }>(`/api/registrations/${r.id}/refund-quote`), enabled: r.status === 'paid' })
  const cancel = useMutation({ mutationFn: () => api<{ refund_cents: number }>(`/api/registrations/${r.id}/cancel`, { body: { reason: 'katılımcı iptali' }, idempotent: true }), onSuccess: (d) => { onChange(); toast(`Kayıt iptal edildi, iade: ${money(d.refund_cents, r.currency)}`) } })
  const expired = r.currency === 'TRY' && new Date(r.quote_valid_until) < new Date()
  return (
    <Card title={<>Kaydınız #{r.id} <Badge status={r.status} /></>}>
      <div className="grid two">
        <div>
          <Breakdown lines={r.breakdown} currency={r.currency} />
          <p>Toplam: <strong>{money(r.total_cents, r.currency)}</strong></p>
          {r.currency === 'TRY' && (
            <p className="hint">Dondurulmuş kur: 1 EUR = {r.fx_rate} TRY {liveRate && liveRate !== r.fx_rate && <>(güncel kur {liveRate}; tutarınız değişmez)</>} · geçerlilik {dt(r.quote_valid_until)}</p>
          )}
        </div>
        <div>
          {r.status === 'pending' && (
            <>
              {expired ? (
                <>
                  <Notice kind="warn">Dondurulmuş kurun süresi doldu. Güncel kurla yeniden fiyatlandırın.</Notice>
                  <button className="btn" onClick={() => requote.mutate()} disabled={requote.isPending}>Yeniden fiyatlandır</button>
                </>
              ) : (
                <>
                  <p className="hint">Ödeme sağlayıcısı simülatörü: sonucu seçin.</p>
                  <div className="actions">
                    <button className="btn" disabled={pay.isPending} onClick={() => pay.mutate('success')}>{pay.isPending ? 'İşleniyor…' : `${money(r.total_cents, r.currency)} öde`}</button>
                    <button className="btn ghost" disabled={pay.isPending} onClick={() => pay.mutate('fail')}>Kart reddedilsin</button>
                  </div>
                </>
              )}
              {pay.data?.outcome === 'failed' && <Notice kind="err">Ödeme reddedildi. Tekrar deneyebilirsiniz.</Notice>}
            </>
          )}
          {r.status === 'paid' && (
            <>
              <Notice>Ödendi · fatura <strong>{r.invoice_number}</strong> · {dt(r.paid_at)}</Notice>
              {refundQ.data && <p className="hint">Şimdi iptal ederseniz: {refundQ.data.rule.label} → {money(refundQ.data.refund_cents, r.currency)}</p>}
              <button className="btn danger small" disabled={cancel.isPending} onClick={async () => (await confirm({ title: 'Kayıt iptal edilsin mi?', body: refundQ.data ? `${refundQ.data.rule.label}: ${money(refundQ.data.refund_cents, r.currency)} iade edilir. Bu işlem geri alınamaz.` : 'Bu işlem geri alınamaz.', action: 'Kaydı iptal et', danger: true })) && cancel.mutate()}>Kaydı iptal et</button>
            </>
          )}
          {(pay.isError || requote.isError || cancel.isError) && <ErrorBox error={pay.error ?? requote.error ?? cancel.error} />}
        </div>
      </div>
    </Card>
  )
}
