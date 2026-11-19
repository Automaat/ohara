/**
 * A cover tapped in the collection grows into the title page's cover, and shrinks back into its
 * tile on the way back, as a same-document View Transition. Pages mark what holds a title's
 * cover with `data-cover-title="<id>"` and call `coversPlaced` once their covers stand where
 * they will stay; the root layout hands every navigation to `carryCover`. app.css animates the
 * transition's pseudo-elements, with transform and opacity only.
 */
import type { OnNavigate } from '$app/navigation';

/** The `view-transition-name` of the cover that moves. */
export const coverName = 'ohara-cover';

/** The property holding the transform that puts the landed cover where it started. */
export const coverStart = '--ohara-cover-start';

/**
 * How long, in milliseconds, a navigation keeps the old page on screen while the new one places
 * its cover. A slower page still opens, and the cover fades out instead of moving, so pages draw
 * what they already know (src/lib/collection-cache.ts) before their answer arrives.
 */
export const placementWait = 400;

/** A box in viewport coordinates, as `getBoundingClientRect` gives it. */
export type Box = Pick<DOMRectReadOnly, 'left' | 'top' | 'width' | 'height'>;

const titlePath = /^\/titles\/(\d+)$/;

/**
 * The title whose cover a navigation carries: one opened from the collection, or the one the
 * reader goes back from. A title opened to add a copy, or any other navigation, carries none.
 */
export function coverTitle(from: URL, to: URL): number | null {
	if (from.origin !== to.origin) return null;
	const opened = titlePath.exec(to.pathname);
	if (from.pathname === '/' && opened && to.search === '') return Number(opened[1]);
	const left = titlePath.exec(from.pathname);
	if (left && to.pathname === '/') return Number(left[1]);
	return null;
}

/** The transform, from the top left corner, that puts a cover laid out at `end` over `start`. */
export function coverOffset(start: Box, end: Box): string {
	const x = start.left - end.left;
	const y = start.top - end.top;
	return `translate(${x}px, ${y}px) scale(${start.width / end.width}, ${start.height / end.height})`;
}

/** Whether any of a drawn box shows in a viewport of the given size. */
export function inView(box: Box, width: number, height: number): boolean {
	return (
		box.width > 0 &&
		box.height > 0 &&
		box.left < width &&
		box.top < height &&
		box.left + box.width > 0 &&
		box.top + box.height > 0
	);
}

let placements = 0;
const sleepers = new Set<() => void>();

/** Says the page on screen has placed its covers, so a cover carried to it can land. */
export function coversPlaced(): void {
	placements += 1;
	for (const wake of [...sleepers]) wake();
}

/** Resolves once a page places its covers after placement number `seen`, or after `ms`. */
function placement(seen: number, ms: number): Promise<void> {
	if (placements > seen) return Promise.resolve();
	return new Promise((resolve) => {
		const wake = () => {
			clearTimeout(timer);
			sleepers.delete(wake);
			resolve();
		};
		const timer = setTimeout(wake, Math.max(0, ms));
		sleepers.add(wake);
	});
}

type View = Window & typeof globalThis;

function coverIn(holder: Element | null): HTMLElement | null {
	return holder?.querySelector<HTMLElement>('.cover') ?? null;
}

function holders(titleID: number): string {
	return `[data-cover-title="${titleID}"]`;
}

/** What tells apart two tiles of one title, such as two copies: their `data-cover-key`. */
function coverKey(cover: HTMLElement, titleID: number): string | null {
	return cover.closest(holders(titleID))?.getAttribute('data-cover-key') ?? null;
}

function shows(cover: HTMLElement, view: View): boolean {
	return inView(cover.getBoundingClientRect(), view.innerWidth, view.innerHeight);
}

/** The cover of the title the reader can see: the tile with `key` if it shows, else the first. */
function shownCover(
	doc: Document,
	view: View,
	titleID: number,
	key: string | null = null
): HTMLElement | null {
	let first: HTMLElement | null = null;
	for (const holder of doc.querySelectorAll(holders(titleID))) {
		const cover = coverIn(holder);
		if (!cover || !shows(cover, view)) continue;
		if (key === null || holder.getAttribute('data-cover-key') === key) return cover;
		first ??= cover;
	}
	return first;
}

/** The cover of the title in the tile or hero the reader clicked. */
function clickedCover(target: EventTarget | null, view: View, titleID: number): HTMLElement | null {
	if (!(target instanceof view.Element)) return null;
	const cover = coverIn(target.closest(holders(titleID)));
	return cover && shows(cover, view) ? cover : null;
}

/**
 * Waits until the cover's artwork shows, or the deadline passes. A lazy image would not load
 * while the browser holds the old page on screen, so the cover's image loads at once.
 */
function artworkShown(cover: HTMLElement, view: View, deadline: number): Promise<void> {
	const image = cover.querySelector('img');
	if (image) image.loading = 'eager';
	const shown = () => {
		const img = cover.querySelector('img');
		return !img || view.getComputedStyle(img).opacity !== '0';
	};
	if (shown()) return Promise.resolve();
	return new Promise((resolve) => {
		const done = () => {
			observer.disconnect();
			clearTimeout(timer);
			resolve();
		};
		const observer = new view.MutationObserver(() => {
			if (shown()) done();
		});
		observer.observe(cover, { attributes: true, childList: true, subtree: true });
		const timer = setTimeout(done, Math.max(0, deadline - Date.now()));
	});
}

/** Where the new page should land a carried cover: the title, and the tile it left, if known. */
type Landing = { titleID: number; key: string | null };

/** The cover the new page places for the title, once it shows, or null by the deadline. */
async function placedCover(
	doc: Document,
	view: View,
	landing: Landing,
	seen: number,
	deadline: number
): Promise<HTMLElement | null> {
	await placement(seen, deadline - Date.now());
	const placed = placements;
	const cover = shownCover(doc, view, landing.titleID, landing.key);
	if (cover) {
		await artworkShown(cover, view, deadline);
		return cover;
	}
	if (Date.now() >= deadline) return null;
	return placedCover(doc, view, landing, placed, deadline);
}

/** Counts navigations, so one that a newer navigation overtook never commits its page. */
let navigations = 0;

/** Counts started transitions, so an older one never clears a newer one's start. */
let carried = 0;

/** The transition that last named each element; only that one clears the name. */
const namedBy = new WeakMap<HTMLElement, number>();

/** The tile each title was last opened from, so going back lands on that tile. */
const openedFrom = new Map<number, string>();

/**
 * What tells apart the tile the reader last opened the title from, its `data-cover-key`, or
 * null when the title was not opened from a tile with one.
 */
export function openedTile(titleID: number): string | null {
	return openedFrom.get(titleID) ?? null;
}

function name(cover: HTMLElement, turn: number) {
	cover.style.viewTransitionName = coverName;
	namedBy.set(cover, turn);
}

function unname(cover: HTMLElement | null, turn: number) {
	if (!cover || namedBy.get(cover) !== turn) return;
	cover.style.viewTransitionName = '';
	namedBy.delete(cover);
}

/**
 * Carries the cover a navigation moves, for `onNavigate`. A link carries the cover the reader
 * clicked, a history move the one in view; nothing moves under reduced motion, without View
 * Transitions, after the browser's own swipe transition, or when no cover is in view.
 *
 * The returned promise holds the navigation until the browser has captured the old page, and
 * the transition's update then waits, at most `placementWait`, for the new page's cover. A
 * navigation that a newer one reached first stays held: SvelteKit would otherwise render its
 * page over the newer one.
 */
export function carryCover(
	navigation: OnNavigate,
	doc: Document = document
): Promise<void> | undefined {
	const mine = ++navigations;
	const view = doc.defaultView;
	const { from, to } = navigation;
	if (!view || !from || !to || !('startViewTransition' in doc)) return;
	if (view.matchMedia('(prefers-reduced-motion: reduce)').matches) return;
	const titleID = coverTitle(from.url, to.url);
	if (titleID === null) return;
	const opening = from.url.pathname === '/';
	let source: HTMLElement | null = null;
	if (navigation.type === 'link') {
		const click: Event | undefined = navigation.event;
		source = clickedCover(click?.target ?? null, view, titleID);
	} else if (navigation.type === 'popstate') {
		const pop: PopStateEvent | undefined = navigation.event;
		const key = opening ? (openedFrom.get(titleID) ?? null) : null;
		if (pop?.hasUAVisualTransition !== true) source = shownCover(doc, view, titleID, key);
	}
	if (!source) return;

	const moving = source;
	if (opening) {
		const key = coverKey(moving, titleID);
		if (key === null) openedFrom.delete(titleID);
		else openedFrom.set(titleID, key);
	}
	const target: Landing = { titleID, key: opening ? null : (openedFrom.get(titleID) ?? null) };
	const start = moving.getBoundingClientRect();
	const seen = placements;
	const root = doc.documentElement;
	const turn = ++carried;
	let landing: HTMLElement | null = null;
	name(moving, turn);

	const clear = () => {
		unname(moving, turn);
		unname(landing, turn);
		if (turn === carried) root.style.removeProperty(coverStart);
	};

	return new Promise((resolve) => {
		const update = async () => {
			if (mine !== navigations) return;
			resolve();
			const deadline = Date.now() + placementWait;
			await navigation.complete;
			landing = await placedCover(doc, view, target, seen, deadline);
			if (!landing || turn !== carried) return;
			name(landing, turn);
			root.style.setProperty(coverStart, coverOffset(start, landing.getBoundingClientRect()));
		};
		try {
			const transition = doc.startViewTransition(update);
			transition.ready.catch(() => undefined);
			transition.updateCallbackDone.catch(() => undefined);
			transition.finished.then(clear, clear);
		} catch {
			clear();
			resolve();
		}
	});
}
