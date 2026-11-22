import { sveltekit } from '@sveltejs/kit/vite';
import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vitest/config';
import { csp } from './src/lib/csp.ts';

export default defineConfig({
	plugins: [
		sveltekit({
			preprocess: vitePreprocess(),
			adapter: adapter({ fallback: 'index.html' }),
			serviceWorker: { register: false },
			csp
		})
	],
	build: { cssTarget: ['chrome120', 'edge120', 'firefox115', 'safari16.4'] },
	server: {
		proxy: {
			'/api': { target: 'http://localhost:8000', changeOrigin: false },
			'/health': 'http://localhost:8000'
		}
	},
	test: {
		include: ['src/**/*.{test,spec}.{js,ts}', 'test/**/*.test.ts'],
		coverage: {
			provider: 'v8',
			reporter: ['text', 'json', 'lcov'],
			include: ['src/lib/**/*.ts'],
			thresholds: { statements: 80, branches: 80, functions: 80, lines: 80 }
		}
	}
});
