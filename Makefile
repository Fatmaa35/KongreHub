SHELL := /bin/bash
COMPOSE := docker compose

.PHONY: up down logs verify reset-demo load chaos test test-integration lint seed-hash fmt

up:            ## Sistemi arka planda ayağa kaldır ve sağlıklı olmasını bekle
	@test -f .env || cp .env.example .env
	$(COMPOSE) up --build -d
	@./scripts/wait-healthy.sh
	@echo "Arayüz: http://localhost:3000  API: http://localhost:8080  Doküman: http://localhost:8080/docs"

down:          ## Sistemi durdur (veriler korunur)
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f --tail=100

verify:        ## Tüm özellik ve zorlu görevleri uçtan uca dener, PASS/FAIL tablosu basar
	@test -f .env || cp .env.example .env
	$(COMPOSE) up --build -d
	$(COMPOSE) --profile verify run --rm --build verify

reset-demo:    ## Verileri ilk seed haline döndürür (aynı tohum)
	$(COMPOSE) run --rm simulator reset
	$(COMPOSE) restart api simulator
	@echo "Demo verisi sıfırlandı."

load:          ## k6 yük testi (sonuçlar load/results/summary.json)
	@mkdir -p load/results && chmod 777 load/results
	$(COMPOSE) --profile load run --rm k6

chaos:         ## Hata enjeksiyonu: veritabanını durdur/başlat, toparlanmayı ölç
	./scripts/chaos.sh

test:          ## Birim testleri + domain kapsamı
	cd backend && go test ./... -coverprofile=coverage.out -coverpkg=./internal/domain/... && go tool cover -func=coverage.out | tail -1

test-integration: ## PostgreSQL gerektiren entegrasyon testleri (TEST_DATABASE_URL)
	cd backend && go test ./internal/app/ -count=1 -v

lint:
	cd backend && go vet ./... && golangci-lint run ./...
	cd frontend && npm run lint && npm run typecheck

seed-hash:     ## Aynı tohum → aynı özet kanıtı
	@cd backend && go run ./cmd/simulator hash 42 && go run ./cmd/simulator hash 42 && go run ./cmd/simulator hash 43
