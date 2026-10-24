import { useQuery } from '@tanstack/react-query'
import { api, download } from '../api'
import { Card, PageTitle, QueryState, Stat } from '../components/ui'
import { money, statusLabel } from '../format'

interface Overview {
  total_submissions: number
  submissions_by_status: Record<string, number>
  acceptance_rate?: number
  average_final_score?: number
  disagreements: number
  reviews: Record<string, number>
  top_keywords: { keyword: string; submissions: number; accepted: number; decided: number }[]
  program: { sessions: number; scheduled_papers: number }
}

export function Reports() {
  const ov = useQuery({ queryKey: ['overview'], queryFn: () => api<Overview>('/api/reports/overview') })
  const rv = useQuery({ queryKey: ['rev-report'], queryFn: () => api<{ items: { id: number; full_name: string; current_load: number; max_load: number; completed: number; open: number; expired: number; overdue: number; avg_days_to_review: number; reminders_received: number }[] }>('/api/reports/reviewers') })
  return (
    <>
      <PageTitle>Raporlar</PageTitle>
      <QueryState q={ov}>
        {(o) => (
          <Card title="Genel durum" actions={<button className="btn secondary small" onClick={() => void download('/api/reports/submissions.csv', 'bildiriler.csv')}>CSV indir</button>}>
            <div className="stats">
              <Stat label="Bildiri" value={o.total_submissions} />
              <Stat label="Kabul oranı" value={o.acceptance_rate != null ? `%${(o.acceptance_rate * 100).toFixed(0)}` : '—'} />
              <Stat label="Ort. puan" value={o.average_final_score?.toFixed(2) ?? '—'} />
              <Stat label="Uyuşmazlık" value={o.disagreements} />
              <Stat label="Bekleyen değerlendirme" value={o.reviews.pending} />
              <Stat label="Süresi dolan atama" value={o.reviews.expired} />
              <Stat label="Oturum / programdaki bildiri" value={`${o.program.sessions} / ${o.program.scheduled_papers}`} />
            </div>
            <h3>Duruma göre</h3>
            <div>{Object.entries(o.submissions_by_status).map(([k, v]) => <span className="chip" key={k}>{statusLabel[k] ?? k}: {v}</span>)}</div>
            <h3>Konu bazında</h3>
            <table>
              <thead><tr><th>Anahtar kelime</th><th className="num">Bildiri</th><th className="num">Karar verilen</th><th className="num">Kabul</th></tr></thead>
              <tbody>{o.top_keywords.map((k) => <tr key={k.keyword}><td>{k.keyword}</td><td className="num">{k.submissions}</td><td className="num">{k.decided}</td><td className="num">{k.accepted}</td></tr>)}</tbody>
            </table>
          </Card>
        )}
      </QueryState>
      <Card title="Hakem performansı ve gecikme">
        <QueryState q={rv} isEmpty={(d) => d.items.length === 0}>
          {(d) => (
            <div className="table-wrap">
              <table>
                <thead><tr><th>Hakem</th><th className="num">Yük</th><th className="num">Tamamlanan</th><th className="num">Açık</th><th className="num">Geciken</th><th className="num">Süresi dolan</th><th className="num">Ort. gün</th><th className="num">Hatırlatma</th></tr></thead>
                <tbody>
                  {d.items.slice(0, 60).map((r) => (
                    <tr key={r.id}><td>{r.full_name}</td><td className="num">{r.current_load}/{r.max_load}</td><td className="num">{r.completed}</td><td className="num">{r.open}</td><td className="num">{r.overdue}</td><td className="num">{r.expired}</td><td className="num">{r.avg_days_to_review.toFixed(1)}</td><td className="num">{r.reminders_received}</td></tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </QueryState>
      </Card>
    </>
  )
}

export function FinanceReport() {
  const q = useQuery({ queryKey: ['finance'], queryFn: () => api<{ revenue: { currency: string; tier: string; count: number; total_cents: number }[]; refunds: { currency: string; tier: string; count: number; total_cents: number }[]; registrations_by_status: Record<string, number>; invoices: { count: number; max_seq: number; gap_free: boolean }; payment_events: Record<string, number> }>('/api/reports/finance'), refetchInterval: 10000 })
  return (
    <>
      <PageTitle>Finans raporu</PageTitle>
      <QueryState q={q}>
        {(f) => (
          <>
            <Card title="Özet">
              <div className="stats">
                {Object.entries(f.registrations_by_status).map(([k, v]) => <Stat key={k} label={statusLabel[k] ?? k} value={v} />)}
                <Stat label="Fatura" value={f.invoices.count} hint={f.invoices.gap_free ? 'boşluksuz ✓' : 'boşluk var!'} />
              </div>
            </Card>
            <div className="grid two">
              <Card title="Gelir (kademe × para birimi)">
                <table><tbody>{f.revenue.map((r, i) => <tr key={i}><td>{r.tier} · {r.currency}</td><td className="num">{r.count}</td><td className="num">{money(r.total_cents, r.currency)}</td></tr>)}</tbody></table>
              </Card>
              <Card title="İadeler ve ödeme olayları">
                <table><tbody>{f.refunds.map((r, i) => <tr key={i}><td>{r.tier} · {r.currency}</td><td className="num">{r.count}</td><td className="num">{money(r.total_cents, r.currency)}</td></tr>)}</tbody></table>
                <p className="hint">Webhook olay sonuçları: {Object.entries(f.payment_events).map(([k, v]) => `${k}=${v}`).join(' · ')}</p>
              </Card>
            </div>
          </>
        )}
      </QueryState>
    </>
  )
}
