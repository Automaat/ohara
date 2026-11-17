import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vitest/config';

export default defineConfig({
	plugins: [sveltekit()],
	build: { cssTarget: ['chrome120', 'edge120', 'firefox115', 'safari16.4'] },
	server: {
		proxy: {
			'/api': 'http://localhost:8000',
			'/health': 'http://localhost:8000'
		}
	},
	test: {
		include: ['src/**/*.{test,spec}.{js,ts}'],
		coverage: {
			provider: 'v8',
			reporter: ['text', 'json', 'lcov'],
			include: ['src/lib/**/*.ts'],
			thresholds: { statements: 80, branches: 80, functions: 80, lines: 80 }
		}
	}
});
