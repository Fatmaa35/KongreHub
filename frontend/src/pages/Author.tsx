import { useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router-dom'
import { useFeedback } from '../components/feedback'
import { api, type Page } from '../api'
import { useAuth } from '../auth'
import { Badge, Card, ErrorBox, PageTitle, QueryState } from '../components/ui'
import { dt } from '../format'

export interface SubmissionSummary {
  id: number
  title: string
  keywords: string[]
  status: string
  round: number
  final_score?: number
  disagreement_flag: boolean
  active_reviewers: number
  submitted_reviews: number
  created_at: string
}

export function MySubmissions() {
  const q = useQuery({ queryKey: ['mine'], queryFn: () => api<Page<SubmissionSummary>>('/api/submissions/mine') })
  return (
    <>
      <PageTitle sub="Gönderdiğiniz ya da yazarı olduğunuz bildiriler">Bildirilerim</PageTitle>
      <Card actions={<Link className="btn" to="/author/new">Yeni bildiri</Link>}>
        <QueryState q={q} isEmpty={(d) => d.items.length === 0} empty="Henüz bildiriniz yok. İlk bildirinizi gönderdiğinizde durumu burada izleyebilirsiniz." emptyAction={<Link className="btn" to="/author/new">Bildiri gönder</Link>}>
          {(d) => (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr><th>#</th><th>Başlık</th><th>Durum</th><th>Tur</th><th>Gönderim</th></tr>
                </thead>
                <tbody>
                  {d.items.map((s) => (
                    <tr key={s.id}>
                      <td>{s.id}</td>
                      <td><Link to={`/submissions/${s.id}`}>{s.title}</Link><div>{s.keywords.map((k) => <span className="chip" key={k}>{k}</span>)}</div></td>
                      <td><Badge status={s.status} /></td>
                      <td>{s.round}</td>
                      <td>{dt(s.created_at, false)}</td>
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

interface AuthorRow {
  full_name: string
  email: string
  institution_id: string
  is_presenter: boolean
}

export function NewSubmission() {
  const { user } = useAuth()
  const nav = useNavigate()
  const qc = useQueryClient()
  const { toast } = useFeedback()
  const inst = useQuery({ queryKey: ['institutions'], queryFn: () => api<{ items: { id: number; name: string }[] }>('/api/institutions') })
  const [title, setTitle] = useState('')
  const [abstract, setAbstract] = useState('')
  const [keywords, setKeywords] = useState('')
  const [duration, setDuration] = useState('20')
  const [file, setFile] = useState<File | null>(null)
  const [authors, setAuthors] = useState<AuthorRow[]>([{ full_name: user!.full_name, email: user!.email, institution_id: '', is_presenter: true }])
  const m = useMutation({
    mutationFn: () => {
      const fd = new FormData()
      fd.set('metadata', JSON.stringify({
        title, abstract, duration_minutes: Number(duration),
        keywords: keywords.split(',').map((k) => k.trim()).filter(Boolean),
        authors: authors.map((a) => ({ full_name: a.full_name, email: a.email, is_presenter: a.is_presenter, institution_id: a.institution_id ? Number(a.institution_id) : null })),
      }))
      if (file) fd.set('file', file)
      return api<{ id: number }>('/api/submissions', { body: fd })
    },
    onSuccess: (s) => {
      void qc.invalidateQueries({ queryKey: ['mine'] })
      toast('Bildiriniz alındı. PDF\'teki yazar bilgisi temizlendi.')
      nav(`/submissions/${s.id}`)
    },
  })
  const set = (i: number, patch: Partial<AuthorRow>) => setAuthors(authors.map((a, j) => (j === i ? { ...a, ...patch } : patch.is_presenter ? { ...a, is_presenter: false } : a)))
  const submit = (e: FormEvent) => {
    e.preventDefault()
    m.mutate()
  }
  return (
    <>
      <PageTitle sub="Çift kör değerlendirme: PDF'in yazar bilgisi (metadata) yükleme sırasında otomatik temizlenir; metinde kimlik bilgisi bırakmayın.">Yeni bildiri</PageTitle>
      <form onSubmit={submit}>
        <Card title="Bildiri bilgileri">
          <div className="field"><label htmlFor="t">Başlık</label><input id="t" value={title} onChange={(e) => setTitle(e.target.value)} required minLength={5} /></div>
          <div className="field"><label htmlFor="a">Özet</label><textarea id="a" value={abstract} onChange={(e) => setAbstract(e.target.value)} required minLength={20} /></div>
          <div className="row">
            <div className="field"><label htmlFor="k">Anahtar kelimeler</label><input id="k" value={keywords} onChange={(e) => setKeywords(e.target.value)} placeholder="deep learning, medical imaging" required /><div className="hint">Virgülle ayırın (1–8)</div></div>
            <div className="field"><label htmlFor="d">Sunum süresi</label><select id="d" value={duration} onChange={(e) => setDuration(e.target.value)}><option value="15">15 dk</option><option value="20">20 dk</option><option value="30">30 dk</option></select></div>
            <div className="field"><label htmlFor="f">PDF dosyası</label><input id="f" type="file" accept="application/pdf" onChange={(e) => setFile(e.target.files?.[0] ?? null)} required /></div>
          </div>
        </Card>
        <Card title="Yazarlar" actions={<button type="button" className="btn secondary small" onClick={() => setAuthors([...authors, { full_name: '', email: '', institution_id: '', is_presenter: false }])}>Yazar ekle</button>}>
          {authors.map((a, i) => (
            <div className="row" key={i} style={{ alignItems: 'end', marginBottom: 8 }}>
              <div><label>Ad soyad</label><input value={a.full_name} onChange={(e) => set(i, { full_name: e.target.value })} required /></div>
              <div><label>E-posta</label><input type="email" value={a.email} onChange={(e) => set(i, { email: e.target.value })} required /></div>
              <div>
                <label>Kurum</label>
                <select value={a.institution_id} onChange={(e) => set(i, { institution_id: e.target.value })}>
                  <option value="">—</option>
                  {inst.data?.items.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
                </select>
              </div>
              <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                <label style={{ margin: 0, display: 'flex', gap: 6, alignItems: 'center' }}><input type="radio" style={{ width: 'auto' }} checked={a.is_presenter} onChange={() => set(i, { is_presenter: true })} /> Sunucu</label>
                {i > 0 && <button type="button" className="btn ghost small" onClick={() => setAuthors(authors.filter((_, j) => j !== i))}>Kaldır</button>}
              </div>
            </div>
          ))}
        </Card>
        {m.isError && <ErrorBox error={m.error} />}
        <button className="btn" disabled={m.isPending}>{m.isPending ? 'Gönderiliyor…' : 'Bildiriyi gönder'}</button>
      </form>
    </>
  )
}
