import { describe, expect, it } from 'vitest';
import type { CollectionFilter, CollectionItem } from './collection';
import {
	filterCollection,
	locationsInUse,
	lowerCase,
	searchBarcode,
	searchTerm
} from './collection-filter';

function item(over: Partial<CollectionItem>): CollectionItem {
	return {
		copy_id: 1,
		created_at_us: 1,
		status: 'owned',
		price_cents: null,
		max_price_cents: null,
		purchased_on: null,
		shop: '',
		acquired_as: null,
		condition: null,
		edition_id: 1,
		barcode: null,
		label: '',
		format: null,
		binding: null,
		title_id: 1,
		kind: 'movie',
		name: '',
		original_title: null,
		polish_title: null,
		year: null,
		...over
	};
}

/** One copy per field the server's filter reads, each findable by its own words. */
const items: CollectionItem[] = [
	item({
		copy_id: 1,
		kind: 'movie',
		format: 'bluray',
		name: 'Interstellar',
		original_title: 'Interstellar',
		polish_title: null,
		label: 'Steelbook',
		shop: 'Empik',
		barcode: '5051892214179'
	}),
	item({
		copy_id: 2,
		kind: 'movie',
		format: 'dvd',
		status: 'wishlist',
		name: 'Siedem samurajów',
		original_title: 'Shichinin no samurai',
		polish_title: 'Siedmiu samurajów',
		shop: 'Allegro'
	}),
	item({
		copy_id: 3,
		kind: 'book',
		name: 'Solaris',
		authors: ['Stanisław Lem'],
		label: 'Wydanie kolekcjonerskie',
		barcode: '9788374690997'
	}),
	item({
		copy_id: 4,
		kind: 'music',
		format: 'vinyl',
		name: 'Kind of Blue',
		authors: ['Miles Davis'],
		shop: 'Media Markt',
		barcode: '0074646393529'
	}),
	item({
		copy_id: 5,
		kind: 'music',
		format: 'cd',
		status: 'wishlist',
		name: 'Blue Train',
		authors: ['John Coltrane'],
		barcode: '40000008'
	}),
	item({
		copy_id: 6,
		kind: 'book',
		name: 'Eden',
		authors: [],
		label: 'Pierwsze',
		shop: '',
		barcode: '9781234567897'
	})
];

const none: CollectionFilter = { kind: '', format: '', status: '', q: '' };

/**
 * The `WHERE` clause of `Store.Collection`, row by row: `t.kind = $1`, `e.format = $2`,
 * `c.status = $3`, then `e.barcode = $4` or the lower-cased `concat_ws(' ', t.name,
 * t.original_title, t.polish_title, array_to_string(t.authors, ' '), e.label, c.shop)` holding
 * `lower($4)`, where `$4` is the search after barcode.Normalize, trimmed.
 */
const cases: [string, Partial<CollectionFilter>, number[]][] = [
	['no filter lists every copy', {}, [1, 2, 3, 4, 5, 6]],
	['kind', { kind: 'book' }, [3, 6]],
	['format', { format: 'vinyl' }, [4]],
	['status owned', { status: 'owned' }, [1, 3, 4, 6]],
	['status wishlist', { status: 'wishlist' }, [2, 5]],
	['kind, format and status together', { kind: 'music', format: 'cd', status: 'wishlist' }, [5]],
	['kind and format that never meet', { kind: 'book', format: 'dvd' }, []],
	['name', { q: 'solaris' }, [3]],
	['original title', { q: 'Shichinin' }, [2]],
	['Polish title', { q: 'siedmiu' }, [2]],
	['authors', { q: 'lem' }, [3]],
	['second author word', { q: 'davis' }, [4]],
	['label', { q: 'steelbook' }, [1]],
	['shop', { q: 'media markt' }, [4]],
	['upper case typed', { q: 'EMPIK' }, [1]],
	['Polish letters, case aside', { q: 'STANISŁAW' }, [3]],
	['missing titles add no space between fields', { q: 'solaris stanisław' }, [3]],
	['an empty author list still takes a space', { q: 'eden  pierwsze' }, [6]],
	['so one space does not bridge it', { q: 'eden pierwsze' }, []],
	['nor does it between a film and its label', { q: 'interstellar steelbook' }, []],
	['two spaces do', { q: 'interstellar  steelbook' }, [1]],
	['a missing title is not the text "null"', { q: 'null' }, []],
	['blue matches two albums', { q: 'blue' }, [4, 5]],
	['search within a kind', { kind: 'music', q: 'blue' }, [4, 5]],
	['search within a status', { status: 'owned', q: 'blue' }, [4]],
	['spaces around the search are trimmed', { q: '  solaris  ' }, [3]],
	['a search of spaces is no search', { q: '   ' }, [1, 2, 3, 4, 5, 6]],
	['EAN-13 as stored', { q: '5051892214179' }, [1]],
	['EAN-13 with spaces and hyphens', { q: '505-1892 214179' }, [1]],
	['ISBN-13 with hyphens', { q: '978-83-7469-099-7' }, [3]],
	['ISBN-10 finds its ISBN-13', { q: '8374690992' }, [3]],
	['ISBN-10 with hyphens', { q: '83-7469-099-2' }, [3]],
	['ISBN-10 ending in X, lower case', { q: '123456789x' }, [6]],
	['UPC-A finds its EAN-13', { q: '074646393529' }, [4]],
	['GTIN-14 with a leading zero', { q: '00074646393529' }, [4]],
	['EAN-8', { q: '40000008' }, [5]],
	['EAN-8 padded to 13 digits', { q: '0000040000008' }, [5]],
	['part of a barcode finds nothing', { q: '50518922' }, []],
	['a wrong check digit finds nothing', { q: '5051892214178' }, []],
	['a wrong ISBN-10 check digit finds nothing', { q: '8374690993' }, []],
	['a barcode search keeps the other filters', { kind: 'movie', q: '9788374690997' }, []]
];

describe('filterCollection', () => {
	it.each(cases)('%s', (_name, filter, copies) => {
		const listed = filterCollection(items, { ...none, ...filter });
		expect(listed.map((i) => i.copy_id)).toEqual(copies);
	});

	it('keeps the order it was given', () => {
		const reversed = [...items].reverse();
		expect(filterCollection(reversed, { ...none, q: 'blue' }).map((i) => i.copy_id)).toEqual([
			5, 4
		]);
	});

	it('matches a stored code that is not a valid barcode only as typed', () => {
		const odd = item({ copy_id: 9, barcode: 'ABC-1' });
		expect(filterCollection([odd], { ...none, q: ' ABC-1 ' })).toEqual([odd]);
		expect(filterCollection([odd], { ...none, q: 'abc-1' })).toEqual([]);
	});
});

describe('the unpriced status', () => {
	/** Owned copies with and without a price, a free one, and a wish that has no price either. */
	const priced: CollectionItem[] = [
		item({ copy_id: 1, kind: 'movie', format: 'dvd', name: 'Alien', price_cents: 2999 }),
		item({ copy_id: 2, kind: 'movie', format: 'dvd', name: 'Aliens' }),
		item({ copy_id: 3, kind: 'book', name: 'Solaris', authors: ['Stanisław Lem'] }),
		item({ copy_id: 4, kind: 'book', name: 'Eden', price_cents: 0 }),
		item({ copy_id: 5, kind: 'movie', format: 'bluray', name: 'Alien³', status: 'wishlist' }),
		item({ copy_id: 6, kind: 'music', format: 'vinyl', name: 'Kind of Blue' })
	];

	it.each([
		['lists owned copies without a price, never a wish', {}, [2, 3, 6]],
		['a price of 0 zł is a price', { kind: 'book' }, [3]],
		['with a kind', { kind: 'movie' }, [2]],
		['with a medium', { format: 'vinyl' }, [6]],
		['with a search', { q: 'lem' }, [3]],
		['with a kind whose only unpriced copy is of another kind', { kind: 'movie', q: 'lem' }, []],
		['with a medium no unpriced copy has', { format: 'bluray' }, []]
	] as [string, Partial<CollectionFilter>, number[]][])('%s', (_name, filter, copies) => {
		const listed = filterCollection(priced, { ...none, status: 'unpriced', ...filter });
		expect(listed.map((i) => i.copy_id)).toEqual(copies);
	});
});

describe('the loan filter', () => {
	/** Lent copies of each kind, one without a due date, and copies on the shelf or wished. */
	const lent: CollectionItem[] = [
		item({ copy_id: 1, kind: 'movie', format: 'dvd', name: 'Rejs', loan_borrower: 'Ania' }),
		item({
			copy_id: 2,
			kind: 'book',
			name: 'Solaris',
			loan_borrower: 'Bartek',
			loan_due_on: '2026-05-01'
		}),
		item({ copy_id: 3, kind: 'book', name: 'Eden' }),
		item({ copy_id: 4, kind: 'movie', format: 'dvd', name: 'Miś', status: 'wishlist' })
	];

	it.each([
		['lists copies out on loan only', {}, [1, 2]],
		['with a kind', { kind: 'book' }, [2]],
		['with a medium', { format: 'dvd' }, [1]],
		['with a search', { q: 'rejs' }, [1]],
		['with a status', { status: 'owned' }, [1, 2]],
		['with the wishlist, which is never lent', { status: 'wishlist' }, []],
		['a borrower is not searched, as on the server', { q: 'ania' }, []]
	] as [string, Partial<CollectionFilter>, number[]][])('%s', (_name, filter, copies) => {
		const listed = filterCollection(lent, { ...none, loaned: true, ...filter });
		expect(listed.map((i) => i.copy_id)).toEqual(copies);
	});
});

describe('the location filter', () => {
	/** Copies on two shelves spelled in different cases, one in a box, one nowhere and a wish. */
	const placed: CollectionItem[] = [
		item({
			copy_id: 1,
			kind: 'movie',
			format: 'dvd',
			name: 'Alien',
			shop: 'Empik',
			location: 'Regał 2'
		}),
		item({ copy_id: 2, kind: 'book', name: 'Solaris', location: 'REGAŁ 2' }),
		item({ copy_id: 3, kind: 'movie', format: 'dvd', name: 'Aliens', location: 'Regał 2 dolny' }),
		item({ copy_id: 4, kind: 'movie', format: 'dvd', name: 'Rejs', location: null }),
		item({ copy_id: 5, kind: 'movie', format: 'dvd', name: 'Seksmisja', status: 'wishlist' })
	];

	/**
	 * The server's `lower(c.location) = lower($5)`, `$5` trimmed, beside the other filters, and
	 * `c.location` as the last field of the search's `concat_ws`.
	 */
	it.each([
		['the whole location, case aside', { location: 'regał 2' }, [1, 2]],
		['trimmed', { location: '  Regał 2  ' }, [1, 2]],
		['never part of one', { location: 'Regał' }, []],
		['with a kind', { location: 'Regał 2', kind: 'book' }, [2]],
		['with a status no placed copy has', { location: 'Regał 2', status: 'wishlist' }, []],
		['a blank location is no filter', { location: ' ' }, [1, 2, 3, 4, 5]],
		['the search reads the location', { q: 'dolny' }, [3]],
		['after the shop, one space apart', { q: 'empik regał 2' }, [1]],
		['a missing location is not the text "null"', { q: 'null' }, []]
	] as [string, Partial<CollectionFilter>, number[]][])('%s', (_name, filter, copies) => {
		const listed = filterCollection(placed, { ...none, ...filter });
		expect(listed.map((i) => i.copy_id)).toEqual(copies);
	});
});

describe('the location filter, as the server folds case', () => {
	const odd: CollectionItem[] = [
		item({ copy_id: 1, location: 'Box 012345678905' }),
		item({ copy_id: 2, location: '\ufeffRegał' }),
		item({ copy_id: 3, location: '\u{10D50}' }),
		item({ copy_id: 4, location: '\u{10D70}' })
	];

	it.each([
		[
			'a code read as a barcode still finds a location holding it as typed',
			{ q: '012345678905' },
			[1]
		],
		['a byte order mark is part of the location', { location: '\ufeffregał' }, [2]],
		['so the location without it is another', { location: 'Regał' }, []],
		['letters Unicode 17 pairs match case aside', { location: '\u{10D50}' }, [3, 4]]
	] as [string, Partial<CollectionFilter>, number[]][])('%s', (_name, filter, copies) => {
		expect(filterCollection(odd, { ...none, ...filter }).map((i) => i.copy_id)).toEqual(copies);
	});

	it('takes a final sigma for σ, so ΟΣ and ος are one place', () => {
		const greek = [item({ copy_id: 1, location: 'ΟΣ' }), item({ copy_id: 2, location: 'ος' })];
		expect(locationsInUse(greek)).toEqual(['ΟΣ']);
		for (const location of ['ΟΣ', 'ος', 'οσ']) {
			expect(filterCollection(greek, { ...none, location }).map((i) => i.copy_id)).toEqual([1, 2]);
		}
	});

	it('merges those letters into one place and orders ties by code point', () => {
		expect(locationsInUse(odd)).toEqual(['\u{10D50}', 'Box 012345678905', '\ufeffRegał']);
	});
});

describe('locationsInUse', () => {
	it('lists each place once whatever its case, the most used first', () => {
		const places = ['Pudło', 'regał 2', 'Regał 2', 'Regał 2', 'Strych', 'Strych', 'Szafa'];
		const items = [...places.map((location) => ({ location })), { location: null }, {}];
		expect(locationsInUse(items)).toEqual(['Regał 2', 'Strych', 'Pudło', 'Szafa']);
	});

	it('lists nothing when no copy has a place', () => {
		expect(locationsInUse([{ location: null }, {}])).toEqual([]);
	});
});

describe('sold copies', () => {
	const shelf: CollectionItem[] = [
		item({ copy_id: 1, kind: 'movie', format: 'dvd', name: 'Alien', price_cents: 2999 }),
		item({ copy_id: 2, kind: 'movie', format: 'dvd', name: 'Aliens', status: 'wishlist' }),
		item({
			copy_id: 3,
			kind: 'movie',
			format: 'dvd',
			name: 'Alien³',
			status: 'sold',
			sold_on: '2026-05-10'
		}),
		item({ copy_id: 4, kind: 'book', name: 'Solaris', status: 'sold', sold_on: '2026-05-10' })
	];

	it.each([
		['the default list leaves them out', {}, [1, 2]],
		['a search leaves them out', { q: 'alien' }, [1, 2]],
		['"Sprzedane" lists them alone', { status: 'sold' }, [3, 4]],
		['"Sprzedane" with a kind', { status: 'sold', kind: 'book' }, [4]],
		['"Sprzedane" with a search', { status: 'sold', q: 'alien' }, [3]],
		['owned copies leave them out', { status: 'owned' }, [1]],
		['copies without a price leave them out', { status: 'unpriced' }, []]
	] as [string, Partial<CollectionFilter>, number[]][])('%s', (_name, filter, copies) => {
		const listed = filterCollection(shelf, { ...none, ...filter });
		expect(listed.map((i) => i.copy_id)).toEqual(copies);
	});
});

describe('the tag filter', () => {
	/** Copies of tagged titles, as `GET /api/collection` gives each the title's tags. */
	const tagged: CollectionItem[] = [
		item({ copy_id: 1, name: 'Obcy', format: 'dvd', tags: ['Horror', 'Kult'] }),
		item({ copy_id: 2, name: 'Coś', format: 'bluray', tags: ['horror'] }),
		item({ copy_id: 3, kind: 'book', name: 'Lalka', tags: ['Do sprzedania'] }),
		item({ copy_id: 4, kind: 'book', name: 'Lód', status: 'wishlist', tags: ['Ścisła'] }),
		item({ copy_id: 5, name: 'Rejs' }),
		item({ copy_id: 6, kind: 'music', name: 'Ήχοι', tags: ['ΟΣ', 'Ᲊ'] })
	];

	it.each([
		['an empty tag lists every copy', { tag: '' }, [1, 2, 3, 4, 5, 6]],
		['a tag of spaces is no tag', { tag: '  ' }, [1, 2, 3, 4, 5, 6]],
		['a final sigma folds as the server folds it', { tag: 'ος' }, [6]],
		['a capital the server folds by Go, not Postgres', { tag: 'ᲊ' }, [6]],
		['case aside', { tag: 'HORROR' }, [1, 2]],
		['trimmed as the server trims it', { tag: '\u00a0kult\t' }, [1]],
		['Polish letters, case aside', { tag: 'ŚCISŁA' }, [4]],
		['the whole tag, not a part', { tag: 'horr' }, []],
		['a tag of several words', { tag: 'do sprzedania' }, [3]],
		['with a medium', { tag: 'horror', format: 'bluray' }, [2]],
		['with a status', { tag: 'ścisła', status: 'owned' }, []],
		['with a search', { tag: 'horror', q: 'obcy' }, [1]]
	] as [string, Partial<CollectionFilter>, number[]][])('%s', (_name, filter, copies) => {
		const listed = filterCollection(tagged, { ...none, ...filter });
		expect(listed.map((i) => i.copy_id)).toEqual(copies);
	});
});

describe('searchBarcode', () => {
	it.each([
		['9788374690997', '9788374690997'],
		['8374690992', '9788374690997'],
		['83 7469 099 2', '9788374690997'],
		['83\t7469-0992', '9788374690997'],
		['123456789X', '9781234567897'],
		['123456789x', '9781234567897'],
		['074646393529', '0074646393529'],
		['00074646393529', '0074646393529'],
		['10074646393526', '10074646393526'],
		['40000008', '40000008'],
		['00000040000008', '40000008']
	])('%s → %s', (raw, code) => {
		expect(searchBarcode(raw)).toBe(code);
	});

	it.each(['', 'solaris', '12345', '8374690993', '5051892214178', '978\n8374690997'])(
		'%j is no barcode',
		(raw) => {
			expect(searchBarcode(raw)).toBeNull();
		}
	);
});

describe('searchTerm', () => {
	it('is the code for a barcode and the trimmed text otherwise', () => {
		expect(searchTerm(' 83-7469-099-2 ')).toBe('9788374690997');
		expect(searchTerm('  Lem ')).toBe('Lem');
		expect(searchTerm('\u0085 Lem\u3000')).toBe('Lem');
		expect(searchTerm('\ufeffLem')).toBe('\ufeffLem');
	});
});

describe('where JavaScript differs from PostgreSQL and Go', () => {
	const greek = item({ copy_id: 7, name: 'ΟΣ' });
	const book = item({ copy_id: 8, kind: 'book', name: 'Solaris', barcode: '9788374690997' });

	it.each([
		['a final capital sigma lowers to σ, as lower() maps it', 'σ', [7]],
		['so ς finds nothing', 'ς', []],
		['U+0085 around the search is white space to Go', '\u0085solaris\u0085', [8]],
		['a search of U+0085 alone is no search', '\u0085', [7, 8]],
		[
			'an ISBN-10 between no-break spaces is no barcode to Go, only text',
			'\u00a08374690992\u00a0',
			[]
		],
		['nor is one between newlines', '\n8374690992\n', []],
		['a lone U+FEFF is text Go does not trim', '\ufeff', []],
		['text between no-break spaces too', '\u00a0solaris\u00a0', [8]]
	])('%s', (_name, q, copies) => {
		const listed = filterCollection([greek, book], { ...none, q });
		expect(listed.map((i) => i.copy_id)).toEqual(copies);
	});
});

describe('lowerCase', () => {
	it.each([
		['ΟΣ', 'οσ'],
		['ŁÓDŹ', 'łódź'],
		['İstanbul', 'istanbul'],
		['Straße', 'straße']
	])('%s → %s', (text, lower) => {
		expect(lowerCase(text)).toBe(lower);
	});
});
