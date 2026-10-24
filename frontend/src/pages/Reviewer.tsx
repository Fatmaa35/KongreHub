import { useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { api, download, type Page } from '../api'
import { Badge, Card, ErrorBox, Notice, PageTitle, QueryState } from '../components/ui'
import { dt, recLabel } from '../format'

interface Assignment {
  id: number
  submission_id: number
  title: string
  abstract: string
  keywords: string[]
  round: number
  version: number
  status: string
  assigned_at: string
  due_at: string
  days_left: number
  response_letter?: string
  my_review?: { originality: number; significance: number; methodology: number; clarity: number; weighted_score: number; recommendation: string; comments_to_author: string }
}

export function ReviewerAssignments() {
  const q = useQuery({ queryKey: ['my-assignments'], queryFn: () => api<Page<Assignment>>('/api/reviewer/assignments') })
  return (
    <>
      <PageTitle sub="Çift kör: yazar bilgileri size gösterilmez. Yanıt süresi atamadan itibaren 14 gündür; 7. ve 12. günde hatırlatma gönderilir.">Değerlendirmelerim</PageTitle>
      <Card>
        <QueryState q={q} isEmpty={(d) => d.items.length === 0} empty="Size atanmış bildiri yok.">
          {(d) => (
            <div className="table-wrap">
              <table>
                <thead><tr><th>Bildiri</th><th>Tur</th><th>Durum</th><th>Son tarih</th><th /></tr></thead>
                <tbody>
                  {d.items.map((a) => (
                    <tr key={a.id}>
                      <td>{a.title}<div>{a.keywords.map((k) => <span className="chip" key={k}>{k}</span>)}</div></td>
                      <td>{a.round}</td>
                      <td><Badge status={a.status} /></td>
                      <td>{dt(a.due_at, false)}{a.status === 'assigned' && <div className={a.days_left < 3 ? 'hint' : 'hint'} style={{ color: a.days_left < 3 ? 'var(--err)' : undefined }}>{a.days_left} gün kaldı</div>}</td>
                      <td><Link className="btn small" to={`/reviewer/assignments/${a.id}`}>{a.status === 'assigned' ? 'Değerlendir' : 'Görüntüle'}</Link></td>
                    </tr>
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

const criteria = [
  ['originality', 'Özgünlük', '%30'],
  ['significance', 'Önem', '%25'],
  ['methodology', 'Yöntem', '%30'],
  ['clarity', 'Açıklık', '%15'],
] as const

export function ReviewForm() {
  const { id } = useParams()
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['assignment', id], queryFn: () => api<Assignment>(`/api/reviewer/assignments/${id}`) })
  const [scores, setScores] = useState<Record<string, number>>({ originality: 7, significance: 7, methodology: 7, clarity: 7 })
  const [rec, setRec] = useState('minor_revision')
  const [toAuthor, setToAuthor] = useState('')
  const [toEditor, setToEditor] = useState('')
  const weighted = scores.originality * 0.3 + scores.significance * 0.25 + scores.methodology * 0.3 + scores.clarity * 0.15
  const submit = useMutation({
    mutationFn: () => api(`/api/reviewer/assignments/${id}/review`, { body: { ...scores, recommendation: rec, comments_to_author: toAuthor, comments_to_editor: toEditor }, idempotent: true }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['assignment', id] })
      void qc.invalidateQueries({ queryKey: ['my-assignments'] })
    },
  })
  const decline = useMutation({ mutationFn: () => api(`/api/reviewer/assignments/${id}/decline`, { body: { reason: 'uygun değilim' } }), onSuccess: () => void qc.invalidateQueries({ queryKey: ['my-assignments'] }) })
  return (
    <QueryState q={q}>
      {(a) => (
        <>
          <PageTitle sub={<>Tur {a.round} · sürüm {a.version} · son tarih {dt(a.due_at, false)}</>}>{a.title}</PageTitle>
          <Card title="Bildiri" actions={<button className="btn secondary small" onClick={() => void download(`/api/submissions/${a.submission_id}/file`, `submission-${a.submission_id}.pdf`)}>PDF indir</button>}>
            <p>{a.abstract}</p>
            {a.response_letter && <Notice kind="warn"><strong>Yazarın yanıt mektubu:</strong> {a.response_letter}</Notice>}
          </Card>
          {a.my_review ? (
            <Card title="Gönderdiğiniz değerlendirme">
              <p>Ağırlıklı puan <strong>{a.my_review.weighted_score.toFixed(2)}</strong> · {recLabel[a.my_review.recommendation]}</p>
              <p>{a.my_review.comments_to_author}</p>
            </Card>
          ) : decline.isSuccess ? (
            <Notice kind="ok">Atamayı reddettiniz; yerinize uygun bir hakem atandı.</Notice>
          ) : (
            <form onSubmit={(e: FormEvent) => { e.preventDefault(); submit.mutate() }}>
              <Card title="Değerlendirme formu" actions={<span>Ağırlıklı puan: <strong>{weighted.toFixed(2)}</strong> / 10</span>}>
                <div className="row">
                  {criteria.map(([k, label, w]) => (
                    <div className="field" key={k}>
                      <label htmlFor={k}>{label} ({w}): {scores[k]}</label>
                      <input id={k} type="range" min={1} max={10} value={scores[k]} onChange={(e) => setScores({ ...scores, [k]: Number(e.target.value) })} />
                    </div>
                  ))}
                </div>
                <div className="field">
                  <label htmlFor="rec">Öneri</label>
                  <select id="rec" value={rec} onChange={(e) => setRec(e.target.value)}>{Object.entries(recLabel).map(([k, v]) => <option key={k} value={k}>{v}</option>)}</select>
                </div>
                <div className="field"><label htmlFor="ta">Yazara yorumlar</label><textarea id="ta" value={toAuthor} onChange={(e) => setToAuthor(e.target.value)} required minLength={10} /><div className="hint">Kimliğinizi ele verecek bilgi yazmayın.</div></div>
                <div className="field"><label htmlFor="te">Yalnızca editöre not (isteğe bağlı)</label><textarea id="te" value={toEditor} onChange={(e) => setToEditor(e.target.value)} /></div>
                {submit.isError && <ErrorBox error={submit.error} />}
                {decline.isError && <ErrorBox error={decline.error} />}
                <div className="actions">
                  <button className="btn" disabled={submit.isPending}>Değerlendirmeyi gönder</button>
                  <button type="button" className="btn ghost" onClick={() => confirm('Bu atamayı reddetmek istiyor musunuz?') && decline.mutate()}>Atamayı reddet</button>
                </div>
              </Card>
            </form>
          )}
        </>
      )}
    </QueryState>
  )
}
