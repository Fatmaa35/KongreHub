// Package idem implements the Idempotency-Key contract for state-changing
// POST requests.
//
//	1. (kullanıcı, anahtar) satırı INSERT … ON CONFLICT DO NOTHING ile ayrılır.
//	2. Satırı ilk ayıran isteği işler; yanıt (durum + gövde) satıra yazılır.
//	3. Aynı anahtarla gelen tekrar istek kaydedilmiş yanıtı aynen döner
//	   (Idempotent-Replayed: true). Gövde farklıysa 422, ilk istek hâlâ
//	   sürüyorsa 409 döner. 5xx yanıtlar saklanmaz; satır silinir ve istemci
//	   yeniden deneyebilir.
package idem

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fatmakaplan/kongrehub/internal/auth"
	"github.com/fatmakaplan/kongrehub/internal/httpx"
)

const Header = "Idempotency-Key"

type recorder struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (r *recorder) WriteHeader(s int) { r.status = s; r.ResponseWriter.WriteHeader(s) }
func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	r.buf.Write(b)
	return r.ResponseWriter.Write(b)
}

// Middleware enforces idempotency. required=true → başlık zorunlu (400).
func Middleware(pool *pgxpool.Pool, required bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(Header)
			if key == "" {
				if required {
					httpx.WriteError(w, httpx.Validation("Idempotency-Key başlığı zorunlu"))
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			if len(key) < 8 || len(key) > 128 {
				httpx.WriteError(w, httpx.Validation("Idempotency-Key 8-128 karakter olmalı"))
				return
			}
			p, ok := auth.From(r.Context())
			if !ok {
				httpx.WriteError(w, httpx.ErrUnauthorized)
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, 12<<20))
			if err != nil {
				httpx.WriteError(w, httpx.Validation("istek gövdesi okunamadı"))
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			h := sha256.New()
			h.Write([]byte(r.Method + " " + r.URL.Path + "\n"))
			h.Write(body)
			hash := hex.EncodeToString(h.Sum(nil))
			ctx := r.Context()

			tag, err := pool.Exec(ctx, `INSERT INTO idempotency_keys (user_id, key, method, path, request_hash)
				VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, p.UserID, key, r.Method, r.URL.Path, hash)
			if err != nil {
				httpx.WriteError(w, err)
				return
			}
			if tag.RowsAffected() == 0 {
				replay(ctx, pool, w, p.UserID, key, hash, r.URL.Path)
				return
			}
			rec := &recorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			// İstemci bağlantıyı kopardıysa bile sonucu kaydet (ayrı bağlam).
			sctx := context.WithoutCancel(ctx)
			if rec.status >= 500 || rec.status == 0 {
				_, _ = pool.Exec(sctx, `DELETE FROM idempotency_keys WHERE user_id=$1 AND key=$2`, p.UserID, key)
				return
			}
			_, _ = pool.Exec(sctx, `UPDATE idempotency_keys SET status_code=$3, response_body=$4 WHERE user_id=$1 AND key=$2`,
				p.UserID, key, rec.status, rec.buf.Bytes())
		})
	}
}

func replay(ctx context.Context, pool *pgxpool.Pool, w http.ResponseWriter, uid int64, key, hash, path string) {
	var storedHash, storedPath string
	var status *int
	var body []byte
	err := pool.QueryRow(ctx, `SELECT request_hash, path, status_code, response_body FROM idempotency_keys WHERE user_id=$1 AND key=$2`,
		uid, key).Scan(&storedHash, &storedPath, &status, &body)
	if errors.Is(err, pgx.ErrNoRows) { // 5xx sonrası silinmiş olabilir
		httpx.WriteError(w, httpx.Conflict("idempotency_retry", "önceki deneme başarısız oldu, lütfen tekrar deneyin"))
		return
	}
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if storedHash != hash || storedPath != path {
		httpx.WriteError(w, httpx.E(http.StatusUnprocessableEntity, "idempotency_key_reuse", "bu Idempotency-Key farklı bir istek için kullanılmış"))
		return
	}
	if status == nil {
		httpx.WriteError(w, httpx.Conflict("idempotency_in_progress", "aynı anahtarlı istek hâlâ işleniyor"))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Idempotent-Replayed", "true")
	w.WriteHeader(*status)
	_, _ = w.Write(body)
}
