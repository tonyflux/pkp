import type { DatabaseSync } from "node:sqlite";
import { beforeEach, describe, expect, it, vi } from "vitest";
import admin, { todayFor } from "../src/admin/index";
import { stats } from "../src/admin/stats";
import { d1, database } from "./d1";

const A = "0f8e3c2a-5b1d-4e6f-9a7b-1c2d3e4f5a6b";
const B = "9a1b2c3d-4e5f-4a6b-8c7d-0e1f2a3b4c5d";
const C = "1b2c3d4e-5f6a-4b7c-9d8e-0f1a2b3c4d5e";
const TODAY = "2026-10-18";

let db: DatabaseSync;

function row(install: string, day: string, counts: Record<string, number>, over: Partial<{ version: string; os: string; arch: string; locale: string }> = {}) {
	const r = { version: "1.0.0", os: "darwin", arch: "arm64", locale: "zh-Hans", ...over };
	db.prepare("INSERT INTO daily VALUES (?, ?, ?, ?, ?, ?, ?, 0)").run(install, day, r.version, r.os, r.arch, r.locale, JSON.stringify(counts));
}

/** A request as it reaches the Worker: through Access (signed in as email) or not. */
function call(path: string, email: string | null = "owner@example.com"): Promise<Response> {
	const ctx = {
		waitUntil() {},
		passThroughOnException() {},
		props: {},
		access: email === null ? undefined : { aud: "test", getIdentity: async () => ({ email }) },
	} as unknown as ExecutionContext;
	return admin.fetch(new Request(`https://admin.test${path}`), { DB: d1(db) }, ctx);
}

beforeEach(() => {
	db = database();
	// A: seen first a month ago, active yesterday and today, upgraded today.
	row(A, "2026-09-10", { search: 1 });
	row(A, "2026-10-17", { search: 3, "route.files": 2 });
	row(A, "2026-10-18", { search: 1, upload: 2 }, { version: "1.1.0" });
	// B: new today on Windows, in English.
	row(B, "2026-10-18", { upload: 1, "route.shares": 1 }, { os: "windows", arch: "amd64", locale: "en" });
	// C: new yesterday; a day after today (a clock ahead) is not counted.
	row(C, "2026-10-17", { "play.video": 4 });
	row(C, "2026-10-19", { "play.video": 9 });
});

describe("stats", () => {
	it("draws every day of the range, oldest first, with active and new installs", async () => {
		const s = await stats(d1(db), TODAY, 7);
		expect(s.daily.map((p) => p.day)).toEqual(["2026-10-12", "2026-10-13", "2026-10-14", "2026-10-15", "2026-10-16", "2026-10-17", "2026-10-18"]);
		expect(s.daily.at(-2)).toEqual({ day: "2026-10-17", active: 2, new: 1 });
		expect(s.daily.at(-1)).toEqual({ day: "2026-10-18", active: 2, new: 1 });
		expect(s.daily[0]).toEqual({ day: "2026-10-12", active: 0, new: 0 });
	});

	it("sums the headline numbers over their own windows", async () => {
		const s = await stats(d1(db), TODAY, 7);
		expect(s.summary).toEqual({ today: 2, yesterday: 2, wau: 3, mau: 3, active: 3, new: 2, total: 3 });
		// A's first day is outside 7 days but inside 90: new there, and in the month.
		const q = await stats(d1(db), TODAY, 90);
		expect(q.summary.new).toBe(3);
	});

	it("ranks features by installs, then uses, then name", async () => {
		const s = await stats(d1(db), TODAY, 7);
		expect(s.features).toEqual([
			{ name: "upload", installs: 2, uses: 3 },
			{ name: "play.video", installs: 1, uses: 4 },
			{ name: "search", installs: 1, uses: 4 },
			{ name: "route.files", installs: 1, uses: 2 },
			{ name: "route.shares", installs: 1, uses: 1 },
		]);
	});

	it("counts an install once, as of its latest day, in the breakdowns", async () => {
		const s = await stats(d1(db), TODAY, 7);
		// A ran 1.0.0 yesterday and 1.1.0 today: one install, on 1.1.0.
		expect(s.versions).toEqual([
			{ value: "1.0.0", installs: 2 },
			{ value: "1.1.0", installs: 1 },
		]);
		expect(s.platforms).toEqual([
			{ value: "darwin/arm64", installs: 2 },
			{ value: "windows/amd64", installs: 1 },
		]);
		expect(s.locales).toEqual([
			{ value: "zh-Hans", installs: 2 },
			{ value: "en", installs: 1 },
		]);
	});
});

describe("the admin Worker", () => {
	it("refuses everything that did not come through Access", async () => {
		for (const path of ["/", "/api/stats?days=7"]) {
			const res = await call(path, null);
			expect(res.status).toBe(403);
			expect(await res.text()).not.toContain("search");
		}
	});

	it("serves the page with a fresh nonce its CSP names", async () => {
		const [one, two] = await Promise.all([call("/"), call("/")]);
		const csp = one.headers.get("Content-Security-Policy") ?? "";
		const nonce = /'nonce-([0-9a-f]{32})'/.exec(csp)?.[1];
		expect(nonce).toBeTruthy();
		const html = await one.text();
		expect(html).toContain(`<script nonce="${nonce}">`);
		expect(html).not.toContain("{{nonce}}");
		expect(two.headers.get("Content-Security-Policy")).not.toBe(csp);
	});

	it("answers stats for the viewer's day and only the offered ranges", async () => {
		const clock = vi.spyOn(Date, "now").mockReturnValue(Date.parse(`${TODAY}T12:00:00Z`));
		try {
			const res = await call(`/api/stats?days=7&today=${TODAY}`);
			expect(res.status).toBe(200);
			const body = (await res.json()) as { today: string; viewer: string; summary: { today: number } };
			expect(body.viewer).toBe("owner@example.com");
			expect(body.today).toBe(TODAY);
			expect(body.summary.today).toBe(2);
			expect((await call("/api/stats?days=365")).status).toBe(400);
			expect((await call("/api/stats", "o@example.com").then((r) => r.json()) as { days: number }).days).toBe(30);
		} finally {
			clock.mockRestore();
		}
	});

	it("takes the viewer's date only within a day of UTC", () => {
		const now = Date.parse("2026-10-18T20:00:00Z");
		expect(todayFor("2026-10-19", now)).toBe("2026-10-19");
		expect(todayFor("2026-10-17", now)).toBe("2026-10-17");
		expect(todayFor("2026-10-21", now)).toBe("2026-10-18");
		expect(todayFor("2026-02-31", now)).toBe("2026-10-18");
		expect(todayFor(null, now)).toBe("2026-10-18");
	});
});
