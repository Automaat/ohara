/** A file format `GET /api/export` answers in. */
export type ExportFormat = 'csv' | 'json';

/** The export's choices on Kolekcja, in menu order. */
export const exportChoices: readonly {
	format: ExportFormat;
	label: string;
	description: string;
}[] = [
	{ format: 'csv', label: 'CSV (Excel)', description: 'Jeden wiersz na egzemplarz' },
	{ format: 'json', label: 'JSON', description: 'Tytuły, wydania i egzemplarze' }
];

/** Where the browser downloads the collection in format from. */
export function exportHref(format: ExportFormat): string {
	return `/api/export?format=${format}`;
}
