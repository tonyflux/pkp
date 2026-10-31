-- ?1 first day of the range, ?2 today, ?3 yesterday, ?4 six days back, ?5 29 days back.
SELECT
	(SELECT count(*) FROM daily WHERE day = ?2) AS today,
	(SELECT count(*) FROM daily WHERE day = ?3) AS yesterday,
	(SELECT count(DISTINCT install_id) FROM daily WHERE day BETWEEN ?4 AND ?2) AS wau,
	(SELECT count(DISTINCT install_id) FROM daily WHERE day BETWEEN ?5 AND ?2) AS mau,
	(SELECT count(DISTINCT install_id) FROM daily WHERE day BETWEEN ?1 AND ?2) AS active,
	(SELECT count(DISTINCT install_id) FROM daily WHERE day <= ?2) AS total;
