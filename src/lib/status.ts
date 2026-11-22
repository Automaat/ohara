import type { PillTone } from '@smykla-skalski/sui';
import type { DatabaseState, Status } from './api.gen';

export type { DatabaseState, Status } from './api.gen';

export class StatusError extends Error {}

/** Reads /api/status. A 503 still carries a body that names the failing part. */
export async function fetchStatus(fetchFn: typeof fetch): Promise<Status> {
	let res: Response;
	try {
		res = await fetchFn('/api/status', { headers: { accept: 'application/json' } });
	} catch {
		throw new StatusError('Serwer nie odpowiada.');
	}
	if (!res.ok && res.status !== 503) {
		throw new StatusError(`Serwer zwrócił błąd ${res.status}.`);
	}
	try {
		return (await res.json()) as Status;
	} catch {
		throw new StatusError('Serwer zwrócił nieczytelną odpowiedź.');
	}
}

const databaseLabels: Record<DatabaseState, { label: string; tone: PillTone }> = {
	ok: { label: 'Działa', tone: 'success' },
	unavailable: { label: 'Niedostępna', tone: 'danger' },
	unconfigured: { label: 'Nieskonfigurowana', tone: 'warning' }
};

/** The Polish word and pill tone for a database state; unknown states read as a warning. */
export function databaseBadge(state: DatabaseState): { label: string; tone: PillTone } {
	return databaseLabels[state] ?? { label: 'Nieznany stan', tone: 'warning' };
}
