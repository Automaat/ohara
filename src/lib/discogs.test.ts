import { describe, expect, it } from 'vitest';
import { discogsCredit, discogsNotice, discogsReleaseUrl } from './discogs';

describe('Discogs notices', () => {
	it('words the credit and the notice as the API terms do', () => {
		expect(discogsCredit).toBe('Data provided by Discogs.');
		expect(discogsNotice).toBe(
			"This application uses Discogs' API but is not affiliated with, sponsored or endorsed by Discogs. 'Discogs' is a trademark of Zink Media, LLC."
		);
	});
});

describe('discogsReleaseUrl', () => {
	it('links a release to its page on discogs.com', () => {
		expect(discogsReleaseUrl(1873013)).toBe('https://www.discogs.com/release/1873013');
	});

	it.each([null, undefined, 0, -5, 1.5, Number.NaN])('has no page for %s', (id) => {
		expect(discogsReleaseUrl(id)).toBeNull();
	});
});
