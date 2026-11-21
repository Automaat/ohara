/**
 * Paths, relative to the base and with their query, that the service worker never answers or
 * keeps: the server's own routes, which are not pages of the app.
 */
export const serverPaths: readonly RegExp[] = [
	/^api(?:[/?]|$)/,
	/^health(?:[/?]|$)/,
	/^metrics(?:[/?]|$)/
];

const entryFile = /(?:^|\/)entry\/(?:start|app)\.[^/]+\.js$/;
const startFile = /(?:^|\/)entry\/start\.[^/]+\.js$/;

/**
 * Whether `html` is this build's index.html. The shell loads the build's start and app entries,
 * whose names change with their bytes, so a maintenance page, a sign-in page or a shell of
 * another build is never taken for it.
 */
export function isAppShell(html: string, immutable: readonly { path: string }[]): boolean {
	const entries = immutable.map(({ path }) => path).filter((path) => entryFile.test(path));
	return (
		entries.some((path) => startFile.test(path)) && entries.every((path) => html.includes(path))
	);
}
