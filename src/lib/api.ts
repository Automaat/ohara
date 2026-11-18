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

const fieldMessages: Record<string, Record<number, string>> = {
	barcode: {
		409: 'Wydanie z tym kodem kreskowym już istnieje.',
		422: 'Nieprawidłowy kod kreskowy: sprawdź cyfry.'
	},
	tmdb_id: { 409: 'Tytuł z tym identyfikatorem TMDB już istnieje.' },
	kind: { 409: 'Nie można zmienić typu tytułu, który ma wydania.' },
	title_id: { 422: 'Wybrany tytuł nie istnieje.' },
	edition_id: { 422: 'Wybrane wydanie nie istnieje.' },
	phrase: { 409: 'Ta fraza już jest na liście.' },
	cover_photo: {
		400: 'Przesyłanie zdjęcia zostało przerwane. Spróbuj ponownie.',
		413: 'Wybierz plik o rozmiarze do 8 MiB.',
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

/** Sends JSON or a binary upload to the API and decodes the response; 204 yields undefined. */
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
			body: binary ? body : JSON.stringify(body)
		});
	} catch {
		throw new ApiError('Serwer nie odpowiada.', 0);
	}
	if (res.status === 204) return undefined as T;
	let parsed: unknown;
	try {
		parsed = await res.json();
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

/** The message to show for any thrown value. */
export function errorMessage(err: unknown): string {
	return err instanceof ApiError ? err.message : 'Nieznany błąd.';
}
