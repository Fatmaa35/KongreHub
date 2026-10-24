import { useEffect, useState } from 'react'
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { useAuth } from '../auth'
import { api, type Role } from '../api'
import { roleLabel } from '../format'
import { Icon, type IconName } from './icons'

type NavItem = { to: string; label: string; icon: IconName }

const nav: Record<Role, NavItem[]> = {
  author: [
    { to: '/', label: 'Genel bakış', icon: 'home' },
    { to: '/author/submissions', label: 'Bildirilerim', icon: 'doc' },
    { to: '/author/new', label: 'Bildiri gönder', icon: 'plus' },
    { to: '/registration', label: 'Kayıt ve ödeme', icon: 'card' },
    { to: '/program', label: 'Kongre programı', icon: 'calendar' },
    { to: '/certificates', label: 'Sertifikalarım', icon: 'award' },
  ],
  reviewer: [
    { to: '/', label: 'Genel bakış', icon: 'home' },
    { to: '/reviewer', label: 'Değerlendirmelerim', icon: 'check' },
    { to: '/registration', label: 'Kayıt ve ödeme', icon: 'card' },
    { to: '/program', label: 'Kongre programı', icon: 'calendar' },
  ],
  editor: [
    { to: '/', label: 'Genel bakış', icon: 'home' },
    { to: '/editor/submissions', label: 'Bildiriler', icon: 'doc' },
    { to: '/editor/assignments', label: 'Hakem atama', icon: 'shuffle' },
    { to: '/editor/reviewers', label: 'Hakem havuzu', icon: 'users' },
    { to: '/editor/program', label: 'Program planı', icon: 'grid' },
    { to: '/reports', label: 'Raporlar', icon: 'chart' },
  ],
  registrar: [
    { to: '/', label: 'Genel bakış', icon: 'home' },
    { to: '/registrar', label: 'Kayıtlar', icon: 'list' },
    { to: '/registrar/group', label: 'Grup kaydı', icon: 'users' },
    { to: '/registrar/invoices', label: 'Faturalar', icon: 'receipt' },
    { to: '/reports/finance', label: 'Finans', icon: 'chart' },
  ],
  attendee: [
    { to: '/', label: 'Genel bakış', icon: 'home' },
    { to: '/registration', label: 'Kayıt ve ödeme', icon: 'card' },
    { to: '/program', label: 'Kongre programı', icon: 'calendar' },
    { to: '/agenda', label: 'Kendi programım', icon: 'list' },
    { to: '/certificates', label: 'Sertifikalarım', icon: 'award' },
  ],
  admin: [
    { to: '/', label: 'Genel bakış', icon: 'home' },
    { to: '/admin/users', label: 'Kullanıcılar', icon: 'users' },
    { to: '/admin/audit', label: 'Denetim kaydı', icon: 'shield' },
    { to: '/reports', label: 'Raporlar', icon: 'chart' },
    { to: '/reports/finance', label: 'Finans', icon: 'chart' },
  ],
}

function initials(name: string) {
  return name
    .replace(/^(Prof\.|Doç\.|Dr\.)\s*/g, '')
    .replace(/^(Prof\.|Doç\.|Dr\.)\s*/g, '')
    .split(/\s+/)
    .slice(0, 2)
    .map((p) => p[0])
    .join('')
    .toLocaleUpperCase('tr')
}

/** Kongre yaka kartı: kim olduğunuzu ve hangi rolle girdiğinizi tek bakışta gösterir. */
function Badge() {
  const { user } = useAuth()
  const live = useQuery({ queryKey: ['live'], queryFn: () => api<{ events_per_minute: number }>('/api/live'), refetchInterval: 5000 })
  if (!user) return null
  return (
    <div className={`badge-card role-${user.role}`}>
      <div className="badge-strap" aria-hidden />
      <div className="badge-top">
        <span className="badge-event">KongreHub 2027 · İstanbul</span>
      </div>
      <div className="badge-body">
        <span className="avatar" aria-hidden>{initials(user.full_name)}</span>
        <div className="badge-who">
          <strong>{user.full_name}</strong>
          {user.institution && <span>{user.institution}</span>}
        </div>
      </div>
      <div className="badge-role">
        <span>{roleLabel[user.role]}</span>
        <span className="live" title="Sistem canlı">
          <span className="live-dot" />
          {live.data ? `${live.data.events_per_minute}/dk` : 'canlı'}
        </span>
      </div>
    </div>
  )
}

export default function Layout() {
  const { user, logout } = useAuth()
  const navigate = useNavigate()
  const loc = useLocation()
  const [open, setOpen] = useState(false)
  useEffect(() => setOpen(false), [loc.pathname])
  if (!user) return null
  return (
    <div className="shell">
      <header className="mobilebar">
        <button className="icon-btn" aria-label="Menüyü aç" aria-expanded={open} onClick={() => setOpen(!open)}>
          <Icon name={open ? 'x' : 'menu'} />
        </button>
        <span className="wordmark">KongreHub</span>
      </header>
      <aside className={`side ${open ? 'open' : ''}`}>
        <div className="wordmark side-mark">KongreHub</div>
        <Badge />
        <nav aria-label="Ana menü">
          {nav[user.role].map((n) => (
            <NavLink key={n.to} to={n.to} end={n.to === '/'}>
              <Icon name={n.icon} />
              {n.label}
            </NavLink>
          ))}
        </nav>
        <button
          className="logout"
          onClick={() => {
            logout()
            navigate('/login')
          }}
        >
          <Icon name="logout" /> Çıkış yap
        </button>
      </aside>
      {open && <div className="side-scrim" onClick={() => setOpen(false)} />}
      <main className="main" id="main">
        <Outlet />
      </main>
    </div>
  )
}
