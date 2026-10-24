// Bildirim (toast) ve onay penceresi: tarayıcının alert/confirm'i yerine.
import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { Icon } from './icons'

type Kind = 'ok' | 'err'
interface Toast { id: number; kind: Kind; text: string }
interface ConfirmReq { title: string; body?: string; action: string; danger?: boolean; resolve: (v: boolean) => void }

interface Ctx {
  toast: (text: string, kind?: Kind) => void
  confirm: (o: Omit<ConfirmReq, 'resolve'>) => Promise<boolean>
}

const FeedbackCtx = createContext<Ctx>({ toast: () => {}, confirm: async () => false })

export function FeedbackProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const [req, setReq] = useState<ConfirmReq | null>(null)
  const seq = useRef(0)
  const toast = useCallback((text: string, kind: Kind = 'ok') => {
    const id = ++seq.current
    setToasts((t) => [...t, { id, kind, text }])
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 4200)
  }, [])
  const confirm = useCallback((o: Omit<ConfirmReq, 'resolve'>) => new Promise<boolean>((resolve) => setReq({ ...o, resolve })), [])
  const close = (v: boolean) => {
    req?.resolve(v)
    setReq(null)
  }
  return (
    <FeedbackCtx.Provider value={{ toast, confirm }}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {toasts.map((t) => (
          <div key={t.id} className={`toast ${t.kind}`}>
            <Icon name={t.kind === 'ok' ? 'check' : 'alert'} />
            <span>{t.text}</span>
          </div>
        ))}
      </div>
      {req && <ConfirmDialog req={req} onClose={close} />}
    </FeedbackCtx.Provider>
  )
}

function ConfirmDialog({ req, onClose }: { req: ConfirmReq; onClose: (v: boolean) => void }) {
  const ok = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    ok.current?.focus()
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && onClose(false)
    window.addEventListener('keydown', esc)
    return () => window.removeEventListener('keydown', esc)
  }, [onClose])
  return (
    <div className="scrim" onClick={() => onClose(false)}>
      <div className="dialog" role="alertdialog" aria-modal aria-labelledby="dlg-t" onClick={(e) => e.stopPropagation()}>
        <h2 id="dlg-t">{req.title}</h2>
        {req.body && <p>{req.body}</p>}
        <div className="dialog-actions">
          <button className="btn quiet" onClick={() => onClose(false)}>Vazgeç</button>
          <button ref={ok} className={`btn ${req.danger ? 'danger' : ''}`} onClick={() => onClose(true)}>{req.action}</button>
        </div>
      </div>
    </div>
  )
}

export const useFeedback = () => useContext(FeedbackCtx)
