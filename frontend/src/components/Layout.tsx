import { useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth'
import { roleLabel } from '../format'
import type { Role } from '../api'

const nav: Record<Role, { to: string; label: string }[]> = {
  author: [
    { to: '/', label: 'Pano' },
    { to: '/author/submissions', label: 'Bildirilerim' },
    { to: '/author/new', label: 'Yeni bildiri' },
    { to: '/registration', label: 'Kayıt & ödeme' },
    { to: '/program', label: 'Program' },
    { to: '/certificates', label: 'Sertifikalar' },
  ],
  reviewer: [
    { to: '/', label: 'Pano' },
    { to: '/reviewer', label: 'Değerlendirmelerim' },
    { to: '/registration', label: 'Kayıt & ödeme' },
    { to: '/program', label: 'Program' },
  ],
  editor: [
    { to: '/', label: 'Pano' },
    { to: '/editor/submissions', label: 'Bildiriler' },
    { to: '/editor/assignments', label: 'Hakem atama' },
    { to: '/editor/reviewers', label: 'Hakem havuzu' },
    { to: '/editor/program', label: 'Program' },
    { to: '/reports', label: 'Raporlar' },
  ],
  registrar: [
    { to: '/', label: 'Pano' },
    { to: '/registrar', label: 'Kayıtlar' },
    { to: '/registrar/group', label: 'Grup kaydı' },
    { to: '/registrar/invoices', label: 'Faturalar' },
    { to: '/reports/finance', label: 'Finans' },
  ],
  attendee: [
    { to: '/', label: 'Pano' },
    { to: '/registration', label: 'Kayıt & ödeme' },
    { to: '/program', label: 'Program' },
    { to: '/agenda', label: 'Programım' },
    { to: '/certificates', label: 'Sertifikalar' },
  ],
  admin: [
    { to: '/', label: 'Pano' },
    { to: '/admin/users', label: 'Kullanıcılar' },
    { to: '/admin/audit', label: 'Denetim kaydı' },
    { to: '/reports', label: 'Raporlar' },
    { to: '/reports/finance', label: 'Finans' },
  ],
}

export default function Layout() {
  const { user, logout } = useAuth()
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  if (!user) return null
  return (
    <div className="shell">
      <header className="topbar">
        <button className="menu-btn" aria-label="Menü" aria-expanded={open} onClick={() => setOpen(!open)}>
          ☰
        </button>
        <NavLink to="/" className="brand">
          <span className="logo">K</span> KongreHub
        </NavLink>
        <div className="who">
          <span className="who-name">{user.full_name}</span>
          <span className="role-chip">{roleLabel[user.role]}</span>
          <button
            className="btn ghost small"
            onClick={() => {
              logout()
              navigate('/login')
            }}
          >
            Çıkış
          </button>
        </div>
      </header>
      <div className="body">
        <nav className={`side ${open ? 'open' : ''}`} onClick={() => setOpen(false)}>
          {nav[user.role].map((n) => (
            <NavLink key={n.to} to={n.to} end={n.to === '/'}>
              {n.label}
            </NavLink>
          ))}
        </nav>
        <main className="main">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
