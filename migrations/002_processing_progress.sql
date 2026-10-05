ALTER TABLE processing_jobs
    ADD COLUMN IF NOT EXISTS progress INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS stage TEXT NOT NULL DEFAULT 'queued';

ALTER TABLE processing_jobs
    DROP CONSTRAINT IF EXISTS processing_jobs_progress_check;

ALTER TABLE processing_jobs
    ADD CONSTRAINT processing_jobs_progress_check CHECK (progress BETWEEN 0 AND 100);
