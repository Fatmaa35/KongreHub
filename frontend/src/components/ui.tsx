import type { ReactNode } from 'react'
import type { UseQueryResult } from '@tanstack/react-query'
import { ApiError } from '../api'
import { statusLabel } from '../format'

export function Spinner({ label = 'Yükleniyor…' }: { label?: string }) {
  return (
    <div className="state" role="status" aria-live="polite">
      <span className="spinner" aria-hidden /> {label}
    </div>
  )
}

export function ErrorBox({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const msg = error instanceof ApiError ? error.message : 'Beklenmeyen bir hata oluştu.'
  const code = error instanceof ApiError ? error.code : ''
  return (
    <div className="state error" role="alert">
      <strong>Bir sorun oluştu.</strong> {msg} {code && <code>{code}</code>}
      {onRetry && (
        <button className="btn small" onClick={onRetry}>
          Tekrar dene
        </button>
      )}
    </div>
  )
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="state empty">{children}</div>
}

/** Her ekranda yükleniyor / hata / boş durumlarını tek yerden ele alır. */
export function QueryState<T>({
  q,
  empty,
  isEmpty,
  children,
}: {
  q: UseQueryResult<T>
  empty?: ReactNode
  isEmpty?: (d: T) => boolean
  children: (d: T) => ReactNode
}) {
  if (q.isPending) return <Spinner />
  if (q.isError) return <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
  if (isEmpty && isEmpty(q.data)) return <Empty>{empty ?? 'Gösterilecek kayıt yok.'}</Empty>
  return <>{children(q.data)}</>
}

export function Badge({ status }: { status: string }) {
  return <span className={`badge s-${status}`}>{statusLabel[status] ?? status}</span>
}

export function Card({ title, actions, children }: { title?: ReactNode; actions?: ReactNode; children: ReactNode }) {
  return (
    <section className="card">
      {(title || actions) && (
        <header className="card-h">
          {title && <h2>{title}</h2>}
          {actions && <div className="actions">{actions}</div>}
        </header>
      )}
      {children}
    </section>
  )
}

export function PageTitle({ children, sub }: { children: ReactNode; sub?: ReactNode }) {
  return (
    <div className="page-title">
      <h1>{children}</h1>
      {sub && <p className="muted">{sub}</p>}
    </div>
  )
}

export function Notice({ kind = 'ok', children }: { kind?: 'ok' | 'warn' | 'err'; children: ReactNode }) {
  return <div className={`notice ${kind}`}>{children}</div>
}

export function Stat({ label, value, hint }: { label: string; value: ReactNode; hint?: string }) {
  return (
    <div className="stat">
      <div className="stat-v">{value}</div>
      <div className="stat-l">{label}</div>
      {hint && <div className="stat-h">{hint}</div>}
    </div>
  )
}
