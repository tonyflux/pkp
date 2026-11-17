import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DatabaseSync } from "node:sqlite";
import worker from "../src/index";
import admin from "../src/admin/index";
import { database, d1 } from "./d1";

const ID = "0f8e3c2a-5b1d-4e6f-9a7b-1c2d3e4f5a6b";
let db: DatabaseSync;
let allowed: boolean;
const env = () => ({ DB: d1(db), LIMITER: { limit: async () => ({ success: allowed }) } }) as unknown as Env;
function post(body: string, contentType = "application/json") {
 return worker.fetch(new Request("https://telemetry.test/v1/crashes", { method: "POST", headers: { "content-type": contentType, "cf-connecting-ip": "192.0.2.1" }, body }) as Request<unknown, IncomingRequestCfProperties>, env());
}
function call(path: string, method = "GET", authorized = true, headers: Record<string, string> = {}) {
 const ctx = { access: authorized ? { getIdentity: async () => ({ email: "owner@example.com" }) } : undefined } as unknown as ExecutionContext;
 return admin.fetch(new Request(`https://admin.test${path}`, { method, headers }), { DB: d1(db) }, ctx);
}
beforeEach(() => { db = database(); allowed = true; });

describe("crash reports", () => {
 it("accepts once, retries identically, and refuses conflicting content without exposing it", async () => {
  const secret = "<script>private token</script>";
  const body = JSON.stringify({ id: ID, report: secret });
  const first = await post(body);
  expect(first.status).toBe(204);
  expect(first.headers.get("location")).toBeNull();
  expect((await post(body)).status).toBe(204);
  const conflict = await post(JSON.stringify({ id: ID, report: "other secret" }));
  expect(conflict.status).toBe(409);
  expect(await conflict.text()).not.toContain("secret");
  expect(db.prepare("SELECT * FROM crashes").all()).toEqual([{ id: ID, report: secret, received_at: expect.any(Number) }]);
  expect(db.prepare("SELECT * FROM daily").all()).toEqual([]);
 });
 it("validates type, UUID, text, UTF-8, and both byte limits", async () => {
  for (const value of [{ id: "bad", report: "secret" }, { id: ID, report: "" }, { id: ID, report: "   " }, { id: ID, report: "secret", extra: "ignored" }, { id: ID, report: 1 }, { id: ID, report: "\ud800" }, null]) {
   const response = await post(JSON.stringify(value));
   expect(response.status).toBe(400);
   expect(await response.text()).not.toContain("secret");
  }
  expect((await post("{}", "text/plain")).status).toBe(415);
  expect((await post(JSON.stringify({ id: ID, report: "é".repeat(60 * 1024 + 1) }))).status).toBe(413);
  expect((await post(JSON.stringify({ id: ID, report: "x", extra: "x".repeat(128 * 1024) }))).status).toBe(413);
  const invalid = await worker.fetch(new Request("https://telemetry.test/v1/crashes", { method: "POST", headers: { "content-type": "application/json" }, body: new Uint8Array([0xff]) }) as Request<unknown, IncomingRequestCfProperties>, env());
  expect(invalid.status).toBe(400);
  expect(db.prepare("SELECT * FROM crashes").all()).toEqual([]);
 });
 it("uses the existing limiter", async () => {
  allowed = false;
  expect((await post(JSON.stringify({ id: ID, report: "secret" }))).status).toBe(429);
  expect(db.prepare("SELECT * FROM crashes").all()).toEqual([]);
 });
 it("protects lists, details and deletion with Access and same-origin checks", async () => {
  await post(JSON.stringify({ id: ID, report: "private" }));
  for (const path of ["/crashes", "/api/crashes", `/api/crashes/${ID}`]) expect((await call(path, "GET", false)).status).toBe(403);
  expect((await call(`/api/crashes/${ID}`, "DELETE", false)).status).toBe(403);
  expect((await call(`/api/crashes/${ID}`, "DELETE", true, { origin: "https://evil.test", "sec-fetch-site": "cross-site" })).status).toBe(403);
  expect((await call(`/api/crashes/${ID}`, "DELETE")).status).toBe(403);
  const list = await call("/api/crashes");
  expect(list.headers.get("cache-control")).toBe("no-store");
  expect(await list.text()).not.toContain("private");
  expect(await (await call(`/api/crashes/${ID}`)).json()).toMatchObject({ report: "private" });
  expect((await call(`/api/crashes/${ID}`, "DELETE", true, { origin: "https://admin.test", "sec-fetch-site": "same-origin" })).status).toBe(204);
  expect((await call(`/api/crashes/${ID}`)).status).toBe(404);
 });
 it("paginates metadata without report text and serves the hardened dashboard", async () => {
  for (let n = 0; n < 21; n++) {
   const id = `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
   db.prepare("INSERT INTO crashes VALUES (?, ?, ?)").run(id, "secret", n);
  }
  const first = await (await call("/api/crashes?page=0")).json() as { reports: unknown[]; more: boolean };
  const second = await (await call("/api/crashes?page=1")).json() as { reports: unknown[]; more: boolean };
  expect(first.reports).toHaveLength(20); expect(first.more).toBe(true);
  expect(second.reports).toHaveLength(1); expect(second.more).toBe(false);
  expect((await call("/api/crashes?page=-1")).status).toBe(400);
  const page = await call("/crashes");
  expect(page.headers.get("cache-control")).toBe("no-store");
  expect(page.headers.get("content-security-policy")).toContain("default-src 'none'");
  expect(await page.text()).not.toContain("secret");
 });
 it("purges old reports on schedule without deleting recent ones", async () => {
  const clock = vi.spyOn(Date, "now").mockReturnValue(100 * 86400 * 1000);
  try {
   db.prepare("INSERT INTO crashes VALUES (?, ?, ?)").run(ID, "old", 70 * 86400);
   db.prepare("INSERT INTO crashes VALUES (?, ?, ?)").run("9a1b2c3d-4e5f-4a6b-8c7d-0e1f2a3b4c5d", "recent", 99 * 86400);
   await worker.scheduled({} as ScheduledController, env());
   expect(db.prepare("SELECT report FROM crashes").all()).toEqual([{ report: "recent" }]);
  } finally { clock.mockRestore(); }
 });
});
