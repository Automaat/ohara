import { version } from '$app/env';
import { assets, immutable, prerendered } from '$app/manifest';
import { self } from '$app/service-worker';
import { serve, type ServiceWorkerScope } from '@smykla-skalski/sui/service-worker';
import { isAppShell, serverPaths } from '#lib/offline-shell.js';

const shell = new URL('./', self.location.href).href;

/**
 * Fetches as the worker does, except that fetching the shell fails unless the answer is this
 * build's index.html. Install then fails as a whole and the previous version keeps serving, so
 * nothing else is ever installed as the page that opens the app offline.
 */
async function fetchChecked(request: Request): Promise<Response> {
	const response = await self.fetch(request);
	if (request.url !== shell || request.mode === 'navigate' || !response.ok) return response;
	if (isAppShell(await response.clone().text(), immutable)) return response;
	throw new TypeError(`${shell} did not answer with this build's index.html`);
}

const scope: ServiceWorkerScope = {
	location: self.location,
	registration: self.registration,
	caches: self.caches,
	clients: self.clients,
	fetch: fetchChecked,
	skipWaiting: () => self.skipWaiting(),
	addEventListener: self.addEventListener.bind(self)
};

serve(scope, {
	version,
	immutable,
	assets,
	prerendered,
	fallback: '',
	never: serverPaths
});
