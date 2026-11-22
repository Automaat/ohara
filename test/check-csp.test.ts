import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { copyFileSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { problems } from '../scripts/csp-problems.mjs';

const fixtures = join(import.meta.dirname, 'check-csp');
const fixture = (name: string) => readFileSync(join(fixtures, name), 'utf8');

describe('built-page CSP check', () => {
	it('passes a page whose head policy lists its boot script', () => {
		expect(problems(fixture('valid.html'))).toEqual([]);
	});

	it.each([
		['1-meta-outside-head.html', 'outside <head> is ignored'],
		['2-second-policy.html', 'is not in script-src of policy 2'],
		['3-uppercase-keyword.html', "script-src of policy 1 allows 'unsafe-eval'"],
		['4-src-in-attribute-value.html', 'is not in script-src of policy 1'],
		['5-script-src-elem-none.html', 'is not in script-src-elem of policy 1']
	])('fails %s, as a browser would block or ignore it', (name, problem) => {
		expect(problems(fixture(name))).toEqual(
			expect.arrayContaining([expect.stringContaining(problem)])
		);
	});

	it('passes a data block the browser never runs', () => {
		expect(problems(fixture('6-data-block.html'))).toEqual([]);
	});
});

const boot = createHash('sha256').update('boot()').digest('base64');

/**
 * A page whose head carries `policy` after `head`; `{hash}` becomes the hash source of the
 * `boot()` script and `{hash-url}` the same hash in base64url digits.
 */
function page(policy: string, body = '<script>boot()</script>', head = '') {
	const content = policy
		.replace('{hash}', `'sha256-${boot}'`)
		.replace('{hash-url}', `'sha256-${boot.replaceAll('+', '-').replaceAll('/', '_')}'`);
	const meta = `<meta http-equiv="content-security-policy" content="${content}">`;
	return `<!doctype html><html><head>${head}${meta}</head><body>${body}</body></html>`;
}

describe('built-page CSP check edge cases', () => {
	it.each([
		['directive names in any case', "SCRIPT-SRC 'self' {hash}"],
		['a base64url hash', 'script-src {hash-url}'],
		[
			'an empty inline script',
			"script-src 'self' {hash}",
			'<script>boot()</script><script></script>'
		],
		[
			'an inline nomodule script',
			'script-src {hash}',
			'<script>boot()</script><script nomodule>x</script>'
		],
		[
			'an external script',
			"script-src 'self' {hash}",
			'<script>boot()</script><script src="/a.js">x</script>'
		],
		[
			'a policy without script directives next to one with them',
			'script-src {hash}',
			undefined,
			'<meta http-equiv="content-security-policy" content="img-src \'self\'">'
		]
	])('passes %s', (_, policy, body?: string, head?: string) => {
		expect(problems(page(policy, body, head))).toEqual([]);
	});

	it.each([
		['a repeated directive (only the first counts)', "script-src 'none'; script-src {hash}"],
		['default-src without the hash when script-src is absent', "default-src 'self'"],
		[
			'an inline module script without its hash',
			'script-src {hash}',
			'<script>boot()</script><script type="module">x</script>'
		],
		[
			'an inline import map without its hash',
			'script-src {hash}',
			'<script>boot()</script><script type="importmap">{}</script>'
		],
		[
			'an inline SVG script without its hash',
			'script-src {hash}',
			'<script>boot()</script><svg><script>x</script></svg>'
		],
		[
			'a script before the policy',
			'script-src {hash}',
			'<script>boot()</script>',
			'<script>x</script>'
		],
		['unsafe-inline in script-src-attr', "script-src {hash}; script-src-attr 'Unsafe-Inline'"],
		['unsafe-inline in default-src that governs scripts', "default-src 'unsafe-inline' {hash}"],
		[
			'a report-only policy only',
			'',
			'<script>boot()</script>',
			'<meta http-equiv="content-security-policy-report-only" content="script-src \'none\'">'
		]
	])('fails %s', (_, policy, body?: string, head?: string) => {
		expect(problems(page(policy, body, head))).not.toEqual([]);
	});

	it.each([
		[
			'a script after a policy that restricts no scripts but before one that does',
			'script-src {hash}',
			undefined,
			'<meta http-equiv="content-security-policy" content="img-src \'none\'"><script>x</script>',
			"inline script 'sha256-LXEWQrcmsEQBYnyp+6wy9chTD7GQPMTbAiWHF5IaSIE=' runs before any enforced policy restricts scripts"
		],
		[
			'comma-separated policies, which Chromium and WebKit enforce one by one',
			"default-src {hash}; img-src 'self', script-src 'none'",
			undefined,
			undefined,
			'is not in script-src of policy 2'
		],
		[
			'a script directive behind a non-ASCII space, which browsers skip',
			'&#160;&#115;cript-src {hash}',
			undefined,
			undefined,
			'runs before any enforced policy restricts scripts'
		],
		[
			'an SVG script with nomodule, which SVG does not have',
			'script-src {hash}',
			'<script>boot()</script><svg><script nomodule>x</script></svg>',
			undefined,
			'is not in script-src of policy 1'
		],
		[
			'an SVG script with a language, which SVG does not read',
			'script-src {hash}',
			'<script>boot()</script><svg><script language="vbscript">x</script></svg>',
			undefined,
			'is not in script-src of policy 1'
		]
	])('fails %s', (_, policy, body, head, problem) => {
		expect(problems(page(policy, body, head))).toEqual(
			expect.arrayContaining([expect.stringContaining(problem)])
		);
	});
});

describe('check-csp CLI', () => {
	let dir = '';
	afterEach(() => rmSync(dir, { recursive: true, force: true }));

	function run(...names: string[]) {
		dir = mkdtempSync(join(tmpdir(), 'check-csp-'));
		for (const name of names) copyFileSync(join(fixtures, name), join(dir, name));
		return spawnSync(process.execPath, ['scripts/check-csp.mjs', dir], { encoding: 'utf8' });
	}

	it('exits 0 when every built page enforces the policy', () => {
		const result = run('valid.html', '6-data-block.html');
		expect(result.status).toBe(0);
		expect(result.stdout).toContain('enforced on 2 built page(s)');
	});

	it('exits 1 and names the page that fails', () => {
		const result = run('valid.html', '5-script-src-elem-none.html');
		expect(result.status).toBe(1);
		expect(result.stderr).toContain('5-script-src-elem-none.html: inline script');
		expect(result.stderr).not.toContain('valid.html');
	});
});
