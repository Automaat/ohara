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

type ErrorBody = { error?: unknown; field?: unknown };

/** What the server's 422 for a barcode means: the shape fits, the check digit does not. */
export const invalidBarcodeMessage = 'Nieprawidłowy kod kreskowy: sprawdź cyfry.';

/** What the server's 413 for a cover photo means. */
export const coverTooLargeMessage = 'Wybierz plik o rozmiarze do 8 MiB.';

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
	title_id: { 422: 'Wybrany tytuł nie istnieje.' },
	edition_id: { 422: 'Wybrane wydanie nie istnieje.' },
	phrase: { 409: 'Ta fraza już jest na liście.' },
	cover_photo: {
		400: 'Przesyłanie zdjęcia zostało przerwane. Spróbuj ponownie.',
		413: coverTooLargeMessage,
		422: 'Wybierz prawidłowe zdjęcie JPEG lub PNG o rozdzielczości do 16 megapikseli.'
	}
};

function messageFor(status: number, body: ErrorBody): string {
	const field = typeof body.field === 'string' ? body.field : undefined;
	const known = field ? fieldMessages[field]?.[status] : undefined;
	if (known) return known;
	if (status === 404) return 'Nie znaleziono.';
	if (status === 409) return 'Nie można usunąć: pozycja ma powiązane wydania lub egzemplarze.';
	if (status === 422 && typeof body.error === 'string') return `Nieprawidłowe dane: ${body.error}.`;
	return `Serwer zwrócił błąd ${status}.`;
}

/**
 * Sends JSON or a binary upload to the API and decodes the response; 204 yields undefined. The
 * API never redirects, so a redirect is the sign-in turning the request away: followed, one to
 * another origin would fail as if the server never answered. A response whose body breaks off
 * counts as no answer: the connection was lost, whatever the status said.
 */
export async function request<T>(
	fetchFn: typeof fetch,
	method: 'GET' | 'POST' | 'PUT' | 'DELETE',
	path: string,
	body?: unknown
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
			redirect: 'manual'
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
