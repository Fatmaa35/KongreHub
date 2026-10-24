# Mimari Karar Kayıtları (ADR)

Her kayıt: bağlam → karar → alternatifler → sonuçlar. Kararlar kodda ilgili dosyaya işaret eder.

---

## ADR-001 — Hakem atamasında min-cost flow (greedy değil)

**Bağlam.** Her bildiriye 3 hakem; uzmanlık, çıkar çatışması (ÇÇ), azami yük ve adil dağılım aynı anda gözetilmeli; 500×150 için < 20 sn.

**Karar.** `internal/domain/assignment` içinde ardışık en kısa yol (SSP) min-cost flow:
kaynak → bildiri (kapasite = eksik hakem) → hakem (kapasite 1, maliyet = (1 − kapsama skoru)·1000) → hedef.
Hakem→hedef arasında kalan kapasite kadar **birim kenar** var ve k. ek incelemenin maliyeti `15·(mevcutYük+k)`; bu konveks maliyet yükü kendiliğinden dengeler. ÇÇ ve uzmanlık eşiği **graf yapısıyla** uygulanır (kenar hiç eklenmez), yani ihlal edilemez. Tüm maliyetler ≥ 0 olduğundan Dijkstra + potansiyeller kullanılır.

**Alternatifler.**
- *Greedy (bildiri sırasıyla en iyi hakem):* hızlı ama sıraya bağımlı; sona kalan bildiriler uzman bulamıyor. Aynı veride greedy 1498, akış 1500 atama yaptı ve ortalama skor 0,851'e karşı 0,886 (test: `TestScale500x150`).
- *ILP / Macar algoritması:* Macar algoritması 1-1 eşleme içindir (bildiri başına 3 hakem ve yük sınırı ek dönüşüm ister); ILP için harici çözücü bağımlılığı gerekir.

**Sonuçlar.** 500×150 → ~0,6 sn. Akıştan bağımsız yazılmış `Validate` her çalıştırmada sonucu denetler; ihlal varsa transaction geri alınır. Atanamayan bildiriler `insufficient_expertise / conflict_of_interest / reviewer_capacity_exhausted` nedenleri ve sayısal açıklamayla raporlanır.

---

## ADR-002 — Çift kör gizliliği "açık projeksiyon" + otomatik sızıntı taraması ile sağlamak

**Bağlam.** Kimlik yalnızca JSON alanlarında değil; dosya adında, PDF metadata'sında, hata mesajlarında ve denetim kayıtlarında da sızabilir.

**Karar.**
1. Her rol için **ayrı alan seçimi** (`buildSubmission`): hakem görünümünde yazar alanları hiç doldurulmaz; yazar görünümünde hakemler bildiri başına sabit takma adlarla (`Hakem A…`) görünür. "Gizlemek için silme" değil, "göstermek için seçme".
2. Yüklenen PDF'lerin Info sözlüğü, XMP ve PieceInfo alanları **yükleme anında** pdfcpu ile silinir ve dosya yeniden yazılır; indirilen dosyanın adı her rolde `submission-{id}-v{n}.pdf`'tir.
3. İlgisiz kaynakta 404 döner (varlık bile sızdırılmaz); hata mesajları kişisel veri içermez.
4. Denetim kayıtlarında ad/e-posta tutulmaz, yalnızca kimlik numaraları tutulur. Yazara gösterilen geçmiş, rolleri takma adlara çevirir.
5. **Rota kaydı (RBAC matrisi)** her uç noktanın rollerini tutar. `make verify` ve entegrasyon testi bu kayıttan tüm GET uçları × yazar/hakem aktörlerini tarar: gövde, `Content-Disposition`, PDF `/Author` ve hata yanıtları.

**Alternatifler.** Yanıt sonrası "yasaklı kelime" filtresi (kırılgan, performans maliyeti var, yeni alanları kaçırır); veritabanı görünümleri (rol başına view; bakım maliyeti yüksek).

**Sonuçlar.** Yeni bir uç nokta eklendiğinde tarama onu otomatik olarak kapsar. Bilinen sınır: PDF **gövde metnindeki** kimlik (ör. "bizim önceki çalışmamız [3]") otomatik tespit edilmez; yazar kuralıdır.

---

## ADR-003 — Eşzamanlılık: koşullu UPDATE + satır kilidi + danışma kilidi; SERIALIZABLE yerine hedefli kilitler

**Bağlam.** Paylaşılan durumlar: hakem yükü, ödeme/kayıt durumu, fatura sayacı, toplu atama, program.

**Karar.**
- **Hakem yükü:** `UPDATE reviewer_profiles SET current_load=current_load+1 WHERE user_id=$1 AND current_load<max_load` (koşullu UPDATE). Ayrıca `CHECK (current_load <= max_load)` son savunma hattıdır. Elle atamada profil satırı `FOR UPDATE` ile kilitlenir.
- **Toplu atama / program üretimi:** `pg_try_advisory_xact_lock`; ikinci eşzamanlı çalıştırma beklemez, `409` alır.
- **Ödeme:** kayıt satırı `FOR UPDATE`; bekleyen (15 dk) ya da başarılı ödeme varsa ikinci ödeme başlatılmaz. `payments_one_success` kısmi tekil dizini ve `payment_events.event_id` birincil anahtarı ek güvencedir.
- **Fatura numarası:** `INSERT … ON CONFLICT (year) DO UPDATE SET last_value=last_value+1 RETURNING`; sayaç satırı, fatura ile **aynı transaction'da** kilitlenir. Transaction geri alınırsa numara da geri alınır, bu yüzden boşluk oluşmaz.
- **Job:** `FOR UPDATE SKIP LOCKED` ile birden fazla job örneği aynı atamayı işlemez.

**Alternatifler.** Her şeyi `SERIALIZABLE` + yeniden deneme ile çözmek (`db.InTx` yeniden denemeyi destekliyor). Ancak sıcak noktada (fatura sayacı) sürekli serileştirme hatası üretir ve davranışı açıklamak zorlaşır. PostgreSQL `SEQUENCE` boşluksuz değildir (rollback numarayı yakar), bu yüzden faturada kullanılamaz.

**Sonuçlar.** Yarışlar testle kanıtlandı (`integration_test.go`): kalan kapasitesi 1 olan hakeme 12 eşzamanlı atamada tam 1 başarı; 25 eşzamanlı ödemede boşluksuz fatura; aynı webhook 10 kez gelince tek "paid"; aynı Idempotency-Key ile 10 eşzamanlı istekte tek ödeme satırı; 8 eşzamanlı ödemede tek başarı. Sayaç tek satır olduğundan fatura üretimi seri hâle gelir (k6: ödeme akışı p95 ≈ 900 ms). Bu, kongre ölçeği için kabul edilebilir bir bedel.

---

## ADR-004 — İdempotans: PostgreSQL tablosunda (kullanıcı, anahtar) rezervasyonu

**Bağlam.** Ödeme, kayıt, değerlendirme, karar, sertifika ve program üretimi tekrarlanan isteklerde aynı sonucu vermeli.

**Karar.** `internal/idem` ara katmanı `(user_id, key)` satırını `INSERT … ON CONFLICT DO NOTHING` ile ayırır. İlk istek işlenir ve yanıtı (durum + gövde) saklanır. Aynı anahtar + aynı gövde gelirse kayıtlı yanıt aynen döner (`Idempotent-Replayed: true`). Farklı gövde `422`, hâlâ süren istek `409` alır. 5xx yanıtlar saklanmaz; satır silinir ve istemci yeniden deneyebilir. Rota kaydında `idempotent()` ile işaretlenen uçlarda başlık zorunludur (`400`).

**Alternatifler.** Redis (ek bağımlılık; transaction ile atomik değil), yalnızca iş seviyesi tekillik (ör. `UNIQUE(registration_id)`). İkincisi tek başına "aynı yanıtı döndür" gereksinimini karşılamaz; biz ikisini birlikte kullanıyoruz.

**Sonuçlar.** Arayüz, bir ödeme denemesi boyunca **aynı anahtarı** tutar; çift tıklama çift ödeme yaratmaz. Webhook tarafında idempotans ayrıca `event_id` ile sağlanır.

---

## ADR-005 — Program çizelgeleme: iki aşama (gruplama + MRV geri izleme) ve ayrı doğrulayıcı

**Bağlam.** Konu benzerliği, salon kapasitesi, konuşmacının aynı anda iki yerde olamaması, saat dilimi uygunluğu (uzaktan sunanlar), oturum başkanı.

**Karar.** `internal/domain/schedule`:
1. **Gruplama:** benzerlik toplamı en düşük (yerleştirmesi en zor) bildiri tohum seçilir; ortalama Jaccard benzerliği en yüksek bildiriler eklenir. Ancak grup **en az bir zaman dilimine ve en büyük salona sığmaya devam ediyorsa** eklenir.
2. **Yerleştirme:** MRV sezgiseli ile geri izleme (en az seçeneği kalan oturum önce, en küçük yeterli salon önce). Tam çözüm bulunamazsa açgözlü yerleştirme ve **onarım** devreye girer: bildiriler mevcut oturumların boşluklarına ya da boş hücrelere tek tek yerleştirilir.
3. Oturum içi konuşma sırası, her konuşmacının kendi saat dilimindeki penceresine göre permütasyonla bulunur (n ≤ 6).
4. Başkan: oturumdaki bildirilerin yazarı olmayan, aynı dilimde başka yerde bulunmayan, uzmanlığı en uygun ve yükü en az aday.
5. `Validate` çözücünün iç yapılarını kullanmadan bütün kısıtları yeniden denetler; hem üretimde, hem sürükle-bırak taşımada, hem testlerde kullanılır. Ayrıca `session_papers` tetikleyicisi "kabul edilmiş + ödemesi yapılmış" kuralını veritabanında zorlar.

**Alternatifler.** CP-SAT / ILP (en iyi çözüm ama harici çözücü ve açıklanabilirlik maliyeti); tavlama benzetimi (kısıt ihlali olasılığı, test edilmesi zor).

**Sonuçlar.** İlk sürüm gruplamada saat dilimini dikkate almıyordu ve testler bunu yakaladı (Los Angeles ile Tokyo konuşmacısı aynı oturumdaydı → oturum hiçbir dilime sığmıyordu). Düzeltmeden sonra 5 farklı tohumda ihlal 0. Programa giremeyen bildiriler `no_paid_author / speaker_unavailable / no_room_or_slot` nedeniyle raporlanır.

---

## ADR-006 — Para ve kur: tam sayı kuruş, kur 10⁻⁶ hassasiyetli tam sayı, satır bazında yuvarlama

**Karar.** Tutarlar `BIGINT` (cent/kuruş), kur `NUMERIC(12,6)`; Go tarafında kur `big.Rat` ile ayrıştırılıp mikro birime çevrilir. İndirimler (öğrenci → üye → grup) kalan tutara sırayla uygulanır. Her satır seçilen para biriminde ayrı yuvarlanır ve `toplam = taban − indirimler` eşitliği DB `CHECK` kısıtıyla korunur. Kur kayıt anında dondurulur (24 saat geçerli); süresi geçerse ödeme `409 quote_expired` alır ve yeniden fiyatlama gerekir. İade, ödenen para biriminde ve dondurulmuş tutar üzerinden yapılır; kesirli kuruşlar aşağı yuvarlanır.

**Alternatifler.** `float64` (yasak ve yanlış); kuru ödeme anında uygulamak (kullanıcı farklı tutar öder, sürpriz yaşar).

---

## ADR-007 — Frontend: React + Vite + TypeScript + TanStack Query

**Karar.** Rol bazlı birçok ekran ve sunucu durumunun yoğun kullanımı (önbellek, yeniden deneme, sayfalama, periyodik yenileme) var. TanStack Query yükleniyor/hata/boş durumlarını tek bir `QueryState` bileşeninde standartlaştırıyor. Vite ile derleme 2 sn sürüyor. TypeScript, API sözleşmesindeki hataları derleme anında yakalıyor. Prod imajı `nginx-unprivileged` (root olmayan kullanıcı). nginx `/api` isteklerini API'ye aktarıyor, böylece tarayıcı aynı origin'den konuşuyor ve CORS gerekmiyor.

**Alternatifler.** HTMX (sunucu tarafı şablon ve Go'da çok fazla görünüm kodu gerekirdi); Next.js (SSR ve Node çalışma zamanı gereksiz).

---

## ADR-008 — Bağımlılık seçimi: dar ve açıklanabilir

- HTTP: `chi` (standart `net/http` uyumlu, rota kalıbını metriklerde etiket olarak kullanıyoruz).
- DB: `pgx/v5` + elle yazılmış SQL (sorgular `EXPLAIN` ile incelendi; ORM kullanılmadı). Migrasyonlar `goose`; SQL dosyaları binary'e gömülü, `migrate` compose servisi tek sefer çalışıyor.
- Metrikler: kendi küçük Prometheus metin biçimi yazıcımız (`internal/metrics`). Resmî istemcinin güncel sürümü Go 1.26 istiyordu; ihtiyacımız sayaç/gauge/histogram ile sınırlı olduğundan ~150 satırla çözüldü ve test edildi.
- PDF: `go-pdf/fpdf` (sertifika, UTF-8 DejaVu fontu gömülü), `pdfcpu` (metadata temizleme), QR için `skip2/go-qrcode`.
- Parola özeti: argon2id (OWASP: m=19 MiB, t=2, p=1). Olmayan kullanıcı için de aynı maliyetli doğrulama yapılır, böylece yanıt süresinden e-posta kaydı sızmaz.
