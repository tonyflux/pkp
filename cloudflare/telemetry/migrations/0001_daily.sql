-- One row per install and local day: that installation was active that day.
-- New reports keep only {"app.open":1}; historical count maps remain untouched.
CREATE TABLE daily (
	install_id TEXT NOT NULL,
	day TEXT NOT NULL,
	version TEXT NOT NULL,
	os TEXT NOT NULL,
	arch TEXT NOT NULL,
	locale TEXT NOT NULL,
	counts TEXT NOT NULL,
	-- Unix seconds of the last copy received.
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (install_id, day)
) WITHOUT ROWID;

CREATE INDEX daily_by_day ON daily (day);
