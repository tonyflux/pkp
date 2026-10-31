-- Every counted name in the range: how many installs used it (who is
-- interested) and how often in all (heavy users weigh more here).
SELECT c.key AS name, count(DISTINCT d.install_id) AS installs, sum(c.value) AS uses
FROM daily AS d, json_each(d.counts) AS c
WHERE d.day BETWEEN ?1 AND ?2
GROUP BY c.key
ORDER BY installs DESC, uses DESC, name;
