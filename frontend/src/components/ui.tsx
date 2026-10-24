import type { ReactNode } from 'react'
import type { UseQueryResult } from '@tanstack/react-query'
import { ApiError } from '../api'
import { statusLabel } from '../format'
import { Icon } from './icons'

export function Spinner({ label = 'Yükleniyor…' }: { label?: string }) {
  return (
    <div className="state" role="status" aria-live="polite">
      <span className="spinner" aria-hidden /> {label}
    </div>
  )
}

/** İçeriğin şeklini önceden gösteren yükleme iskeleti. */
export function Skeleton({ rows = 4 }: { rows?: number }) {
  return (
    <div className="skeleton" role="status" aria-label="Yükleniyor">
      {Array.from({ length: rows }, (_, i) => (
        <span key={i} style={{ width: `${92 - ((i * 17) % 35)}%` }} />
      ))}
    </div>
  )
}

export function ErrorBox({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const msg = error instanceof ApiError ? error.message : 'Beklenmeyen bir hata oluştu.'
  const code = error instanceof ApiError ? error.code : ''
  return (
    <div className="state error" role="alert">
      <Icon name="alert" />
      <div>
        <strong>{msg}</strong>
        {code && <div className="hint">Hata kodu: <code>{code}</code></div>}
      </div>
      {onRetry && (
        <button className="btn quiet small" onClick={onRetry}>
          Tekrar dene
        </button>
      )}
    </div>
  )
}

export function Empty({ children, action }: { children: ReactNode; action?: ReactNode }) {
  return (
    <div className="state empty">
      <Icon name="inbox" size={28} />
      <div>{children}</div>
      {action}
    </div>
  )
}

/** Her ekranda yükleniyor / hata / boş durumlarını tek yerden ele alır. */
export function QueryState<T>({
  q,
  empty,
  emptyAction,
  isEmpty,
  rows,
  children,
}: {
  q: UseQueryResult<T>
  empty?: ReactNode
  emptyAction?: ReactNode
  isEmpty?: (d: T) => boolean
  rows?: number
  children: (d: T) => ReactNode
}) {
  if (q.isPending) return <Skeleton rows={rows} />
  if (q.isError) return <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
  if (isEmpty && isEmpty(q.data)) return <Empty action={emptyAction}>{empty ?? 'Gösterilecek kayıt yok.'}</Empty>
  return <>{children(q.data)}</>
}

export function Badge({ status }: { status: string }) {
  return <span className={`pill s-${status}`}>{statusLabel[status] ?? status}</span>
}

export function Card({ title, actions, children, tone }: { title?: ReactNode; actions?: ReactNode; children: ReactNode; tone?: 'plain' | 'feature' }) {
  return (
    <section className={`card ${tone === 'feature' ? 'feature' : ''}`}>
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

export function PageTitle({ children, sub, actions }: { children: ReactNode; sub?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="page-title">
      <div>
        <h1>{children}</h1>
        {sub && <p className="lede">{sub}</p>}
      </div>
      {actions && <div className="actions">{actions}</div>}
    </div>
  )
}

export function Notice({ kind = 'ok', children }: { kind?: 'ok' | 'warn' | 'err'; children: ReactNode }) {
  return (
    <div className={`notice ${kind}`}>
      <Icon name={kind === 'ok' ? 'check' : 'alert'} />
      <div>{children}</div>
    </div>
  )
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

/** Gerçek bir sıra olan süreçler için adım göstergesi (bildiri yaşam döngüsü). */
export function Steps({ steps, current, failed }: { steps: string[]; current: number; failed?: boolean }) {
  return (
    <ol className="steps">
      {steps.map((s, i) => (
        <li key={s} className={i < current ? 'done' : i === current ? (failed ? 'failed' : 'now') : ''} aria-current={i === current ? 'step' : undefined}>
          <span className="step-dot">{i < current ? <Icon name="check" size={14} /> : i + 1}</span>
          <span className="step-l">{s}</span>
        </li>
      ))}
    </ol>
  )
}
