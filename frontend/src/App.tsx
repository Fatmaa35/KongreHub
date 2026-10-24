import type { ReactNode } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { useAuth } from './auth'
import type { Role } from './api'
import Layout from './components/Layout'
import Login from './pages/Login'
import Dashboard from './pages/Dashboard'
import VerifyCertificate from './pages/VerifyCertificate'
import { MySubmissions, NewSubmission } from './pages/Author'
import SubmissionDetail from './pages/SubmissionDetail'
import { ReviewerAssignments, ReviewForm } from './pages/Reviewer'
import { EditorSubmissions, Assignments, ReviewerPool } from './pages/Editor'
import ProgramEditor from './pages/ProgramEditor'
import { Program, Agenda } from './pages/Program'
import Registration from './pages/Registration'
import Certificates from './pages/Certificates'
import { Registrations, GroupRegistration, Invoices } from './pages/Registrar'
import { Reports, FinanceReport } from './pages/Reports'
import { Users, AuditLog } from './pages/Admin'

function Guard({ roles, children }: { roles?: Role[]; children: ReactNode }) {
  const { user } = useAuth()
  const loc = useLocation()
  if (!user) return <Navigate to="/login" replace state={{ from: loc.pathname }} />
  if (roles && !roles.includes(user.role))
    return (
      <div className="state error" role="alert">
        Bu sayfaya erişim yetkiniz yok (403).
      </div>
    )
  return <>{children}</>
}

const SELF: Role[] = ['author', 'reviewer', 'editor', 'attendee']

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/verify/:id" element={<VerifyCertificate />} />
      <Route
        element={
          <Guard>
            <Layout />
          </Guard>
        }
      >
        <Route index element={<Dashboard />} />
        <Route path="author/submissions" element={<Guard roles={['author']}><MySubmissions /></Guard>} />
        <Route path="author/new" element={<Guard roles={['author']}><NewSubmission /></Guard>} />
        <Route path="submissions/:id" element={<Guard roles={['author', 'reviewer', 'editor', 'admin']}><SubmissionDetail /></Guard>} />
        <Route path="reviewer" element={<Guard roles={['reviewer']}><ReviewerAssignments /></Guard>} />
        <Route path="reviewer/assignments/:id" element={<Guard roles={['reviewer']}><ReviewForm /></Guard>} />
        <Route path="editor/submissions" element={<Guard roles={['editor', 'admin']}><EditorSubmissions /></Guard>} />
        <Route path="editor/assignments" element={<Guard roles={['editor', 'admin']}><Assignments /></Guard>} />
        <Route path="editor/reviewers" element={<Guard roles={['editor', 'admin']}><ReviewerPool /></Guard>} />
        <Route path="editor/program" element={<Guard roles={['editor', 'admin']}><ProgramEditor /></Guard>} />
        <Route path="program" element={<Program />} />
        <Route path="agenda" element={<Agenda />} />
        <Route path="registration" element={<Guard roles={SELF}><Registration /></Guard>} />
        <Route path="certificates" element={<Guard roles={SELF}><Certificates /></Guard>} />
        <Route path="registrar" element={<Guard roles={['registrar', 'admin']}><Registrations /></Guard>} />
        <Route path="registrar/group" element={<Guard roles={['registrar']}><GroupRegistration /></Guard>} />
        <Route path="registrar/invoices" element={<Guard roles={['registrar', 'admin']}><Invoices /></Guard>} />
        <Route path="reports" element={<Guard roles={['editor', 'admin']}><Reports /></Guard>} />
        <Route path="reports/finance" element={<Guard roles={['registrar', 'admin']}><FinanceReport /></Guard>} />
        <Route path="admin/users" element={<Guard roles={['admin']}><Users /></Guard>} />
        <Route path="admin/audit" element={<Guard roles={['admin']}><AuditLog /></Guard>} />
        <Route path="*" element={<div className="state empty">Sayfa bulunamadı.</div>} />
      </Route>
    </Routes>
  )
}
