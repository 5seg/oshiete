import { bindings, defineConfig } from "cf/config";
import * as entrypoint from "./src/index.ts" with { type: "cf-worker" };

export default defineConfig({
	worker: {
		name: "oshiete",
		compatibilityDate: "2026-10-14",
		entrypoint,

		// `o.5seg.top` is where the beacon posts and where /b.js is served from.
		domains: ["o.5seg.top"],

		observability: {
			enabled: true,
			logs: { enabled: true, headSamplingRate: 1 },
			traces: { enabled: true, headSamplingRate: 0.01 },
		},

		env: {
			DB: bindings.d1({ id: "c70f5fd2-91e8-4802-81b9-44817cc4a5c3" }),

			// Key for the visitor and session MACs. Rotating this value makes
			// every visitor a stranger again, and nodes the name-linking window.
			OSHIETE_SECRET: bindings.secret(),

			// Bearer token for GET /since. Read-only mirror access.
			SYNC_TOKEN: bindings.secret(),

			// Optional comma-separated origin allowlist for POST /e. Left unset,
			// any origin may post — the endpoint is public by design.
			ALLOWED_ORIGINS: bindings.text(""),
		},
	},
});
