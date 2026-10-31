/**
 * PKP's anonymous usage statistics. Each install posts, about once an hour, its running
 * counts per local day: how often each feature was used. One row per
 * (install, day) is kept and replaced by every later copy, so a repeat or a
 * retry never counts twice; a day's row existing is that install being
 * active that day.
 *
 * The install id is a random UUID the app forgets when the user switches
 * statistics off. Counts have fixed names and the address is used for rate
 * limiting, not stored in the statistics database. Reports are unauthenticated
 * and can be forged; rate limiting does not establish their authenticity.
 */

const MAX_BODY_BYTES = 32 * 1024;
const MAX_DAYS = 31;
const MAX_NAMES = 64;
const MAX_COUNT = 1_000_000;
// A local day may run a day ahead of UTC; one past this many days back is
// a clock that is wrong, not a backlog.
const PAST_DAYS = 35;
const DAY_MS = 86_400_000;

const INSTALL = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const VERSION = /^[0-9A-Za-z][0-9A-Za-z.+-]{0,31}$/;
const OS: Record<string, true> = { darwin: true, windows: true, linux: true };
const ARCH: Record<string, true> = { arm64: true, amd64: true };
const LOCALE = /^[a-z]{2}(-[A-Za-z]{2,4})?$/;
const DAY = /^[0-9]{4}-[0-9]{2}-[0-9]{2}$/;
// Keep in sync with telemetryNames in internal/services/telemetry.go.
const NAMES: Record<string, true> = {
	"app.open": true, "app_lock.on": true,
	"route.files": true, "route.transfers": true, "route.player": true, "route.shares": true, "route.settings": true,
	"route.viewer": true, "route.assistant": true, "route.cleanup": true,
	"play.video": true, "play.audio": true, upload: true, download: true, cloud_add: true, "viewer.open": true,
	"scope.drive": true, search: true, "share.create": true, "assistant.ask": true, "organize.run": true,
	"organize.undo": true, "cleanup.scan": true, "cleanup.remove": true, "menubar.open": true,
};

/** One install's report, as checked. */
export interface Report {
	install: string;
	version: string;
	os: string;
	arch: string;
	locale: string;
	days: { day: string; counts: Record<string, number> }[];
}

/** A JSON error the app can read, never echoing what was sent. */
function problem(status: number, error: string, message: string, headers: Record<string, string> = {}): Response {
	return Response.json({ error, message }, { status, headers: { ...headers, "Cache-Control": "no-store" } });
}

function isObject(v: unknown): v is Record<string, unknown> {
	return typeof v === "object" && v !== null && !Array.isArray(v);
}

function isDay(v: unknown, now: number): v is string {
	if (typeof v !== "string" || !DAY.test(v)) return false;
	const t = Date.parse(`${v}T00:00:00Z`);
	// Date.parse rolls 2026-02-31 over; a real date reads back the same.
	if (Number.isNaN(t) || new Date(t).toISOString().slice(0, 10) !== v) return false;
	return t >= now - PAST_DAYS * DAY_MS && t <= now + DAY_MS;
}

/** The report in body, or why it is not one. */
export function parseReport(body: unknown, now: number): Report | string {
	if (!isObject(body)) return "the body is not a JSON object";
	const { install, version, os, arch, locale, days } = body;
	if (typeof install !== "string" || !INSTALL.test(install)) return "install";
	if (typeof version !== "string" || !VERSION.test(version)) return "version";
	if (typeof os !== "string" || !Object.hasOwn(OS, os)) return "os";
	if (typeof arch !== "string" || !Object.hasOwn(ARCH, arch)) return "arch";
	if (typeof locale !== "string" || !LOCALE.test(locale)) return "locale";
	if (!Array.isArray(days) || days.length === 0 || days.length > MAX_DAYS) return "days";
	const seen = new Set<string>();
	const checked: Report["days"] = [];
	for (const d of days) {
		if (!isObject(d) || !isDay(d.day, now) || seen.has(d.day)) return "days[].day";
		seen.add(d.day);
		if (!isObject(d.counts)) return "days[].counts";
		const entries = Object.entries(d.counts);
		if (entries.length > MAX_NAMES) return "days[].counts";
		const counts: Record<string, number> = {};
		for (const [name, n] of entries) {
			if (!Object.hasOwn(NAMES, name) || !Number.isSafeInteger(n) || (n as number) < 0 || (n as number) > MAX_COUNT) {
				return "days[].counts";
			}
			counts[name] = n as number;
		}
		checked.push({ day: d.day, counts });
	}
	return { install, version, os, arch, locale, days: checked };
}

/** The body as text, or null once it passes MAX_BODY_BYTES. */
async function readLimited(body: ReadableStream<Uint8Array> | null): Promise<string | null> {
	if (!body) return "";
	const reader = body.getReader();
	const chunks: Uint8Array[] = [];
	let size = 0;
	for (;;) {
		const { done, value } = await reader.read();
		if (done) break;
		size += value.byteLength;
		if (size > MAX_BODY_BYTES) {
			await reader.cancel();
			return null;
		}
		chunks.push(value);
	}
	const all = new Uint8Array(size);
	let at = 0;
	for (const c of chunks) {
		all.set(c, at);
		at += c.byteLength;
	}
	return new TextDecoder().decode(all);
}

const UPSERT = `INSERT INTO daily (install_id, day, version, os, arch, locale, counts, updated_at)
VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)
ON CONFLICT (install_id, day) DO UPDATE SET
	version = excluded.version, os = excluded.os, arch = excluded.arch, locale = excluded.locale,
	counts = excluded.counts, updated_at = excluded.updated_at`;

/** Keeps a report: every day it names replaces that day's earlier copy. */
export async function store(db: D1Database, r: Report, now: number): Promise<void> {
	const at = Math.floor(now / 1000);
	await db.batch(
		r.days.map((d) =>
			db.prepare(UPSERT).bind(r.install, d.day, r.version, r.os, r.arch, r.locale, JSON.stringify(d.counts), at),
		),
	);
}

export default {
	async fetch(request, env): Promise<Response> {
		const url = new URL(request.url);
		if (url.pathname !== "/v1/usage") {
			return problem(404, "not_found", "Nothing is served here.");
		}
		if (request.method !== "POST") {
			return problem(405, "method_not_allowed", "Only POST is served.", { Allow: "POST" });
		}
		const { success } = await env.LIMITER.limit({ key: request.headers.get("cf-connecting-ip") ?? "" });
		if (!success) {
			return problem(429, "rate_limited", "Too many reports; try again later.", { "Retry-After": "60" });
		}
		const declared = Number(request.headers.get("content-length") ?? "0");
		if (declared > MAX_BODY_BYTES) {
			return problem(413, "too_large", "The report is too large.");
		}
		const text = await readLimited(request.body);
		if (text === null) {
			return problem(413, "too_large", "The report is too large.");
		}
		let body: unknown;
		try {
			body = JSON.parse(text);
		} catch {
			return problem(400, "bad_json", "The body is not JSON.");
		}
		const now = Date.now();
		const report = parseReport(body, now);
		if (typeof report === "string") {
			return problem(400, "bad_report", `The report's ${report} is not valid.`);
		}
		try {
			await store(env.DB, report, now);
		} catch (err) {
			console.error("D1 did not keep a report", err instanceof Error ? err.message : "error");
			return problem(503, "unavailable", "The report was not kept; send it again later.");
		}
		return new Response(null, { status: 204 });
	},
} satisfies ExportedHandler<Env>;
