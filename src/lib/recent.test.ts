import { describe, expect, it } from 'vitest';
import { logScan, recentScans, retractScan } from './recent.svelte';

describe('logScan', () => {
	it('keeps the 20 newest scans, newest first', () => {
		for (let i = 0; i < 25; i++) logScan(String(i).padStart(8, '0'), 'added', `#${i}`);
		expect(recentScans.list).toHaveLength(20);
		expect(recentScans.list[0]).toMatchObject({ barcode: '00000024', label: '#24' });
		expect(recentScans.list.at(-1)?.barcode).toBe('00000005');
		expect(new Set(recentScans.list.map((r) => r.id)).size).toBe(20);
	});

	it('marks an undone add without its link to the copy', () => {
		const id = logScan('5901234123457', 'added', 'Rejs', '/copies/40');
		logScan('5901234123458', 'skipped', 'Nie dodano');
		retractScan(id);
		expect(recentScans.list[1]).toMatchObject({ id, outcome: 'undone', label: 'Rejs' });
		expect(recentScans.list[1].href).toBeUndefined();
		expect(recentScans.list[0].outcome).toBe('skipped');
		retractScan(-1);
	});
});
