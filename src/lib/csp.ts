import type { sveltekit } from '@sveltejs/kit/vite';

type KitOptions = NonNullable<Parameters<typeof sveltekit>[0]>;

/**
 * The enforced Content-Security-Policy. Built pages carry it in a `<meta>` tag
 * with the hash of SvelteKit's inline boot script. zxing compiles its bundled
 * `.wasm` (`'wasm-unsafe-eval'`), Svelte and SUI set inline style attributes and
 * SUI inlines its Latin fonts as `data:`, and covers come from any `https:` host
 * a reader pastes.
 */
export const csp = {
	mode: 'hash',
	directives: {
		'default-src': ['self'],
		'script-src': ['self', 'wasm-unsafe-eval'],
		'style-src': ['self', 'unsafe-inline'],
		'img-src': ['self', 'data:', 'https:'],
		'font-src': ['self', 'data:'],
		'connect-src': ['self'],
		'worker-src': ['self'],
		'manifest-src': ['self'],
		'object-src': ['none'],
		'base-uri': ['self'],
		'form-action': ['self']
	}
} satisfies KitOptions['csp'];
