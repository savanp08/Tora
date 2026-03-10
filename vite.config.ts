import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vitest/config';
import { playwright } from '@vitest/browser-playwright';
import { sveltekit } from '@sveltejs/kit/vite';
import { fileURLToPath } from 'node:url';

const projectRoot = fileURLToPath(new URL('.', import.meta.url));
const crossOriginIsolationHeaders = {
	'Cross-Origin-Embedder-Policy': 'credentialless',
	'Cross-Origin-Opener-Policy': 'same-origin'
} as const;
type HeaderMiddlewareServer = {
	middlewares: {
		use: (
			fn: (
				_: unknown,
				res: { setHeader: (name: string, value: string) => void },
				next: () => void
			) => void
		) => void;
	};
};
const crossOriginIsolationPlugin = {
	name: 'cross-origin-isolation-plugin',
	configureServer(server: HeaderMiddlewareServer) {
		server.middlewares.use((_, res, next) => {
			res.setHeader('Cross-Origin-Embedder-Policy', 'credentialless');
			res.setHeader('Cross-Origin-Opener-Policy', 'same-origin');
			next();
		});
	},
	configurePreviewServer(server: HeaderMiddlewareServer) {
		server.middlewares.use((_, res, next) => {
			res.setHeader('Cross-Origin-Embedder-Policy', 'credentialless');
			res.setHeader('Cross-Origin-Opener-Policy', 'same-origin');
			next();
		});
	}
};

export default defineConfig({
	plugins: [
		crossOriginIsolationPlugin,
		tailwindcss(),
		sveltekit()
	],
	server: {
		host: true, // Enable this if you want to access the dev server from other devices on the network
		headers: crossOriginIsolationHeaders,
		fs: {
			// Allow importing shared root config (e.g. /limits.ts) from src/.
			allow: [projectRoot]
		}
	},
	preview: {
		headers: crossOriginIsolationHeaders
	},

	test: {
		expect: { requireAssertions: true },
		projects: [
			{
				extends: './vite.config.ts',
				test: {
					name: 'client',
					browser: {
						enabled: true,
						provider: playwright(),
						instances: [{ browser: 'chromium', headless: true }]
					},
					include: ['src/**/*.svelte.{test,spec}.{js,ts}'],
					exclude: ['src/lib/server/**']
				}
			},

			{
				extends: './vite.config.ts',
				test: {
					name: 'server',
					environment: 'node',
					include: ['src/**/*.{test,spec}.{js,ts}'],
					exclude: ['src/**/*.svelte.{test,spec}.{js,ts}']
				}
			}
		]
	}
});
