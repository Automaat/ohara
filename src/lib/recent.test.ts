import { describe, expect, it } from 'vitest';
import { logScan, recentScans } from './recent.svelte';

describe('logScan', () => {
	it('keeps the 20 newest scans, newest first', () => {
		for (let i = 0; i < 25; i++) logScan(String(i).padStart(8, '0'), 'added', `#${i}`);
		expect(recentScans.list).toHaveLength(20);
		expect(recentScans.list[0]).toMatchObject({ barcode: '00000024', label: '#24' });
		expect(recentScans.list.at(-1)?.barcode).toBe('00000005');
		expect(new Set(recentScans.list.map((r) => r.id)).size).toBe(20);
	});
});
