// KongreHub yük testi (k6). Çalıştırma: make load   (veya: BASE_URL=http://localhost:8080 k6 run load/load.js)
// Senaryolar:
//   read_mix   — katılımcılar program, canlı pano ve kur uçlarını okur (en yaygın trafik)
//   editor     — editör bildiri listesinde keyset sayfalama ile gezinir
//   checkout   — yeni katılımcılar kayıt olur ve Idempotency-Key ile öder (yazma + kilit + fatura sayacı)
import http from 'k6/http'
import { check, sleep } from 'k6'
import { Trend } from 'k6/metrics'
import exec from 'k6/execution'

const BASE = __ENV.BASE_URL || 'http://localhost:8080'
const PASS = 'Demo123!'
const checkoutLatency = new Trend('checkout_duration', true)

export const options = {
  scenarios: {
    read_mix: { executor: 'ramping-vus', exec: 'readMix', startVUs: 0, stages: [{ duration: '20s', target: 40 }, { duration: '60s', target: 40 }, { duration: '10s', target: 0 }] },
    editor: { executor: 'constant-vus', exec: 'editor', vus: 5, duration: '90s' },
    checkout: { executor: 'per-vu-iterations', exec: 'checkout', vus: 20, iterations: 5, maxDuration: '90s' },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    'http_req_duration{scenario:read_mix}': ['p(95)<300'],
    'http_req_duration{scenario:editor}': ['p(95)<500'],
    checkout_duration: ['p(95)<1500'],
    // senaryo bazında istek sayıları özet dosyasına düşsün diye
    'http_reqs{scenario:read_mix}': ['count>0'],
    'http_reqs{scenario:editor}': ['count>0'],
    'http_reqs{scenario:checkout}': ['count>0'],
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
}

const json = { headers: { 'Content-Type': 'application/json' } }
function login(email) {
  const r = http.post(`${BASE}/api/auth/login`, JSON.stringify({ email, password: PASS }), json)
  return r.json('access_token')
}
const auth = (t, extra = {}) => ({ headers: Object.assign({ Authorization: `Bearer ${t}`, 'Content-Type': 'application/json' }, extra) })

export function setup() {
  const admin = login('admin@kongrehub.test')
  const editor = login('editor@kongrehub.test')
  // okuma senaryosu için hazır katılımcılar
  const list = http.get(`${BASE}/api/admin/users?role=attendee&limit=50`, auth(admin)).json('items')
  const readers = list.slice(0, 20).map((u) => login(u.email))
  // ödeme senaryosu: her VU için taze kullanıcılar (5 iterasyon × 20 VU)
  const run = Date.now()
  const buyers = []
  for (let i = 0; i < 100; i++) {
    const email = `k6.${run}.${i}@load.example`
    http.post(`${BASE}/api/admin/users`, JSON.stringify({ email, password: PASS, full_name: `Yük Testi ${i}`, role: 'attendee' }), auth(admin))
    buyers.push(email)
  }
  return { readers, editor, buyers }
}

export function readMix(d) {
  const t = d.readers[__VU % d.readers.length]
  const r1 = http.get(`${BASE}/api/program`, Object.assign(auth(t), { tags: { name: 'GET /api/program' } }))
  const r2 = http.get(`${BASE}/api/live`, Object.assign(auth(t), { tags: { name: 'GET /api/live' } }))
  const r3 = http.get(`${BASE}/api/fx/current`, Object.assign(auth(t), { tags: { name: 'GET /api/fx/current' } }))
  check(r1, { 'program 200': (r) => r.status === 200 })
  check(r2, { 'live 200': (r) => r.status === 200 })
  check(r3, { 'fx 200': (r) => r.status === 200 })
  sleep(0.5)
}

export function editor(d) {
  let cursor = ''
  for (let page = 0; page < 5; page++) {
    const r = http.get(`${BASE}/api/submissions?limit=50${cursor ? `&cursor=${cursor}` : ''}`, Object.assign(auth(d.editor), { tags: { name: 'GET /api/submissions' } }))
    check(r, { 'submissions 200': (x) => x.status === 200 })
    cursor = r.json('next_cursor') || ''
    if (!cursor) break
  }
  sleep(1)
}

export function checkout(d) {
  const email = d.buyers[exec.scenario.iterationInTest]
  const t = login(email)
  const start = Date.now()
  const reg = http.post(`${BASE}/api/registrations`, JSON.stringify({ currency: __ITER % 2 ? 'TRY' : 'EUR' }), Object.assign(auth(t, { 'Idempotency-Key': `reg-${email}` }), { tags: { name: 'POST /api/registrations' } }))
  check(reg, { 'registration 201': (r) => r.status === 201 })
  const id = reg.json('id')
  const pay = http.post(`${BASE}/api/registrations/${id}/payments`, JSON.stringify({ outcome: 'success' }), Object.assign(auth(t, { 'Idempotency-Key': `pay-${email}` }), { tags: { name: 'POST /api/registrations/{id}/payments' } }))
  check(pay, { 'payment 201 paid': (r) => r.status === 201 && r.json('outcome') === 'paid' })
  checkoutLatency.add(Date.now() - start)
}
