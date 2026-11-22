const numberFormat = new Intl.NumberFormat('pl-PL');
const pluralRules = new Intl.PluralRules('pl-PL');
const purchaseDateFormat = new Intl.DateTimeFormat('pl-PL', {
	dateStyle: 'medium',
	timeZone: 'UTC'
});

/** A count with its Polish noun, in the form for one, a few (2–4, 22–24…) or many. */
function counted(count: number, one: string, few: string, many: string): string {
	const plural = pluralRules.select(count);
	const noun = plural === 'one' ? one : plural === 'few' ? few : many;
	return `${numberFormat.format(count)} ${noun}`;
}

export function resultCount(count: number): string {
	return counted(count, 'wynik', 'wyniki', 'wyników');
}

export function editionCount(count: number): string {
	return counted(count, 'wydanie', 'wydania', 'wydań');
}

export function copyCount(count: number): string {
	return counted(count, 'egzemplarz', 'egzemplarze', 'egzemplarzy');
}

/** The discs an edition holds, such as "2 płyty". */
export function discCount(count: number): string {
	return counted(count, 'płyta', 'płyty', 'płyt');
}

/** A limited edition's run, such as "nakład 500 egz.". */
export function limitedRun(count: number): string {
	return `nakład ${numberFormat.format(count)} egz.`;
}

export function titleCount(count: number): string {
	return counted(count, 'tytuł', 'tytuły', 'tytułów');
}

/** Copies on their way, such as "2 zamówione". */
export function orderedCount(count: number): string {
	return counted(count, 'zamówiony', 'zamówione', 'zamówionych');
}

/** Codes in the scan queue, such as "2 kody czekają na uzupełnienie". */
export function queuedCodes(count: number): string {
	const plural = pluralRules.select(count);
	const phrase = plural === 'one' ? 'kod czeka' : plural === 'few' ? 'kody czekają' : 'kodów czeka';
	return `${numberFormat.format(count)} ${phrase} na uzupełnienie`;
}

export function purchaseDate(date: string | null): string {
	return date ? purchaseDateFormat.format(new Date(date)) : '';
}

/** How many times a queued code was scanned, such as "2 skany". */
export function scanCount(count: number): string {
	return counted(count, 'skan', 'skany', 'skanów');
}
