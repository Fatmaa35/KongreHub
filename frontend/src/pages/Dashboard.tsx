import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../api'
import { useAuth } from '../auth'
import { Card, PageTitle, QueryState, Stat } from '../components/ui'
import { roleLabel, time } from '../format'

interface Live {
  counts: Record<string, number>
  events_per_minute: number
  recent_events: { id: number; at: string; action: string; entity: string }[]
  fx: { rate: string; observed_at: string }
  server_time: string
}

const actionLabel: Record<string, string> = {
  'registration.created': 'Yeni kayıt',
  'registration.paid': 'Ödeme alındı',
  'payment.initiated': 'Ödeme başlatıldı',
  'review.submitted': 'Değerlendirme gönderildi',
  'review.reminder_sent': 'Hakem hatırlatması',
  'assignment.created': 'Hakem atandı',
  'assignment.expired': 'Süresi dolan atama',
  'submission.created': 'Yeni bildiri',
  'submission.status_changed': 'Bildiri durumu değişti',
  'fx.recorded': 'Kur güncellendi',
  'program.generated': 'Program oluşturuldu',
  'certificate.issued': 'Sertifika üretildi',
}

const quick: Record<string, { to: string; label: string }[]> = {
  author: [{ to: '/author/new', label: 'Yeni bildiri gönder' }, { to: '/author/submissions', label: 'Bildirilerimi gör' }],
  reviewer: [{ to: '/reviewer', label: 'Bekleyen değerlendirmeler' }],
  editor: [{ to: '/editor/assignments', label: 'Otomatik hakem ataması' }, { to: '/editor/program', label: 'Programı oluştur' }],
  registrar: [{ to: '/registrar', label: 'Kayıtlar' }, { to: '/registrar/group', label: 'Grup kaydı' }],
  attendee: [{ to: '/registration', label: 'Kayıt ol ve öde' }, { to: '/program', label: 'Programa göz at' }],
  admin: [{ to: '/admin/audit', label: 'Denetim kaydı' }, { to: '/admin/users', label: 'Kullanıcılar' }],
}

export default function Dashboard() {
  const { user } = useAuth()
  // canlı akış: simülatörün ürettiği olaylar birkaç saniyede bir görünür
  const q = useQuery({ queryKey: ['live'], queryFn: () => api<Live>('/api/live'), refetchInterval: 3000 })
  return (
    <>
      <PageTitle sub={`${roleLabel[user!.role]} olarak giriş yaptınız`}>Hoş geldiniz, {user!.full_name}</PageTitle>
      <div className="actions" style={{ marginBottom: 18 }}>
        {quick[user!.role].map((x) => (
          <Link key={x.to} className="btn" to={x.to}>
            {x.label}
          </Link>
        ))}
      </div>
      <QueryState q={q}>
        {(d) => (
          <div className="grid two">
            <Card title={<><span className="live-dot" />Sistem canlı</>}>
              <div className="stats">
                <Stat label="Bildiri" value={d.counts.submissions} />
                <Stat label="Kayıt" value={d.counts.registrations} />
                <Stat label="Ödenmiş kayıt" value={d.counts.paid_registrations} />
                <Stat label="Açık hakem ataması" value={d.counts.open_assignments} />
                <Stat label="Değerlendirme" value={d.counts.reviews} />
                <Stat label="Olay / dk" value={d.events_per_minute} hint="son 1 dakika" />
              </div>
              <p className="hint" style={{ marginTop: 12 }}>
                EUR/TRY kuru: <strong>{d.fx.rate}</strong> ({time(d.fx.observed_at)}) · sunucu saati {time(d.server_time)}
              </p>
            </Card>
            <Card title="Son olaylar">
              {d.recent_events.length === 0 ? (
                <p className="muted">Henüz olay yok.</p>
              ) : (
                <ul className="feed">
                  {d.recent_events.map((e) => (
                    <li key={e.id}>
                      <time>{time(e.at)}</time> {actionLabel[e.action] ?? e.action}
                    </li>
                  ))}
                </ul>
              )}
            </Card>
          </div>
        )}
      </QueryState>
    </>
  )
}
