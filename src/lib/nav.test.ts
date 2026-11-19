import { describe, expect, it } from 'vitest';
import type { NavSection } from '@smykla-skalski/sui';
import { currentItem, currentTab, morePages, sections, tabs } from './nav';

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

describe('currentTab', () => {
	it.each([
		['/', '/'],
		['/titles', '/'],
		['/titles/12', '/'],
		['/editions/4/edit', '/'],
		['/copies/9', '/'],
		['/deals', '/deals'],
		['/deals/keywords', '/deals'],
		['/valuation', '/valuation'],
		['/scan', '/scan'],
		['/scan/queue', '/scan'],
		['/scan/5901234123457', '/scan'],
		['/informacje', undefined],
		['/dealsx', undefined],
		['/scanner', undefined]
	])('%s → %s', (pathname, href) => {
		expect(currentTab(pathname)).toBe(href);
	});
});

describe('tabs', () => {
	it('names Kolekcja, Okazje and Wartość in order', () => {
		expect(tabs.map((t) => t.label)).toEqual(['Kolekcja', 'Okazje', 'Wartość']);
	});
});

describe('morePages', () => {
	it('lists the pages that have no place in the tab bar', () => {
		expect(morePages(sections).map((item) => item.href)).toEqual([
			'/titles',
			'/scan/queue',
			'/informacje'
		]);
	});
});
