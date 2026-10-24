// Package audit writes append-only audit records (kim, ne zaman, neyi, eski/yeni).
//
// Çift kör kuralı: Old/New alanlarına asla ad, e-posta veya kurum yazılmaz;
// yalnızca kimlik numaraları ve iş verisi tutulur. Yazara gösterilen geçmiş
// ayrıca takma adlarla (Hakem A…) üretilir.
package audit

import (
	"context"
	"encoding/json"

	"github.com/fatmakaplan/kongrehub/internal/db"
)

// Entry is one audit record.
type Entry struct {
	ActorID   *int64
	ActorRole string
	Action    string
	Entity    string
	EntityID  string
	Old       any
	New       any
}

// Log inserts the entry using q (genellikle iş ile aynı transaction).
func Log(ctx context.Context, q db.Querier, e Entry) error {
	var oldJ, newJ []byte
	var err error
	if e.Old != nil {
		if oldJ, err = json.Marshal(e.Old); err != nil {
			return err
		}
	}
	if e.New != nil {
		if newJ, err = json.Marshal(e.New); err != nil {
			return err
		}
	}
	if e.ActorRole == "" {
		e.ActorRole = "system"
	}
	_, err = q.Exec(ctx, `INSERT INTO audit_log (actor_id, actor_role, action, entity, entity_id, old_value, new_value)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, e.ActorID, e.ActorRole, e.Action, e.Entity, e.EntityID, nullJSON(oldJ), nullJSON(newJ))
	return err
}

func nullJSON(b []byte) any {
	if b == nil {
		return nil
	}
	return b
}

// Ptr is a helper for actor ids.
func Ptr(v int64) *int64 { return &v }
