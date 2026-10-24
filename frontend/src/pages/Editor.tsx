import { useState } from 'react'
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api, type Page } from '../api'
import { Badge, Card, ErrorBox, Notice, PageTitle, QueryState, Spinner, Stat } from '../components/ui'
import { dt, statusLabel } from '../format'
import type { SubmissionSummary } from './Author'

export function EditorSubmissions() {
  const [status, setStatus] = useState('')
  const [text, setText] = useState('')
  const [disagree, setDisagree] = useState(false)
  // keyset sayfalama: "daha fazla" ile next_cursor kullanılır
  const q = useInfiniteQuery({
    queryKey: ['submissions', status, text, disagree],
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      api<Page<SubmissionSummary>>(`/api/submissions?limit=50&status=${status}&q=${encodeURIComponent(text)}${disagree ? '&disagreement=true' : ''}${pageParam ? `&cursor=${pageParam}` : ''}`),
    getNextPageParam: (last) => last.next_cursor || undefined,
  })
  const items = q.data?.pages.flatMap((p) => p.items) ?? []
  return (
    <>
      <PageTitle sub="Tüm bildiriler; editör yazar ve hakem kimliklerini görür">Bildiriler</PageTitle>
      <Card>
        <div className="row" style={{ marginBottom: 12 }}>
          <select value={status} onChange={(e) => setStatus(e.target.value)} aria-label="Durum">
            <option value="">Tüm durumlar</option>
            {['submitted', 'under_review', 'reviews_complete', 'revision_requested', 'accepted', 'rejected', 'withdrawn'].map((s) => <option key={s} value={s}>{statusLabel[s]}</option>)}
          </select>
          <input placeholder="Başlıkta ara…" value={text} onChange={(e) => setText(e.target.value)} />
          <label style={{ display: 'flex', alignItems: 'center', gap: 6 }}><input type="checkbox" style={{ width: 'auto' }} checked={disagree} onChange={(e) => setDisagree(e.target.checked)} /> Yalnızca uyuşmazlıklı</label>
        </div>
        {q.isPending ? <Spinner /> : q.isError ? <ErrorBox error={q.error} onRetry={() => void q.refetch()} /> : items.length === 0 ? <div className="state empty">Kayıt yok.</div> : (
          <div className="table-wrap">
            <table>
              <thead><tr><th>#</th><th>Başlık</th><th>Durum</th><th>Hakem</th><th>Puan</th><th>Gönderim</th></tr></thead>
              <tbody>
                {items.map((s) => (
                  <tr key={s.id}>
                    <td>{s.id}</td>
                    <td><Link to={`/submissions/${s.id}`}>{s.title}</Link> {s.disagreement_flag && <span className="badge s-pending">uyuşmazlık</span>}</td>
                    <td><Badge status={s.status} /></td>
                    <td>{s.submitted_reviews}/{s.active_reviewers}</td>
                    <td>{s.final_score?.toFixed(2) ?? '—'}</td>
                    <td>{dt(s.created_at, false)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {q.hasNextPage && <p><button className="btn secondary" disabled={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>{q.isFetchingNextPage ? 'Yükleniyor…' : 'Daha fazla'}</button></p>}
      </Card>
    </>
  )
}

interface RunResult {
  dry_run: boolean
  scope: string
  created: number
  stats: { papers: number; reviewers: number; candidate_edges: number; assigned: number; requested: number; mean_score: number; load_min: number; load_max: number; load_mean: number; load_stddev: number; duration_ms: number }
  unassigned: { paper_id: number; needed: number; assigned: number; reason: string; message: string; qualified_reviewers: number; excluded_by_coi: Record<string, number> }[]
  violations: string[]
  ran_at: string
}

const reasonText: Record<string, string> = { insufficient_expertise: 'Uzman yetersiz', conflict_of_interest: 'Çıkar çatışması', reviewer_capacity_exhausted: 'Hakem kapasitesi dolu' }

export function Assignments() {
  const qc = useQueryClient()
  const report = useQuery({ queryKey: ['assign-report'], queryFn: () => api<{ report: RunResult | null; at?: string }>('/api/assignments/report') })
  const [result, setResult] = useState<RunResult | null>(null)
  const run = useMutation({
    mutationFn: (body: { dry_run: boolean; scope?: string }) => api<RunResult>('/api/assignments/auto', { body, idempotent: true }),
    onSuccess: (r) => {
      setResult(r)
      void qc.invalidateQueries({ queryKey: ['assign-report'] })
    },
  })
  const jobs = useMutation({ mutationFn: () => api<{ reminders_sent: Record<string, number>; expired: number; reassigned: number; scanned: number }>('/api/jobs/reminders/run', { body: {} }) })
  const raw = result ?? report.data?.report ?? null
  const shown = raw ? { ...raw, violations: raw.violations ?? [], unassigned: raw.unassigned ?? [] } : null
  return (
    <>
      <PageTitle sub="Min-cost flow: her bildiriye 3 hakem; uzmanlık skoru, çıkar çatışması (aynı kurum, ortak yayın, danışmanlık) ve azami yük gözetilir, yük dengelenir.">Hakem atama</PageTitle>
      <Card title="Çalıştır" actions={
        <>
          <button className="btn secondary" disabled={run.isPending} onClick={() => run.mutate({ dry_run: true, scope: 'all' })}>Ölçüm: 500 bildirinin tamamı (deneme)</button>
          <button className="btn secondary" disabled={run.isPending} onClick={() => run.mutate({ dry_run: true })}>Bekleyenler (deneme)</button>
          <button className="btn" disabled={run.isPending} onClick={() => run.mutate({ dry_run: false })}>Bekleyenlere ata</button>
        </>
      }>
        {run.isPending && <Spinner label="Atama hesaplanıyor…" />}
        {run.isError && <ErrorBox error={run.error} />}
        {!shown && !run.isPending && <p className="muted">Henüz bir atama çalıştırılmadı.</p>}
        {shown && (
          <>
            <Notice kind={shown.violations.length ? 'err' : 'ok'}>
              {shown.dry_run ? 'Deneme (kaydedilmedi)' : `${shown.created} atama kaydedildi`} · bağımsız doğrulayıcı: {shown.violations.length === 0 ? 'ihlal yok ✓' : `${shown.violations.length} ihlal`} · {dt(shown.ran_at)}
            </Notice>
            <div className="stats wide">
              <Stat label="Bildiri / hakem" value={`${shown.stats.papers} / ${shown.stats.reviewers}`} />
              <Stat label="Atanan / istenen" value={`${shown.stats.assigned} / ${shown.stats.requested}`} />
              <Stat label="Süre" value={`${shown.stats.duration_ms} ms`} hint="sınır: 20 sn" />
              <Stat label="Ort. uzmanlık skoru" value={shown.stats.mean_score.toFixed(3)} />
              <Stat label="Yük (min–max)" value={`${shown.stats.load_min}–${shown.stats.load_max}`} hint={`σ = ${shown.stats.load_stddev.toFixed(2)}`} />
              <Stat label="Aday kenar" value={shown.stats.candidate_edges} />
            </div>
            <h3>Atanamayan bildiriler ({shown.unassigned.length})</h3>
            {shown.unassigned.length === 0 ? <p className="muted">Tüm bildirilere 3 hakem atandı.</p> : (
              <div className="table-wrap">
                <table>
                  <thead><tr><th>Bildiri</th><th>Atanan</th><th>Neden</th><th>Açıklama</th></tr></thead>
                  <tbody>
                    {shown.unassigned.map((u) => (
                      <tr key={u.paper_id}>
                        <td><Link to={`/submissions/${u.paper_id}`}>#{u.paper_id}</Link></td>
                        <td>{u.assigned}/{u.needed}</td>
                        <td><span className="badge s-pending">{reasonText[u.reason] ?? u.reason}</span></td>
                        <td>{u.message}{Object.keys(u.excluded_by_coi ?? {}).length > 0 && <div className="hint">ÇÇ elenen: {Object.entries(u.excluded_by_coi).map(([k, v]) => `${k}=${v}`).join(', ')}</div>}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </>
        )}
      </Card>
      <Card title="Hatırlatma ve süre aşımı job'ı" actions={<button className="btn secondary" disabled={jobs.isPending} onClick={() => jobs.mutate()}>Şimdi çalıştır</button>}>
        <p className="muted">Arka planda her dakika çalışır. 7. ve 12. günde hatırlatma, 14 günde atama düşürülüp yerine yeni hakem atanır. Tekrar çalıştırmak aynı hatırlatmayı iki kez göndermez (idempotent).</p>
        {jobs.isError && <ErrorBox error={jobs.error} />}
        {jobs.data && <Notice>Taranan {jobs.data.scanned} · hatırlatma {JSON.stringify(jobs.data.reminders_sent)} · süresi dolan {jobs.data.expired} · yeniden atanan {jobs.data.reassigned}</Notice>}
      </Card>
    </>
  )
}

export function ReviewerPool() {
  const [kw, setKw] = useState('')
  const q = useInfiniteQuery({
    queryKey: ['pool', kw],
    initialPageParam: '',
    queryFn: ({ pageParam }) => api<Page<{ id: number; full_name: string; institution: string; keywords: string[]; max_load: number; current_load: number; completed_reviews: number; expired_assignments: number }>>(`/api/reviewers?limit=50${kw ? `&keyword=${encodeURIComponent(kw)}` : ''}${pageParam ? `&cursor=${pageParam}` : ''}`),
    getNextPageParam: (l) => l.next_cursor || undefined,
  })
  const items = q.data?.pages.flatMap((p) => p.items) ?? []
  return (
    <>
      <PageTitle sub="Uzmanlık alanları ve anlık yükler">Hakem havuzu</PageTitle>
      <Card>
        <input placeholder="Anahtar kelimeyle filtrele (ör. deep learning)" value={kw} onChange={(e) => setKw(e.target.value)} style={{ marginBottom: 12 }} />
        <QueryState q={q} isEmpty={() => items.length === 0}>
          {() => (
            <div className="table-wrap">
              <table>
                <thead><tr><th>Hakem</th><th>Kurum</th><th>Uzmanlık</th><th className="num">Yük</th><th className="num">Tamamlanan</th><th className="num">Süresi dolan</th></tr></thead>
                <tbody>
                  {items.map((r) => (
                    <tr key={r.id}>
                      <td>{r.full_name}</td><td>{r.institution}</td>
                      <td>{r.keywords.map((k) => <span className="chip" key={k}>{k}</span>)}</td>
                      <td className="num">{r.current_load}/{r.max_load}</td><td className="num">{r.completed_reviews}</td><td className="num">{r.expired_assignments}</td>
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
