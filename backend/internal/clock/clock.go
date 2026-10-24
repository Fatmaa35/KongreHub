// Package clock provides the application time source. DEMO_MODE açıkken
// yönetici saati ileri alabilir (14 günlük süre aşımı ve ücret kademeleri
// beklemeden gösterilebilsin diye); üretimde ofset her zaman sıfırdır.
package clock

import (
	"sync/atomic"
	"time"
)

type Clock struct {
	offset atomic.Int64 // nanoseconds
}

func New() *Clock { return &Clock{} }

func (c *Clock) Now() time.Time { return time.Now().Add(time.Duration(c.offset.Load())) }

func (c *Clock) Offset() time.Duration { return time.Duration(c.offset.Load()) }

func (c *Clock) SetOffset(d time.Duration) { c.offset.Store(int64(d)) }
