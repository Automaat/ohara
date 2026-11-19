import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

// The title catalog is part of Kolekcja: titles without copies are its "Bez egzemplarzy" filter.
export const load: PageLoad = () => {
	redirect(308, '/');
};
