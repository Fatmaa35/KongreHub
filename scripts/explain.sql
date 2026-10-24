-- README'deki EXPLAIN (ANALYZE) ölçümleri. Her sorgu önce dizinle, sonra dizin
-- transaction içinde düşürülüp (ROLLBACK ile geri alınarak) dizinsiz ölçülür.
\echo '=== Q1: hatırlatma job''ı — vadesi gelen açık atamalar (review_assignments_open_due_idx, kısmi dizin) ==='
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) SELECT id, submission_id, round, assigned_at FROM review_assignments
 WHERE status='assigned' AND assigned_at <= now() - interval '7 days' AND id > 0 ORDER BY id LIMIT 100;
EXPLAIN (ANALYZE, COSTS OFF) SELECT count(*) FROM review_assignments WHERE status='assigned' AND due_at < now();
BEGIN;
DROP INDEX review_assignments_open_due_idx;
\echo '--- dizinsiz ---'
EXPLAIN (ANALYZE, COSTS OFF) SELECT count(*) FROM review_assignments WHERE status='assigned' AND due_at < now();
ROLLBACK;

\echo '=== Q2: editör listesi — duruma göre keyset sayfalama (submissions_status_id_idx) ==='
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) SELECT id, title, status FROM submissions
 WHERE status='under_review' AND id < 150000 ORDER BY id DESC LIMIT 51;
BEGIN;
DROP INDEX submissions_status_id_idx;
\echo '--- dizinsiz ---'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) SELECT id, title, status FROM submissions
 WHERE status='under_review' AND id < 150000 ORDER BY id DESC LIMIT 51;
ROLLBACK;

\echo '=== Q3: hakemin açık atamaları (review_assignments_reviewer_idx) ==='
EXPLAIN (ANALYZE, COSTS OFF) SELECT id FROM review_assignments WHERE reviewer_id=(SELECT min(user_id) FROM reviewer_profiles) AND status='assigned';
BEGIN;
DROP INDEX review_assignments_reviewer_idx;
\echo '--- dizinsiz ---'
EXPLAIN (ANALYZE, COSTS OFF) SELECT id FROM review_assignments WHERE reviewer_id=(SELECT min(user_id) FROM reviewer_profiles) AND status='assigned';
ROLLBACK;

\echo '=== Q2b: seyrek durum (revision_requested, ~10 satır) — dizinin asıl kazandırdığı yer ==='
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) SELECT id, title, status FROM submissions
 WHERE status='revision_requested' ORDER BY id DESC LIMIT 51;
BEGIN;
DROP INDEX submissions_status_id_idx;
\echo '--- dizinsiz ---'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) SELECT id, title, status FROM submissions
 WHERE status='revision_requested' ORDER BY id DESC LIMIT 51;
ROLLBACK;
