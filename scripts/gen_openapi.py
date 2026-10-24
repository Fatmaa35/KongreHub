#!/usr/bin/env python3
"""OpenAPI 3 belgesini çalışan API'nin rota kaydından (/api/admin/routes) üretir.
Roller, idempotans başlığı ve yol parametreleri gerçek koddan gelir; açıklamalar ve
örnek gövdeler bu dosyadadır.  Kullanım: python3 scripts/gen_openapi.py > backend/api/openapi.yaml"""
import json, os, re, sys, urllib.request

API = os.environ.get("API_URL", "http://localhost:8080")

def call(path, body=None, token=None):
    req = urllib.request.Request(API + path, data=json.dumps(body).encode() if body else None,
                                 headers={"Content-Type": "application/json", **({"Authorization": "Bearer " + token} if token else {})})
    return json.load(urllib.request.urlopen(req))

tok = call("/api/auth/login", {"email": "admin@kongrehub.test", "password": "Demo123!"})["access_token"]
routes = call("/api/admin/routes", token=tok)["items"]

D = {  # (method, path) -> (özet, örnek gövde)
 ("POST","/api/auth/login"): ("Giriş; access + refresh token döner", {"email":"author@kongrehub.test","password":"Demo123!"}),
 ("POST","/api/auth/refresh"): ("Refresh token rotasyonu (eskisi iptal edilir; tekrar kullanımı tüm oturumları kapatır)", {"refresh_token":"..."}),
 ("POST","/api/auth/logout"): ("Refresh token iptali", {"refresh_token":"..."}),
 ("GET","/api/me"): ("Oturumdaki kullanıcı", None),
 ("POST","/api/submissions"): ("Bildiri gönderimi (multipart: metadata JSON + file PDF). PDF yazar metadata'sı temizlenir.", None),
 ("GET","/api/submissions/{id}"): ("Bildiri detayı; role göre çift kör projeksiyon (hakem yazarı, yazar hakemi göremez)", None),
 ("GET","/api/submissions/{id}/file"): ("PDF indir (nötr dosya adı, temizlenmiş metadata)", None),
 ("POST","/api/submissions/{id}/revisions"): ("Düzeltilmiş sürüm (multipart: response_letter + file); tur+1", None),
 ("POST","/api/submissions/{id}/withdraw"): ("Bildiriyi geri çek", {"reason":"..."}),
 ("POST","/api/assignments/auto"): ("Min-cost flow otomatik hakem ataması (dry_run, scope=pending|all)", {"dry_run":True,"scope":"all"}),
 ("POST","/api/submissions/{id}/assignments"): ("Elle hakem ataması (satır kilidi, ÇÇ ve kapasite kontrolü)", {"reviewer_id":42}),
 ("POST","/api/assignments/{id}/release"): ("Açık atamayı geri al", {"reason":"..."}),
 ("POST","/api/reviewer/assignments/{id}/review"): ("Değerlendirme formu; tur tamamlanınca uyuşmazlık kontrolü (>3 → 4. hakem)", {"originality":8,"significance":7,"methodology":8,"clarity":9,"recommendation":"minor_revision","comments_to_author":"...","comments_to_editor":""}),
 ("POST","/api/reviewer/assignments/{id}/decline"): ("Atamayı reddet; yerine hakem atanır", {"reason":"..."}),
 ("POST","/api/submissions/{id}/decision"): ("Editör kararı (accept|minor_revision|major_revision|reject)", {"decision":"accept","note_to_author":"..."}),
 ("POST","/api/jobs/reminders/run"): ("Hatırlatma/süre aşımı job'ını şimdi çalıştır (idempotent)", {}),
 ("POST","/api/program/generate"): ("Program çizelgeleme + bağımsız doğrulama", {"dry_run":True}),
 ("GET","/api/program/validate"): ("Kayıtlı programı bağımsız doğrulayıcıyla denetle", None),
 ("PUT","/api/program/sessions/{id}"): ("Oturumu taşı (sürükle-bırak); çakışma yaratırsa 409", {"room_id":3,"slot_id":7}),
 ("POST","/api/agenda"): ("Kişisel programa oturum ekle", {"session_id":5}),
 ("GET","/api/agenda.ics"): ("Kişisel program iCalendar dışa aktarımı", None),
 ("POST","/api/registrations/quote"): ("Fiyat teklifi (kademe, indirimler, kur)", {"currency":"TRY","at":"2027-03-01T10:00:00+03:00"}),
 ("POST","/api/registrations"): ("Kayıt oluştur; TRY ise kur dondurulur", {"currency":"TRY"}),
 ("POST","/api/registrations/group"): ("Grup kaydı (5+ kişi → %10 indirim), tek transaction", {"name":"Lab","currency":"EUR","emails":["a@x.example"]}),
 ("POST","/api/registrations/{id}/payments"): ("Ödeme (sağlayıcı simülatörü: success|fail|async)", {"outcome":"success"}),
 ("POST","/api/registrations/{id}/requote"): ("Süresi geçen kur dondurmasını yenile", {}),
 ("POST","/api/registrations/{id}/cancel"): ("İptal ve tarihe göre kademeli iade", {"reason":"..."}),
 ("GET","/api/registrations/{id}/refund-quote"): ("İade önizlemesi (?at=RFC3339)", None),
 ("POST","/api/fx"): ("EUR/TRY kuru kaydet", {"rate":"45.123456"}),
 ("POST","/api/payments/webhook"): ("Ödeme sağlayıcı webhook'u (X-Signature: HMAC-SHA256); event_id ile idempotent", {"event_id":"evt_1","type":"payment.succeeded","provider_ref":"pay_x","amount_cents":30000,"currency":"EUR"}),
 ("POST","/api/certificates"): ("Sertifika üret (attendance|presenter)", {"kind":"attendance"}),
 ("GET","/api/certificates/{id}/pdf"): ("Ed25519 imzalı QR içeren sertifika PDF'i", None),
 ("GET","/api/certificates/verify"): ("Herkese açık QR doğrulaması (?id=&sig=)", None),
 ("POST","/api/admin/users"): ("Kullanıcı oluştur", {"email":"x@y.example","password":"Demo123!","full_name":"X","role":"attendee"}),
 ("POST","/api/admin/clock"): ("(DEMO_MODE) uygulama saatini ileri al", {"offset_hours":0}),
 ("POST","/api/admin/demo/backdate-assignment"): ("(DEMO_MODE) tek atamayı geriye tarihle", {"assignment_id":1,"days":15}),
}

def y(v, ind):
    return json.dumps(v, ensure_ascii=False)

out = ["openapi: 3.0.3", "info:", "  title: KongreHub API", "  version: 1.0.0",
       "  description: |", "    Bilimsel kongre, çift kör hakem değerlendirmesi, program ve kayıt yönetimi.",
       "    Hata biçimi her uçta aynıdır: {\"error\":{\"code\":\"...\",\"message\":\"...\"}}.",
       "    x-roles: uca erişebilen roller (boş = oturum açmış herkes). Rol dışı çağrı 403, oturumsuz 401 döner.",
       "    Büyük listeler keyset sayfalıdır: ?limit=&cursor= → next_cursor.",
       "servers:", "  - url: http://localhost:8080",
       "components:", "  securitySchemes:", "    bearer: {type: http, scheme: bearer, bearerFormat: JWT}",
       "  parameters:", "    IdempotencyKey: {name: Idempotency-Key, in: header, required: true, schema: {type: string, minLength: 8, maxLength: 128},",
       "      description: 'Aynı anahtarla tekrar istek ilk yanıtı aynen döner (Idempotent-Replayed: true); farklı gövde 422.'}",
       "  schemas:", "    Error:", "      type: object", "      properties:", "        error:", "          type: object",
       "          properties: {code: {type: string}, message: {type: string}, details: {type: object}}",
       "  responses:",
       "    Error: {description: Hata, content: {application/json: {schema: {$ref: '#/components/schemas/Error'}}}}",
       "paths:",
       "  /health:", "    get: {summary: 'Sağlık (veritabanı + depolama); 200 veya 503', tags: [ops], responses: {'200': {description: OK}, '503': {$ref: '#/components/responses/Error'}}}",
       "  /metrics:", "    get: {summary: Prometheus metrikleri, tags: [ops], responses: {'200': {description: text/plain}}}"]
paths = {}
for r in routes:
    paths.setdefault(r["pattern"], []).append(r)
for p in sorted(paths):
    out.append(f"  {p}:")
    for r in paths[p]:
        m = r["method"]
        summ, body = D.get((m, p), (f"{m} {p}", None))
        tag = p.split("/")[2] if p.count("/") >= 2 else "api"
        out.append(f"    {m.lower()}:")
        out.append(f"      summary: {y(summ,0)}")
        out.append(f"      tags: [{tag}]")
        out.append(f"      x-roles: {y(r['roles'] or [],0)}")
        out.append("      security: []" if r["public"] else "      security: [{bearer: []}]")
        params = [f"{{name: {n}, in: path, required: true, schema: {{type: string}}}}" for n in re.findall(r"{(\w+)}", p)]
        if r.get("idempotency_key"):
            params.append("{$ref: '#/components/parameters/IdempotencyKey'}")
        if params:
            out.append("      parameters:")
            out += [f"        - {x}" for x in params]
        if body is not None:
            out.append("      requestBody: {content: {application/json: {example: " + y(body,0) + "}}}")
        elif m == "POST" and "multipart" in summ:
            out.append("      requestBody: {content: {multipart/form-data: {schema: {type: object}}}}")
        out.append("      responses:")
        ok = "201" if m == "POST" and any(k in p for k in ["/submissions", "/registrations", "/certificates", "/payments", "/agenda", "/rooms", "/fx", "/users", "/assignments"]) and not p.endswith(("auto","run","quote","generate","decision","withdraw","release","decline","requote","cancel","refresh","login","logout","webhook","clock","backdate-assignment")) else "200"
        out.append(f"        '{ok}': {{description: Başarılı}}")
        for c in (["400","401","403","404","409"] if not r["public"] else ["400","401"]):
            out.append(f"        '{c}': {{$ref: '#/components/responses/Error'}}")
print("\n".join(out))
