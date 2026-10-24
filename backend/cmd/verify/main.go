// Command verify runs every feature (F-1..F-7) and hard task (T-1..T-5)
// end to end against a running, seeded system and prints a PASS/FAIL table.
// Herhangi bir kontrol başarısızsa çıkış kodu 1'dir.
package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fatmakaplan/kongrehub/internal/app"
	"github.com/fatmakaplan/kongrehub/internal/e2e"
)

func main() {
	api := env("API_URL", "http://localhost:8080")
	front := os.Getenv("FRONTEND_URL")
	e2e.WebhookSecret = env("PAYMENT_WEBHOOK_SECRET", "whsec_dev")
	e2e.Signer = app.SignWebhook
	if err := waitHealthy(api, 3*time.Minute); err != nil {
		fmt.Fprintln(os.Stderr, "API hazır değil:", err)
		os.Exit(1)
	}
	r := e2e.NewRunner(api, front)
	if os.Getenv("VERBOSE") == "1" {
		r.Log = func(f string, a ...any) { fmt.Printf("  · "+f+"\n", a...) }
	}
	start := time.Now()
	fmt.Printf("KongreHub doğrulama — API %s\n\n", api)
	results := r.Run()
	failed := 0
	fmt.Printf("%-7s %-6s %-8s %s\n", "KİMLİK", "SONUÇ", "SÜRE", "AÇIKLAMA")
	fmt.Println(strings.Repeat("─", 110))
	for _, res := range results {
		st := "PASS"
		if !res.Pass {
			st = "FAIL"
			failed++
		}
		fmt.Printf("%-7s %-6s %-8s %s\n", res.ID, st, res.Duration.Round(10*time.Millisecond), res.Title)
		fmt.Printf("%-23s ↳ %s\n", "", res.Detail)
	}
	fmt.Println(strings.Repeat("─", 110))
	fmt.Printf("Toplam: %d kontrol, %d PASS, %d FAIL — %v\n", len(results), len(results)-failed, failed, time.Since(start).Round(time.Second))
	if failed > 0 {
		os.Exit(1)
	}
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func waitHealthy(api string, d time.Duration) error {
	deadline := time.Now().Add(d)
	var last error
	for time.Now().Before(deadline) {
		resp, err := http.Get(api + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
			last = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			last = err
		}
		time.Sleep(2 * time.Second)
	}
	return last
}
