import { useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { useFeedback } from '../components/feedback'
import { api, download, type Page } from '../api'
import { useAuth } from '../auth'
import { Badge, Card, ErrorBox, Notice, PageTitle, QueryState, Steps } from '../components/ui'
import { dt, recLabel } from '../format'

interface Review {
  reviewer: string
  reviewer_id?: number
  reviewer_name?: string
  round: number
  originality: number
  significance: number
  methodology: number
  clarity: number
  weighted_score: number
  recommendation: string
  comments_to_author: string
  comments_to_editor?: string
}

interface Submission {
  id: number
  title: string
  abstract: string
  keywords: string[]
  status: string
  round: number
  current_version: number
  duration_minutes: number
  final_score?: number
  disagreement_flag?: boolean
  created_at: string
  authors?: { position: number; full_name: string; email: string; institution?: string; is_presenter: boolean }[]
  files: { version: number; size_bytes: number; original_name?: string; response_letter?: string; created_at: string }[]
  reviews?: Review[]
  decisions?: { round: number; decision: string; note_to_author: string; decided_at: string }[]
  assignments?: { id: number; reviewer_id: number; reviewer_name: string; round: number; status: string; reason: string; match_score: number; due_at: string }[]
  aggregate?: { count: number; mean: number; spread: number; disagreement: boolean }
  my_assignment_id?: number
}

/** Bildirinin yaşam döngüsündeki yeri: gerçek bir sıra olduğu için adım göstergesi. */
function Lifecycle({ status, round }: { status: string; round: number }) {
  const steps = ['Gönderildi', 'Hakem incelemesi', round > 1 || status === 'revision_requested' ? 'Düzeltme turu' : 'Değerlendirmeler tamam', 'Karar']
  const idx: Record<string, number> = { submitted: 0, under_review: round > 1 ? 2 : 1, reviews_complete: round > 1 ? 3 : 2, revision_requested: 2, accepted: 4, rejected: 3, withdrawn: 1 }
  const failed = status === 'rejected' || status === 'withdrawn'
  const labels = [...steps]
  if (status === 'accepted') labels[3] = 'Kabul edildi'
  if (status === 'rejected') labels[3] = 'Reddedildi'
  if (status === 'withdrawn') labels[1] = 'Geri çekildi'
  return <Steps steps={labels} current={idx[status] ?? 0} failed={failed} />
}

const reasonLabel: Record<string, string> = { auto: 'otomatik', manual: 'elle', disagreement: 'uyuşmazlık (4. hakem)', replacement: 'yerine atama', revision: 'düzeltme turu' }

export default function SubmissionDetail() {
  const { id } = useParams()
  const { user } = useAuth()
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['submission', id], queryFn: () => api<Submission>(`/api/submissions/${id}`) })
  const refresh = () => void qc.invalidateQueries({ queryKey: ['submission', id] })
  return (
    <QueryState q={q}>
      {(s) => (
        <>
          <PageTitle sub={<>Bildiri #{s.id}, {s.round}. tur, sürüm {s.current_version}, {s.duration_minutes} dakikalık sunum</>} actions={<Badge status={s.status} />}>{s.title}</PageTitle>
          <Lifecycle status={s.status} round={s.round} />
          <div className="grid two">
            <Card title="Özet" actions={<button className="btn secondary small" onClick={() => void download(`/api/submissions/${s.id}/file`, `submission-${s.id}.pdf`)}>PDF indir</button>}>
              <p>{s.abstract}</p>
              <div>{s.keywords.map((k) => <span className="chip" key={k}>{k}</span>)}</div>
              {user!.role === 'reviewer' && s.my_assignment_id ? (
                <p><Link className="btn" to={`/reviewer/assignments/${s.my_assignment_id}`}>Değerlendirme formuna git</Link></p>
              ) : null}
              {user!.role === 'reviewer' && <p className="hint">Çift kör değerlendirme: yazar bilgileri gizlidir.</p>}
            </Card>
            {s.authors && (
              <Card title="Yazarlar">
                <ol>
                  {s.authors.map((a) => (
                    <li key={a.position}>{a.full_name} — <span className="muted">{a.institution || 'kurum belirtilmemiş'}</span> {a.is_presenter && <span className="chip">sunucu</span>}</li>
                  ))}
                </ol>
                {s.final_score != null && <p>Ortalama puan: <strong>{s.final_score.toFixed(2)}</strong></p>}
              </Card>
            )}
          </div>
          <Card title="Sürümler">
            <table>
              <thead><tr><th>Sürüm</th><th>Boyut</th><th>Yüklenme</th><th>Yanıt mektubu</th><th /></tr></thead>
              <tbody>
                {s.files.map((f) => (
                  <tr key={f.version}>
                    <td>v{f.version}</td>
                    <td>{(f.size_bytes / 1024).toFixed(1)} KB</td>
                    <td>{dt(f.created_at)}</td>
                    <td>{f.response_letter || '—'}</td>
                    <td><button className="btn ghost small" onClick={() => void download(`/api/submissions/${s.id}/file?version=${f.version}`, `submission-${s.id}-v${f.version}.pdf`)}>İndir</button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Card>
          {s.decisions && s.decisions.length > 0 && (
            <Card title="Kararlar">
              {s.decisions.map((d) => (
                <p key={d.round}><strong>Tur {d.round}: {recLabel[d.decision]}</strong> — {d.note_to_author} <span className="muted">({dt(d.decided_at)})</span></p>
              ))}
            </Card>
          )}
          {s.reviews && (
            <Card title={user!.role === 'author' ? 'Hakem değerlendirmeleri (anonim)' : 'Değerlendirmeler'}>
              {s.reviews.length === 0 ? <p className="muted">Karar verilene kadar değerlendirmeler yazara gösterilmez.</p> : s.reviews.map((r, i) => (
                <div className="review-card" key={i}>
                  <strong>{r.reviewer}</strong> {r.reviewer_name && <span className="muted">({r.reviewer_name})</span>} · tur {r.round} · puan <strong>{r.weighted_score.toFixed(2)}</strong> · {recLabel[r.recommendation]}
                  <div className="scores">
                    <span>Özgünlük {r.originality}</span><span>Önem {r.significance}</span><span>Yöntem {r.methodology}</span><span>Açıklık {r.clarity}</span>
                  </div>
                  <p>{r.comments_to_author}</p>
                  {r.comments_to_editor && <p className="hint">Editöre not: {r.comments_to_editor}</p>}
                </div>
              ))}
              {s.aggregate && s.aggregate.count > 0 && (
                <Notice kind={s.aggregate.disagreement ? 'warn' : 'ok'}>
                  Bu tur: {s.aggregate.count} değerlendirme, ortalama {s.aggregate.mean.toFixed(2)}, fark {s.aggregate.spread.toFixed(2)}
                  {s.aggregate.disagreement && ' — uyuşmazlık eşiği (>3) aşıldı, 4. hakem atanır'}
                </Notice>
              )}
            </Card>
          )}
          {user!.role === 'author' && s.status === 'revision_requested' && <RevisionForm id={s.id} onDone={refresh} />}
          {user!.role === 'author' && ['submitted', 'under_review'].includes(s.status) && <Withdraw id={s.id} onDone={refresh} />}
          {s.assignments && <EditorPanel s={s} onDone={refresh} />}
          {user!.role !== 'reviewer' && <History id={s.id} />}
        </>
      )}
    </QueryState>
  )
}

function RevisionForm({ id, onDone }: { id: number; onDone: () => void }) {
  const [file, setFile] = useState<File | null>(null)
  const [letter, setLetter] = useState('')
  const m = useMutation({
    mutationFn: () => {
      const fd = new FormData()
      fd.set('response_letter', letter)
      if (file) fd.set('file', file)
      return api(`/api/submissions/${id}/revisions`, { body: fd })
    },
    onSuccess: onDone,
  })
  return (
    <Card title="Düzeltilmiş sürümü yükle">
      <form onSubmit={(e: FormEvent) => { e.preventDefault(); m.mutate() }}>
        <div className="field"><label>Hakemlere yanıt mektubu</label><textarea value={letter} onChange={(e) => setLetter(e.target.value)} required minLength={10} /></div>
        <div className="field"><label>PDF</label><input type="file" accept="application/pdf" onChange={(e) => setFile(e.target.files?.[0] ?? null)} required /></div>
        {m.isError && <ErrorBox error={m.error} />}
        <button className="btn" disabled={m.isPending}>Yükle ve tekrar incelemeye gönder</button>
      </form>
    </Card>
  )
}

function Withdraw({ id, onDone }: { id: number; onDone: () => void }) {
  const { confirm } = useFeedback()
  const m = useMutation({ mutationFn: () => api(`/api/submissions/${id}/withdraw`, { body: { reason: 'yazar isteği' } }), onSuccess: onDone })
  return (
    <p>
      <button className="btn danger small" disabled={m.isPending} onClick={async () => (await confirm({ title: 'Bildiri geri çekilsin mi?', body: 'Hakem atamaları iptal edilir ve bildiri değerlendirmeden çıkar.', action: 'Geri çek', danger: true })) && m.mutate()}>Bildiriyi geri çek</button>
      {m.isError && <ErrorBox error={m.error} />}
    </p>
  )
}

function EditorPanel({ s, onDone }: { s: Submission; onDone: () => void }) {
  const { toast } = useFeedback()
  const [reviewerId, setReviewerId] = useState('')
  const [decision, setDecision] = useState('accept')
  const [note, setNote] = useState('')
  const pool = useQuery({
    queryKey: ['reviewers', s.keywords[0]],
    queryFn: () => api<Page<{ id: number; full_name: string; current_load: number; max_load: number; keywords: string[] }>>(`/api/reviewers?limit=50&keyword=${encodeURIComponent(s.keywords[0])}`),
  })
  const assign = useMutation({ mutationFn: () => api(`/api/submissions/${s.id}/assignments`, { body: { reviewer_id: Number(reviewerId) } }), onSuccess: () => { onDone(); toast('Hakem atandı') } })
  const release = useMutation({ mutationFn: (aid: number) => api(`/api/assignments/${aid}/release`, { body: { reason: 'editör' } }), onSuccess: onDone })
  const decide = useMutation({ mutationFn: () => api(`/api/submissions/${s.id}/decision`, { body: { decision, note_to_author: note }, idempotent: true }), onSuccess: () => { onDone(); toast(`Karar kaydedildi: ${recLabel[decision]}`) } })
  return (
    <div className="grid two">
      <Card title="Hakem atamaları">
        <div className="table-wrap">
          <table>
            <thead><tr><th>Hakem</th><th>Tur</th><th>Durum</th><th>Neden</th><th>Skor</th><th>Son tarih</th><th /></tr></thead>
            <tbody>
              {s.assignments!.map((a) => (
                <tr key={a.id}>
                  <td>{a.reviewer_name}</td><td>{a.round}</td><td><Badge status={a.status} /></td><td>{reasonLabel[a.reason] ?? a.reason}</td>
                  <td>{a.match_score.toFixed(2)}</td><td>{dt(a.due_at, false)}</td>
                  <td>{a.status === 'assigned' && <button className="btn ghost small" onClick={() => release.mutate(a.id)}>Geri al</button>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <form onSubmit={(e) => { e.preventDefault(); assign.mutate() }} style={{ marginTop: 12 }}>
          <label>Elle hakem ata (önerilenler: "{s.keywords[0]}" uzmanları)</label>
          <div style={{ display: 'flex', gap: 8 }}>
            <select value={reviewerId} onChange={(e) => setReviewerId(e.target.value)} required>
              <option value="">Hakem seçin…</option>
              {pool.data?.items.map((r) => <option key={r.id} value={r.id}>{r.full_name} ({r.current_load}/{r.max_load})</option>)}
            </select>
            <button className="btn" disabled={assign.isPending}>Ata</button>
          </div>
          {assign.isError && <ErrorBox error={assign.error} />}
          {release.isError && <ErrorBox error={release.error} />}
        </form>
      </Card>
      <Card title="Editör kararı">
        {s.status !== 'reviews_complete' ? (
          <p className="muted">Karar, turdaki tüm değerlendirmeler tamamlanınca verilebilir (durum: {s.status}).</p>
        ) : (
          <form onSubmit={(e) => { e.preventDefault(); decide.mutate() }}>
            <div className="field">
              <label>Karar</label>
              <select value={decision} onChange={(e) => setDecision(e.target.value)}>
                {Object.entries(recLabel).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </div>
            <div className="field"><label>Yazara not</label><textarea value={note} onChange={(e) => setNote(e.target.value)} /></div>
            {decide.isError && <ErrorBox error={decide.error} />}
            <button className="btn" disabled={decide.isPending}>Kararı kaydet</button>
          </form>
        )}
      </Card>
    </div>
  )
}

function History({ id }: { id: number }) {
  const q = useQuery({ queryKey: ['history', id], queryFn: () => api<{ items: { at: string; actor: string; action: string; status?: string }[] | null }>(`/api/submissions/${id}/history`) })
  return (
    <Card title="Geçmiş">
      <QueryState q={q} isEmpty={(d) => !d.items?.length}>
        {(d) => (
          <ul className="feed">
            {d.items!.map((e, i) => <li key={i}><time>{dt(e.at)}</time> <span>{e.actor}: {e.action}{e.status ? ` → ${e.status}` : ''}</span></li>)}
          </ul>
        )}
      </QueryState>
    </Card>
  )
}
