import { describe, expect, it } from 'vitest';
import { exportChoices, exportHref } from './export';

describe('collection export', () => {
	it('offers CSV first, then JSON', () => {
		expect(exportChoices.map((c) => c.format)).toEqual(['csv', 'json']);
		expect(exportChoices.map((c) => c.label)).toEqual(['CSV (Excel)', 'JSON']);
	});

	it.each(['csv', 'json'] as const)('downloads %s from the export route', (format) => {
		expect(exportHref(format)).toBe(`/api/export?format=${format}`);
	});
});
