import { createServer } from 'node:http';

/**
 * A Discogs API double for the smoke suite (e2e/compose.yaml): the collection endpoints the push to
 * the Discogs collection calls, kept in memory. Release 2000002 is in the collection already, in an
 * instance no Ohara copy made. Any other request answers 404.
 */
const token = 'Discogs token=e2e-discogs-token';
const owner = 'e2e-owner';
const grades = [
	'Mint (M)',
	'Near Mint (NM or M-)',
	'Very Good Plus (VG+)',
	'Very Good (VG)',
	'Good Plus (G+)',
	'Good (G)',
	'Fair (F)',
	'Poor (P)'
];
const fields = [
	{ id: 11, name: 'Media Condition', type: 'dropdown', options: grades, position: 1, public: true },
	{
		id: 12,
		name: 'Sleeve Condition',
		type: 'dropdown',
		options: [...grades, 'Generic', 'Not Graded', 'No Cover'],
		position: 2,
		public: true
	},
	{ id: 3, name: 'Notes', type: 'textarea', lines: 3, position: 3, public: false }
];

/** The instances of each release: instance ID and folder. */
const held = new Map([[2000002, [{ instance_id: 555, folder_id: 1 }]]]);
let nextInstance = 1000;

function send(res, status, body) {
	res.writeHead(status, { 'content-type': 'application/json' });
	res.end(body === undefined ? '' : JSON.stringify(body));
}

const collection = `/users/${owner}/collection`;
const releasePath = new RegExp(`^${collection}/releases/(\\d+)$`);
const addPath = new RegExp(`^${collection}/folders/(\\d+)/releases/(\\d+)$`);
const fieldPath = new RegExp(
	`^${collection}/folders/\\d+/releases/\\d+/instances/\\d+/fields/\\d+$`
);

createServer((req, res) => {
	const url = new URL(req.url ?? '/', 'http://discogs');
	if (req.headers.authorization !== token) {
		return send(res, 401, { message: 'You must authenticate.' });
	}
	const path = url.pathname;
	let m;
	if (req.method === 'GET' && path === '/oauth/identity') {
		return send(res, 200, { id: 1, username: owner, resource_url: '', consumer_name: 'ohara' });
	}
	if (req.method === 'GET' && path === `${collection}/fields`) return send(res, 200, { fields });
	if (req.method === 'GET' && (m = path.match(releasePath))) {
		const id = Number(m[1]);
		const instances = held.get(id) ?? [];
		if (instances.length === 0) return send(res, 404, { message: 'Release not found.' });
		return send(res, 200, {
			pagination: { page: 1, pages: 1, per_page: 100, items: instances.length },
			releases: instances.map((i) => ({ id, rating: 0, ...i }))
		});
	}
	if (req.method === 'POST' && (m = path.match(addPath))) {
		const instance = { instance_id: ++nextInstance, folder_id: Number(m[1]) };
		held.set(Number(m[2]), [...(held.get(Number(m[2])) ?? []), instance]);
		return send(res, 201, { instance_id: instance.instance_id, resource_url: '' });
	}
	if (req.method === 'POST' && fieldPath.test(path) && url.searchParams.has('value')) {
		return send(res, 204);
	}
	send(res, 404, { message: 'The requested resource was not found.' });
}).listen(8080);
