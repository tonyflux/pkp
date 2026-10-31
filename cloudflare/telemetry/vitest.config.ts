import { readFileSync } from "node:fs";
import { defineConfig } from "vitest/config";

// The Workers import *.html and *.sql as text, as Wrangler bundles them.
export default defineConfig({
	plugins: [
		{
			name: "text-modules",
			enforce: "pre",
			load(id) {
				if (/\.(html|sql)$/.test(id)) return `export default ${JSON.stringify(readFileSync(id, "utf8"))};`;
			},
		},
	],
});
