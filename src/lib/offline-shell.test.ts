import { describe, expect, it } from 'vitest';
import { isAppShell, serverPaths } from './offline-shell';

const immutable = [
	{ path: '_app/immutable/entry/start.BObYn4LE.js' },
	{ path: '_app/immutable/entry/app.C8Sptbws.js' },
	{ path: '_app/immutable/chunks/CI_hNbau.js' },
	{ path: '_app/immutable/nodes/0.Dx1.js' }
];

const shell = `<!doctype html>
<html lang="pl">
	<body>
		<script>
			Promise.all([
				import("/_app/immutable/entry/start.BObYn4LE.js"),
				import("/_app/immutable/entry/app.C8Sptbws.js")
			]).then(([kit, app]) => kit.start(app, document.body));
		</script>
	</body>
</html>`;

describe('serverPaths', () => {
	const excluded = (path: string) => serverPaths.some((pattern) => pattern.test(path));

	it.each([
		'api',
		'api/',
		'api/titles',
		'api?x=1',
		'health',
		'health?x=1',
		'health/',
		'metrics',
		'metrics?x=1',
		'share/abc',
		'share/abc/wishes'
	])('keeps %s away from the worker', (path) => {
		expect(excluded(path)).toBe(true);
	});

	it.each([
		'',
		'scan',
		'scan/queue',
		'titles/1',
		'apiary',
		'healthy',
		'metrics-board',
		'shared',
		'deals?q=api'
	])('leaves the app page %s to the worker', (path) => {
		expect(excluded(path)).toBe(false);
	});
});

describe('isAppShell', () => {
	it("accepts this build's index.html", () => {
		expect(isAppShell(shell, immutable)).toBe(true);
	});

	it.each([
		['a maintenance page', '<!doctype html><h1>Maintenance</h1>'],
		['a sign-in page', '<!doctype html><title>Login - Authelia</title><form></form>'],
		['an empty answer', ''],
		[
			"another build's shell",
			shell.replace('start.BObYn4LE', 'start.Zz9Older').replace('app.C8Sptbws', 'app.Yy8Older')
		],
		[
			"a shell that loads only this build's start entry",
			shell.replace('app.C8Sptbws', 'app.Yy8Older')
		]
	])('refuses %s', (_, html) => {
		expect(isAppShell(html, immutable)).toBe(false);
	});

	it('refuses everything when the build names no start entry', () => {
		expect(isAppShell(shell, [{ path: '_app/immutable/chunks/CI_hNbau.js' }])).toBe(false);
	});
});
