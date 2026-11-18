import { describe, expect, it } from 'vitest';
import {
	centsToInput,
	collectionURL,
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
	options,
	parsePLN,
	titlePayload,
	titleToForm,
	type Edition,
	spentLabel,
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

describe('collectionURL', () => {
	it.each([
		[{ kind: '', format: '', status: '', q: '' }, '/api/collection'],
		[
			{ kind: 'movie', format: 'uhd', status: 'owned', q: ' lem ' },
			'/api/collection?kind=movie&format=uhd&status=owned&q=lem'
		],
		[{ kind: '', format: '', status: 'wishlist', q: '' }, '/api/collection?status=wishlist']
	] as const)('%j → %s', (filter, url) => {
		expect(collectionURL(filter)).toBe(url);
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

describe('title form', () => {
	it('round-trips a title', () => {
		const form = titleToForm(title);
		expect(form.authors).toBe('Stanisław Lem\nKtoś');
		expect(form.year).toBe('1961');
		const parsed = titlePayload(form);
		expect(parsed.ok && parsed.value).toMatchObject({
			kind: 'book',
			authors: ['Stanisław Lem', 'Ktoś'],
			bn_id: 'b1',
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
		const parsed = editionPayload(editionToForm(edition), 1, 'movie');
		expect(parsed.ok && parsed.value).toMatchObject({
			barcode: '5901234123457',
			format: 'bluray',
			pl_subtitles: 'yes',
			packaging: ['steelbook'],
			binding: null,
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
