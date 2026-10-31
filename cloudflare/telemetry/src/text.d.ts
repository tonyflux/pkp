// Wrangler bundles these as text modules by default (its Text rule); the
// tests load them the same way (vitest.config.ts).
declare module "*.html" {
	const text: string;
	export default text;
}

declare module "*.sql" {
	const text: string;
	export default text;
}
