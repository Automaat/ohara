import { describe, expect, it } from 'vitest';
import type { CollectionFilter, CollectionItem } from './collection';
import { filterCollection, lowerCase, searchBarcode, searchTerm } from './collection-filter';

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
