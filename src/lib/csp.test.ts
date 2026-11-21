import { describe, expect, it } from 'vitest';
import { csp } from './csp';

describe('Content-Security-Policy', () => {
	it('is enforced, not only reported', () => {
		expect(csp).not.toHaveProperty('reportOnly');
		expect(csp.directives['default-src']).toEqual(['self']);
	});

	it('allows no inline or evaluated script, only compiling the bundled WebAssembly', () => {
		expect(csp.directives['script-src']).toEqual(['self', 'wasm-unsafe-eval']);
	});

	it('shows covers from any https host', () => {
		expect(csp.directives['img-src']).toContain('https:');
		expect(csp.directives['img-src']).not.toContain('http:');
	});

	it('loads no plugins and keeps base and form targets on this origin', () => {
		expect(csp.directives['object-src']).toEqual(['none']);
		expect(csp.directives['base-uri']).toEqual(['self']);
		expect(csp.directives['form-action']).toEqual(['self']);
	});
});
