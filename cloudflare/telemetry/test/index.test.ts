import type { DatabaseSync } from "node:sqlite";
import { beforeEach, describe, expect, it } from "vitest";
import worker from "../src/index";
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
		days: [{ day: day(), counts: { "route.files": 3, "play.video": 1 } }],
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
	it("keeps one row per install and day, the last copy winning", async () => {
		expect((await post(report({ days: [{ day: day(1), counts: { search: 2 } }, { day: day(), counts: { upload: 1 } }] }))).status).toBe(204);
		// The hourly resend of today, grown: replaced, not added to.
		expect((await post(report({ version: "1.4.1", days: [{ day: day(), counts: { upload: 3 } }] }))).status).toBe(204);
		expect((await post(report({ install: OTHER }))).status).toBe(204);

		expect(rows()).toEqual([
			{ install_id: INSTALL, day: day(1), version: "1.4.0", counts: '{"search":2}' },
			{ install_id: INSTALL, day: day(), version: "1.4.1", counts: '{"upload":3}' },
			{ install_id: OTHER, day: day(), version: "1.4.0", counts: '{"route.files":3,"play.video":1}' },
		]);
	});

	const bad: [string, unknown][] = [
		["an install id that is not a random UUID", report({ install: "user@example.com" })],
		["a name that could carry content", report({ days: [{ day: day(), counts: { "Movie.2026.mkv": 1 } }] })],
		["a path as a name", report({ days: [{ day: day(), counts: { "route/files": 1 } }] })],
		["an unknown but well-formed name", report({ days: [{ day: day(), counts: { "route.secret": 1 } }] })],
		["a prototype property as a name", report({ days: [{ day: day(), counts: JSON.parse('{"constructor":1}') }] })],
		["a negative count", report({ days: [{ day: day(), counts: { upload: -1 } }] })],
		["a fractional count", report({ days: [{ day: day(), counts: { upload: 1.5 } }] })],
		["a day from a wrong clock", report({ days: [{ day: "2001-01-01", counts: { upload: 1 } }] })],
		["a day in the future", report({ days: [{ day: day(-3), counts: { upload: 1 } }] })],
		["a date that does not exist", report({ days: [{ day: "2026-02-31", counts: { upload: 1 } }] })],
		["the same day twice", report({ days: [{ day: day(), counts: {} }, { day: day(), counts: {} }] })],
		["no days", report({ days: [] })],
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
