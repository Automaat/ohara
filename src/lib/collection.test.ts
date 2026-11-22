import { describe, expect, it } from 'vitest';
import { purchaseDate } from './polish';
import {
	centsToInput,
	collectionSearch,
	copyPayload,
	copyToForm,
	displayName,
	editionPayload,
	editionSummary,
	editionToForm,
	formatFits,
	formatLabels,
	formatOptions,
	formatPLN,
	kindCounts,
	loanPayload,
	loanSummary,
	loanToForm,
	options,
	parsePLN,
	readCollectionFilter,
	shelfLine,
	shelfSummary,
	titlePayload,
	titleToForm,
	type Edition,
	spentLabel,
	uncopiedLine,
	uncopiedTitles,
	unpricedCount,
	unpricedLabel,
	wishLine,
	withoutCopies,
	type Title
} from './collection';

describe('money', () => {
	it.each([
		['49,99', 4999],
		['49.9', 4990],
		['49', 4900],
		['1 234,50 zł', 123450],
		['0', 0],
		['', null],
		['  ', null],
		['abc', undefined],
		['1,234', undefined],
		['-5', undefined],
		['1000000', 100000000],
		['1000000,01', undefined]
	])('parsePLN(%j) = %j', (input, cents) => {
		expect(parsePLN(input)).toBe(cents);
	});

	it('formats and round-trips grosze', () => {
		expect(formatPLN(4999).replace(/\s/g, ' ')).toBe('49,99 zł');
		expect(formatPLN(null)).toBe('');
		expect(centsToInput(4990)).toBe('49,90');
		expect(centsToInput(null)).toBe('');
		expect(parsePLN(centsToInput(123456))).toBe(123456);
	});
});

describe('labels', () => {
	it('lists options in key order', () => {
		expect(options(formatLabels)).toEqual([
			{ value: 'dvd', label: 'DVD' },
			{ value: 'bluray', label: 'Blu-ray' },
			{ value: 'uhd', label: '4K UHD' },
			{ value: 'cd', label: 'CD' },
			{ value: 'vinyl', label: 'Winyl' },
			{ value: 'cassette', label: 'Kaseta' }
		]);
	});

	it('offers each kind its own media', () => {
		expect(formatOptions('movie').map((o) => o.value)).toEqual(['dvd', 'bluray', 'uhd']);
		expect(formatOptions('music').map((o) => o.value)).toEqual(['cd', 'vinyl', 'cassette']);
		expect(formatOptions('book')).toEqual([]);
		expect(formatFits('music', 'vinyl')).toBe(true);
		expect(formatFits('music', 'dvd')).toBe(false);
		expect(formatFits('book', '')).toBe(false);
	});

	it.each([
		[{ name: 'Seksmisja', polish_title: 'Seksmisja', original_title: null }, 'Seksmisja'],
		[{ name: 'Solaris', polish_title: null, original_title: 'Solaris' }, 'Solaris'],
		[{ name: 'Dune', polish_title: 'Diuna', original_title: 'Dune' }, 'Dune (Diuna)'],
		[{ name: 'Diuna', polish_title: null, original_title: 'Dune' }, 'Diuna (Dune)']
	])('displayName %j', (t, want) => {
		expect(displayName(t)).toBe(want);
	});

	it('summarizes editions', () => {
		expect(
			editionSummary({
				format: 'bluray',
				binding: null,
				label: 'Kolekcjonerska',
				packaging: ['steelbook'],
				barcode: '5901234123457'
			})
		).toBe('Blu-ray · Kolekcjonerska · Steelbook · 5901234123457');
		expect(editionSummary({ format: null, binding: 'paperback', label: '', barcode: null })).toBe(
			'Miękka oprawa'
		);
		expect(editionSummary({ format: null, binding: null, label: '', barcode: null })).toBe(
			'Wydanie bez opisu'
		);
	});
});

describe('collection search links', () => {
	it('restores shareable filters and encodes special characters', () => {
		const filter = {
			kind: 'music',
			format: 'vinyl',
			status: 'owned',
			q: 'Łódź + jazz & blues'
		} as const;
		expect(readCollectionFilter(new URLSearchParams(collectionSearch(filter)))).toEqual(filter);
	});

	it('ignores unsupported options without dropping the query', () => {
		expect(
			readCollectionFilter(
				new URLSearchParams('kind=constructor&format=bad&status=owned&q=Solaris')
			)
		).toEqual({ kind: '', format: '', status: 'owned', q: 'Solaris' });
	});

	it('keeps the unpriced copies in the address, with kind, format and search', () => {
		const filter = { kind: 'movie', format: 'dvd', status: 'unpriced', q: 'Alien' } as const;
		expect(collectionSearch(filter)).toBe('?kind=movie&format=dvd&status=unpriced&q=Alien');
		expect(readCollectionFilter(new URLSearchParams(collectionSearch(filter)))).toEqual(filter);
	});

	it('keeps the lent copies in the address, as the server reads them, with the other filters', () => {
		const filter = { kind: 'book', format: '', status: 'owned', q: 'Lem', loaned: true } as const;
		expect(collectionSearch(filter)).toBe('?kind=book&status=owned&loaned=1&q=Lem');
		expect(readCollectionFilter(new URLSearchParams(collectionSearch(filter)))).toEqual(filter);
		expect(collectionSearch({ ...filter, loaned: false })).toBe('?kind=book&status=owned&q=Lem');
		expect(readCollectionFilter(new URLSearchParams('loaned=true'))).toEqual({
			kind: '',
			format: '',
			status: '',
			q: ''
		});
	});

	it('drops the loan filter while titles without copies are listed', () => {
		expect(readCollectionFilter(new URLSearchParams('status=none&loaned=1'))).toEqual({
			kind: '',
			format: '',
			status: 'none',
			q: ''
		});
	});

	it('keeps titles without copies in the address, with no format', () => {
		const filter = { kind: 'book', format: '', status: 'none', q: 'Lem' } as const;
		expect(collectionSearch(filter)).toBe('?kind=book&status=none&q=Lem');
		expect(readCollectionFilter(new URLSearchParams(collectionSearch(filter)))).toEqual(filter);
		expect(readCollectionFilter(new URLSearchParams('status=none&format=dvd'))).toEqual({
			kind: '',
			format: '',
			status: 'none',
			q: ''
		});
	});
});

const title: Title = {
	id: 1,
	kind: 'book',
	name: 'Solaris',
	original_title: null,
	polish_title: null,
	year: 1961,
	tmdb_id: null,
	authors: ['Stanisław Lem', 'Ktoś'],
	bn_id: 'b1',
	openlibrary_id: null,
	notes: '',
	edition_count: 0,
	copy_count: 0,
	created_at: '',
	updated_at: ''
};

describe('titles without copies', () => {
	const catalog: Title[] = [
		{ ...title, id: 1 },
		{ ...title, id: 2, name: 'Lalka', authors: ['Bolesław Prus'], copy_count: 1 },
		{
			...title,
			id: 3,
			kind: 'movie',
			name: 'Seksmisja',
			polish_title: null,
			original_title: 'Sexmission',
			authors: [],
			edition_count: 2
		},
		{ ...title, id: 4, kind: 'music', name: 'Kind of Blue', authors: ['Miles Davis'] }
	];
	const ids = (titles: Title[]) => titles.map((t) => t.id);

	it('keeps only titles with no copy', () => {
		expect(ids(withoutCopies(catalog))).toEqual([1, 3, 4]);
	});

	it.each([
		[{ kind: '', q: '' }, [1, 3, 4]],
		[{ kind: 'movie', q: '' }, [3]],
		[{ kind: 'book', q: '' }, [1]],
		[{ kind: '', q: '  LEM ' }, [1]],
		[{ kind: '', q: 'sexmission' }, [3]],
		[{ kind: '', q: 'solaris stanisław' }, [1]],
		[{ kind: '', q: 'prus' }, []],
		[{ kind: 'movie', q: 'miles' }, []]
	] as const)('narrows to %j', (filter, want) => {
		expect(ids(uncopiedTitles(catalog, filter))).toEqual(want);
	});

	it.each([
		[
			{ kind: 'book', authors: ['Stanisław Lem', 'Ktoś'], edition_count: 0 },
			'Stanisław Lem, Ktoś · Brak wydań'
		],
		[{ kind: 'movie', authors: [], edition_count: 2 }, 'Film · 2 wydania'],
		[{ kind: 'music', authors: ['Miles Davis'], edition_count: 1 }, 'Miles Davis · 1 wydanie']
	] as const)('describes %j as %s', (t, want) => {
		expect(uncopiedLine({ ...t, authors: [...t.authors] })).toBe(want);
	});
});

describe('title form', () => {
	it('round-trips a title', () => {
		const form = titleToForm({ ...title, cover_url: 'https://example.test/title.jpg' });
		expect(form.authors).toBe('Stanisław Lem\nKtoś');
		expect(form.year).toBe('1961');
		const parsed = titlePayload(form);
		expect(parsed.ok && parsed.value).toMatchObject({
			kind: 'book',
			authors: ['Stanisław Lem', 'Ktoś'],
			bn_id: 'b1',
			cover_url: 'https://example.test/title.jpg',
			year: 1961,
			tmdb_id: null
		});
	});

	it('keeps commas inside an author name', () => {
		const parsed = titlePayload(
			titleToForm({ ...title, authors: ['Asimov, Isaac', 'Lem, Stanisław'] })
		);
		expect(parsed.ok && parsed.value.authors).toEqual(['Asimov, Isaac', 'Lem, Stanisław']);
	});

	it('drops book fields from a movie', () => {
		const form = { ...titleToForm(title), kind: 'movie' as const, tmdb_id: '11906' };
		const parsed = titlePayload(form);
		expect(parsed.ok && parsed.value).toMatchObject({
			authors: [],
			bn_id: null,
			openlibrary_id: null,
			tmdb_id: 11906
		});
	});

	it('starts empty with a kind', () => {
		expect(titleToForm(null, 'book')).toMatchObject({ kind: 'book', name: '', authors: '' });
	});

	it.each([
		[{ name: ' ' }, 'name'],
		[{ name: 'x', year: '19x4' }, 'year'],
		[{ name: 'x', kind: 'movie' as const, tmdb_id: 'abc' }, 'tmdb_id']
	])('rejects %j on %s', (patch, field) => {
		const parsed = titlePayload({ ...titleToForm(null), ...patch });
		expect(parsed.ok).toBe(false);
		expect(!parsed.ok && parsed.issue.field).toBe(field);
	});
});

const edition: Edition = {
	id: 3,
	title_id: 1,
	barcode: '5901234123457',
	label: '',
	publisher: 'Best Film',
	year: 2019,
	format: 'bluray',
	pl_subtitles: 'yes',
	pl_voiceover: 'no',
	packaging: ['steelbook'],
	binding: null,
	discogs_release_id: null,
	notes: '',
	copy_count: 0,
	created_at: '',
	updated_at: ''
};

describe('edition form', () => {
	it('round-trips a movie edition', () => {
		const form = editionToForm({ ...edition, cover_url: 'https://example.test/edition.jpg' });
		const parsed = editionPayload(form, 1, 'movie');
		expect(parsed.ok && parsed.value).toMatchObject({
			barcode: '5901234123457',
			format: 'bluray',
			pl_subtitles: 'yes',
			packaging: ['steelbook'],
			binding: null,
			cover_url: 'https://example.test/edition.jpg',
			year: 2019
		});
	});

	it('drops movie fields from a book edition', () => {
		const form = { ...editionToForm(edition), binding: 'hardcover' as const, barcode: ' ' };
		const parsed = editionPayload(form, 2, 'book');
		expect(parsed.ok && parsed.value).toMatchObject({
			title_id: 2,
			barcode: null,
			format: null,
			pl_subtitles: 'unknown',
			pl_voiceover: 'unknown',
			packaging: [],
			binding: 'hardcover'
		});
	});

	it('requires a format for movies and a numeric year', () => {
		const noFormat = editionPayload(editionToForm(null), 1, 'movie');
		expect(!noFormat.ok && noFormat.issue.field).toBe('format');
		const badYear = editionPayload({ ...editionToForm(edition), year: 'MMXIX' }, 1, 'movie');
		expect(!badYear.ok && badYear.issue.field).toBe('year');
	});

	it("keeps an album edition's medium and Discogs release only", () => {
		const form = {
			...editionToForm(edition),
			format: 'vinyl' as const,
			discogs_release_id: ' 1873013 ',
			binding: 'hardcover' as const
		};
		expect(editionPayload(form, 4, 'music')).toEqual({
			ok: true,
			value: {
				title_id: 4,
				barcode: '5901234123457',
				label: '',
				publisher: 'Best Film',
				year: 2019,
				format: 'vinyl',
				pl_subtitles: 'unknown',
				pl_voiceover: 'unknown',
				packaging: [],
				binding: null,
				discogs_release_id: 1873013,
				cover_url: '',
				notes: ''
			}
		});
		const movie = editionPayload(form, 4, 'movie');
		expect(!movie.ok && movie.issue.field).toBe('format');
		const asMovie = editionPayload({ ...form, format: 'dvd' }, 4, 'movie');
		expect(asMovie.ok && asMovie.value.discogs_release_id).toBeNull();
	});

	it('requires a music medium and a numeric Discogs release for albums', () => {
		const disc = editionPayload({ ...editionToForm(edition), format: 'bluray' }, 4, 'music');
		expect(!disc.ok && disc.issue.field).toBe('format');
		const badID = editionPayload(
			{ ...editionToForm(edition), format: 'cd', discogs_release_id: 'r1873013' },
			4,
			'music'
		);
		expect(!badID.ok && badID.issue.field).toBe('discogs_release_id');
	});

	it('starts empty', () => {
		expect(editionToForm(null)).toMatchObject({
			format: '',
			pl_subtitles: 'unknown',
			packaging: []
		});
	});
});

describe('copy form', () => {
	it('round-trips an owned copy', () => {
		const form = copyToForm({
			edition_id: 3,
			status: 'owned',
			price_cents: 4999,
			purchased_on: '2026-11-01',
			shop: 'OLX',
			acquired_as: 'used',
			condition: 'good',
			notes: '',
			max_price_cents: null
		});
		expect(form.price).toBe('49,99');
		const parsed = copyPayload(form, 3);
		expect(parsed.ok && parsed.value).toEqual({
			edition_id: 3,
			status: 'owned',
			price_cents: 4999,
			purchased_on: '2026-11-01',
			shop: 'OLX',
			acquired_as: 'used',
			condition: 'good',
			notes: '',
			max_price_cents: null
		});
	});

	it('keeps the maximum price of a wishlist copy only', () => {
		const form = copyToForm({
			edition_id: 3,
			status: 'wishlist',
			price_cents: null,
			purchased_on: null,
			shop: '',
			acquired_as: null,
			condition: null,
			notes: '',
			max_price_cents: 6000
		});
		expect(form.max_price).toBe('60,00');
		const wish = copyPayload(form, 3);
		expect(wish.ok && wish.value.max_price_cents).toBe(6000);
		const owned = copyPayload({ ...form, status: 'owned' }, 3);
		expect(owned.ok && owned.value.max_price_cents).toBeNull();
		const blank = copyPayload({ ...form, max_price: ' ' }, 3);
		expect(blank.ok && blank.value.max_price_cents).toBeNull();
	});

	it('strips price and date from a wishlist copy', () => {
		const form = {
			...copyToForm(null),
			status: 'wishlist' as const,
			price: '10',
			purchased_on: '2026-01-01'
		};
		const parsed = copyPayload(form, 3);
		expect(parsed.ok && parsed.value).toMatchObject({
			price_cents: null,
			purchased_on: null,
			acquired_as: null,
			condition: null
		});
	});

	it('round-trips a wish with a market percent', () => {
		const form = copyToForm({
			edition_id: 3,
			status: 'wishlist',
			price_cents: null,
			purchased_on: null,
			shop: '',
			acquired_as: null,
			condition: null,
			notes: '',
			max_price_cents: null,
			max_market_percent: 70
		});
		expect(form.market_percent).toBe('70');
		const wish = copyPayload(form, 3);
		expect(wish.ok && wish.value.max_market_percent).toBe(70);
		const typed = copyPayload({ ...form, market_percent: ' 80 % ' }, 3);
		expect(typed.ok && typed.value.max_market_percent).toBe(80);
		const blank = copyPayload({ ...form, market_percent: ' ' }, 3);
		expect(blank.ok && 'max_market_percent' in blank.value).toBe(false);
		const owned = copyPayload({ ...form, status: 'owned' }, 3);
		expect(owned.ok && 'max_market_percent' in owned.value).toBe(false);
	});

	it.each(['0', '101', '70,5', 'dużo', '-5'])('rejects market percent %j', (percent) => {
		const parsed = copyPayload(
			{ ...copyToForm(null), status: 'wishlist', market_percent: percent },
			3
		);
		expect(!parsed.ok && parsed.issue.field).toBe('max_market_percent');
	});

	it.each([
		[{ price: 'dużo' }, 'price_cents'],
		[{ purchased_on: '1.11.2026' }, 'purchased_on'],
		[{ status: 'wishlist' as const, max_price: 'tanio' }, 'max_price_cents']
	])('rejects %j on %s', (patch, field) => {
		const parsed = copyPayload({ ...copyToForm(null), ...patch }, 3);
		expect(!parsed.ok && parsed.issue.field).toBe(field);
	});
});

describe('spentLabel', () => {
	const base = { copies: 3, owned: 3, wishlist: 0, priced: 3, spent_cents: 12000 };
	it.each([
		{ name: 'every owned copy priced', totals: base, want: /^120,00\s*zł$/ },
		{
			name: 'some without a price',
			totals: { ...base, priced: 1 },
			want: /^120,00\s*zł \(2 bez ceny\)$/
		},
		{
			name: 'none priced',
			totals: { ...base, priced: 0, spent_cents: 0 },
			want: /^0,00\s*zł \(3 bez ceny\)$/
		}
	])('$name', ({ totals, want }) => {
		expect(spentLabel(totals)).toMatch(want);
	});
});

describe('unpriced copies', () => {
	it.each([
		{ owned: 3, priced: 1, want: 2, label: '2 bez ceny' },
		{ owned: 3, priced: 3, want: 0, label: '0 bez ceny' },
		{ owned: 12345, priced: 0, want: 12345, label: /^12\s345 bez ceny$/ }
	])('$owned owned, $priced priced', ({ owned, priced, want, label }) => {
		expect(unpricedCount({ owned, priced })).toBe(want);
		expect(unpricedLabel(want)).toMatch(label);
	});
});

describe('collection header', () => {
	it('counts copies of each kind, including kinds with none', () => {
		const items = [{ kind: 'movie' }, { kind: 'book' }, { kind: 'movie' }] as const;
		expect(kindCounts(items)).toEqual({ movie: 2, book: 1, music: 0 });
		expect(kindCounts([])).toEqual({ movie: 0, book: 0, music: 0 });
	});

	it.each([
		{ owned: 20, wishlist: 4, want: '20 na półce · 4 na liście życzeń' },
		{ owned: 0, wishlist: 0, want: '0 na półce · 0 na liście życzeń' },
		{ owned: 12345, wishlist: 1, want: /^12\s345 na półce · 1 na liście życzeń$/ }
	])('summarizes $owned owned and $wishlist wanted', ({ owned, wishlist, want }) => {
		expect(shelfSummary({ owned, wishlist })).toMatch(want);
	});
});

describe('shelf tile line', () => {
	const owned = {
		status: 'owned' as const,
		max_price_cents: null,
		authors: ['Stanisław Lem'],
		format: null,
		binding: 'hardcover' as const,
		label: ''
	};

	it.each([
		{ max: 8000, want: /^Lista życzeń · do\u00a080\szł$/ },
		{ max: 7999, want: /^Lista życzeń · do\u00a079,99\szł$/ },
		{ max: 0, want: /^Lista życzeń · do\u00a00\szł$/ },
		{ max: null, want: /^Lista życzeń$/ }
	])('names a wish with limit $max', ({ max, want }) => {
		expect(wishLine(max)).toMatch(want);
		expect(shelfLine({ ...owned, status: 'wishlist', max_price_cents: max })).toMatch(want);
	});

	it.each([
		{ due: '2026-05-09', want: 'Wypożyczony: Ania · po terminie' },
		{ due: '2026-05-10', want: 'Wypożyczony: Ania' },
		{ due: '2026-05-11', want: 'Wypożyczony: Ania' },
		{ due: undefined, want: 'Wypożyczony: Ania' }
	])('names who has a lent copy due on $due, late only after that day', ({ due, want }) => {
		const lent = { ...owned, loan_borrower: 'Ania', loan_due_on: due };
		expect(shelfLine(lent, '2026-05-10')).toBe(want);
	});

	it('takes the reader’s today when none is given', () => {
		expect(shelfLine({ ...owned, loan_borrower: 'Ania', loan_due_on: '2000-01-01' })).toBe(
			'Wypożyczony: Ania · po terminie'
		);
		expect(shelfLine({ ...owned, loan_borrower: 'Ania', loan_due_on: '2999-01-01' })).toBe(
			'Wypożyczony: Ania'
		);
	});

	it('gives an owned copy its creators and edition, without a status', () => {
		expect(shelfLine(owned)).toBe('Stanisław Lem · Twarda oprawa');
		expect(shelfLine({ ...owned, authors: [], format: 'bluray', binding: null })).toBe('Blu-ray');
		expect(shelfLine({ ...owned, authors: undefined, binding: null })).toBe('Wydanie bez opisu');
	});
});

describe('loans', () => {
	it('starts a loan form lent today, with no borrower or due date', () => {
		expect(loanToForm('2026-05-10')).toEqual({ borrower: '', lent_on: '2026-05-10', due_on: '' });
	});

	it.each([
		{
			form: { borrower: '  Ania ', lent_on: '2026-05-10', due_on: '2026-06-01' },
			want: { ok: true, value: { borrower: 'Ania', lent_on: '2026-05-10', due_on: '2026-06-01' } }
		},
		{
			form: { borrower: 'Ania', lent_on: ' ', due_on: '' },
			want: { ok: true, value: { borrower: 'Ania', lent_on: null, due_on: null } }
		},
		{
			form: { borrower: 'Ania', lent_on: '2026-05-10', due_on: '2026-05-10' },
			want: { ok: true, value: { borrower: 'Ania', lent_on: '2026-05-10', due_on: '2026-05-10' } }
		},
		{ form: { borrower: ' ', lent_on: '', due_on: '' }, field: 'borrower' },
		{ form: { borrower: 'Ania', lent_on: '10.05.2026', due_on: '' }, field: 'lent_on' },
		{ form: { borrower: 'Ania', lent_on: '', due_on: 'jutro' }, field: 'due_on' },
		{ form: { borrower: 'Ania', lent_on: '2026-05-10', due_on: '2026-05-09' }, field: 'due_on' }
	])('reads a loan form $form', ({ form, want, field }) => {
		const parsed = loanPayload(form);
		if (want) expect(parsed).toEqual(want);
		else expect(!parsed.ok && parsed.issue.field).toBe(field);
	});

	const loan = { borrower: 'Ania', lent_on: '2026-05-01', due_on: null, returned_on: null };
	const may = (d: number) => purchaseDate(`2026-05-${String(d).padStart(2, '0')}`);
	it.each([
		{ over: {}, want: `Ania · od ${may(1)}` },
		{ over: { due_on: '2026-05-10' }, want: `Ania · od ${may(1)} · termin ${may(10)}` },
		{
			over: { due_on: '2026-05-09' },
			want: `Ania · od ${may(1)} · termin ${may(9)} · po terminie`
		},
		{
			over: { due_on: '2026-05-09', returned_on: '2026-05-20' },
			want: `Ania · ${may(1)} – ${may(20)}`
		}
	])('sums up a loan $over on 10 May', ({ over, want }) => {
		expect(loanSummary({ ...loan, ...over }, '2026-05-10')).toBe(want);
	});
});
