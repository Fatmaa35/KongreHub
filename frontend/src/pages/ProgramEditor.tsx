import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError } from '../api'
import { Card, ErrorBox, Notice, PageTitle, QueryState, Spinner, Stat } from '../components/ui'
import { time } from '../format'
import type { SessionView } from './Program'

interface GenResult {
  dry_run: boolean
  stats: { papers: number; scheduled: number; sessions: number; avg_intra_session_similarity: number; duration_ms: number; search_nodes: number }
  violations: { code: string; message: string }[]
  unscheduled: { paper_id: number; reason: string; message: string; title: string }[]
}

const reasonText: Record<string, string> = { no_paid_author: 'Ödeme yapan yazar yok', speaker_unavailable: 'Konuşmacı uygun değil (saat dilimi)', no_room_or_slot: 'Uygun salon/zaman yok' }

/** Program oluşturma + sürükle-bırak düzenleme (bonus). Her taşıma sunucuda bağımsız doğrulayıcıdan geçer. */
export default function ProgramEditor() {
  const qc = useQueryClient()
  const rooms = useQuery({ queryKey: ['rooms'], queryFn: () => api<{ items: { id: number; name: string; capacity: number }[] }>('/api/rooms') })
  const slots = useQuery({ queryKey: ['slots'], queryFn: () => api<{ items: { id: number; starts_at: string }[] }>('/api/slots') })
  const prog = useQuery({ queryKey: ['program'], queryFn: () => api<{ sessions: SessionView[] }>('/api/program') })
  const val = useQuery({ queryKey: ['program-validate'], queryFn: () => api<{ valid: boolean; violations: { code: string; message: string }[]; sessions: number; scheduled: number }>('/api/program/validate') })
  const [gen, setGen] = useState<GenResult | null>(null)
  const [dragging, setDragging] = useState<number | null>(null)
  const [over, setOver] = useState<string | null>(null)
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['program'] })
    void qc.invalidateQueries({ queryKey: ['program-validate'] })
  }
  const generate = useMutation({
    mutationFn: (dry: boolean) => api<GenResult>('/api/program/generate', { body: { dry_run: dry }, idempotent: true }),
    onSuccess: (r) => {
      setGen(r)
      if (!r.dry_run) refresh()
    },
  })
  const move = useMutation({
    mutationFn: (v: { id: number; room_id: number; slot_id: number }) => api(`/api/program/sessions/${v.id}`, { method: 'PUT', body: { room_id: v.room_id, slot_id: v.slot_id } }),
    onSuccess: refresh,
  })
  const moveErr = move.error instanceof ApiError ? (move.error.details?.violations as { message: string }[] | undefined) : undefined
  return (
    <>
      <PageTitle sub="Kabul edilmiş ve en az bir yazarı ödeme yapmış bildiriler konu benzerliğine göre oturumlara gruplanır; salon kapasitesi, konuşmacı çakışması, saat dilimi uygunluğu ve oturum başkanı kısıtları gözetilir.">Program oluşturma</PageTitle>
      <Card title="Oluştur ve doğrula" actions={
        <>
          <button className="btn secondary" disabled={generate.isPending} onClick={() => generate.mutate(true)}>Deneme (kaydetme)</button>
          <button className="btn" disabled={generate.isPending} onClick={() => confirm('Mevcut program yeniden oluşturulacak. Devam?') && generate.mutate(false)}>Programı yeniden oluştur</button>
        </>
      }>
        {generate.isPending && <Spinner label="Çizelgeleniyor…" />}
        {generate.isError && <ErrorBox error={generate.error} />}
        {val.data && <Notice kind={val.data.valid ? 'ok' : 'err'}>Bağımsız doğrulayıcı: {val.data.valid ? `program çakışmasız ✓ (${val.data.sessions} oturum, ${val.data.scheduled} bildiri)` : `${val.data.violations.length} ihlal`}</Notice>}
        {gen && (
          <>
            <div className="stats">
              <Stat label="Programa alınan" value={`${gen.stats.scheduled} / ${gen.stats.papers}`} />
              <Stat label="Oturum" value={gen.stats.sessions} />
              <Stat label="Oturum içi benzerlik" value={gen.stats.avg_intra_session_similarity.toFixed(3)} hint="Jaccard ort." />
              <Stat label="Süre" value={`${gen.stats.duration_ms} ms`} />
            </div>
            {gen.unscheduled.length > 0 && (
              <>
                <h3>Programa alınamayanlar ({gen.unscheduled.length})</h3>
                <ul>{gen.unscheduled.map((u) => <li key={u.paper_id}>#{u.paper_id} {u.title} — <strong>{reasonText[u.reason] ?? u.reason}</strong></li>)}</ul>
              </>
            )}
          </>
        )}
      </Card>
      <Card title="Izgara (oturumu sürükleyip boş bir hücreye bırakın)">
        {move.isError && <ErrorBox error={move.error} />}
        {moveErr && <ul className="hint">{moveErr.slice(0, 5).map((v, i) => <li key={i}>{v.message}</li>)}</ul>}
        {move.isSuccess && <Notice>Oturum taşındı; program hâlâ çakışmasız.</Notice>}
        <QueryState q={prog}>
          {(p) => !rooms.data || !slots.data ? <Spinner /> : (
            <div className="table-wrap">
              <table>
                <thead><tr><th>Saat</th>{rooms.data.items.map((r) => <th key={r.id}>{r.name} ({r.capacity})</th>)}</tr></thead>
                <tbody>
                  {slots.data.items.map((sl) => (
                    <tr key={sl.id}>
                      <td><strong>{new Date(sl.starts_at).toLocaleDateString('tr-TR', { day: 'numeric', month: 'short', timeZone: 'Europe/Istanbul' })}</strong><br />{time(sl.starts_at)}</td>
                      {rooms.data.items.map((r) => {
                        const s = p.sessions.find((x) => x.room_id === r.id && x.slot_id === sl.id)
                        const key = `${r.id}-${sl.id}`
                        return (
                          <td key={r.id} style={{ minWidth: 160 }}>
                            <div
                              className={`drop ${over === key ? 'over' : ''}`}
                              onDragOver={(e) => { if (!s) { e.preventDefault(); setOver(key) } }}
                              onDragLeave={() => setOver(null)}
                              onDrop={() => { setOver(null); if (dragging) move.mutate({ id: dragging, room_id: r.id, slot_id: sl.id }) }}
                            >
                              {s && (
                                <div className="session" draggable onDragStart={() => setDragging(s.id)} onDragEnd={() => setDragging(null)}>
                                  <strong>{s.title}</strong>
                                  <div className="hint">{s.talks.length} konuşma · {s.chair}</div>
                                </div>
                              )}
                            </div>
                          </td>
                        )
                      })}
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
