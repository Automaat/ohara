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

export function purchaseDate(date: string | null): string {
	return date ? purchaseDateFormat.format(new Date(date)) : '';
}
