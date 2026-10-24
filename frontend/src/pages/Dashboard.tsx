import { useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api, type Page, type Role } from '../api'
import { useAuth } from '../auth'
import { Card, PageTitle, QueryState, Stat } from '../components/ui'
import { Icon, type IconName } from '../components/icons'
import { time } from '../format'

interface Live {
  counts: Record<string, number>
  events_per_minute: number
  recent_events: { id: number; at: string; action: string; entity: string }[]
  fx: { rate: string; observed_at: string }
  server_time: string
}

const actionLabel: Record<string, string> = {
  'registration.created': 'Yeni kongre kaydı',
  'registration.paid': 'Kayıt ücreti ödendi',
  'registration.cancelled': 'Kayıt iptal edildi',
  'payment.initiated': 'Ödeme başlatıldı',
  'review.submitted': 'Değerlendirme gönderildi',
  'review.reminder_sent': 'Hakeme hatırlatma gitti',
  'review.disagreement_detected': 'Puan uyuşmazlığı: 4. hakem atandı',
  'assignment.created': 'Hakem atandı',
  'assignment.expired': 'Süresi dolan atama yeniden verildi',
  'assignment.auto_run': 'Otomatik hakem ataması çalıştı',
  'submission.created': 'Yeni bildiri gönderildi',
  'submission.status_changed': 'Bildiri durumu değişti',
  'fx.recorded': 'EUR/TRY kuru güncellendi',
  'program.generated': 'Program yeniden oluşturuldu',
  'program.session_moved': 'Oturum taşındı',
  'certificate.issued': 'Sertifika düzenlendi',
  'seed.loaded': 'Demo verisi yüklendi',
  'user.created': 'Yeni kullanıcı hesabı',
  'room.created': 'Yeni salon eklendi',
  'assignment.released': 'Hakem ataması geri alındı',
  'assignment.declined': 'Hakem atamayı reddetti',
  'registration.group_created': 'Grup kaydı açıldı',
  'registration.requoted': 'Kayıt yeniden fiyatlandı',
}

interface Next { to: string; title: string; desc: string; icon: IconName }

const nextSteps: Record<Role, Next[]> = {
  author: [
    { to: '/author/submissions', title: 'Bildirilerimi izle', desc: 'Durum, hakem yorumları ve kararlar', icon: 'doc' },
    { to: '/author/new', title: 'Yeni bildiri gönder', desc: 'PDF yükleyin, yazar bilgisi otomatik gizlenir', icon: 'plus' },
    { to: '/registration', title: 'Kaydımı tamamla', desc: 'Programa alınmak için en az bir yazar ödemeli', icon: 'card' },
  ],
  reviewer: [
    { to: '/reviewer', title: 'Bekleyen değerlendirmeler', desc: 'Yanıt süresi atamadan itibaren 14 gün', icon: 'check' },
    { to: '/program', title: 'Kongre programı', desc: 'Oturumlar ve salonlar', icon: 'calendar' },
  ],
  editor: [
    { to: '/editor/assignments', title: 'Hakemleri ata', desc: 'Uzmanlık, çıkar çatışması ve yük dengesiyle', icon: 'shuffle' },
    { to: '/editor/submissions?disagreement=true', title: 'Karar bekleyenler', desc: 'Değerlendirmesi tamamlanan bildiriler', icon: 'doc' },
    { to: '/editor/program', title: 'Programı kur', desc: 'Çakışmasız oturum planı, sürükle-bırak', icon: 'grid' },
  ],
  registrar: [
    { to: '/registrar', title: 'Kayıtları izle', desc: 'Ödemeler canlı olarak düşer', icon: 'list' },
    { to: '/registrar/group', title: 'Grup kaydı aç', desc: '5+ kişide her üyeye %10 indirim', icon: 'users' },
    { to: '/registrar/invoices', title: 'Faturalar', desc: 'Boşluksuz numaralandırma', icon: 'receipt' },
  ],
  attendee: [
    { to: '/registration', title: 'Kongreye kaydol', desc: 'Erken kayıt fiyatı şu an geçerli', icon: 'card' },
    { to: '/program', title: 'Oturumları keşfet', desc: 'Beğendiklerini kendi programına ekle', icon: 'calendar' },
    { to: '/certificates', title: 'Sertifika al', desc: 'QR kodla doğrulanabilir PDF', icon: 'award' },
  ],
  admin: [
    { to: '/admin/audit', title: 'Denetim kaydı', desc: 'Kim, ne zaman, neyi değiştirdi', icon: 'shield' },
    { to: '/admin/users', title: 'Kullanıcılar', desc: 'Roller ve kurumlar', icon: 'users' },
    { to: '/reports', title: 'Raporlar', desc: 'Bildiri ve hakem istatistikleri', icon: 'chart' },
  ],
}

function ReviewerQueue() {
  const q = useQuery({ queryKey: ['my-assignments'], queryFn: () => api<Page<{ id: number; title: string; status: string; days_left: number }>>('/api/reviewer/assignments?status=assigned') })
  if (!q.data || q.data.items.length === 0) return null
  const urgent = [...q.data.items].sort((a, b) => a.days_left - b.days_left).slice(0, 3)
  return (
    <Card title={`Sizi bekleyen ${q.data.items.length} değerlendirme`}>
      <ul className="feed">
        {urgent.map((a) => (
          <li key={a.id}>
            <time style={{ color: a.days_left < 3 ? 'var(--tile)' : undefined }}>{a.days_left} gün</time>
            <Link to={`/reviewer/assignments/${a.id}`}>{a.title}</Link>
          </li>
        ))}
      </ul>
    </Card>
  )
}

export default function Dashboard() {
  const { user } = useAuth()
  const seen = useRef<number>(0)
  // canlı akış: simülatörün ürettiği olaylar birkaç saniyede bir görünür
  const q = useQuery({ queryKey: ['live'], queryFn: () => api<Live>('/api/live'), refetchInterval: 3000 })
  const hour = new Date().getHours()
  const greet = hour < 12 ? 'Günaydın' : hour < 18 ? 'İyi günler' : 'İyi akşamlar'
  const firstName = user!.full_name.replace(/^(Prof\.|Doç\.|Dr\.)\s*/g, '').replace(/^(Prof\.|Doç\.|Dr\.)\s*/g, '').split(' ')[0]
  return (
    <>
      <PageTitle sub="Kongreye 10 Mayıs 2027'de İstanbul'da başlıyoruz.">{greet}, {firstName}</PageTitle>

      <div className="next-grid">
        {nextSteps[user!.role].map((n) => (
          <Link key={n.to} to={n.to} className="next">
            <span className="next-icon"><Icon name={n.icon} /></span>
            <span>
              <strong>{n.title}</strong>
              <small>{n.desc}</small>
            </span>
          </Link>
        ))}
      </div>

      {user!.role === 'reviewer' && <ReviewerQueue />}

      <QueryState q={q} rows={6}>
        {(d) => {
          const prev = seen.current
          seen.current = d.recent_events[0]?.id ?? prev
          return (
            <div className="grid two">
              <Card title={<><Icon name="pulse" /> Kongre şu anda</>}>
                <div className="stats">
                  <Stat label="Bildiri" value={d.counts.submissions} />
                  <Stat label="Kayıt" value={d.counts.registrations} hint={`${d.counts.paid_registrations} ödenmiş`} />
                  <Stat label="Açık hakem ataması" value={d.counts.open_assignments} />
                  <Stat label="Değerlendirme" value={d.counts.reviews} />
                </div>
                <p className="hint" style={{ marginTop: 14 }}>
                  1 EUR = <strong>{d.fx.rate}</strong> TRY ({time(d.fx.observed_at)} itibarıyla). TRY ile kaydolanların kuru kayıt anında sabitlenir.
                </p>
              </Card>
              <Card title="Son hareketler" actions={<span className="hint">{d.events_per_minute} olay / dakika</span>}>
                {d.recent_events.length === 0 ? (
                  <p className="muted">Henüz hareket yok.</p>
                ) : (
                  <ul className="feed">
                    {d.recent_events.slice(0, 9).map((e) => (
                      <li key={e.id} className={prev && e.id > prev ? 'feed-new' : ''}>
                        <time>{time(e.at)}</time> {actionLabel[e.action] ?? e.action}
                      </li>
                    ))}
                  </ul>
                )}
              </Card>
            </div>
          )
        }}
      </QueryState>
    </>
  )
}
