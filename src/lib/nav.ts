import type { NavItem, NavSection } from '@smykla-skalski/sui';

export const sections: NavSection[] = [
	{
		items: [
			{ href: '/', label: 'Kolekcja', icon: 'book' },
			{ href: '/titles', label: 'Tytuły', icon: 'repositories' },
			{ href: '/scan', label: 'Skanuj', icon: 'plus' },
			{ href: '/scan/queue', label: 'Do uzupełnienia', icon: 'pending' }
		]
	}
];

/** The nav item for `pathname`: the longest href that is the path or one of its parents. */
export function currentItem(pathname: string, navSections: NavSection[]): NavItem | undefined {
	let best: NavItem | undefined;
	for (const item of navSections.flatMap((s) => s.items)) {
		const matches =
			item.href === '/'
				? pathname === '/'
				: pathname === item.href || pathname.startsWith(`${item.href}/`);
		if (matches && (!best || item.href.length > best.href.length)) {
			best = item;
		}
	}
	return best;
}
