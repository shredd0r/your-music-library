-- Jobs whose plugin is offline or disabled wait without using attempts.
-- waiting_since: unix seconds when the job started waiting, 0 = not waiting.
ALTER TABLE queue_jobs ADD COLUMN waiting_since INTEGER NOT NULL DEFAULT 0;
