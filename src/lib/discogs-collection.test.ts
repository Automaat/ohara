import { describe, expect, it } from 'vitest';
import { discogsShelf, type CopyDiscogs } from './discogs-collection';

const state = (over: Partial<CopyDiscogs>): CopyDiscogs => ({
	enabled: true,
	pushable: true,
	instance_id: null,
	release_url: null,
	...over
});

describe('discogsShelf', () => {
	it('offers the first push of an owned album copy', () => {
		expect(discogsShelf(state({}))).toEqual({
			releaseUrl: null,
			action: 'Dodaj do kolekcji Discogs'
		});
	});

	it('links a pushed copy to its release and offers an update', () => {
		const url = 'https://www.discogs.com/release/1873013';
		expect(discogsShelf(state({ instance_id: 901, release_url: url }))).toEqual({
			releaseUrl: url,
			action: 'Zaktualizuj stan w Discogs'
		});
	});

	it('keeps the link of a copy that can no longer be pushed', () => {
		const url = 'https://www.discogs.com/release/1873013';
		expect(discogsShelf(state({ pushable: false, instance_id: 901, release_url: url }))).toEqual({
			releaseUrl: url,
			action: null
		});
	});

	it.each([
		['no answer', null],
		['no token', state({ enabled: false, pushable: false })],
		['a copy it cannot push', state({ pushable: false })]
	])('shows nothing for %s', (_name, s) => {
		expect(discogsShelf(s)).toBeNull();
	});
});
