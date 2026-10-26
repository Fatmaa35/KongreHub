import { useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api, type Page, type Role } from '../api'
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

interface Next { to: string; title: string; desc: string }

const nextSteps: Record<Role, Next[]> = {
  author: [
    { to: '/author/submissions', title: 'Bildirilerimi izle', desc: 'Durum, hakem yorumları ve kararlar' },
    { to: '/author/new', title: 'Yeni bildiri gönder', desc: 'PDF yükleyin, yazar bilgisi otomatik gizlenir' },
    { to: '/registration', title: 'Kaydımı tamamla', desc: 'Programa alınmak için en az bir yazar ödemeli' },
  ],
  reviewer: [
    { to: '/reviewer', title: 'Bekleyen değerlendirmeler', desc: 'Yanıt süresi atamadan itibaren 14 gün' },
    { to: '/program', title: 'Kongre programı', desc: 'Oturumlar ve salonlar' },
  ],
  editor: [
    { to: '/editor/assignments', title: 'Hakemleri ata', desc: 'Uzmanlık, çıkar çatışması ve yük dengesiyle' },
    { to: '/editor/submissions?disagreement=true', title: 'Karar bekleyenler', desc: 'Değerlendirmesi tamamlanan bildiriler' },
    { to: '/editor/program', title: 'Programı kur', desc: 'Çakışmasız oturum planı, sürükle-bırak' },
  ],
  registrar: [
    { to: '/registrar', title: 'Kayıtları izle', desc: 'Ödemeler canlı olarak düşer' },
    { to: '/registrar/group', title: 'Grup kaydı aç', desc: '5+ kişide her üyeye %10 indirim' },
    { to: '/registrar/invoices', title: 'Faturalar', desc: 'Boşluksuz numaralandırma' },
  ],
  attendee: [
    { to: '/registration', title: 'Kongreye kaydol', desc: 'Erken kayıt fiyatı şu an geçerli' },
    { to: '/program', title: 'Oturumları keşfet', desc: 'Beğendiklerini kendi programına ekle' },
    { to: '/certificates', title: 'Sertifika al', desc: 'QR kodla doğrulanabilir PDF' },
  ],
  admin: [
    { to: '/admin/audit', title: 'Denetim kaydı', desc: 'Kim, ne zaman, neyi değiştirdi' },
    { to: '/admin/users', title: 'Kullanıcılar', desc: 'Roller ve kurumlar' },
    { to: '/reports', title: 'Raporlar', desc: 'Bildiri ve hakem istatistikleri' },
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

const milestones = [
  { date: new Date('2026-09-01'), label: 'Çağrı açıldı', when: '1 Eylül 2026' },
  { date: new Date('2027-01-15'), label: 'Bildiri son tarihi', when: '15 Ocak 2027' },
  { date: new Date('2027-03-01'), label: 'Hakem kararları', when: '1 Mart 2027' },
  { date: new Date('2027-05-10'), label: 'Kongre', when: '10–12 Mayıs, İstanbul' },
]

function Hero() {
  const now = new Date()
  const days = Math.max(0, Math.ceil((milestones[3].date.getTime() - now.getTime()) / 86400000))
  const nextIdx = milestones.findIndex((m) => m.date > now)
  return (
    <section className="hero" aria-label="Kongre takvimi">
      <div className="countdown">
        <b>{days}</b>
        <span>gün kaldı</span>
      </div>
      <ol className="milestones">
        {milestones.map((m, i) => (
          <li key={m.label} className={m.date <= now ? 'past' : i === nextIdx ? 'next' : ''}>
            <b>{m.label}</b>
            <span>{m.when}</span>
          </li>
        ))}
      </ol>
    </section>
  )
}

export default function Dashboard() {
  const { user } = useAuth()
  const seen = useRef<number>(0)
  // canlı akış: simülatörün ürettiği olaylar birkaç saniyede bir görünür
  const q = useQuery({ queryKey: ['live'], queryFn: () => api<Live>('/api/live'), refetchInterval: 3000 })
  return (
    <>
      <PageTitle sub={`${roleLabel[user!.role]} · ${user!.institution ?? ''}`}>Genel bakış</PageTitle>
      <Hero />

      <Card title="Yapılacaklar">
        <ul className="todo">
          {nextSteps[user!.role].map((n) => (
            <li key={n.to}>
              <Link to={n.to}>
                <strong>{n.title}</strong>
                <small>{n.desc}</small>
              </Link>
            </li>
          ))}
        </ul>
      </Card>

      {user!.role === 'reviewer' && <ReviewerQueue />}

      <QueryState q={q} rows={6}>
        {(d) => {
          const prev = seen.current
          seen.current = d.recent_events[0]?.id ?? prev
          return (
            <div className="grid two">
              <Card title="Güncel durum">
                <div className="stats">
                  <Stat label="Bildiri" value={d.counts.submissions} />
                  <Stat label="Kayıt" value={d.counts.registrations} hint={`${d.counts.paid_registrations} ödenmiş`} />
                  <Stat label="Açık hakem ataması" value={d.counts.open_assignments} />
                  <Stat label="Değerlendirme" value={d.counts.reviews} />
                </div>
                <p className="fx">
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
