import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { OnNavigate } from '$app/navigation';
import {
	carryCover,
	coverName,
	coverOffset,
	coverStart,
	coverTitle,
	coversPlaced,
	inView,
	placementWait,
	type Box
} from './cover-transition';

const origin = 'https://ohara.test';
const url = (path: string) => new URL(path, origin);

describe('coverTitle', () => {
	it.each([
		['/', '/titles/7', 7],
		['/?kind=movie', '/titles/7', 7],
		['/titles/7', '/', 7],
		['/titles/7', '/?status=none', 7],
		['/', '/titles/7?add=copy', null],
		['/', '/titles/new', null],
		['/', '/scan', null],
		['/scan', '/titles/7', null],
		['/titles/7', '/titles/8', null],
		['/titles/7', '/deals', null],
		['/editions/3', '/', null]
	])('%s → %s carries %s', (from, to, want) => {
		expect(coverTitle(url(from), url(to))).toBe(want);
	});

	it('carries nothing to another site', () => {
		expect(coverTitle(url('/'), new URL('https://example.com/titles/7'))).toBeNull();
	});
});

describe('coverOffset', () => {
	it('moves and scales the landed box onto the start', () => {
		const start = { left: 20, top: 300, width: 100, height: 150 };
		const end = { left: 16, top: 120, width: 200, height: 300 };
		expect(coverOffset(start, end)).toBe('translate(4px, 180px) scale(0.5, 0.5)');
	});

	it('is no move for the same box', () => {
		const box = { left: 5, top: 6, width: 10, height: 15 };
		expect(coverOffset(box, box)).toBe('translate(0px, 0px) scale(1, 1)');
	});
});

describe('inView', () => {
	it.each([
		[{ left: 0, top: 0, width: 10, height: 10 }, true],
		[{ left: -9, top: -9, width: 10, height: 10 }, true],
		[{ left: 395, top: 795, width: 10, height: 10 }, true],
		[{ left: 0, top: 800, width: 10, height: 10 }, false],
		[{ left: 400, top: 0, width: 10, height: 10 }, false],
		[{ left: -10, top: 0, width: 10, height: 10 }, false],
		[{ left: 0, top: -10, width: 10, height: 10 }, false],
		[{ left: 0, top: 0, width: 0, height: 0 }, false]
	])('%o in a 400×800 viewport: %s', (box, want) => {
		expect(inView(box, 400, 800)).toBe(want);
	});
});

class FakeElement {
	readonly style = { viewTransitionName: '' };
	parent: FakeElement | null = null;
	readonly children: FakeElement[] = [];
	tag = 'div';

	constructor(
		readonly classes: string[] = [],
		readonly titleID?: number,
		public rect: Box = { left: 0, top: 0, width: 0, height: 0 }
	) {}

	add<T extends FakeElement>(child: T): T {
		child.parent = this;
		this.children.push(child);
		return child;
	}

	matches(selector: string): boolean {
		const title = /^\[data-cover-title="(\d+)"\]$/.exec(selector);
		if (title) return this.titleID === Number(title[1]);
		if (selector.startsWith('.')) return this.classes.includes(selector.slice(1));
		return this.tag === selector;
	}

	closest(selector: string): FakeElement | null {
		if (this.matches(selector)) return this;
		return this.parent?.closest(selector) ?? null;
	}

	all(selector: string): FakeElement[] {
		return this.children.flatMap((child) => [
			...(child.matches(selector) ? [child] : []),
			...child.all(selector)
		]);
	}

	querySelector(selector: string): FakeElement | null {
		return this.all(selector)[0] ?? null;
	}

	getBoundingClientRect(): Box {
		return this.rect;
	}
}

class FakeImage extends FakeElement {
	loading = 'lazy';
	opacity = '0';
	tag = 'img';
}

class FakeObserver {
	static live = new Set<FakeObserver>();
	constructor(private readonly callback: () => void) {}
	observe() {
		FakeObserver.live.add(this);
	}
	disconnect() {
		FakeObserver.live.delete(this);
	}
	static notify() {
		for (const observer of [...FakeObserver.live]) observer.callback();
	}
}

class FakeRootStyle {
	readonly values = new Map<string, string>();
	setProperty(name: string, value: string) {
		this.values.set(name, value);
	}
	removeProperty(name: string) {
		this.values.delete(name);
	}
}

/** A page with one title's cover, held by a tile or a hero, and the link inside the holder. */
function pageWith(titleID: number, rect: Box, opacity = '1') {
	const body = new FakeElement();
	const holder = body.add(new FakeElement([], titleID));
	const link = holder.add(new FakeElement(['link']));
	const cover = holder.add(new FakeElement(['cover'], undefined, rect));
	const image = cover.add(new FakeImage());
	image.opacity = opacity;
	return { body, holder, link, cover, image };
}

type Transition = { update: Promise<void>; finish: () => void };

function fakeDocument(options: { reduced?: boolean; viewTransitions?: boolean } = {}) {
	const transitions: Transition[] = [];
	const view = {
		innerWidth: 400,
		innerHeight: 800,
		Element: FakeElement,
		MutationObserver: FakeObserver,
		matchMedia: () => ({ matches: options.reduced ?? false }),
		getComputedStyle: (image: FakeImage) => ({ opacity: image.opacity })
	};
	const root = { style: new FakeRootStyle() };
	const doc = {
		defaultView: view,
		documentElement: root,
		body: new FakeElement(),
		querySelectorAll(selector: string) {
			return doc.body.all(selector);
		},
		startViewTransition(update: () => Promise<void>) {
			let finish = () => {};
			const finishing = new Promise<void>((resolve) => (finish = resolve));
			const updateCallbackDone = Promise.resolve().then(update);
			transitions.push({ update: updateCallbackDone, finish });
			return {
				ready: updateCallbackDone,
				updateCallbackDone,
				finished: updateCallbackDone.then(() => finishing)
			};
		}
	};
	if (options.viewTransitions === false) {
		Reflect.deleteProperty(doc, 'startViewTransition');
	}
	return { doc, root, transitions, asDocument: () => doc as unknown as Document };
}

function navigation(type: 'link' | 'popstate' | 'goto', from: string, to: string, event?: object) {
	let done = () => {};
	const complete = new Promise<void>((resolve) => (done = resolve));
	const nav = {
		type,
		from: { url: url(from), params: {}, route: { id: null } },
		to: { url: url(to), params: {}, route: { id: null } },
		willUnload: false,
		complete,
		event
	};
	return { nav: nav as unknown as OnNavigate, done };
}

const tileBox = { left: 20, top: 300, width: 100, height: 150 };
const heroBox = { left: 16, top: 120, width: 132, height: 198 };

describe('carryCover', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		FakeObserver.live.clear();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it('grows the tapped cover into the hero once its artwork shows, past a late collection load', async () => {
		const { doc, root, transitions, asDocument } = fakeDocument();
		const collection = pageWith(7, tileBox);
		doc.body = collection.body;
		const { nav, done } = navigation('link', '/', '/titles/7', { target: collection.link });

		await carryCover(nav, asDocument());
		expect(transitions).toHaveLength(1);
		expect(collection.cover.style.viewTransitionName).toBe(coverName);

		coversPlaced();
		doc.body = new FakeElement();
		done();
		await vi.advanceTimersByTimeAsync(50);

		const title = pageWith(7, heroBox, '0');
		doc.body = title.body;
		coversPlaced();
		await vi.advanceTimersByTimeAsync(50);
		expect(title.image.loading).toBe('eager');
		expect(title.cover.style.viewTransitionName).toBe('');

		title.image.opacity = '1';
		FakeObserver.notify();
		await transitions[0]?.update;
		expect(title.cover.style.viewTransitionName).toBe(coverName);
		expect(root.style.values.get(coverStart)).toBe(coverOffset(tileBox, heroBox));

		transitions[0]?.finish();
		await vi.advanceTimersByTimeAsync(0);
		expect(collection.cover.style.viewTransitionName).toBe('');
		expect(title.cover.style.viewTransitionName).toBe('');
		expect(root.style.values.has(coverStart)).toBe(false);
	});

	it('lets the cover fade out when no tile for it comes into view in time', async () => {
		const { doc, root, transitions, asDocument } = fakeDocument();
		const title = pageWith(7, heroBox);
		doc.body = title.body;
		const { nav, done } = navigation('popstate', '/titles/7', '/', {
			hasUAVisualTransition: false
		});

		await carryCover(nav, asDocument());
		expect(title.cover.style.viewTransitionName).toBe(coverName);

		const hidden = pageWith(7, { left: 20, top: 1200, width: 100, height: 150 });
		doc.body = hidden.body;
		done();
		coversPlaced();
		await vi.advanceTimersByTimeAsync(placementWait);
		await transitions[0]?.update;
		expect(hidden.cover.style.viewTransitionName).toBe('');
		expect(root.style.values.has(coverStart)).toBe(false);

		transitions[0]?.finish();
		await vi.advanceTimersByTimeAsync(0);
		expect(title.cover.style.viewTransitionName).toBe('');
	});

	it('lands on the tile the restored scroll shows', async () => {
		const { doc, root, transitions, asDocument } = fakeDocument();
		const title = pageWith(7, heroBox);
		doc.body = title.body;
		const { nav, done } = navigation('popstate', '/titles/7', '/', {});

		await carryCover(nav, asDocument());
		const collection = pageWith(7, tileBox);
		doc.body = collection.body;
		done();
		coversPlaced();
		await transitions[0]?.update;
		expect(collection.cover.style.viewTransitionName).toBe(coverName);
		expect(root.style.values.get(coverStart)).toBe(coverOffset(heroBox, tileBox));
	});

	it('moves nothing when the browser cannot, or the reader asks for less motion', () => {
		for (const options of [{ viewTransitions: false }, { reduced: true }]) {
			const { doc, transitions, asDocument } = fakeDocument(options);
			const collection = pageWith(7, tileBox);
			doc.body = collection.body;
			const { nav } = navigation('link', '/', '/titles/7', { target: collection.link });

			expect(carryCover(nav, asDocument())).toBeUndefined();
			expect(transitions).toHaveLength(0);
			expect(collection.cover.style.viewTransitionName).toBe('');
		}
	});

	it.each([
		['a tab switch', 'link', '/titles/7', '/', 'outside'],
		['a page without covers', 'link', '/', '/scan', 'link'],
		['a programmatic move', 'goto', '/titles/7', '/', 'link'],
		['a swipe the browser animated', 'popstate', '/titles/7', '/', 'swiped'],
		['a cover out of view', 'popstate', '/', '/titles/7', 'hidden']
	] as const)('moves nothing for %s', (_case, type, from, to, input) => {
		const { doc, transitions, asDocument } = fakeDocument();
		const box = input === 'hidden' ? { left: 20, top: 900, width: 100, height: 150 } : tileBox;
		const page = pageWith(7, box);
		doc.body = page.body;
		const tabBar = doc.body.add(new FakeElement(['tab']));
		const event =
			input === 'outside'
				? { target: tabBar }
				: input === 'swiped'
					? { hasUAVisualTransition: true }
					: { target: page.link };
		const { nav } = navigation(type, from, to, event);

		expect(carryCover(nav, asDocument())).toBeUndefined();
		expect(transitions).toHaveLength(0);
		expect(page.cover.style.viewTransitionName).toBe('');
	});

	it('lets the navigation go on when the transition cannot start', async () => {
		const { doc, asDocument } = fakeDocument();
		const collection = pageWith(7, tileBox);
		doc.body = collection.body;
		doc.startViewTransition = () => {
			throw new Error('no transition');
		};
		const { nav } = navigation('link', '/', '/titles/7', { target: collection.link });

		await carryCover(nav, asDocument());
		expect(collection.cover.style.viewTransitionName).toBe('');
	});
});
