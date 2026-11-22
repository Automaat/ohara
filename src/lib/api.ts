import type { ErrorBody as GeneratedErrorBody } from './api.gen';
import type { FormIssue } from './collection';

/** An API failure with a Polish message and, for validation errors, the field it names. */
export class ApiError extends Error {
	constructor(
		message: string,
		readonly status: number,
		readonly field?: string
	) {
		super(message);
	}
}

/** The server's error body, read without trusting its value types. */
type ErrorBody = { [K in keyof GeneratedErrorBody]?: unknown };

/** What the server's 422 for a barcode means: the shape fits, the check digit does not. */
export const invalidBarcodeMessage = 'Nieprawidłowy kod kreskowy: sprawdź cyfry.';

/** What the server's 413 for a cover photo means. */
export const coverTooLargeMessage = 'Wybierz plik o rozmiarze do 8 MiB.';

/** What the server's 422 for a cover photo means, and what a photo the browser cannot read says. */
export const invalidCoverMessage =
	'Wybierz prawidłowe zdjęcie JPEG lub PNG o rozdzielczości do 16 megapikseli.';

/** What a request that never got an answer says. */
export const noAnswerMessage = 'Serwer nie odpowiada.';

/**
 * What a request the sign-in in front of the app turned away says: a 401, or a redirect to the
 * sign-in page on another origin.
 */
export const sessionExpiredMessage = 'Sesja wygasła. Odśwież, aby zalogować się ponownie.';

const fieldMessages: Record<string, Record<number, string>> = {
	barcode: {
		409: 'Wydanie z tym kodem kreskowym już istnieje.',
		422: invalidBarcodeMessage
	},
	tmdb_id: { 409: 'Tytuł z tym identyfikatorem TMDB już istnieje.' },
	kind: { 409: 'Nie można zmienić typu tytułu, który ma wydania.' },
	series_number: { 409: 'Inny tytuł tej serii ma już ten numer tomu.' },
	copy_number: {
		409: 'Inny egzemplarz tego wydania ma już ten numer.',
		422: 'Numer egzemplarza nie może przekraczać nakładu wydania.'
	},
	limited_to: { 422: 'Nakład nie może być mniejszy niż numer egzemplarza tego wydania.' },
	title_id: { 422: 'Wybrany tytuł nie istnieje.' },
	edition_id: { 422: 'Wybrane wydanie nie istnieje.' },
	phrase: { 409: 'Ta fraza już jest na liście.' },
	status: { 409: 'Wypożyczony egzemplarz zostaje na półce, dopóki nie wróci.' },
	cover_photo: {
		400: 'Przesyłanie zdjęcia zostało przerwane. Spróbuj ponownie.',
		413: coverTooLargeMessage,
		422: invalidCoverMessage
	}
};

/** Fields whose errors carry a Polish message the page shows as it is. */
const ownMessageFields = new Set(['discogs_collection']);

function messageFor(status: number, body: ErrorBody): string {
	const field = typeof body.field === 'string' ? body.field : undefined;
	if (field && ownMessageFields.has(field) && typeof body.error === 'string') return body.error;
	const known = field ? fieldMessages[field]?.[status] : undefined;
	if (known) return known;
	if (status === 404) return 'Nie znaleziono.';
	if (status === 409) return 'Nie można usunąć: pozycja ma powiązane wydania lub egzemplarze.';
	if (status === 422 && typeof body.error === 'string') return `Nieprawidłowe dane: ${body.error}.`;
	return `Serwer zwrócił błąd ${status}.`;
}

/** How long a request may go unanswered before it fails as no answer: above the server's own
 * bounds for a normal answer (17 seconds for a lookup, both provider rounds together, 8 seconds
 * per search source) and below its 30-second request timeout. Weak signal can keep a connection up while answers stop. */
export const requestDeadlineMs = 20_000;

type Method = 'GET' | 'POST' | 'PUT' | 'DELETE';

/**
 * Sends JSON or a binary upload to the API and decodes the response; 204 yields undefined. The
 * API never redirects, so a redirect is the sign-in turning the request away: followed, one to
 * another origin would fail as if the server never answered. A response whose body breaks off
 * counts as no answer: the connection was lost, whatever the status said. A request without a
 * whole answer after deadlineMs is aborted and fails as no answer (status 0), like a lost
 * connection: the server may still have done what it asked.
 */
export async function request<T>(
	fetchFn: typeof fetch,
	method: Method,
	path: string,
	body?: unknown,
	deadlineMs = requestDeadlineMs
): Promise<T> {
	const abort = new AbortController();
	let timer: ReturnType<typeof setTimeout> | undefined;
	const expired = new Promise<never>((_resolve, reject) => {
		timer = setTimeout(() => {
			abort.abort();
			reject(new ApiError(noAnswerMessage, 0));
		}, deadlineMs);
	});
	try {
		return await Promise.race([exchange<T>(fetchFn, method, path, body, abort.signal), expired]);
	} finally {
		clearTimeout(timer);
	}
}

async function exchange<T>(
	fetchFn: typeof fetch,
	method: Method,
	path: string,
	body: unknown,
	signal: AbortSignal
): Promise<T> {
	let res: Response;
	const binary = body instanceof Blob;
	try {
		res = await fetchFn(path, {
			method,
			headers:
				body === undefined
					? { accept: 'application/json' }
					: {
							accept: 'application/json',
							'content-type': binary ? 'application/octet-stream' : 'application/json'
						},
			body: binary ? body : JSON.stringify(body),
			redirect: 'manual',
			signal
		});
	} catch {
		throw new ApiError(noAnswerMessage, 0);
	}
	if (res.type === 'opaqueredirect' || res.status === 401) {
		throw new ApiError(sessionExpiredMessage, 401);
	}
	if (res.status === 204) return undefined as T;
	let text: string;
	try {
		text = await res.text();
	} catch {
		throw new ApiError(noAnswerMessage, 0);
	}
	let parsed: unknown;
	try {
		parsed = JSON.parse(text);
	} catch {
		throw new ApiError(
			res.ok ? 'Serwer zwrócił nieczytelną odpowiedź.' : `Serwer zwrócił błąd ${res.status}.`,
			res.status
		);
	}
	if (!res.ok) {
		const errBody = (parsed ?? {}) as ErrorBody;
		const field = typeof errBody.field === 'string' ? errBody.field : undefined;
		throw new ApiError(messageFor(res.status, errBody), res.status, field);
	}
	return parsed as T;
}

/**
 * A fetch that aborts each request, its body included, once ms passed, so a request the network
 * holds open fails as one that got no answer.
 */
export function deadlineFetch(fetchFn: typeof fetch, ms: number): typeof fetch {
	return (input, init) => {
		const controller = new AbortController();
		setTimeout(() => controller.abort(), ms);
		return fetchFn(input, { ...init, signal: controller.signal });
	};
}

/** Whether a request got no answer at all, as without a connection. */
export function isNoAnswer(err: unknown): boolean {
	return err instanceof ApiError && err.status === 0;
}

/** Whether the sign-in in front of the app turned a request away. */
export function isSessionExpired(err: unknown): boolean {
	return err instanceof ApiError && err.status === 401;
}

/** The message to show for any thrown value. */
export function errorMessage(err: unknown): string {
	return err instanceof ApiError ? err.message : 'Nieznany błąd.';
}

/** The form issue for a failed save: the field the API named, if any, and the message. */
export function issueFrom(err: unknown): FormIssue {
	return { field: err instanceof ApiError ? (err.field ?? '') : '', message: errorMessage(err) };
}
