// Package pricing implements registration fees, discounts, FX freezing and
// refund rules. Tüm tutarlar tam sayı (cent/kuruş); kur 1e-6 hassasiyetli
// tam sayı olarak tutulur (float kullanılmaz).
package pricing

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	_ "time/tzdata" // konteynerde tz veritabanı olmasa da çalışsın
)

// Istanbul is the conference time zone.
var Istanbul = mustLoad("Europe/Istanbul")

func mustLoad(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

// Calendar holds the dates that drive tiers and refunds.
type Calendar struct {
	EarlyUntil      time.Time // bu ana kadar (dahil değil) erken kayıt
	NormalUntil     time.Time // bu ana kadar normal, sonrası geç
	ConferenceStart time.Time
}

// DefaultCalendar is the KongreHub 2027 calendar (İstanbul saati).
func DefaultCalendar() Calendar {
	return Calendar{
		EarlyUntil:      time.Date(2027, 2, 1, 0, 0, 0, 0, Istanbul),
		NormalUntil:     time.Date(2027, 4, 16, 0, 0, 0, 0, Istanbul),
		ConferenceStart: time.Date(2027, 5, 10, 9, 0, 0, 0, Istanbul),
	}
}

type Tier string

const (
	Early  Tier = "early"
	Normal Tier = "normal"
	Late   Tier = "late"
)

// TierAt returns the fee tier for a registration created at t.
func (c Calendar) TierAt(t time.Time) Tier {
	switch {
	case t.Before(c.EarlyUntil):
		return Early
	case t.Before(c.NormalUntil):
		return Normal
	default:
		return Late
	}
}

// BaseFeesEURCents per tier (regular attendee).
var BaseFeesEURCents = map[Tier]int64{Early: 30000, Normal: 40000, Late: 50000}

// Discount percentages, applied sequentially on the remaining amount.
const (
	StudentPct    = 50
	MemberPct     = 20
	GroupPct      = 10
	GroupMinSize  = 5
	QuoteValidity = 24 * time.Hour // dondurulmuş kurun geçerlilik süresi
)

// Rate is EUR→TRY with 6 decimal places stored as integer micros.
type Rate int64

// ParseRate parses "36.512345" into micros without floating point.
func ParseRate(s string) (Rate, error) {
	r, ok := new(big.Rat).SetString(strings.TrimSpace(s))
	if !ok || r.Sign() <= 0 {
		return 0, fmt.Errorf("geçersiz kur: %q", s)
	}
	r.Mul(r, big.NewRat(1_000_000, 1))
	n := new(big.Int).Quo(r.Num(), r.Denom())
	return Rate(n.Int64()), nil
}

func (r Rate) String() string {
	return fmt.Sprintf("%d.%06d", int64(r)/1_000_000, int64(r)%1_000_000)
}

// roundDiv divides with half-up rounding for non-negative operands.
func roundDiv(a, b int64) int64 { return (a + b/2) / b }

// ConvertEURCents converts EUR cents to the target currency's minor units.
func ConvertEURCents(eurCents int64, currency string, rate Rate) int64 {
	if currency == "EUR" {
		return eurCents
	}
	return roundDiv(eurCents*int64(rate), 1_000_000)
}

// QuoteInput describes who registers.
type QuoteInput struct {
	At        time.Time
	Student   bool
	Member    bool
	GroupSize int    // 0/1 = bireysel
	Currency  string // EUR | TRY
	Rate      Rate   // currency TRY ise zorunlu
}

// Line is one breakdown row in the chosen currency.
type Line struct {
	Code   string `json:"code"`
	Label  string `json:"label"`
	Amount int64  `json:"amount_cents"`
}

// Quote is a priced registration.
type Quote struct {
	Tier          Tier      `json:"tier"`
	Category      string    `json:"category"`
	Currency      string    `json:"currency"`
	Rate          string    `json:"fx_rate"`
	BaseCents     int64     `json:"base_cents"`
	DiscountCents int64     `json:"discount_cents"`
	TotalCents    int64     `json:"total_cents"`
	Lines         []Line    `json:"lines"`
	ValidUntil    time.Time `json:"valid_until"`
}

var ErrCurrency = errors.New("para birimi EUR veya TRY olmalı")

// Price computes a quote. İndirimler sırayla kalan tutara uygulanır:
// öğrenci → üye → grup. Her satır seçilen para biriminde ayrı yuvarlanır ve
// toplam = taban − indirimler eşitliği her zaman korunur.
func Price(cal Calendar, in QuoteInput) (Quote, error) {
	if in.Currency != "EUR" && in.Currency != "TRY" {
		return Quote{}, ErrCurrency
	}
	if in.Currency == "EUR" {
		in.Rate = 1_000_000
	}
	if in.Rate <= 0 {
		return Quote{}, errors.New("TRY için kur gerekli")
	}
	tier := cal.TierAt(in.At)
	baseEUR := BaseFeesEURCents[tier]
	q := Quote{Tier: tier, Category: "regular", Currency: in.Currency, Rate: in.Rate.String(), ValidUntil: in.At.Add(QuoteValidity)}
	if in.Student {
		q.Category = "student"
	}
	q.BaseCents = ConvertEURCents(baseEUR, in.Currency, in.Rate)
	q.Lines = append(q.Lines, Line{"base_" + string(tier), tierLabel(tier), q.BaseCents})

	remaining := q.BaseCents
	apply := func(code, label string, pct int64) {
		d := roundDiv(remaining*pct, 100)
		remaining -= d
		q.DiscountCents += d
		q.Lines = append(q.Lines, Line{code, label, -d})
	}
	if in.Student {
		apply("student", fmt.Sprintf("Öğrenci indirimi %%%d", StudentPct), StudentPct)
	}
	if in.Member {
		apply("member", fmt.Sprintf("Üye indirimi %%%d", MemberPct), MemberPct)
	}
	if in.GroupSize >= GroupMinSize {
		apply("group", fmt.Sprintf("Grup indirimi (%d+ kişi) %%%d", GroupMinSize, GroupPct), GroupPct)
	}
	q.TotalCents = q.BaseCents - q.DiscountCents
	return q, nil
}

func tierLabel(t Tier) string {
	switch t {
	case Early:
		return "Erken kayıt ücreti"
	case Normal:
		return "Normal kayıt ücreti"
	}
	return "Geç kayıt ücreti"
}

// QuoteExpired reports whether a frozen quote created at `created` is stale.
func QuoteExpired(created, now time.Time) bool { return now.Sub(created) > QuoteValidity }

// RefundRule describes the refund tier applied at cancellation.
type RefundRule struct {
	Code    string `json:"code"`
	Percent int    `json:"percent"`
	Label   string `json:"label"`
}

// RefundFor returns the rule by days left before conference start.
//
//	≥60 gün: %90 (%10 idari kesinti) · 30–59: %50 · 7–29: %25 · <7 veya başladıktan sonra: %0
func (c Calendar) RefundFor(cancelAt time.Time) RefundRule {
	days := int(c.ConferenceStart.Sub(cancelAt).Hours() / 24)
	switch {
	case cancelAt.After(c.ConferenceStart) || days < 7:
		return RefundRule{"none", 0, "Kongreye 7 günden az kala iade yapılmaz"}
	case days < 30:
		return RefundRule{"late", 25, "7–29 gün kala %25 iade"}
	case days < 60:
		return RefundRule{"mid", 50, "30–59 gün kala %50 iade"}
	default:
		return RefundRule{"early", 90, "60+ gün kala %90 iade (%10 idari kesinti)"}
	}
}

// RefundAmount rounds down (kesir kuruşlar kurumda kalır) in the paid currency.
func RefundAmount(paidCents int64, rule RefundRule) int64 {
	return paidCents * int64(rule.Percent) / 100
}
