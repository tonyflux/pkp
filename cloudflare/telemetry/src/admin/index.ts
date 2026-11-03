/**
 * PKP opt-in active installation dashboard over stored daily reports.
 * A Worker of its own so that the reports' endpoint can stay public while
 * this one sits wholly behind Cloudflare Access (Workers & Pages >
 * pkp-telemetry-admin > Access). It answers nothing a request Access did
 * not authenticate: without Access turned on, every request is refused.
 */
import page from "./page.html";
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
		if (request.method !== "GET") {
			return problem(405, "method_not_allowed", "Only GET is served.");
		}

		if (url.pathname === "/") {
			// Inline script and style run only with this response's nonce.
			const nonce = crypto.randomUUID().replaceAll("-", "");
			return new Response(page.replaceAll("{{nonce}}", nonce), {
				headers: {
					"Content-Type": "text/html; charset=utf-8",
					"Cache-Control": "no-store",
					"Content-Security-Policy": `default-src 'none'; script-src 'nonce-${nonce}'; style-src 'nonce-${nonce}'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`,
					"X-Content-Type-Options": "nosniff",
					"Referrer-Policy": "no-referrer",
				},
			});
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
