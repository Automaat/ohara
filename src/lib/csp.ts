import type { sveltekit } from '@sveltejs/kit/vite';

type KitOptions = NonNullable<Parameters<typeof sveltekit>[0]>;

/**
 * The enforced Content-Security-Policy. Built pages carry it in a `<meta>` tag
 * with the hash of SvelteKit's inline boot script. zxing compiles its bundled
 * `.wasm` (`'wasm-unsafe-eval'`), Svelte and SUI set inline style attributes and
 * SUI inlines its Latin fonts as `data:`. Covers and listing photos come from
 * any host a reader or a deal source names; `http:` keeps plain-HTTP photos
 * showing where Ohara itself runs over HTTP (an HTTPS page upgrades them).
 */
export const csp = {
	mode: 'hash',
	directives: {
		'default-src': ['self'],
		'script-src': ['self', 'wasm-unsafe-eval'],
		'style-src': ['self', 'unsafe-inline'],
		'img-src': ['self', 'data:', 'https:', 'http:'],
		'font-src': ['self', 'data:'],
		'connect-src': ['self'],
		'worker-src': ['self'],
		'manifest-src': ['self'],
		'object-src': ['none'],
		'base-uri': ['self'],
		'form-action': ['self']
	}
} satisfies KitOptions['csp'];
