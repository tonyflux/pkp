-- Each day from ?1 to ?2 that has rows: installs active that day, and how
-- many of them were seen for the first time that day (new installs).
SELECT d.day, count(*) AS active, sum(f.first_day = d.day) AS new
FROM daily AS d
JOIN (SELECT install_id, min(day) AS first_day FROM daily GROUP BY install_id) AS f USING (install_id)
WHERE d.day BETWEEN ?1 AND ?2
GROUP BY d.day
ORDER BY d.day;
