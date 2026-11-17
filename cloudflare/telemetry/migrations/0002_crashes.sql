-- Crash reports are separate from usage data; the daily cron removes them after 29 days.
CREATE TABLE crashes (
 id TEXT PRIMARY KEY NOT NULL,
 report TEXT NOT NULL,
 received_at INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX crashes_by_received ON crashes (received_at, id);
