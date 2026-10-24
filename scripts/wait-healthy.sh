#!/usr/bin/env bash
# api, simulator ve frontend konteynerleri "healthy" olana kadar bekler (varsayılan 300 sn).
set -euo pipefail
timeout=${1:-300}
start=$(date +%s)
for svc in api simulator frontend; do
  while true; do
    id=$(docker compose ps -q "$svc" 2>/dev/null || true)
    st=$( [ -n "$id" ] && docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$id" 2>/dev/null || echo starting)
    [ "$st" = "healthy" ] && { echo "$svc: healthy ($(( $(date +%s) - start )) sn)"; break; }
    if [ $(( $(date +%s) - start )) -gt "$timeout" ]; then
      echo "$svc $timeout sn içinde sağlıklı olmadı (durum: $st)"; docker compose ps; exit 1
    fi
    sleep 2
  done
done
echo "Arayüz: http://localhost:3000  API: http://localhost:8080  Doküman: http://localhost:8080/docs"
