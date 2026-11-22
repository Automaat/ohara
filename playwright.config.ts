import { defineConfig } from '@playwright/test';

/** Smoke flows against the stack scripts/e2e.sh starts from e2e/compose.yaml. */
export default defineConfig({
	testDir: 'e2e',
	retries: 0,
	workers: 1,
	timeout: 30_000,
	reporter: 'list',
	use: {
		baseURL: 'http://localhost:8100',
		trace: 'retain-on-failure'
	},
	projects: [
		{
			name: 'phone',
			use: {
				browserName: 'chromium',
				viewport: { width: 390, height: 844 },
				deviceScaleFactor: 3,
				isMobile: true,
				hasTouch: true,
				locale: 'pl-PL'
			}
		}
	]
});
