import { useState } from 'react'
import { useInfiniteQuery } from '@tanstack/react-query'
import { api, type Page, type User } from '../api'
import { Card, PageTitle, QueryState } from '../components/ui'
import { dt, roleLabel } from '../format'

export function Users() {
  const [role, setRole] = useState('')
  const q = useInfiniteQuery({
    queryKey: ['users', role],
    initialPageParam: '',
    queryFn: ({ pageParam }) => api<Page<User>>(`/api/admin/users?limit=50&role=${role}${pageParam ? `&cursor=${pageParam}` : ''}`),
    getNextPageParam: (l) => l.next_cursor || undefined,
  })
  const items = q.data?.pages.flatMap((p) => p.items) ?? []
  return (
    <>
      <PageTitle>Kullanıcılar</PageTitle>
      <Card>
        <select value={role} onChange={(e) => setRole(e.target.value)} style={{ maxWidth: 260, marginBottom: 12 }} aria-label="Rol">
          <option value="">Tüm roller</option>
          {Object.entries(roleLabel).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </select>
        <QueryState q={q} isEmpty={() => items.length === 0}>
          {() => (
            <div className="table-wrap">
              <table>
                <thead><tr><th>#</th><th>Ad</th><th>E-posta</th><th>Rol</th><th>Kurum</th><th>Saat dilimi</th></tr></thead>
                <tbody>{items.map((u) => <tr key={u.id}><td>{u.id}</td><td>{u.full_name}</td><td>{u.email}</td><td>{roleLabel[u.role]}</td><td>{u.institution}</td><td>{u.timezone}</td></tr>)}</tbody>
              </table>
            </div>
          )}
        </QueryState>
        {q.hasNextPage && <button className="btn secondary" onClick={() => void q.fetchNextPage()}>Daha fazla</button>}
      </Card>
    </>
  )
}

interface AuditRow {
  id: number
  at: string
  actor_id?: number
  actor_role: string
  action: string
  entity: string
  entity_id: string
  old_value: unknown
  new_value: unknown
}

export function AuditLog() {
  const [entity, setEntity] = useState('')
  const q = useInfiniteQuery({
    queryKey: ['audit', entity],
    initialPageParam: '',
    queryFn: ({ pageParam }) => api<Page<AuditRow>>(`/api/admin/audit?limit=50&entity=${entity}${pageParam ? `&cursor=${pageParam}` : ''}`),
    getNextPageParam: (l) => l.next_cursor || undefined,
  })
  const items = q.data?.pages.flatMap((p) => p.items) ?? []
  return (
    <>
      <PageTitle sub="Eklemeli-yalnız: veritabanı tetikleyicisi güncelleme ve silmeyi reddeder">Denetim kaydı</PageTitle>
      <Card>
        <select value={entity} onChange={(e) => setEntity(e.target.value)} style={{ maxWidth: 260, marginBottom: 12 }} aria-label="Varlık">
          <option value="">Tümü</option>
          {['submission', 'registration', 'assignment', 'program', 'certificate', 'user', 'fx_rate'].map((e) => <option key={e}>{e}</option>)}
        </select>
        <QueryState q={q} isEmpty={() => items.length === 0}>
          {() => (
            <div className="table-wrap">
              <table>
                <thead><tr><th>Zaman</th><th>Kim</th><th>İşlem</th><th>Neyi</th><th>Eski → yeni</th></tr></thead>
                <tbody>
                  {items.map((a) => (
                    <tr key={a.id}>
                      <td>{dt(a.at)}</td><td>{a.actor_role}{a.actor_id ? ` #${a.actor_id}` : ''}</td><td><code>{a.action}</code></td><td>{a.entity} {a.entity_id}</td>
                      <td style={{ maxWidth: 380, fontSize: '0.8rem' }}>{a.old_value ? JSON.stringify(a.old_value) : '—'} → {a.new_value ? JSON.stringify(a.new_value) : '—'}</td>
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
