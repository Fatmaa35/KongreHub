import { render, screen } from '@testing-library/react'
import { Badge, Empty, ErrorBox } from './ui'
import { ApiError } from '../api'
import { money } from '../format'

describe('UI durumları', () => {
  it('hata kutusu sunucu mesajını ve kodunu gösterir', () => {
    render(<ErrorBox error={new ApiError(409, 'conflict_of_interest', 'çıkar çatışması: aynı kurum')} />)
    expect(screen.getByRole('alert')).toHaveTextContent('çıkar çatışması: aynı kurum')
    expect(screen.getByRole('alert')).toHaveTextContent('conflict_of_interest')
  })
  it('boş durum metni', () => {
    render(<Empty>Kayıt yok</Empty>)
    expect(screen.getByText('Kayıt yok')).toBeInTheDocument()
  })
  it('durum rozetleri Türkçe', () => {
    render(<Badge status="under_review" />)
    expect(screen.getByText('İncelemede')).toBeInTheDocument()
  })
  it('para biçimi kuruş → TL', () => {
    expect(money(866963, 'TRY')).toContain('8.669,63')
  })
})
