/**
 * PKP opt-in active installation dashboard over stored daily reports.
 * A Worker of its own so that the reports' endpoint can stay public while
 * this one sits wholly behind Cloudflare Access (Workers & Pages >
 * pkp-telemetry-admin > Access). It answers nothing a request Access did
 * not authenticate: without Access turned on, every request is refused.
 */
import page from "./page.html";
import crashesPage from "./crashes.html";
import { shiftDay, stats } from "./stats";

const DAY = /^[0-9]{4}-[0-9]{2}-[0-9]{2}$/;
const DAY_MS = 86_400_000;

function problem(status: number, error: string, message: string): Response {
	return Response.json({ error, message }, { status, headers: { "Cache-Control": "no-store" } });
}

/**
 * The day the dashboard counts as today: the viewer's own date, which the
 * page sends, as long as it is within a day of UTC; otherwise UTC's.
 */
export function todayFor(asked: string | null, now: number): string {
	const utc = new Date(now).toISOString().slice(0, 10);
	if (!asked || !DAY.test(asked) || shiftDay(asked, 0) !== asked) return utc;
	return Math.abs(Date.parse(`${asked}T00:00:00Z`) - Date.parse(`${utc}T00:00:00Z`)) <= DAY_MS ? asked : utc;
}

const REFUSED = `<!doctype html><meta charset="utf-8"><title>PKP Active Installations</title>
<p style="font:15px system-ui;margin:48px">This dashboard is available only behind Cloudflare Access. Enable Access for
Workers &amp; Pages › pkp-telemetry-admin › Access in the Cloudflare dashboard.</p>`;

export default {
	async fetch(request, env, ctx): Promise<Response> {
		const url = new URL(request.url);
		if (!ctx.access) {
			return url.pathname.startsWith("/api/")
				? problem(403, "access_required", "Sign in through Cloudflare Access.")
				: new Response(REFUSED, { status: 403, headers: { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" } });
		}
		const match = /^\/api\/crashes\/([0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12})$/.exec(url.pathname);
		if (request.method === "DELETE" && match) {
			if (request.headers.get("Origin") !== url.origin || request.headers.get("Sec-Fetch-Site") !== "same-origin") {
				return problem(403, "forbidden", "Same-origin request required.");
			}
			await env.DB.batch([env.DB.prepare("DELETE FROM crashes WHERE id = ?").bind(match[1])]);
			return new Response(null, { status: 204, headers: { "Cache-Control": "no-store" } });
		}
		if (request.method !== "GET") {
			return problem(405, "method_not_allowed", "Only GET is served.");
		}

		if (url.pathname === "/" || url.pathname === "/crashes") {
			// Inline script and style run only with this response's nonce.
			const nonce = crypto.randomUUID().replaceAll("-", "");
			return new Response((url.pathname === "/crashes" ? crashesPage : page).replaceAll("{{nonce}}", nonce), {
				headers: {
					"Content-Type": "text/html; charset=utf-8",
					"Cache-Control": "no-store",
					"Content-Security-Policy": `default-src 'none'; script-src 'nonce-${nonce}'; style-src 'nonce-${nonce}'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`,
					"X-Content-Type-Options": "nosniff",
					"Referrer-Policy": "no-referrer",
				},
			});
		}
		if (url.pathname === "/api/crashes") {
			const raw = url.searchParams.get("page") ?? "0";
			if (!/^(0|[1-9][0-9]{0,5})$/.test(raw)) return problem(400, "bad_page", "Invalid page.");
			const [result] = await env.DB.batch([env.DB.prepare("SELECT id, received_at FROM crashes ORDER BY received_at DESC, id DESC LIMIT 21 OFFSET ?").bind(Number(raw) * 20)]);
			return Response.json({ reports: result.results.slice(0, 20), more: result.results.length > 20 }, { headers: { "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff" } });
		}
		if (match) {
			const [result] = await env.DB.batch([env.DB.prepare("SELECT id, report, received_at FROM crashes WHERE id = ?").bind(match[1])]);
			if (!result.results.length) return problem(404, "not_found", "Report not found.");
			return Response.json(result.results[0], { headers: { "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff" } });
		}

		if (url.pathname === "/api/stats") {
			const days = Number(url.searchParams.get("days") ?? "30");
			if (days !== 7 && days !== 30 && days !== 90) {
				return problem(400, "bad_range", "days must be 7, 30 or 90.");
			}
			const today = todayFor(url.searchParams.get("today"), Date.now());
			const [data, identity] = await Promise.all([stats(env.DB, today, days), ctx.access.getIdentity()]);
			return Response.json(
				{ ...data, viewer: typeof identity?.email === "string" ? identity.email : "" },
				{ headers: { "Cache-Control": "no-store" } },
			);
		}

		return problem(404, "not_found", "Nothing is served here.");
	},
} satisfies ExportedHandler<Pick<Env, "DB">>;
