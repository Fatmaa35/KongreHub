package pricing

import (
	"testing"
	"time"
)

var cal = DefaultCalendar()

func at(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 12, 0, 0, 0, Istanbul) }

func TestTiers(t *testing.T) {
	cases := map[time.Time]Tier{
		at(2026, 10, 24): Early,
		time.Date(2027, 1, 31, 23, 59, 59, 0, Istanbul): Early,
		time.Date(2027, 2, 1, 0, 0, 0, 0, Istanbul):     Normal,
		at(2027, 4, 15): Normal,
		at(2027, 4, 16): Late,
		at(2027, 5, 11): Late,
	}
	for ts, want := range cases {
		if got := cal.TierAt(ts); got != want {
			t.Errorf("%v: got %s want %s", ts, got, want)
		}
	}
}

func TestParseRate(t *testing.T) {
	r, err := ParseRate("36.512345")
	if err != nil || r != 36512345 || r.String() != "36.512345" {
		t.Fatalf("%v %v %s", r, err, r)
	}
	r, _ = ParseRate("40")
	if r != 40_000_000 {
		t.Fatal(r)
	}
	for _, bad := range []string{"", "abc", "-1", "0"} {
		if _, err := ParseRate(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestPriceRegularEUR(t *testing.T) {
	q, err := Price(cal, QuoteInput{At: at(2027, 3, 1), Currency: "EUR"})
	if err != nil || q.Tier != Normal || q.TotalCents != 40000 || q.DiscountCents != 0 {
		t.Fatalf("%+v %v", q, err)
	}
}

func TestDiscountsStackSequentially(t *testing.T) {
	// geç kayıt 500€, öğrenci %50 → 250, üye %20 → 200, grup %10 → 180
	q, err := Price(cal, QuoteInput{At: at(2027, 5, 1), Student: true, Member: true, GroupSize: 5, Currency: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	if q.BaseCents != 50000 || q.TotalCents != 18000 || q.DiscountCents != 32000 || len(q.Lines) != 4 || q.Category != "student" {
		t.Fatalf("%+v", q)
	}
	var sum int64
	for _, l := range q.Lines {
		sum += l.Amount
	}
	if sum != q.TotalCents {
		t.Fatalf("lines %d != total %d", sum, q.TotalCents)
	}
}

func TestGroupDiscountNeedsFive(t *testing.T) {
	q4, _ := Price(cal, QuoteInput{At: at(2026, 11, 1), GroupSize: 4, Currency: "EUR"})
	q5, _ := Price(cal, QuoteInput{At: at(2026, 11, 1), GroupSize: 5, Currency: "EUR"})
	if q4.TotalCents != 30000 || q5.TotalCents != 27000 {
		t.Fatalf("q4=%d q5=%d", q4.TotalCents, q5.TotalCents)
	}
}

func TestTRYConversionUsesFrozenRateAndIntegers(t *testing.T) {
	r, _ := ParseRate("36.123457")
	q, err := Price(cal, QuoteInput{At: at(2026, 11, 1), Member: true, Currency: "TRY", Rate: r})
	if err != nil {
		t.Fatal(err)
	}
	// 30000 cent × 36.123457 = 1083703.71 → 1083704 kuruş; %20 = 216740.8 → 216741
	if q.BaseCents != 1083704 || q.DiscountCents != 216741 || q.TotalCents != 866963 || q.Rate != "36.123457" {
		t.Fatalf("%+v", q)
	}
	if _, err := Price(cal, QuoteInput{At: at(2026, 11, 1), Currency: "TRY"}); err == nil {
		t.Fatal("TRY without rate must fail")
	}
	if _, err := Price(cal, QuoteInput{At: at(2026, 11, 1), Currency: "USD"}); err != ErrCurrency {
		t.Fatal("USD must fail")
	}
}

func TestQuoteExpiry(t *testing.T) {
	c := at(2026, 11, 1)
	if QuoteExpired(c, c.Add(23*time.Hour)) || !QuoteExpired(c, c.Add(25*time.Hour)) {
		t.Fatal("quote validity 24h")
	}
}

func TestRefundTiers(t *testing.T) {
	cases := []struct {
		at   time.Time
		pct  int
		code string
	}{
		{at(2027, 1, 1), 90, "early"},
		{at(2027, 3, 10), 90, "early"}, // 60 gün kala
		{at(2027, 3, 20), 50, "mid"},
		{at(2027, 4, 20), 25, "late"},
		{at(2027, 5, 5), 0, "none"},
		{at(2027, 5, 12), 0, "none"},
	}
	for _, c := range cases {
		r := cal.RefundFor(c.at)
		if r.Percent != c.pct || r.Code != c.code {
			t.Errorf("%v: got %+v", c.at, r)
		}
	}
	if RefundAmount(866963, RefundRule{Percent: 50}) != 433481 {
		t.Fatal("refund rounds down")
	}
}
