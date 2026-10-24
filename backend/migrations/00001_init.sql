-- +goose Up
-- KongreHub ilk şema.
-- Kurallar: para tutarları BIGINT (kuruş/cent), float yok; kur NUMERIC.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ---------------------------------------------------------------
-- Kimlik
-- ---------------------------------------------------------------
CREATE TABLE institutions (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    country     TEXT NOT NULL
);

CREATE TABLE users (
    id              BIGSERIAL PRIMARY KEY,
    email           TEXT NOT NULL,
    password_hash   TEXT NOT NULL,
    full_name       TEXT NOT NULL CHECK (length(full_name) BETWEEN 2 AND 200),
    role            TEXT NOT NULL CHECK (role IN ('author','reviewer','editor','registrar','attendee','admin')),
    institution_id  BIGINT REFERENCES institutions(id),
    timezone        TEXT NOT NULL DEFAULT 'Europe/Istanbul',
    -- Konuşmacı uygunluk penceresi (kendi saat diliminde, saat cinsinden)
    avail_start_hour SMALLINT NOT NULL DEFAULT 8  CHECK (avail_start_hour BETWEEN 0 AND 23),
    avail_end_hour   SMALLINT NOT NULL DEFAULT 22 CHECK (avail_end_hour BETWEEN 1 AND 24),
    is_member       BOOLEAN NOT NULL DEFAULT false,
    is_student      BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (avail_start_hour < avail_end_hour)
);
CREATE UNIQUE INDEX users_email_lower_uq ON users (lower(email));
CREATE INDEX users_role_idx ON users (role, id);

CREATE TABLE refresh_tokens (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  BYTEA NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX refresh_tokens_user_idx ON refresh_tokens (user_id);

-- ---------------------------------------------------------------
-- Hakem havuzu ve çıkar çatışması verisi
-- ---------------------------------------------------------------
CREATE TABLE reviewer_profiles (
    user_id       BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    keywords      TEXT[] NOT NULL CHECK (cardinality(keywords) > 0),
    max_load      INT NOT NULL CHECK (max_load BETWEEN 1 AND 50),
    current_load  INT NOT NULL DEFAULT 0,
    is_active     BOOLEAN NOT NULL DEFAULT true,
    -- Yük asla kapasiteyi aşamaz: eşzamanlı atamalarda son savunma hattı.
    CONSTRAINT reviewer_load_within_capacity CHECK (current_load >= 0 AND current_load <= max_load)
);
CREATE INDEX reviewer_profiles_keywords_gin ON reviewer_profiles USING gin (keywords);

-- Ortak yayın (simetrik ilişki; user_a < user_b ile tekilleştirilir)
CREATE TABLE coauthorships (
    user_a  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_b  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    paper_title TEXT NOT NULL,
    year    INT NOT NULL CHECK (year BETWEEN 1950 AND 2100),
    PRIMARY KEY (user_a, user_b, paper_title),
    CHECK (user_a < user_b)
);
CREATE INDEX coauthorships_b_idx ON coauthorships (user_b);

CREATE TABLE advisor_relations (
    advisor_id  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    student_id  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (advisor_id, student_id),
    CHECK (advisor_id <> student_id)
);
CREATE INDEX advisor_relations_student_idx ON advisor_relations (student_id);

-- ---------------------------------------------------------------
-- Bildiriler
-- ---------------------------------------------------------------
CREATE TABLE submissions (
    id               BIGSERIAL PRIMARY KEY,
    title            TEXT NOT NULL CHECK (length(title) BETWEEN 5 AND 300),
    abstract         TEXT NOT NULL CHECK (length(abstract) BETWEEN 20 AND 5000),
    keywords         TEXT[] NOT NULL CHECK (cardinality(keywords) BETWEEN 1 AND 8),
    status           TEXT NOT NULL DEFAULT 'submitted' CHECK (status IN (
                        'submitted','under_review','reviews_complete','revision_requested',
                        'accepted','rejected','withdrawn')),
    round            INT NOT NULL DEFAULT 1 CHECK (round BETWEEN 1 AND 5),
    current_version  INT NOT NULL DEFAULT 1 CHECK (current_version >= 1),
    submitter_id     BIGINT NOT NULL REFERENCES users(id),
    expected_audience INT NOT NULL DEFAULT 30 CHECK (expected_audience > 0),
    duration_minutes  INT NOT NULL DEFAULT 20 CHECK (duration_minutes IN (15,20,30)),
    disagreement_flag BOOLEAN NOT NULL DEFAULT false,
    final_score      NUMERIC(5,2),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Editör listesi: duruma göre filtre + keyset sayfalama (id DESC)
CREATE INDEX submissions_status_id_idx ON submissions (status, id DESC);
CREATE INDEX submissions_submitter_idx ON submissions (submitter_id, id DESC);
CREATE INDEX submissions_keywords_gin ON submissions USING gin (keywords);

CREATE TABLE submission_authors (
    submission_id   BIGINT NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
    position        INT NOT NULL CHECK (position BETWEEN 1 AND 20),
    user_id         BIGINT REFERENCES users(id),
    full_name       TEXT NOT NULL,
    email           TEXT NOT NULL,
    institution_id  BIGINT REFERENCES institutions(id),
    is_presenter    BOOLEAN NOT NULL DEFAULT false,
    PRIMARY KEY (submission_id, position)
);
CREATE UNIQUE INDEX submission_authors_email_uq ON submission_authors (submission_id, lower(email));
CREATE INDEX submission_authors_user_idx ON submission_authors (user_id);
-- Her bildiride en fazla bir sunucu yazar
CREATE UNIQUE INDEX submission_one_presenter ON submission_authors (submission_id) WHERE is_presenter;

CREATE TABLE submission_files (
    id              BIGSERIAL PRIMARY KEY,
    submission_id   BIGINT NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
    version         INT NOT NULL CHECK (version >= 1),
    storage_key     TEXT NOT NULL,
    sha256          TEXT NOT NULL,
    size_bytes      BIGINT NOT NULL CHECK (size_bytes > 0),
    original_name   TEXT NOT NULL,          -- yalnızca yazar/editör görür
    response_letter TEXT,                   -- düzeltme turu yanıt mektubu
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (submission_id, version)
);

-- ---------------------------------------------------------------
-- Hakem atamaları ve değerlendirmeler
-- ---------------------------------------------------------------
CREATE TABLE review_assignments (
    id              BIGSERIAL PRIMARY KEY,
    submission_id   BIGINT NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
    reviewer_id     BIGINT NOT NULL REFERENCES users(id),
    round           INT NOT NULL CHECK (round >= 1),
    status          TEXT NOT NULL DEFAULT 'assigned' CHECK (status IN ('assigned','submitted','expired','declined','released')),
    reason          TEXT NOT NULL DEFAULT 'auto' CHECK (reason IN ('auto','manual','disagreement','replacement','revision')),
    match_score     NUMERIC(6,4) NOT NULL DEFAULT 0,
    assigned_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    due_at          TIMESTAMPTZ NOT NULL,
    completed_at    TIMESTAMPTZ,
    UNIQUE (submission_id, reviewer_id, round)
);
-- Hatırlatma/süre job'ı: açık atamaları vadesine göre tarar (kısmi dizin)
CREATE INDEX review_assignments_open_due_idx ON review_assignments (due_at) WHERE status = 'assigned';
CREATE INDEX review_assignments_reviewer_idx ON review_assignments (reviewer_id, status);
CREATE INDEX review_assignments_submission_idx ON review_assignments (submission_id, round);

CREATE TABLE reviews (
    assignment_id   BIGINT PRIMARY KEY REFERENCES review_assignments(id) ON DELETE CASCADE,
    originality     SMALLINT NOT NULL CHECK (originality BETWEEN 1 AND 10),
    significance    SMALLINT NOT NULL CHECK (significance BETWEEN 1 AND 10),
    methodology     SMALLINT NOT NULL CHECK (methodology BETWEEN 1 AND 10),
    clarity         SMALLINT NOT NULL CHECK (clarity BETWEEN 1 AND 10),
    weighted_score  NUMERIC(5,2) NOT NULL CHECK (weighted_score BETWEEN 1 AND 10),
    recommendation  TEXT NOT NULL CHECK (recommendation IN ('accept','minor_revision','major_revision','reject')),
    comments_to_author TEXT NOT NULL CHECK (length(comments_to_author) >= 10),
    comments_to_editor TEXT NOT NULL DEFAULT '',
    submitted_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE decisions (
    id              BIGSERIAL PRIMARY KEY,
    submission_id   BIGINT NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
    round           INT NOT NULL,
    decision        TEXT NOT NULL CHECK (decision IN ('accept','minor_revision','major_revision','reject')),
    note_to_author  TEXT NOT NULL DEFAULT '',
    decided_by      BIGINT NOT NULL REFERENCES users(id),
    decided_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (submission_id, round)
);

-- Hatırlatma job'ının idempotans anahtarı: aynı atama için aynı tür hatırlatma bir kez
CREATE TABLE reminders (
    assignment_id   BIGINT NOT NULL REFERENCES review_assignments(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('day7','day12','expired')),
    sent_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (assignment_id, kind)
);

CREATE TABLE job_runs (
    id          BIGSERIAL PRIMARY KEY,
    job         TEXT NOT NULL,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    stats       JSONB NOT NULL DEFAULT '{}'
);

-- ---------------------------------------------------------------
-- Program
-- ---------------------------------------------------------------
CREATE TABLE rooms (
    id        BIGSERIAL PRIMARY KEY,
    name      TEXT NOT NULL UNIQUE,
    capacity  INT NOT NULL CHECK (capacity > 0)
);

CREATE TABLE time_slots (
    id         BIGSERIAL PRIMARY KEY,
    starts_at  TIMESTAMPTZ NOT NULL,
    ends_at    TIMESTAMPTZ NOT NULL,
    UNIQUE (starts_at),
    CHECK (ends_at > starts_at)
);

CREATE TABLE program_sessions (
    id          BIGSERIAL PRIMARY KEY,
    title       TEXT NOT NULL,
    topic       TEXT NOT NULL,
    room_id     BIGINT NOT NULL REFERENCES rooms(id),
    slot_id     BIGINT NOT NULL REFERENCES time_slots(id),
    chair_id    BIGINT REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Bir salon aynı anda tek oturum barındırır
    UNIQUE (room_id, slot_id),
    -- Bir başkan aynı anda tek oturum yönetir
    UNIQUE (chair_id, slot_id)
);

CREATE TABLE session_papers (
    session_id      BIGINT NOT NULL REFERENCES program_sessions(id) ON DELETE CASCADE,
    submission_id   BIGINT NOT NULL UNIQUE REFERENCES submissions(id),
    position        INT NOT NULL CHECK (position >= 1),
    starts_at       TIMESTAMPTZ NOT NULL,
    ends_at         TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (session_id, position),
    CHECK (ends_at > starts_at)
);

-- Katılımcının kişisel programı (bonus: iCal)
CREATE TABLE personal_agenda (
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id  BIGINT NOT NULL REFERENCES program_sessions(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, session_id)
);

-- ---------------------------------------------------------------
-- Kayıt, ücret, ödeme, fatura, iade
-- ---------------------------------------------------------------
CREATE TABLE fx_rates (
    id          BIGSERIAL PRIMARY KEY,
    base        TEXT NOT NULL DEFAULT 'EUR' CHECK (base = 'EUR'),
    quote       TEXT NOT NULL DEFAULT 'TRY' CHECK (quote = 'TRY'),
    rate        NUMERIC(12,6) NOT NULL CHECK (rate > 0),
    observed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX fx_rates_observed_idx ON fx_rates (observed_at DESC);

CREATE TABLE registration_groups (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    created_by  BIGINT NOT NULL REFERENCES users(id),
    size        INT NOT NULL CHECK (size >= 1),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE registrations (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users(id),
    group_id        BIGINT REFERENCES registration_groups(id),
    tier            TEXT NOT NULL CHECK (tier IN ('early','normal','late')),
    category        TEXT NOT NULL CHECK (category IN ('regular','student')),
    is_member       BOOLEAN NOT NULL,
    currency        TEXT NOT NULL CHECK (currency IN ('EUR','TRY')),
    fx_rate         NUMERIC(12,6) NOT NULL CHECK (fx_rate > 0),   -- dondurulmuş EUR→TRY kuru
    base_cents      BIGINT NOT NULL CHECK (base_cents >= 0),
    discount_cents  BIGINT NOT NULL CHECK (discount_cents >= 0),
    total_cents     BIGINT NOT NULL CHECK (total_cents >= 0),
    breakdown       JSONB NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','paid','cancelled','refunded')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_at         TIMESTAMPTZ,
    cancelled_at    TIMESTAMPTZ,
    CHECK (total_cents = base_cents - discount_cents),
    CHECK (status <> 'paid' OR paid_at IS NOT NULL)
);
-- Bir kullanıcının aynı anda tek aktif kaydı olur
CREATE UNIQUE INDEX registrations_one_active ON registrations (user_id) WHERE status IN ('pending','paid');
CREATE INDEX registrations_status_id_idx ON registrations (status, id DESC);

CREATE TABLE payments (
    id              BIGSERIAL PRIMARY KEY,
    registration_id BIGINT NOT NULL REFERENCES registrations(id),
    provider_ref    TEXT NOT NULL UNIQUE,
    amount_cents    BIGINT NOT NULL CHECK (amount_cents > 0),
    currency        TEXT NOT NULL CHECK (currency IN ('EUR','TRY')),
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','succeeded','failed')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX payments_registration_idx ON payments (registration_id);
-- Bir kayıt için en fazla bir başarılı ödeme
CREATE UNIQUE INDEX payments_one_success ON payments (registration_id) WHERE status = 'succeeded';

-- Sağlayıcıdan gelen olaylar: event_id tekil → tekrar teslimler etkisiz
CREATE TABLE payment_events (
    event_id      TEXT PRIMARY KEY,
    provider_ref  TEXT NOT NULL,
    type          TEXT NOT NULL CHECK (type IN ('payment.succeeded','payment.failed')),
    payload       JSONB NOT NULL,
    received_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    outcome       TEXT NOT NULL
);

-- Boşluksuz fatura numarası: sayaç satırı işlemle birlikte kilitlenir/geri alınır
CREATE TABLE invoice_counters (
    year        INT PRIMARY KEY,
    last_value  BIGINT NOT NULL DEFAULT 0 CHECK (last_value >= 0)
);

CREATE TABLE invoices (
    id              BIGSERIAL PRIMARY KEY,
    year            INT NOT NULL,
    seq             BIGINT NOT NULL CHECK (seq >= 1),
    number          TEXT NOT NULL UNIQUE,
    registration_id BIGINT NOT NULL UNIQUE REFERENCES registrations(id),
    amount_cents    BIGINT NOT NULL CHECK (amount_cents >= 0),
    currency        TEXT NOT NULL,
    issued_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (year, seq)
);

CREATE TABLE refunds (
    id              BIGSERIAL PRIMARY KEY,
    registration_id BIGINT NOT NULL UNIQUE REFERENCES registrations(id),
    percent         INT NOT NULL CHECK (percent BETWEEN 0 AND 100),
    amount_cents    BIGINT NOT NULL CHECK (amount_cents >= 0),
    currency        TEXT NOT NULL,
    rule            TEXT NOT NULL,
    created_by      BIGINT NOT NULL REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------
-- Sertifikalar
-- ---------------------------------------------------------------
CREATE TABLE certificates (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     BIGINT NOT NULL REFERENCES users(id),
    kind        TEXT NOT NULL CHECK (kind IN ('attendance','presenter')),
    holder_name TEXT NOT NULL,
    detail      TEXT NOT NULL DEFAULT '',
    signature   TEXT NOT NULL,
    issued_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, kind)
);

-- ---------------------------------------------------------------
-- İdempotans ve denetim
-- ---------------------------------------------------------------
CREATE TABLE idempotency_keys (
    user_id         BIGINT NOT NULL,
    key             TEXT NOT NULL CHECK (length(key) BETWEEN 8 AND 128),
    method          TEXT NOT NULL,
    path            TEXT NOT NULL,
    request_hash    TEXT NOT NULL,
    status_code     INT,
    response_body   BYTEA,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, key)
);

CREATE TABLE audit_log (
    id          BIGSERIAL PRIMARY KEY,
    at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_id    BIGINT,
    actor_role  TEXT NOT NULL,
    action      TEXT NOT NULL,
    entity      TEXT NOT NULL,
    entity_id   TEXT NOT NULL,
    old_value   JSONB,
    new_value   JSONB
);
CREATE INDEX audit_log_entity_idx ON audit_log (entity, entity_id, id DESC);

-- Eklemeli-yalnız: UPDATE/DELETE/TRUNCATE tetikleyiciyle reddedilir.
-- +goose StatementBegin
CREATE FUNCTION audit_log_append_only() RETURNS trigger AS $$
BEGIN
    IF current_setting('kongrehub.allow_audit_reset', true) = 'on' THEN
        RETURN NULL; -- yalnızca demo sıfırlama (TRUNCATE) için oturum bayrağı
    END IF;
    RAISE EXCEPTION 'audit_log is append-only' USING ERRCODE = 'insufficient_privilege';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER audit_log_no_update BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_append_only();
CREATE TRIGGER audit_log_no_truncate BEFORE TRUNCATE ON audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION audit_log_append_only();

-- Uygulama ayarları (demo saat kaydırma vb.)
CREATE TABLE app_settings (
    key    TEXT PRIMARY KEY,
    value  TEXT NOT NULL
);

-- +goose Down
DROP SCHEMA public CASCADE;
CREATE SCHEMA public;
