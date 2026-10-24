#!/usr/bin/env bash
# Hata enjeksiyonu (kaos) senaryosu: veritabanını durdurur, API'nin bu sırada
# askıda kalmadan 503/hata ile yanıt verdiğini gösterir, veritabanını yeniden
# başlatır ve API'nin YENİDEN BAŞLATILMADAN kendiliğinden toparlandığını ölçer.
#
# Varsayılan: docker compose. Yerel deneme için STOP_CMD/START_CMD verilebilir.
set -euo pipefail
API=${API_URL:-http://localhost:8080}
STOP_CMD=${STOP_CMD:-"docker compose stop db"}
START_CMD=${START_CMD:-"docker compose start db"}
DOWN_SECONDS=${DOWN_SECONDS:-10}

say() { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*"; }
code() { curl -s -o /dev/null -m 3 -w '%{http_code}' "$1" || true; }
login() {
  curl -s -o /dev/null -m 5 -w '%{http_code}' -H 'Content-Type: application/json' \
    -d '{"email":"attendee@kongrehub.test","password":"Demo123!"}' "$API/api/auth/login" || true
}

say "Başlangıç: /health=$(code "$API/health"), login=$(login)"
[ "$(code "$API/health")" = "200" ] || { say "API sağlıklı değil, önce sistemi başlatın"; exit 1; }

say "Hata enjeksiyonu: veritabanı durduruluyor ($STOP_CMD)"
eval "$STOP_CMD" >/dev/null
sleep 2
H=$(code "$API/health"); L=$(login)
start=$(date +%s%N); P=$(code "$API/api/program"); took=$(( ($(date +%s%N)-start)/1000000 ))
say "Kesinti sırasında: /health=$H (503 beklenir), login=$L (503 beklenir), istek $took ms içinde yanıtlandı (askıda kalmadı)"
[ "$H" = "503" ] || { say "HATA: health 503 değil"; exit 1; }
[ "$took" -lt 5000 ] || { say "HATA: istek askıda kaldı"; exit 1; }
sleep "$DOWN_SECONDS"

say "Veritabanı yeniden başlatılıyor ($START_CMD)"
t0=$(date +%s%N)
eval "$START_CMD" >/dev/null
recovered=""
for i in $(seq 1 120); do
  if [ "$(code "$API/health")" = "200" ] && [ "$(login)" = "200" ]; then recovered=yes; break; fi
  sleep 0.5
done
t=$(( ($(date +%s%N)-t0)/1000000 ))
if [ -z "$recovered" ]; then say "HATA: API 60 sn içinde toparlanmadı"; exit 1; fi
say "TOPARLANDI: veritabanı başlatıldıktan $t ms sonra /health=200 ve login=200 (API yeniden başlatılmadı; pgx havuzu bağlantıları yeniledi)"
say "Doğrulama: $(curl -s "$API/health")"
