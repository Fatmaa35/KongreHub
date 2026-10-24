// Command api is the KongreHub HTTP server.
//
//	api            → HTTP sunucusu (varsayılan)
//	api migrate    → migrasyonları uygular ve çıkar (compose "migrate" servisi)
//	api healthcheck→ /health'i çağırır; distroless imajda curl olmadığı için
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/fatmakaplan/kongrehub/internal/app"
	"github.com/fatmakaplan/kongrehub/internal/config"
	"github.com/fatmakaplan/kongrehub/internal/db"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level()}))
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "healthcheck":
		os.Exit(healthcheck())
	case "migrate":
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		url := os.Getenv("DATABASE_URL")
		var err error
		for i := 0; i < 30; i++ { // veritabanı yeni açılıyor olabilir
			if err = db.Migrate(ctx, url); err == nil {
				log.Info("migrasyonlar uygulandı")
				return
			}
			log.Warn("migrasyon denemesi başarısız", "attempt", i+1, "err", err.Error())
			time.Sleep(2 * time.Second)
		}
		log.Error("migrasyon başarısız", "err", err.Error())
		os.Exit(1)
	case "serve":
		if err := serve(log); err != nil {
			log.Error("sunucu hatası", "err", err.Error())
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "kullanım: api [serve|migrate|healthcheck]")
		os.Exit(2)
	}
}

func level() slog.Level {
	if os.Getenv("LOG_LEVEL") == "debug" {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

func serve(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := db.Open(ctx, cfg.DatabaseURL, log)
	if err != nil {
		return err
	}
	defer pool.Close()
	a, err := app.New(cfg, pool, log)
	if err != nil {
		return err
	}
	a.LoadClock(ctx)
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           a.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	jobCtx, cancelJobs := context.WithCancel(ctx)
	defer cancelJobs()
	a.StartJobs(jobCtx)
	errCh := make(chan error, 1)
	go func() {
		log.Info("API dinlemede", "addr", cfg.HTTPAddr, "demo_mode", cfg.DemoMode)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Info("kapanış başladı: yeni istek alınmıyor, açık istekler tamamlanıyor")
	cancelJobs()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	log.Info("kapanış tamamlandı")
	return nil
}

func healthcheck() int {
	addr := os.Getenv("HEALTHCHECK_URL")
	if addr == "" {
		addr = "http://127.0.0.1:8080/health"
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(addr)
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
