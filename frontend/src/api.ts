// İnce API istemcisi: JWT ekler, 401'de refresh token ile bir kez yeniler,
// sunucunun {"error":{code,message}} biçimini ApiError'a çevirir.
export class ApiError extends Error {
  status: number
  code: string
  details?: Record<string, unknown>
  constructor(status: number, code: string, message: string, details?: Record<string, unknown>) {
    super(message)
    this.status = status
    this.code = code
    this.details = details
  }
}

export type Role = 'author' | 'reviewer' | 'editor' | 'registrar' | 'attendee' | 'admin'

export interface User {
  id: number
  email: string
  full_name: string
  role: Role
  institution?: string
  timezone: string
  is_student: boolean
  is_member: boolean
}

interface Session {
  access_token: string
  refresh_token: string
  user: User
}

const KEY = 'kongrehub.session'

export function loadSession(): Session | null {
  try {
    const raw = localStorage.getItem(KEY)
    return raw ? (JSON.parse(raw) as Session) : null
  } catch {
    return null
  }
}

export function saveSession(s: Session | null) {
  try {
    if (s) localStorage.setItem(KEY, JSON.stringify(s))
    else localStorage.removeItem(KEY)
  } catch {
    /* depolama kapalı olabilir */
  }
  window.dispatchEvent(new Event('kongrehub-session'))
}

let refreshing: Promise<boolean> | null = null

async function refresh(): Promise<boolean> {
  const s = loadSession()
  if (!s) return false
  const res = await fetch('/api/auth/refresh', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ refresh_token: s.refresh_token }),
  })
  if (!res.ok) {
    saveSession(null)
    return false
  }
  saveSession((await res.json()) as Session)
  return true
}

export function newIdempotencyKey(): string {
  return `ui-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
}

interface Options {
  method?: string
  body?: unknown
  idempotent?: boolean | string
  raw?: boolean
}

export async function api<T = unknown>(path: string, opts: Options = {}, retried = false): Promise<T> {
  const headers: Record<string, string> = {}
  const s = loadSession()
  if (s) headers.Authorization = `Bearer ${s.access_token}`
  let body: BodyInit | undefined
  if (opts.body instanceof FormData) body = opts.body
  else if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(opts.body)
  }
  if (opts.idempotent) headers['Idempotency-Key'] = typeof opts.idempotent === 'string' ? opts.idempotent : newIdempotencyKey()
  let res: Response
  try {
    res = await fetch(path, { method: opts.method ?? (body ? 'POST' : 'GET'), headers, body })
  } catch {
    throw new ApiError(0, 'network', 'Sunucuya ulaşılamıyor. Bağlantınızı kontrol edin.')
  }
  if (res.status === 401 && s && !retried && !path.startsWith('/api/auth/')) {
    refreshing ??= refresh().finally(() => (refreshing = null))
    if (await refreshing) return api<T>(path, opts, true)
  }
  if (!res.ok) {
    let code = 'http_' + res.status
    let message = `İstek başarısız (${res.status})`
    let details: Record<string, unknown> | undefined
    try {
      const j = await res.json()
      code = j.error?.code ?? code
      message = j.error?.message ?? message
      details = j.error?.details
    } catch {
      /* gövde JSON değil */
    }
    throw new ApiError(res.status, code, message, details)
  }
  if (opts.raw) return res as unknown as T
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

/** Yetkili indirme: blob alıp tarayıcıda kaydettirir. */
export async function download(path: string, filename: string) {
  const res = await api<Response>(path, { raw: true })
  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 2000)
}

export interface Page<T> {
  items: T[]
  next_cursor?: string
}
