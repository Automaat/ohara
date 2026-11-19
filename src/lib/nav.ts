import type { NavItem, NavSection } from '@smykla-skalski/sui';

export const sections: NavSection[] = [
	{
		items: [
			{ href: '/', label: 'Kolekcja', icon: 'book' },
			{ href: '/scan', label: 'Skanuj', icon: 'plus' },
			{ href: '/scan/queue', label: 'Do uzupełnienia', icon: 'pending' },
			{ href: '/deals', label: 'Okazje', icon: 'tag' },
			{ href: '/valuation', label: 'Wartość', icon: 'gauge' }
		]
	},
	{
		items: [{ href: '/informacje', label: 'Informacje o aplikacji', icon: 'info' }]
	}
];

/** Whether `pathname` is the page at `href` or one under it; `/` owns only itself. */
function within(pathname: string, href: string): boolean {
	return href === '/' ? pathname === '/' : pathname === href || pathname.startsWith(`${href}/`);
}

/**
 * The nav item for `pathname`: the longest href that is the path or one of its parents, else the
 * item of the tab that owns the page, so a title, edition or copy page marks Kolekcja.
 */
export function currentItem(pathname: string, navSections: NavSection[]): NavItem | undefined {
	const items = navSections.flatMap((s) => s.items);
	let best: NavItem | undefined;
	for (const item of items) {
		if (within(pathname, item.href) && (!best || item.href.length > best.href.length)) {
			best = item;
		}
	}
	if (best) return best;
	const owner = currentTab(pathname);
	return owner ? items.find((item) => item.href === owner) : undefined;
}

export type TabIcon = 'grid' | 'tag' | 'trend';

/** A place in the phone tab bar and the pages that belong to it. */
export type Tab = { href: string; label: string; icon: TabIcon; paths: string[] };

/** The phone tab bar's places, in order. */
export const tabs: Tab[] = [
	{
		href: '/',
		label: 'Kolekcja',
		icon: 'grid',
		paths: ['/', '/titles', '/editions', '/copies']
	},
	{ href: '/deals', label: 'Okazje', icon: 'tag', paths: ['/deals'] },
	{ href: '/valuation', label: 'Wartość', icon: 'trend', paths: ['/valuation'] }
];

/** The scan button beside the tabs. */
export const scanHref = '/scan';

/** The tab bar place for `pathname`: a tab's href, the scan button's, or undefined. */
export function currentTab(pathname: string): string | undefined {
	const places = [
		...tabs.flatMap((tab) => tab.paths.map((path) => ({ href: tab.href, path }))),
		{ href: scanHref, path: scanHref }
	];
	let best: { href: string; path: string } | undefined;
	for (const place of places) {
		if (within(pathname, place.path) && (!best || place.path.length > best.path.length)) {
			best = place;
		}
	}
	return best?.href;
}

/** The pages the tab bar leaves out, which a phone reaches from the settings sheet. */
export function morePages(navSections: NavSection[]): NavItem[] {
	const inBar = new Set([...tabs.map((tab) => tab.href), scanHref]);
	return navSections.flatMap((s) => s.items).filter((item) => !inBar.has(item.href));
}
