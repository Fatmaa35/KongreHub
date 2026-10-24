export function money(cents: number, currency: string) {
  return new Intl.NumberFormat('tr-TR', { style: 'currency', currency }).format(cents / 100)
}

export function dt(iso?: string | null, withTime = true) {
  if (!iso) return '—'
  const d = new Date(iso)
  return d.toLocaleString('tr-TR', withTime ? { dateStyle: 'medium', timeStyle: 'short', timeZone: 'Europe/Istanbul' } : { dateStyle: 'medium', timeZone: 'Europe/Istanbul' })
}

export function time(iso: string) {
  return new Date(iso).toLocaleTimeString('tr-TR', { hour: '2-digit', minute: '2-digit', timeZone: 'Europe/Istanbul' })
}

export const statusLabel: Record<string, string> = {
  submitted: 'Gönderildi',
  under_review: 'İncelemede',
  reviews_complete: 'Değerlendirmeler tamam',
  revision_requested: 'Düzeltme istendi',
  accepted: 'Kabul',
  rejected: 'Ret',
  withdrawn: 'Geri çekildi',
  assigned: 'Bekliyor',
  pending: 'Ödeme bekliyor',
  paid: 'Ödendi',
  cancelled: 'İptal',
  refunded: 'İade edildi',
  expired: 'Süresi doldu',
  declined: 'Reddedildi',
  released: 'Geri alındı',
}

export const recLabel: Record<string, string> = {
  accept: 'Kabul',
  minor_revision: 'Küçük düzeltme',
  major_revision: 'Büyük düzeltme',
  reject: 'Ret',
}

export const roleLabel: Record<string, string> = {
  author: 'Yazar',
  reviewer: 'Hakem',
  editor: 'Editör / Program Komitesi',
  registrar: 'Kayıt Görevlisi',
  attendee: 'Katılımcı',
  admin: 'Yönetim',
}
