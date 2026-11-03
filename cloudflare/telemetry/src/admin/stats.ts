// What the dashboard shows for a range of days, read in one D1 batch. The
// SQL lives beside this file; Wrangler bundles *.sql as text.
import breakdownSQL from "./queries/breakdown.sql";
import dailySQL from "./queries/daily.sql";
import summarySQL from "./queries/summary.sql";

const DAY_MS = 86_400_000;

export interface DayPoint {
	day: string;
	active: number;
	firstSeen: number;
}

export interface Count {
	value: string;
	installs: number;
}

export interface Stats {
	today: string;
	days: number;
	summary: {
		today: number;
		yesterday: number;
		active7: number;
		active30: number;
		/** Observed opt-in IDs active at least once in the range. */
		active: number;
		/** IDs with their earliest reported local date in the range, not new installs. */
		firstSeen: number;
		/** Distinct reported IDs through today, not actual installs or people. */
		observed: number;
	};
	/** Every day of the range, oldest first; days without rows read 0. */
	daily: DayPoint[];
	versions: Count[];
	platforms: Count[];
	locales: Count[];
}

/** The day n days after day (n may be negative); days are YYYY-MM-DD. */
export function shiftDay(day: string, n: number): string {
	return new Date(Date.parse(`${day}T00:00:00Z`) + n * DAY_MS).toISOString().slice(0, 10);
}

export async function stats(db: D1Database, today: string, days: number): Promise<Stats> {
	const from = shiftDay(today, 1 - days);
	const [daily, summary, breakdown] = await db.batch([
		db.prepare(dailySQL).bind(from, today),
		db.prepare(summarySQL).bind(from, today, shiftDay(today, -1), shiftDay(today, -6), shiftDay(today, -29)),
		db.prepare(breakdownSQL).bind(from, today),
	]);

	const byDay = new Map((daily.results as DayPoint[]).map((p) => [p.day, p]));
	const series: DayPoint[] = [];
	for (let i = 0; i < days; i++) {
		const day = shiftDay(from, i);
		const p = byDay.get(day);
		series.push({ day, active: p?.active ?? 0, firstSeen: p?.firstSeen ?? 0 });
	}
	const totals = (summary.results[0] ?? {}) as Omit<Stats["summary"], "firstSeen">;
	const parts = breakdown.results as (Count & { kind: string })[];
	const part = (kind: string): Count[] =>
		parts.filter((p) => p.kind === kind).map(({ value, installs }) => ({ value, installs }));

	return {
		today,
		days,
		summary: {
			today: totals.today ?? 0,
			yesterday: totals.yesterday ?? 0,
			active7: totals.active7 ?? 0,
			active30: totals.active30 ?? 0,
			active: totals.active ?? 0,
			firstSeen: series.reduce((sum, p) => sum + p.firstSeen, 0),
			observed: totals.observed ?? 0,
		},
		daily: series,
		versions: part("version"),
		platforms: part("platform"),
		locales: part("locale"),
	};
}
