/**
 * Scan queue DELETEs on their way to the server. A removal often goes out as the queue page
 * closes, so the module keeps them past that page: a later read of the queue, or a scan that
 * would queue the same barcode again, waits for them instead of racing them.
 */
const inFlight = new Set<Promise<unknown>>();

/** Keeps a queue DELETE in view until it settles, and hands it back unchanged. */
export function trackDelete<T>(request: Promise<T>): Promise<T> {
	inFlight.add(request);
	const settle = () => inFlight.delete(request);
	request.then(settle, settle);
	return request;
}

/** Resolves once every queue DELETE sent so far has settled, whatever its outcome. */
export async function queueDeletesSettled(): Promise<void> {
	await Promise.allSettled([...inFlight]);
}
