import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import { api, loadSession, saveSession, type User } from './api'

interface AuthCtx {
  user: User | null
  login: (email: string, password: string) => Promise<User>
  logout: () => void
}

const Ctx = createContext<AuthCtx>({ user: null, login: async () => Promise.reject(), logout: () => {} })

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(() => loadSession()?.user ?? null)
  useEffect(() => {
    const on = () => setUser(loadSession()?.user ?? null)
    window.addEventListener('kongrehub-session', on)
    return () => window.removeEventListener('kongrehub-session', on)
  }, [])
  const login = useCallback(async (email: string, password: string) => {
    const s = await api<{ access_token: string; refresh_token: string; user: User }>('/api/auth/login', { body: { email, password } })
    saveSession(s)
    return s.user
  }, [])
  const logout = useCallback(() => {
    const s = loadSession()
    if (s) void api('/api/auth/logout', { body: { refresh_token: s.refresh_token } }).catch(() => {})
    saveSession(null)
  }, [])
  return <Ctx.Provider value={{ user, login, logout }}>{children}</Ctx.Provider>
}

export const useAuth = () => useContext(Ctx)
