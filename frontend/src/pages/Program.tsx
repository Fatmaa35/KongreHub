import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, download } from '../api'
import { Card, ErrorBox, PageTitle, QueryState } from '../components/ui'
import { time } from '../format'

export interface SessionView {
  id: number
  title: string
  topic: string
  room_id: number
  room: string
  capacity: number
  slot_id: number
  starts_at: string
  ends_at: string
  chair: string
  in_agenda: boolean
  talks: { submission_id: number; title: string; presenter: string; starts_at: string; ends_at: string }[]
}

export function groupByDay(sessions: SessionView[]) {
  const days = new Map<string, Map<string, SessionView[]>>()
  for (const s of sessions) {
    const d = new Date(s.starts_at).toLocaleDateString('tr-TR', { weekday: 'long', day: 'numeric', month: 'long', timeZone: 'Europe/Istanbul' })
    const t = time(s.starts_at)
    if (!days.has(d)) days.set(d, new Map())
    const slots = days.get(d)!
    if (!slots.has(t)) slots.set(t, [])
    slots.get(t)!.push(s)
  }
  return days
}

export function SessionCard({ s, action }: { s: SessionView; action?: React.ReactNode }) {
  return (
    <div className="session">
      <h4>{s.title}</h4>
      <div className="hint">{s.room} ({s.capacity} kişi) · Başkan: {s.chair || '—'}</div>
      <ol>
        {s.talks.map((t) => <li key={t.submission_id}><strong>{time(t.starts_at)}</strong> {t.title} <span className="muted">— {t.presenter}</span></li>)}
      </ol>
      {action}
    </div>
  )
}

export function Program() {
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['program'], queryFn: () => api<{ sessions: SessionView[] }>('/api/program') })
  const toggle = useMutation({
    mutationFn: (s: SessionView) => (s.in_agenda ? api(`/api/agenda/${s.id}`, { method: 'DELETE' }) : api('/api/agenda', { body: { session_id: s.id } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['program'] }),
  })
  return (
    <>
      <PageTitle sub="10–12 Mayıs 2027, İstanbul (saatler İstanbul saatiyle)">Kongre programı</PageTitle>
      {toggle.isError && <ErrorBox error={toggle.error} />}
      <QueryState q={q} isEmpty={(d) => d.sessions.length === 0} empty="Program henüz yayınlanmadı.">
        {(d) => (
          <>
            {[...groupByDay(d.sessions)].map(([day, slots]) => (
              <div className="prog-day" key={day}>
                <h3>{day}</h3>
                <div className="prog-grid">
                  {[...slots].map(([t, ss]) => (
                    <div className="prog-row" key={t}>
                      <div className="prog-time">{t}</div>
                      <div className="prog-cells">
                        {ss.map((s) => (
                          <SessionCard key={s.id} s={s} action={<button className={`btn small ${s.in_agenda ? 'secondary' : ''}`} style={{ marginTop: 8 }} onClick={() => toggle.mutate(s)}>{s.in_agenda ? '✓ Programımda' : 'Programıma ekle'}</button>} />
                        ))}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </>
        )}
      </QueryState>
    </>
  )
}

export function Agenda() {
  const q = useQuery({ queryKey: ['agenda'], queryFn: () => api<{ sessions: SessionView[] }>('/api/agenda') })
  return (
    <>
      <PageTitle sub="Seçtiğiniz oturumlar; takviminize iCal olarak aktarabilirsiniz">Kişisel programım</PageTitle>
      <Card actions={<button className="btn" onClick={() => void download('/api/agenda.ics', 'kongrehub-program.ics')}>iCal (.ics) indir</button>}>
        <QueryState q={q} isEmpty={(d) => d.sessions.length === 0} empty="Programınıza henüz oturum eklemediniz. Program sayfasından ekleyebilirsiniz.">
          {(d) => <div className="prog-cells">{d.sessions.map((s) => <SessionCard key={s.id} s={s} action={<div className="hint">{new Date(s.starts_at).toLocaleDateString('tr-TR', { timeZone: 'Europe/Istanbul' })} {time(s.starts_at)}–{time(s.ends_at)}</div>} />)}</div>}
        </QueryState>
      </Card>
    </>
  )
}
