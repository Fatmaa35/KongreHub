# TESTING — KongreHub nasıl denenir?

## Tek komutla doğrulama

```bash
cp .env.example .env
make verify          # = docker compose up --build -d --wait && docker compose --profile verify run --rm verify
```

`make verify`, ayağa kalkmış ve seed edilmiş sistem üzerinde aşağıdaki **her kimliği** API ve arayüz vekili üzerinden uçtan uca dener. Sonunda `PASS/FAIL` tablosu basar ve herhangi bir hata varsa **çıkış kodu 1** döner. Süre ~5 sn (ilk derleme hariç). Kontroller kendi verisini (yeni yazar, yeni katılımcılar) oluşturduğu için **sıfırlamadan tekrar çalıştırılabilir**; yine de başlangıç verisine dönmek için:

```bash
make reset-demo      # tüm veriyi siler, aynı tohumla (SEED=42) yeniden yükler
```

Örnek çıktı (yerel çalıştırma, tam çıktı: [`load/results/verify-local.txt`](load/results/verify-local.txt)):

```
KİMLİK  SONUÇ  SÜRE     AÇIKLAMA
HEALTH  PASS   0s       GET /health + /metrics
AUTH    PASS   110ms    Kimlik doğrulama (yanlış parola, token yenileme/rotasyon)
AUTHZ   PASS   240ms    Yetki ihlalleri: tüm uç noktalar × roller (403/401)
T-1     PASS   1.19s    Hakem atama (500×150 < 20 sn, ÇÇ, yük, nedenler, eşzamanlılık)
F-2     PASS   …        Hakem havuzu ve elle atama (keyset, ÇÇ reddi)
F-1     PASS   …        Bildiri gönderimi (dosya, yazarlar, anahtar kelimeler)
F-3     PASS   …        Değerlendirme formu, karar ve düzeltme turu
T-3     PASS   …        Ağırlıklı skor, uyuşmazlık→4. hakem, 14 gün süre aşımı, idempotent job
T-2     PASS   …        Çift kör gizlilik: uç nokta × rol sızıntı taraması, PDF metadata
F-4     PASS   …        Program: salon, zaman dilimi, oturum
T-4     PASS   …        Program çizelgeleme + bağımsız doğrulayıcı + çakışma reddi
F-5     PASS   …        Kayıt ve ücretlendirme (idempotent ödeme, grup)
T-5     PASS   …        Kademeli ücret, kur dondurma, iade, webhook idempotansı, boşluksuz fatura
F-6     PASS   …        Sertifika üretimi (PDF + imzalı QR doğrulama)
F-7     PASS   …        Raporlar
UI      PASS   0s       Arayüz erişilebilir ve API'ye vekil (proxy)
Toplam: 16 kontrol, 16 PASS, 0 FAIL
```

## (a) Demo hesapları

Tüm hesapların parolası **`Demo123!`**. Giriş ekranında her rol için tek tıkla giriş düğmesi de var.

| Rol | E-posta | Başlangıç durumu |
|---|---|---|
| Yazar | `author@kongrehub.test` | 4 bildiri: #1 kabul edildi + programda, #101 düzeltme istendi (anonim hakem yorumları görünür), #151 incelemede, #201 gönderildi |
| Hakem | `reviewer@kongrehub.test` | İncelemede olan bildirilerde açık değerlendirme atamaları |
| Editör / program komitesi | `editor@kongrehub.test` | Tüm bildiriler, atama, karar, program |
| Kayıt görevlisi | `registrar@kongrehub.test` | Kayıtlar, grup kaydı, faturalar, finans raporu, kur girişi |
| Katılımcı | `attendee@kongrehub.test` | Kayıtsız başlar: kayıt → ödeme → sertifika akışı denenebilir |
| Yönetim | `admin@kongrehub.test` | Kullanıcılar, denetim kaydı, rota/rol matrisi, demo uçları |

Seed'deki diğer ~1070 kullanıcının (hakemler, yazarlar, katılımcılar) parolası da `Demo123!`'dir. E-postalarını Yönetim → Kullanıcılar sayfasında görebilirsiniz.

## (b) Kimlik bazında: nasıl denenir / beklenen sonuç

| Kimlik | Nasıl denenir (API / arayüz) | Beklenen sonuç |
|---|---|---|
| `GET /health` | `curl localhost:8080/health` | `200 {"status":"ok","checks":{"database":"up","storage":"up"}}`; DB kapalıyken `503` |
| Kimlik doğrulama | Yanlış parolayla `POST /api/auth/login`; `POST /api/auth/refresh` iki kez aynı token ile | `401 invalid_credentials`; ilk yenileme `200` (yeni token), aynı eski token tekrar → `401` (rotasyon) |
| Yetki ihlali | Yazar token'ı ile `GET /api/submissions`; hakem ile `POST /api/assignments/auto`; yazar ile başkasının bildirisi | `403 forbidden`; başkasının kaynağı → `404` (varlık sızdırılmaz). `make verify` 67 uç × 6 rol matrisini tarar |
| **F-1** Bildiri gönderimi | Yazar → *Yeni bildiri* (PDF + yazarlar + anahtar kelimeler) veya `requests.http` | `201`; anahtar kelimeler normalize; PDF'in `/Author` alanı silinmiş; dosyasız gönderim `400 validation_error` |
| **F-2** Hakem havuzu ve atama | Editör → *Hakem havuzu* / bildiri detayında *Elle hakem ata* | Keyset sayfalı liste; ÇÇ'li hakem → `409 conflict_of_interest`; kapasitesi dolu → `409 reviewer_at_capacity` |
| **F-3** Değerlendirme, karar, düzeltme | Hakem → *Değerlendir* (4 ölçüt, öneri, yorumlar); Editör → karar; Yazar → *Düzeltilmiş sürümü yükle* | Puan 1–10 dışı → `400`; karar `reviews_complete` dışında → `409 invalid_transition`; düzeltme → tur 2, ilgili hakemlere yeni atama (`reason=revision`) |
| **F-4** Program | Katılımcı → *Program*; Editör → *Program* ızgarası | 6 salon × 15 dilim, oturumlar, başkanlar; kişisel programa ekleme ve `.ics` indirme |
| **F-5** Kayıt ve ücret | Katılımcı → *Kayıt & ödeme*: EUR/TRY seç, kaydol, öde | Döküm satırları (kademe, indirimler); ödeme sonrası fatura numarası; aynı Idempotency-Key ile tekrar → aynı yanıt |
| **F-6** Sertifika | Ödemiş katılımcı → *Sertifikalar* → oluştur → PDF / Doğrula | PDF'te imzalı QR; QR'daki bağlantı `/verify/{id}?sig=…` sayfasında "Geçerli"; imza değiştirilirse "Doğrulanamadı" |
| **F-7** Raporlar | Editör → *Raporlar* (CSV indir); Kayıt görevlisi → *Finans* | Durum dağılımı, kabul oranı, konu bazında kabul, hakem performansı/gecikme, gelir ve iade, fatura boşluğu = 0 |
| **T-1** Hakem atama | Editör → *Hakem atama* → "Ölçüm: 500 bildirinin tamamı" | ~0,6 sn; 1492/1500 atama; doğrulayıcı "ihlal yok"; 3 atanamayan bildiri: 2 × uzman yetersiz, 1 × çıkar çatışması |
| **T-2** Çift kör | Hakem olarak bildiri detayı ve PDF; yazar olarak #101 | Hakem görünümünde yazar alanı yok, PDF metadata'sı ve dosya adı nötr; yazar hakemleri "Hakem A/B/C" olarak görür; editör ikisini de görür |
| **T-3** Uyuşmazlık ve süreler | `make verify` (puanlar 9, 9, 3 → fark 6) veya hakemlerle elle; `POST /api/admin/demo/backdate-assignment` + `POST /api/jobs/reminders/run` iki kez | 4. hakem otomatik atanır (`reason=disagreement`); 15 gün geriye alınan atama `expired`, yerine 1 hakem; ikinci job çalıştırması `expired=0`, hatırlatma 0 (idempotent) |
| **T-4** Program çizelgeleme | Editör → *Program* → oluştur / bir oturumu dolu hücreye sürükle | Doğrulayıcı "çakışmasız"; ödemesi olmayan kabul edilmiş bildiriler `no_paid_author` ile dışarıda; dolu hücreye taşıma `409 schedule_conflict` + ihlal listesi |
| **T-5** Ücret, kur, iade, fatura | `requests.http` → F-5/T-5 bölümü | Erken/normal/geç 300/400/500 €; öğrenci+üye+grup 180 €; TRY kuru kayıtta donar (yeni kur girilse de ödenen tutar değişmez); iade %90/%50/%25/%0; webhook tekrarları `duplicate`; geçersiz imza `401`; fatura boşluğu 0 |

## (c) Arayüzde elle denenecek kısa senaryolar

1. **Canlılık:** Herhangi bir hesapla giriş yapın; panodaki "Sistem canlı" kartında sayılar ve "Son olaylar" listesi birkaç saniyede bir değişir. Simülatör kayıt/ödeme, değerlendirme ve kur olayları üretir.
2. **Yazar:** *Yeni bildiri* ile `scripts/sample.pdf` dosyasını yükleyin (içinde `/Author (Ayse Yilmaz)` var). Ardından bildiri detayından PDF'i indirip özelliklerine bakın: yazar alanı boştur. #101'de anonim hakem yorumlarını görün ve düzeltilmiş sürüm yükleyin.
3. **Editör:** *Hakem atama* → önce "Ölçüm" (deneme), sonra "Bekleyenlere ata". Atanamayanlar tablosundaki nedenlere bakın. *Bildiriler* → "Yalnızca uyuşmazlıklı" filtresiyle 4. hakem atanmış bildirileri açın.
4. **Hakem:** *Değerlendirmelerim* → bir atamayı açın; kaydırıcılarla puan verirken ağırlıklı puan anlık güncellenir. Bildiri sayfasında yazar bilgisi görünmez.
5. **Program:** Editör → *Program* ızgarasında bir oturumu boş bir hücreye sürükleyin (geçerliyse taşınır), ardından dolu bir hücreye sürükleyin (reddedilir, nedeni listelenir).
6. **Katılımcı:** TRY ile kaydolun → Kayıt görevlisi hesabıyla `POST /api/fx` ile kuru değiştirin → katılımcı ekranında "güncel kur farklı, tutarınız değişmez" notu görünür → ödeyin → *Sertifikalar*'dan PDF alın ve "Doğrula" bağlantısını açın.
7. **Mobil:** Tarayıcıyı ~390 px genişliğe küçültün; menü ☰ düğmesine taşınır, tablolar yatay kaydırılır.

## Kanıt komutları (görev kimliğiyle)

| Kimlik | Komut | Ne kanıtlar |
|---|---|---|
| T-1 | `cd backend && go test ./internal/domain/assignment -run TestScale500x150 -v` | 500×150, < 20 sn, bağımsız doğrulayıcı 0 ihlal, greedy ile karşılaştırma |
| T-1 | `make verify` → `T-1` satırı | Gerçek veritabanında 500/150 deneme + gerçek atama + 4 eşzamanlı çalıştırma (biri çalışır, diğerleri 409) |
| T-1 | `go test ./internal/app -run TestRaceManualAssignmentRespectsCapacity` | Kalan kapasitesi 1 olan hakeme 12 eşzamanlı atama → tam 1 başarı |
| T-2 | `make verify` → `T-2` satırı; `go test ./internal/pdfmeta` | Tüm GET uçları × yazar/hakem aktörleri için gövde, dosya adı, PDF metadata ve hata mesajı taraması |
| T-3 | `go test ./internal/domain/review -v`; `go test ./internal/app -run TestReminderJobConcurrentRunsAreIdempotent` | Ağırlıklı skor, eşik (>3), durum makinesi, düzeltme hakemleri, hatırlatma günleri; eşzamanlı job'larda tekrar yok |
| T-4 | `go test ./internal/domain/schedule -v` | 5 tohumda çakışmasız program; doğrulayıcının 12 ihlal türünü yakaladığı negatif test; saat dilimi senaryoları |
| T-5 | `go test ./internal/domain/pricing -v`; `go test ./internal/app -run 'TestRace(InvoicesGapFree|DuplicateWebhook|SameIdempotencyKey)'` | Kademeler, indirimler, kur, iade; boşluksuz fatura, webhook ve idempotans yarışları |
| Seed | `make seed-hash` | Aynı tohum → aynı SHA-256 özet; farklı tohum → farklı |
| Hepsi | `make test` / `make test-integration` | Birim + PostgreSQL entegrasyon testleri, domain kapsamı |
| Yük | `make load` | k6: okuma, editör listesi, ödeme senaryoları (README'deki tablo) |
| Kaos | `make chaos` | DB durdurulur → 503 (askıda kalmaz) → başlatılır → API yeniden başlatılmadan toparlanır |
