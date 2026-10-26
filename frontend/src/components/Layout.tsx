import { useEffect, useState } from 'react'
import { Link, NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth'
import type { Role } from '../api'
import { roleLabel } from '../format'
import { Icon } from './icons'

type NavItem = { to: string; label: string }

const nav: Record<Role, NavItem[]> = {
  author: [
    { to: '/', label: 'Genel bakış' },
    { to: '/author/submissions', label: 'Bildirilerim' },
    { to: '/author/new', label: 'Bildiri gönder' },
    { to: '/registration', label: 'Kayıt ve ödeme' },
    { to: '/program', label: 'Program' },
    { to: '/certificates', label: 'Sertifikalar' },
  ],
  reviewer: [
    { to: '/', label: 'Genel bakış' },
    { to: '/reviewer', label: 'Değerlendirmelerim' },
    { to: '/registration', label: 'Kayıt ve ödeme' },
    { to: '/program', label: 'Program' },
  ],
  editor: [
    { to: '/', label: 'Genel bakış' },
    { to: '/editor/submissions', label: 'Bildiriler' },
    { to: '/editor/assignments', label: 'Hakem atama' },
    { to: '/editor/reviewers', label: 'Hakem havuzu' },
    { to: '/editor/program', label: 'Program planı' },
    { to: '/reports', label: 'Raporlar' },
  ],
  registrar: [
    { to: '/', label: 'Genel bakış' },
    { to: '/registrar', label: 'Kayıtlar' },
    { to: '/registrar/group', label: 'Grup kaydı' },
    { to: '/registrar/invoices', label: 'Faturalar' },
    { to: '/reports/finance', label: 'Finans' },
  ],
  attendee: [
    { to: '/', label: 'Genel bakış' },
    { to: '/registration', label: 'Kayıt ve ödeme' },
    { to: '/program', label: 'Program' },
    { to: '/agenda', label: 'Kendi programım' },
    { to: '/certificates', label: 'Sertifikalar' },
  ],
  admin: [
    { to: '/', label: 'Genel bakış' },
    { to: '/admin/users', label: 'Kullanıcılar' },
    { to: '/admin/audit', label: 'Denetim kaydı' },
    { to: '/reports', label: 'Raporlar' },
    { to: '/reports/finance', label: 'Finans' },
  ],
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
      <header className="topbar">
        <div className="topbar-in">
          <button className="icon-btn menu-btn" aria-label="Menü" aria-expanded={open} onClick={() => setOpen(!open)}>
            <Icon name={open ? 'x' : 'menu'} />
          </button>
          <Link to="/" className="wordmark">KongreHub</Link>
          <span className="event">10–12 Mayıs 2027 · İstanbul</span>
          <div className="who">
            <div className="who-name">
              <strong title={user.institution ?? undefined}>{user.full_name}</strong>
              <span>{roleLabel[user.role]}</span>
            </div>
            <button
              className="logout"
              onClick={() => {
                logout()
                navigate('/login')
              }}
            >
              Çıkış
            </button>
          </div>
        </div>
      </header>
      <nav className={`tabs ${open ? 'open' : ''}`} aria-label="Ana menü">
        <div className="tabs-in">
          {nav[user.role].map((n) => (
            <NavLink key={n.to} to={n.to} end={n.to === '/'}>
              {n.label}
            </NavLink>
          ))}
        </div>
      </nav>
      <main className="main" id="main">
        <Outlet />
      </main>
    </div>
  )
}
