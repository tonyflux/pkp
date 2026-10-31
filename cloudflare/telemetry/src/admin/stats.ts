// What the dashboard shows for a range of days, read in one D1 batch. The
// SQL lives beside this file; Wrangler bundles *.sql as text.
import breakdownSQL from "./queries/breakdown.sql";
import dailySQL from "./queries/daily.sql";
import featuresSQL from "./queries/features.sql";
import summarySQL from "./queries/summary.sql";

const DAY_MS = 86_400_000;

export interface DayPoint {
	day: string;
	active: number;
	new: number;
}

export interface Count {
	value: string;
	installs: number;
}

export interface Feature {
	name: string;
	installs: number;
	uses: number;
}

export interface Stats {
	today: string;
	days: number;
	summary: {
		today: number;
		yesterday: number;
		wau: number;
		mau: number;
		/** Installs active at least once in the range. */
		active: number;
		/** Installs first seen in the range. */
		new: number;
		/** Installs ever seen up to today. */
		total: number;
	};
	/** Every day of the range, oldest first; days without rows read 0. */
	daily: DayPoint[];
	features: Feature[];
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
	const [daily, summary, features, breakdown] = await db.batch([
		db.prepare(dailySQL).bind(from, today),
		db.prepare(summarySQL).bind(from, today, shiftDay(today, -1), shiftDay(today, -6), shiftDay(today, -29)),
		db.prepare(featuresSQL).bind(from, today),
		db.prepare(breakdownSQL).bind(from, today),
	]);

	const byDay = new Map((daily.results as DayPoint[]).map((p) => [p.day, p]));
	const series: DayPoint[] = [];
	for (let i = 0; i < days; i++) {
		const day = shiftDay(from, i);
		const p = byDay.get(day);
		series.push({ day, active: p?.active ?? 0, new: p?.new ?? 0 });
	}
	const totals = (summary.results[0] ?? {}) as Omit<Stats["summary"], "new">;
	const parts = breakdown.results as (Count & { kind: string })[];
	const part = (kind: string): Count[] =>
		parts.filter((p) => p.kind === kind).map(({ value, installs }) => ({ value, installs }));

	return {
		today,
		days,
		summary: {
			today: totals.today ?? 0,
			yesterday: totals.yesterday ?? 0,
			wau: totals.wau ?? 0,
			mau: totals.mau ?? 0,
			active: totals.active ?? 0,
			new: series.reduce((sum, p) => sum + p.new, 0),
			total: totals.total ?? 0,
		},
		daily: series,
		features: features.results as Feature[],
		versions: part("version"),
		platforms: part("platform"),
		locales: part("locale"),
	};
}
