import { describe, expect, it } from 'vitest';
import type { Copy, TitleDetail } from './collection';
import {
	addCopyHref,
	addCopyStart,
	copyFacts,
	copyLine,
	copyRequest,
	editionArtwork,
	editionName,
	editionsLine,
	kindAndYear,
	openedFromCollection,
	plainClick,
	previewHero,
	noRating,
	ratingOptions,
	soleCopy,
	titleArtwork,
	titleHero,
	valueCard,
	wantsCopy,
	type TitleEdition
} from './title-detail';
import type { CopyValue, CopyValueDay, Estimate } from './valuation';

const nbsp = (s: string | null | undefined) => s?.replace(/[\u00a0\u202f]/g, ' ');

function copy(over: Partial<Copy> = {}): Copy {
	return {
		id: 7,
		edition_id: 3,
		status: 'owned',
		price_cents: 8999,
		purchased_on: '2023-12-24',
		shop: 'Empik',
		acquired_as: 'new',
		condition: null,
		notes: '',
		max_price_cents: null,
		created_at: '2023-12-24T10:00:00Z',
		updated_at: '2023-12-24T10:00:00Z',
		...over
	};
}

function edition(over: Partial<TitleEdition> = {}): TitleEdition {
	return {
		id: 3,
		title_id: 1,
		barcode: '5051889690011',
		label: '',
		publisher: 'Warner',
		year: 2020,
		format: 'uhd',
		pl_subtitles: 'yes',
		pl_voiceover: 'unknown',
		packaging: ['slipcover'],
		binding: null,
		discogs_release_id: null,
		notes: '',
		copy_count: 1,
		created_at: '2023-12-24T10:00:00Z',
		updated_at: '2023-12-24T10:00:00Z',
		copies: [copy()],
		...over
	};
}

function title(over: Partial<TitleDetail> = {}): TitleDetail {
	return {
		id: 1,
		kind: 'movie',
		name: 'Interstellar',
		original_title: null,
		polish_title: null,
		year: 2014,
		tmdb_id: 157336,
		authors: [],
		bn_id: null,
		openlibrary_id: null,
		notes: '',
		tags: [],
		edition_count: 1,
		copy_count: 1,
		created_at: '2023-12-24T10:00:00Z',
		updated_at: '2023-12-24T10:00:00Z',
		editions: [edition()],
		...over
	};
}

const estimate = (cents: number, source: Estimate['source'] = 'olx'): Estimate => ({
	cents,
	source,
	count: 4,
	trimmed: 0
});

function valued(over: Partial<CopyValue> = {}): CopyValue {
	return {
		copy_id: 7,
		edition_id: 3,
		title_id: 1,
		title_name: 'Interstellar',
		kind: 'movie',
		format: 'uhd',
		barcode: '5051889690011',
		price_cents: 8999,
		floor: estimate(1600, 'skupszop'),
		mid: estimate(7400),
		ceiling: estimate(10300, 'discogs'),
		low_confidence: false,
		likely_sold: null,
		...over
	};
}

describe('soleCopy', () => {
	it('leads with the only copy of the only edition', () => {
		const t = title();
		expect(soleCopy(t)).toEqual({ edition: t.editions[0], copy: t.editions[0]?.copies[0] });
	});

	it.each([
		['no editions', title({ editions: [] })],
		['an edition without copies', title({ editions: [edition({ copies: [] })] })],
		['two copies', title({ editions: [edition({ copies: [copy(), copy({ id: 8 })] })] })],
		['two editions', title({ editions: [edition(), edition({ id: 4 })] })]
	])('is null with %s', (_, t) => {
		expect(soleCopy(t)).toBeNull();
	});
});

describe('adding a copy from a link', () => {
	it('links to the page with the add request', () => {
		expect(addCopyHref('/titles/12')).toBe('/titles/12?add=copy');
		const url = new URL(addCopyHref('/editions/4'), 'https://ohara.test');
		expect(url.pathname).toBe('/editions/4');
		expect(wantsCopy(url.searchParams)).toBe(true);
	});

	it.each(['', '?add=edition', '?add=', '?copy=add'])('ignores %j', (search) => {
		expect(wantsCopy(new URLSearchParams(search))).toBe(false);
	});

	it('starts adding once per opened page, never on a refresh', () => {
		const request = copyRequest();
		request.open(5, new URLSearchParams('add=copy'));
		expect(request.take(4)).toBe(false);
		expect(request.take(5)).toBe(true);
		expect(request.take(5)).toBe(false);
	});

	it('ignores a page opened without the request', () => {
		const request = copyRequest();
		request.open(5, new URLSearchParams('add=copy'));
		request.open(6, new URLSearchParams(''));
		expect(request.take(5)).toBe(false);
		expect(request.take(6)).toBe(false);
	});

	it('drops the request when the page closes before its answer', () => {
		const request = copyRequest();
		request.open(5, new URLSearchParams('add=copy'));
		request.close();
		expect(request.take(5)).toBe(false);
	});

	it('starts with a new edition when the title has none', () => {
		expect(addCopyStart(title({ editions: [] }))).toEqual({ form: 'edition' });
	});

	it('opens the copy form of the only edition, with or without copies', () => {
		expect(addCopyStart(title({ editions: [edition({ id: 5, copies: [] })] }))).toEqual({
			form: 'copy',
			editionID: 5
		});
		expect(addCopyStart(title())).toEqual({ form: 'copy', editionID: 3 });
	});

	it('leaves the choice to the reader among several editions', () => {
		expect(addCopyStart(title({ editions: [edition(), edition({ id: 4 })] }))).toBeNull();
	});
});

describe('artwork', () => {
	it('prefers an edition photo, then its catalog cover, then the title cover', () => {
		const t = { cover_url: 'https://t/cover.jpg' };
		expect(editionArtwork({ cover_override_url: '/own', cover_url: 'https://e' }, t)).toBe('/own');
		expect(editionArtwork({ cover_url: 'https://e' }, t)).toBe('https://e');
		expect(editionArtwork({}, t)).toBe('https://t/cover.jpg');
		expect(editionArtwork({}, {})).toBeUndefined();
	});

	it('shows the only edition art in the hero', () => {
		const t = title({ cover_url: 'https://t', editions: [edition({ cover_url: 'https://e' })] });
		expect(titleArtwork(t)).toBe('https://e');
	});

	it('shows the title art over several editions, else the first edition with art', () => {
		const editions = [edition(), edition({ id: 4, cover_override_url: '/api/editions/4/cover' })];
		expect(titleArtwork(title({ cover_url: 'https://t', editions }))).toBe('https://t');
		expect(titleArtwork(title({ editions }))).toBe('/api/editions/4/cover');
		expect(titleArtwork(title({ editions: [] }))).toBeUndefined();
	});
});

describe('hero lines', () => {
	it('names the kind and year', () => {
		expect(kindAndYear({ kind: 'movie', year: 2014 })).toBe('Film · 2014');
		expect(kindAndYear({ kind: 'music', year: null })).toBe('Muzyka');
	});

	it('names an edition without its barcode', () => {
		expect(editionName(edition())).toBe('4K UHD · Slipcover');
		expect(editionName(edition({ format: null, packaging: [] }))).toBe('Wydanie bez opisu');
	});

	it('names the only edition, or counts editions and copies', () => {
		expect(editionsLine(title())).toBe('4K UHD · Slipcover');
		expect(editionsLine(title({ editions: [] }))).toBe('Brak wydań');
		const two = [edition(), edition({ id: 4, copies: [copy({ id: 8 }), copy({ id: 9 })] })];
		expect(editionsLine(title({ editions: two }))).toBe('2 wydania · 3 egzemplarze');
		const five = Array.from({ length: 5 }, (_, i) => edition({ id: i + 10, copies: [] }));
		expect(editionsLine(title({ editions: five }))).toBe('5 wydań · 0 egzemplarzy');
	});
});

describe('hero', () => {
	it('shows a loaded title', () => {
		const book = title({
			kind: 'book',
			name: 'Solaris',
			polish_title: 'Solaris',
			original_title: 'Solaris (1961)',
			year: 1961,
			authors: ['Stanisław Lem', 'Joanna Kowalska'],
			cover_url: 'https://image.test/title.jpg',
			editions: [edition({ format: null, packaging: [], binding: 'hardcover' })]
		});
		expect(titleHero(book)).toEqual({
			title: 'Solaris (Solaris (1961))',
			overline: 'Książka · 1961',
			meta: 'Twarda oprawa',
			byline: 'Stanisław Lem, Joanna Kowalska',
			kind: 'book',
			artwork: 'https://image.test/title.jpg'
		});
		expect(titleHero(title({ editions: [] })).byline).toBeUndefined();
	});

	it('shows a title still loading as the collection listed it', () => {
		const shown = {
			kind: 'music' as const,
			name: 'Kind of Blue',
			original_title: null,
			polish_title: null,
			year: 1959,
			authors: ['Miles Davis'],
			cover_url: 'https://image.test/kob.jpg'
		};
		expect(previewHero(shown)).toEqual({
			title: 'Kind of Blue',
			overline: 'Muzyka · 1959',
			meta: '\u00a0',
			byline: 'Miles Davis',
			kind: 'music',
			artwork: 'https://image.test/kob.jpg'
		});
		expect(previewHero({ ...shown, authors: [], cover_url: '' })).toMatchObject({
			byline: undefined,
			artwork: undefined
		});
	});

	it('changes only the editions line once the title loads', () => {
		const t = title({ authors: ['Christopher Nolan'], cover_url: 'https://image.test/t.jpg' });
		const loaded = titleHero({
			...t,
			editions: [edition({ cover_url: 'https://image.test/e.jpg' })]
		});
		const loading = previewHero({ ...t, cover_url: 'https://image.test/e.jpg' });
		expect({ ...loading, meta: loaded.meta }).toEqual(loaded);
	});
});

describe('copyFacts', () => {
	it('shows what an owned copy cost, when, where and its state', () => {
		expect(
			copyFacts(copy({ condition: 'very_good' })).map((f) => [f.label, nbsp(f.value)])
		).toEqual([
			['Cena', '89,99 zł'],
			['Kupiono', '24 gru 2023'],
			['Sklep', 'Empik'],
			['Stan', 'Nowy · Bardzo dobry']
		]);
	});

	it('marks unknown facts with a dash and keeps a free copy at 0 zł', () => {
		const facts = copyFacts(
			copy({ price_cents: 0, purchased_on: null, shop: '', acquired_as: null })
		);
		expect(facts.map((f) => nbsp(f.value))).toEqual(['0,00 zł', '—', '—', '—']);
		expect(copyFacts(copy({ price_cents: null }))[0]?.value).toBe('—');
	});

	it('shows the maximum price and where to look for a wanted copy', () => {
		const facts = copyFacts(
			copy({ status: 'wishlist', price_cents: null, max_price_cents: 6000, shop: 'OLX' })
		);
		expect(facts.map((f) => [f.label, nbsp(f.value)])).toEqual([
			['Cena maksymalna', '60,00 zł'],
			['Gdzie szukać', 'OLX'],
			['Stan', 'Nowy']
		]);
	});

	it('says a wish takes offers of only its edition', () => {
		const facts = copyFacts(
			copy({ status: 'wishlist', price_cents: null, max_price_cents: null, exact_edition: true })
		);
		expect(facts.map((f) => [f.label, f.value])).toContainEqual(['Oferty', 'Tylko to wydanie']);
		expect(
			copyFacts(copy({ status: 'wishlist', price_cents: null })).map((f) => f.label)
		).not.toContain('Oferty');
	});
});

describe('copyFacts with a location', () => {
	it('shows where an owned copy is kept, last', () => {
		const facts = copyFacts(copy({ location: 'Regał 2' }));
		expect(facts.at(-1)).toEqual({ label: 'Miejsce', value: 'Regał 2' });
	});
});

describe('copyFacts of a sold copy', () => {
	it('keeps the purchase and adds the sale', () => {
		const facts = copyFacts(
			copy({ status: 'sold', sold_on: '2026-05-10', sale_price_cents: 3500 })
		);
		expect(facts.map((f) => [f.label, nbsp(f.value)])).toEqual([
			['Cena', '89,99 zł'],
			['Kupiono', '24 gru 2023'],
			['Sklep', 'Empik'],
			['Sprzedano', '10 maj 2026'],
			['Cena sprzedaży', '35,00 zł'],
			['Stan', 'Nowy']
		]);
		expect(copyFacts(copy({ status: 'sold', sold_on: '2026-05-10' }))[4]?.value).toBe('—');
	});

	it('lists what it sold for in its row', () => {
		expect(
			nbsp(copyLine(copy({ status: 'sold', sold_on: '2026-05-10', sale_price_cents: 3500 })))
		).toBe('Sprzedany · 35,00 zł · Empik');
		expect(copyLine(copy({ status: 'sold', sold_on: '2026-05-10', shop: '' }))).toBe('Sprzedany');
	});
});

describe('copyFacts of an ordered copy', () => {
	it('names the order and the expected delivery', () => {
		const facts = copyFacts(copy({ status: 'ordered', expected_on: '2026-12-15' }));
		expect(facts.map((f) => [f.label, nbsp(f.value)])).toEqual([
			['Cena', '89,99 zł'],
			['Zamówiono', '24 gru 2023'],
			['Sklep', 'Empik'],
			['Spodziewana dostawa', '15 gru 2026'],
			['Stan', 'Nowy']
		]);
		expect(copyFacts(copy({ status: 'ordered' }))[3]).toEqual({
			label: 'Spodziewana dostawa',
			value: '—'
		});
	});

	it('lists what it cost in its row', () => {
		expect(nbsp(copyLine(copy({ status: 'ordered' })))).toBe('Zamówiony · 89,99 zł · Empik');
	});
});

describe('copyLine', () => {
	it('lists the status, the price and the shop that are known', () => {
		expect(nbsp(copyLine(copy()))).toBe('Na półce · 89,99 zł · Empik');
		expect(copyLine(copy({ price_cents: null, shop: '' }))).toBe('Na półce');
		expect(nbsp(copyLine(copy({ status: 'wishlist', max_price_cents: 6000, shop: '' })))).toBe(
			'Lista życzeń · do 60,00 zł'
		);
		expect(copyLine(copy({ status: 'wishlist', max_price_cents: null, shop: 'OLX' }))).toBe(
			'Lista życzeń · OLX'
		);
	});
});

describe('valueCard', () => {
	it('shows the used value, its range and the difference from the price paid', () => {
		const card = valueCard(copy(), [valued({ copy_id: 99 }), valued()]);
		expect(nbsp(card?.used)).toBe('74,00 zł');
		expect(nbsp(card?.floor)).toBe('16,00 zł');
		expect(nbsp(card?.ceiling)).toBe('103,00 zł');
		expect(nbsp(card?.delta)).toBe('−16 zł względem zakupu');
		expect(card?.usedAt).toBeCloseTo(((7400 - 1600) / (10300 - 1600)) * 100);
		expect(card?.paidAt).toBeCloseTo(((8999 - 1600) / (10300 - 1600)) * 100);
	});

	it.each([
		[5000, '+24 zł względem zakupu'],
		[7400, '0 zł względem zakupu'],
		[7449, '0 zł względem zakupu'],
		[7351, '0 zł względem zakupu'],
		[7350, '+1 zł względem zakupu'],
		[123400, '−1160 zł względem zakupu']
	])('rounds the difference from %i grosze paid to whole złoty', (paid, delta) => {
		expect(nbsp(valueCard(copy({ price_cents: paid }), [valued()])?.delta)).toBe(delta);
	});

	it('leaves out the difference and the paid mark without a price', () => {
		const card = valueCard(copy({ price_cents: null }), [valued()]);
		expect(card?.delta).toBeNull();
		expect(card?.paidAt).toBeNull();
	});

	it('runs the bar from 0 without a buy-back offer and to the higher mark without a new price', () => {
		const card = valueCard(copy({ price_cents: 10000 }), [valued({ floor: null, ceiling: null })]);
		expect(card?.floor).toBe('—');
		expect(card?.ceiling).toBe('—');
		expect(card?.usedAt).toBeCloseTo(74);
		expect(card?.paidAt).toBe(100);
	});

	it('keeps marks outside the range at its ends', () => {
		const card = valueCard(copy({ price_cents: 20000 }), [
			valued({ floor: estimate(8000, 'skupszop') })
		]);
		expect(card?.usedAt).toBe(0);
		expect(card?.paidAt).toBe(100);
	});

	it('centres the marks on a range without width', () => {
		const card = valueCard(copy({ price_cents: 0 }), [
			valued({ floor: estimate(0, 'skupszop'), mid: estimate(0), ceiling: null })
		]);
		expect(card?.usedAt).toBe(50);
		expect(card?.paidAt).toBe(50);
	});

	it('shows the buy-back and new prices without a used value, with no used mark or difference', () => {
		const card = valueCard(copy(), [valued({ mid: null })]);
		expect(card?.used).toBe('—');
		expect(nbsp(card?.floor)).toBe('16,00 zł');
		expect(nbsp(card?.ceiling)).toBe('103,00 zł');
		expect(card?.usedAt).toBeNull();
		expect(card?.delta).toBeNull();
		expect(card?.paidAt).toBeCloseTo(((8999 - 1600) / (10300 - 1600)) * 100);
	});

	const day = (d: string, mid: number | null, copy_id = 7): CopyValueDay => ({
		copy_id,
		day: d,
		floor_cents: 1600,
		mid_cents: mid,
		ceiling_cents: null
	});

	it("draws the trend of the copy's used value over its days", () => {
		const card = valueCard(
			copy(),
			[valued()],
			[
				day('2026-11-18', 6000),
				day('2026-11-18', 99999, 99),
				day('2026-11-19', 6000),
				day('2026-11-20', 7400),
				day('2026-11-20', 1, 99)
			]
		);
		expect(card?.trend).toEqual({ path: 'M4 32 L46 32 L88 4', dots: [] });
	});

	it('leaves a gap on a day without a used value and ends on the last one', () => {
		const card = valueCard(
			copy(),
			[valued()],
			[
				day('2026-11-18', 6000),
				day('2026-11-19', null),
				day('2026-11-20', 7400),
				day('2026-11-21', null)
			]
		);
		expect(card?.trend).toEqual({
			path: '',
			dots: [
				{ x: 4, y: 32 },
				{ x: 88, y: 4 }
			]
		});
	});

	it('leaves a gap on a day with no stored value', () => {
		const card = valueCard(
			copy(),
			[valued()],
			[day('2026-11-18', 6000), day('2026-11-19', 6000), day('2026-11-21', 7400)]
		);
		expect(card?.trend).toEqual({ path: 'M4 32 L32 32', dots: [{ x: 88, y: 4 }] });
	});

	it('keeps a single missing day visible over 90 days', () => {
		const days = Array.from({ length: 90 }, (_, i) =>
			day(new Date(Date.UTC(2026, 7, 24 + i)).toISOString().slice(0, 10), 5000)
		).filter((_, i) => i !== 45);
		const trend = valueCard(copy(), [valued()], days)?.trend;
		const runs = trend?.path.split(' M').map((run) => run.match(/-?[\d.]+/g)?.map(Number) ?? []);
		expect(runs).toHaveLength(2);
		const [before, after] = runs ?? [];
		const gap = (after?.[0] ?? 0) - (before?.at(-2) ?? 0);
		expect(gap).toBeGreaterThan(1.8);
		expect(trend?.dots).toEqual([]);
	});

	it('keeps the trend of a copy whose prices left the window', () => {
		const days = [day('2026-11-18', 6000), day('2026-11-19', 7400)];
		const expired = valueCard(copy(), [valued({ floor: null, mid: null, ceiling: null })], days);
		expect(expired).toEqual({
			used: '—',
			floor: '—',
			ceiling: '—',
			usedAt: null,
			paidAt: null,
			delta: null,
			trend: { path: 'M4 32 L88 4', dots: [] }
		});
		expect(valueCard(copy(), [], days)?.trend).toEqual(expired?.trend);
		expect(valueCard(copy({ status: 'wishlist' }), [], days)).toBeNull();
	});

	it('shows no trend under two days with a used value', () => {
		expect(valueCard(copy(), [valued()])?.trend).toBeNull();
		expect(valueCard(copy(), [valued()], [day('2026-11-20', 7400)])?.trend).toBeNull();
		expect(
			valueCard(copy(), [valued()], [day('2026-11-19', null), day('2026-11-20', 7400)])?.trend
		).toBeNull();
		expect(
			valueCard(copy(), [valued()], [day('2026-11-19', 7000, 99), day('2026-11-20', 7400)])?.trend
		).toBeNull();
	});

	it('is null for a copy without prices, without a valuation or not owned', () => {
		expect(valueCard(copy(), [valued({ floor: null, mid: null, ceiling: null })])).toBeNull();
		expect(valueCard(copy(), [valued({ copy_id: 99 })])).toBeNull();
		expect(valueCard(copy(), [])).toBeNull();
		expect(valueCard(copy({ status: 'wishlist' }), [valued()])).toBeNull();
	});
});

describe('the way back to the collection', () => {
	const from = (path: string) => ({ url: new URL(path, 'https://ohara.test') });

	it.each([
		['link', from('/'), true],
		['link', from('/?kind=movie&status=none'), true],
		['link', from('/scan'), false],
		['link', from('/titles/3'), false],
		['popstate', from('/'), false],
		['goto', from('/'), false],
		['enter', null, false]
	] as const)('a %s navigation from %o opened from the collection: %s', (type, origin, want) => {
		expect(openedFromCollection({ type, from: origin })).toBe(want);
	});

	const click = {
		button: 0,
		metaKey: false,
		ctrlKey: false,
		shiftKey: false,
		altKey: false,
		defaultPrevented: false
	};

	it('handles a plain click itself', () => {
		expect(plainClick(click)).toBe(true);
	});

	it.each([
		{ button: 1 },
		{ metaKey: true },
		{ ctrlKey: true },
		{ shiftKey: true },
		{ altKey: true },
		{ defaultPrevented: true }
	])('leaves %o to the browser', (change) => {
		expect(plainClick({ ...click, ...change })).toBe(false);
	});
});

describe('rating', () => {
	it('offers no rating, then 1 to 10 out of 10', () => {
		expect(ratingOptions[0]).toEqual({ value: noRating, label: 'Brak oceny' });
		expect(ratingOptions.slice(1).map((o) => o.value)).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9, 10]);
		expect(ratingOptions.at(-1)?.label).toBe('10 / 10');
	});
});
