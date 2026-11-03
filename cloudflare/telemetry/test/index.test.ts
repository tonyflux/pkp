import type { DatabaseSync } from "node:sqlite";
import { beforeEach, describe, expect, it } from "vitest";
import worker from "../src/index";
import { stats } from "../src/admin/stats";
import { d1, database } from "./d1";

const IncomingRequest = Request<unknown, IncomingRequestCfProperties>;
const INSTALL = "0f8e3c2a-5b1d-4e6f-9a7b-1c2d3e4f5a6b";
const OTHER = "9a1b2c3d-4e5f-4a6b-8c7d-0e1f2a3b4c5d";

/** Today and n days back as the app writes days: YYYY-MM-DD. */
const day = (back = 0) => new Date(Date.now() - back * 86_400_000).toISOString().slice(0, 10);

let db: DatabaseSync;
let allow: boolean;

function env(): Env {
	return {
		DB: d1(db),
		LIMITER: {
			async limit() {
				return { success: allow };
			},
		},
	} as Env;
}

function report(over: Record<string, unknown> = {}) {
	return {
		install: INSTALL,
		version: "1.4.0",
		os: "darwin",
		arch: "arm64",
		locale: "zh-Hans",
		days: [{ day: day(), counts: { "app.open": 1 } }],
		...over,
	};
}

function post(body: unknown, init: { method?: string; path?: string } = {}): Promise<Response> {
	const request = new IncomingRequest(`https://telemetry.test${init.path ?? "/v1/usage"}`, {
		method: init.method ?? "POST",
		headers: { "cf-connecting-ip": "203.0.113.7", "Content-Type": "application/json" },
		body: init.method === "GET" ? undefined : typeof body === "string" ? body : JSON.stringify(body),
	});
	return worker.fetch(request, env());
}

function rows() {
	return db.prepare("SELECT install_id, day, version, counts FROM daily ORDER BY install_id, day").all() as {
		install_id: string;
		day: string;
		version: string;
		counts: string;
	}[];
}

beforeEach(() => {
	db = database();
	allow = true;
});

describe("reports", () => {
	it("deduplicates repeated install/date reports and stores only daily presence", async () => {
		expect((await post(report({ days: [{ day: day(1), counts: { search: 2 } }, { day: day(), counts: { upload: 1 } }] }))).status).toBe(204);
		// A later report may contain higher event counts but never more presence.
		expect((await post(report({ version: "1.4.1", days: [{ day: day(), counts: { upload: 999, "route.files": 3 } }] }))).status).toBe(204);
		expect((await post(report({ install: OTHER }))).status).toBe(204);

		expect(rows()).toEqual([
			{ install_id: INSTALL, day: day(1), version: "1.4.0", counts: '{"app.open":1}' },
			{ install_id: INSTALL, day: day(), version: "1.4.1", counts: '{"app.open":1}' },
			{ install_id: OTHER, day: day(), version: "1.4.0", counts: '{"app.open":1}' },
		]);
		const s = await stats(d1(db), day(), 7);
		expect(s.summary).toEqual({ today: 2, yesterday: 1, active7: 2, active30: 2, active: 2, firstSeen: 2, observed: 2 });
		expect(s.daily.at(-2)).toEqual({ day: day(1), active: 1, firstSeen: 1 });
		expect(s.daily.at(-1)).toEqual({ day: day(), active: 2, firstSeen: 1 });
	});

	it("checks and discards bounded count maps without maintaining feature identifiers", async () => {
		expect((await post(report({ days: [{ day: day(), counts: { "route.secret": 12, constructor: 1, "app.open": 0 } }] }))).status).toBe(204);
		expect(rows()[0].counts).toBe('{"app.open":1}');
	});

	it("treats an accepted empty or zero map as presence, never event frequency", async () => {
		expect((await post(report({ days: [{ day: day(1), counts: {} }, { day: day(), counts: { "app.open": 0 } }] }))).status).toBe(204);
		expect(rows().map(({ counts }) => counts)).toEqual(['{"app.open":1}', '{"app.open":1}']);
	});

	it("retains historical rows when another date is reported", async () => {
		db.prepare("INSERT INTO daily VALUES (?, ?, ?, ?, ?, ?, ?, 0)").run(INSTALL, day(2), "1.3.0", "darwin", "arm64", "en", '{"search":5}');
		expect((await post(report())).status).toBe(204);
		expect(rows()).toEqual([
			{ install_id: INSTALL, day: day(2), version: "1.3.0", counts: '{"search":5}' },
			{ install_id: INSTALL, day: day(), version: "1.4.0", counts: '{"app.open":1}' },
		]);
	});

	const bad: [string, unknown][] = [
		["an install id that is not a random UUID", report({ install: "user@example.com" })],
		["a name that could carry content", report({ days: [{ day: day(), counts: { "Movie.2026.mkv": 1 } }] })],
		["a path as a name", report({ days: [{ day: day(), counts: { "route/files": 1 } }] })],
		["an excessively long name", report({ days: [{ day: day(), counts: { ["n".repeat(50)]: 1 } }] })],
		["too many entries", report({ days: [{ day: day(), counts: Object.fromEntries(Array.from({ length: 65 }, (_, i) => [`n${i}`, 1])) }] })],
		["a negative count", report({ days: [{ day: day(), counts: { upload: -1 } }] })],
		["a fractional count", report({ days: [{ day: day(), counts: { upload: 1.5 } }] })],
		["a count above the bound", report({ days: [{ day: day(), counts: { upload: 1_000_001 } }] })],
		["a nonnumeric count", report({ days: [{ day: day(), counts: { upload: "1" } }] })],
		["counts that are not an object", report({ days: [{ day: day(), counts: [] }] })],
		["a day from a wrong clock", report({ days: [{ day: "2001-01-01", counts: { upload: 1 } }] })],
		["a day in the future", report({ days: [{ day: day(-3), counts: { upload: 1 } }] })],
		["a date that does not exist", report({ days: [{ day: "2026-02-31", counts: { upload: 1 } }] })],
		["the same day twice", report({ days: [{ day: day(), counts: {} }, { day: day(), counts: {} }] })],
		["no days", report({ days: [] })],
		["too many dates", report({ days: Array.from({ length: 32 }, (_, i) => ({ day: day(i), counts: { "app.open": 1 } })) })],
		["an unknown system", report({ os: "<script>" })],
		["not JSON", "{"],
	];
	it.each(bad)("refuses %s and keeps nothing", async (_, body) => {
		const res = await post(body);
		expect(res.status).toBe(400);
		expect(rows()).toEqual([]);
	});

	it("refuses a body past 32 KiB without keeping any of it", async () => {
		const counts = Object.fromEntries(Array.from({ length: 4000 }, (_, i) => [`n${i}`, 1]));
		const res = await post(report({ days: [{ day: day(), counts }] }));
		expect(res.status).toBe(413);
		expect(rows()).toEqual([]);
	});

	it("serves only POST /v1/usage, within the rate limit", async () => {
		expect((await post(null, { method: "GET" })).status).toBe(405);
		expect((await post(report(), { path: "/v1/other" })).status).toBe(404);
		allow = false;
		const limited = await post(report());
		expect(limited.status).toBe(429);
		expect(limited.headers.get("Retry-After")).toBe("60");
		expect(rows()).toEqual([]);
	});

	// The Worker owns its whole Custom Domain: every other path, old TMDB
	// reads included, is a JSON 404 that keeps nothing and is not cached.
	it.each(["/", "/3/configuration?language=zh-CN", "/favicon.ico", "/v1/usage/"])("answers %s with a JSON 404", async (path) => {
		const res = await post(null, { method: "GET", path });
		expect(res.status).toBe(404);
		expect(res.headers.get("Cache-Control")).toBe("no-store");
		expect(await res.json()).toEqual({ error: "not_found", message: "Nothing is served here." });
		expect(rows()).toEqual([]);
	});
});
