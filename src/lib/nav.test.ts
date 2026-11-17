import { describe, expect, it } from 'vitest';
import type { NavSection } from '@smykla-skalski/sui';
import { currentItem } from './nav';

const navSections: NavSection[] = [
	{
		items: [
			{ href: '/', label: 'Kolekcja', icon: 'book' },
			{ href: '/deals', label: 'Okazje', icon: 'tag' },
			{ href: '/deals/watch', label: 'Obserwowane', icon: 'viewer' }
		]
	}
];

describe('currentItem', () => {
	it.each([
		['/', '/'],
		['/deals', '/deals'],
		['/deals/123', '/deals'],
		['/deals/watch', '/deals/watch'],
		['/deals/watch/7', '/deals/watch'],
		['/dealsx', undefined],
		['/unknown', undefined]
	])('%s → %s', (pathname, href) => {
		expect(currentItem(pathname, navSections)?.href).toBe(href);
	});
});
