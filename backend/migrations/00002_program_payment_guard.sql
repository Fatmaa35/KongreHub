-- +goose Up
-- Savunma derinliği: bir bildiri ancak kabul edilmişse ve en az bir yazarı
-- kayıt ücretini ödemişse programa eklenebilir (T-5 kuralı uygulama katmanında
-- da kontrol edilir; bu tetikleyici yanlışlıkla atlanmasını engeller).
-- +goose StatementBegin
CREATE FUNCTION session_paper_guard() RETURNS trigger AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM submissions WHERE id = NEW.submission_id AND status = 'accepted') THEN
        RAISE EXCEPTION 'submission % is not accepted', NEW.submission_id USING ERRCODE = 'check_violation';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM submission_authors sa
        JOIN registrations r ON r.user_id = sa.user_id AND r.status = 'paid'
        WHERE sa.submission_id = NEW.submission_id
    ) THEN
        RAISE EXCEPTION 'submission % has no author with a paid registration', NEW.submission_id USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER session_papers_guard BEFORE INSERT OR UPDATE ON session_papers
    FOR EACH ROW EXECUTE FUNCTION session_paper_guard();

-- +goose Down
DROP TRIGGER IF EXISTS session_papers_guard ON session_papers;
DROP FUNCTION IF EXISTS session_paper_guard();
