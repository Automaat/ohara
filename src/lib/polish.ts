const numberFormat = new Intl.NumberFormat('pl-PL');
const pluralRules = new Intl.PluralRules('pl-PL');
const purchaseDateFormat = new Intl.DateTimeFormat('pl-PL', {
	dateStyle: 'medium',
	timeZone: 'UTC'
});

export function resultCount(count: number): string {
	const plural = pluralRules.select(count);
	const noun = plural === 'one' ? 'wynik' : plural === 'few' ? 'wyniki' : 'wyników';
	return `${numberFormat.format(count)} ${noun}`;
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
