-- Installs active in the range by version, platform and language, each as
-- of its latest day there: an install that upgraded counts once, for the
-- version it runs now. (With one max(), SQLite takes the bare columns from
-- the row holding it.)
WITH latest AS (
	SELECT install_id, version, os, arch, locale, max(day) AS day
	FROM daily
	WHERE day BETWEEN ?1 AND ?2
	GROUP BY install_id
)
SELECT 'version' AS kind, version AS value, count(*) AS installs FROM latest GROUP BY version
UNION ALL
SELECT 'platform', os || '/' || arch, count(*) FROM latest GROUP BY os, arch
UNION ALL
SELECT 'locale', locale, count(*) FROM latest GROUP BY locale
ORDER BY kind, installs DESC, value;
