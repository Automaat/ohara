import { describe, expect, it, vi } from 'vitest';
import { QueueTally } from './queue-tally.svelte';

const entry = (id: number, barcode: string) => ({
	id,
	barcode,
	reason: 'not_found',
	detail: '',
	scans: 1,
	created_at: '2026-11-22T08:00:00Z',
	last_scanned_at: '2026-11-22T08:00:00Z'
});

const listing = (...barcodes: string[]) =>
	new Response(JSON.stringify(barcodes.map((b, i) => entry(i + 1, b))), { status: 200 });

/** A queue read the test answers when it chooses, to hold a load in flight. */
function held() {
	const answers: ((res: Response) => void)[] = [];
	const fetchFn: typeof fetch = () => new Promise<Response>((resolve) => answers.push(resolve));
	return {
		fetchFn,
		asked: (n: number) => vi.waitFor(() => expect(answers).toHaveLength(n)),
		answer: (i: number, res: Response) => answers[i]?.(res)
	};
}

async function loaded(...barcodes: string[]): Promise<QueueTally> {
	const tally = new QueueTally();
	await tally.load(async () => listing(...barcodes));
	return tally;
}

describe('QueueTally', () => {
	it('counts the queue it read and nothing before that', async () => {
		const tally = new QueueTally();
		expect(tally.count).toBeNull();
		await tally.load(async () => listing('5901234123457', '5907777123456'));
		expect(tally.count).toBe(2);
	});

	it('reads the queue at its path', async () => {
		const asked: string[] = [];
		await new QueueTally().load(async (input, init) => {
			asked.push(`${init?.method ?? 'GET'} ${String(input)}`);
			return listing();
		});
		expect(asked).toEqual(['GET /api/scan-queue']);
	});

	it('counts a scan the queue took once, however often it comes back', async () => {
		const tally = await loaded('5901234123457');
		tally.queued('5907777123456');
		expect(tally.count).toBe(2);
		tally.queued('5907777123456');
		tally.queued('5901234123457');
		expect(tally.count).toBe(2);
	});

	it('takes out a code an add resolved and counts it again when the add is undone', async () => {
		const tally = await loaded('5901234123457', '5907777123456');
		tally.resolved(31, '5907777123456');
		expect(tally.count).toBe(1);
		tally.requeued(31);
		expect(tally.count).toBe(2);
		tally.requeued(31);
		expect(tally.count).toBe(2);
	});

	it('leaves the count alone when an undone add resolved no queued code', async () => {
		const tally = await loaded('5901234123457');
		tally.resolved(31, '5907777123456');
		expect(tally.count).toBe(1);
		tally.requeued(31);
		tally.requeued(99);
		expect(tally.count).toBe(1);
	});

	it('forgets the count when the queue read fails, and counts again on the next one', async () => {
		const tally = await loaded('5901234123457');
		await tally.load(async () => new Response(JSON.stringify({ error: 'boom' }), { status: 500 }));
		expect(tally.count).toBeNull();
		tally.queued('5907777123456');
		expect(tally.count).toBeNull();
		await tally.load(async () => listing('5901234123457', '5907777123456'));
		expect(tally.count).toBe(2);
	});

	it('applies the changes made while a read is out over its answer', async () => {
		const tally = await loaded('5901234123457', '5907777123456');
		const queue = held();
		const reading = tally.load(queue.fetchFn);
		tally.queued('5900000000001');
		tally.queued('5900000000002');
		tally.resolved(31, '5907777123456');
		expect(tally.count).toBe(3);
		await queue.asked(1);
		queue.answer(0, listing('5901234123457', '5907777123456'));
		await reading;
		expect(tally.count).toBe(3);
		tally.requeued(31);
		expect(tally.count).toBe(4);
	});

	it('keeps the newer read when an older answer comes last', async () => {
		const tally = new QueueTally();
		const queue = held();
		const older = tally.load(queue.fetchFn);
		const newer = tally.load(queue.fetchFn);
		await queue.asked(2);
		queue.answer(1, listing('5901234123457'));
		await newer;
		queue.answer(0, listing('5901234123457', '5907777123456', '5900000000001'));
		await older;
		expect(tally.count).toBe(1);
	});

	it('keeps the newer read when an older one fails last', async () => {
		const tally = new QueueTally();
		const queue = held();
		const older = tally.load(queue.fetchFn);
		const newer = tally.load(queue.fetchFn);
		await queue.asked(2);
		queue.answer(1, listing('5901234123457'));
		await newer;
		queue.answer(0, new Response(null, { status: 503 }));
		await older;
		expect(tally.count).toBe(1);
	});
});
