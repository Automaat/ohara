/**
 * The collection shelf draws a long list in parts, so a phone never stalls on thousands of
 * tiles at once (#204): the first screen at once, the rest a part per task. Every tile ends up
 * in the page, so the list, the search in the page and the cover transitions see all of it.
 */

/** Tiles drawn with the rest of the page: more than the first screen of the widest layout. */
export const firstTiles = 60;

/** Tiles each later task adds, a part short enough not to hold a throttled phone. */
export const tilesPerStep = 150;

/**
 * How many leading rows to draw after the rows change: every row up to the `budget`-th one
 * not drawn before, so a refresh of what is on screen keeps every tile and a new filter or
 * order creates at most `budget` tiles at once.
 */
export function drawnCount<K>(keys: readonly K[], drawn: ReadonlySet<K>, budget: number): number {
	let fresh = 0;
	for (let i = 0; i < keys.length; i++) {
		if (drawn.has(keys[i])) continue;
		if (fresh === budget) return i;
		fresh += 1;
	}
	return keys.length;
}
