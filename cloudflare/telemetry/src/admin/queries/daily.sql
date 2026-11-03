-- Each day from ?1 to ?2 with rows: active installations and IDs whose
-- earliest reported local date is that day, not verified new installations.
SELECT d.day, count(*) AS active, sum(f.first_day = d.day) AS firstSeen
FROM daily AS d
JOIN (SELECT install_id, min(day) AS first_day FROM daily GROUP BY install_id) AS f USING (install_id)
WHERE d.day BETWEEN ?1 AND ?2
GROUP BY d.day
ORDER BY d.day;
