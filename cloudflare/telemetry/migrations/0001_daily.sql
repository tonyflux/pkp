-- One row per install and local day: that install was active that day, and
-- counts (a JSON object, feature name → times used) is its running total.
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
