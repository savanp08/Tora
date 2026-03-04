import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vitest/config';
import { playwright } from '@vitest/browser-playwright';
import { sveltekit } from '@sveltejs/kit/vite';
import monacoEditorPlugin from 'vite-plugin-monaco-editor';

const monacoPluginFactory =
	((monacoEditorPlugin as unknown as { default?: typeof monacoEditorPlugin }).default ??
		monacoEditorPlugin);

export default defineConfig({
	plugins: [
		monacoPluginFactory({
			// Keep editor base worker + TS worker (required for JavaScript language mode),
			// plus common built-ins used by Monaco.
			languageWorkers: ['editorWorkerService', 'typescript', 'json', 'html', 'css']
		}),
		tailwindcss(),
		sveltekit()
	],
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
