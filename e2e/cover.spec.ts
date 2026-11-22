import type { APIRequestContext, Page, Request } from '@playwright/test';
import { expect, expectNoSourceRequests, test } from './fixtures.js';

/** The title and edition each test changes the cover of, made through the API. */
let titleID: number;
let editionID: number;

test.beforeEach(async ({ request }) => {
	const title = await request.post('/api/titles', {
		data: { kind: 'movie', name: `Okładka ${Date.now()}` }
	});
	expect(title.ok()).toBe(true);
	titleID = ((await title.json()) as { id: number }).id;
	const edition = await request.post('/api/editions', {
		data: { title_id: titleID, format: 'bluray' }
	});
	expect(edition.ok()).toBe(true);
	editionID = ((await edition.json()) as { id: number }).id;
});

/**
 * Node's Buffer, which Playwright takes for an in-memory file; the project carries no Node type
 * definitions, so it is declared here as far as the tests use it.
 */
declare const Buffer: { from(bytes: Uint8Array): Uint8Array };

/**
 * A JPEG the browser draws at width × height: gradients and shapes, so it compresses like a photo
 * rather than like noise or a flat colour.
 */
async function photo(page: Page, width: number, height: number): Promise<Uint8Array> {
	const bytes = await page.evaluate(
		async ([w, h]) => {
			const canvas = new OffscreenCanvas(w, h);
			const ctx = canvas.getContext('2d');
			if (!ctx) throw new Error('no 2d context');
			const sky = ctx.createLinearGradient(0, 0, w, h);
			sky.addColorStop(0, '#1d3557');
			sky.addColorStop(0.5, '#a8dadc');
			sky.addColorStop(1, '#e63946');
			ctx.fillStyle = sky;
			ctx.fillRect(0, 0, w, h);
			for (let i = 0; i < 40; i++) {
				ctx.fillStyle = `hsl(${(i * 37) % 360} 60% 50% / 0.6)`;
				ctx.beginPath();
				const x = ((i * 97) % 100) * (w / 100);
				const y = ((i * 61) % 100) * (h / 100);
				ctx.arc(x, y, (w / 40) * (1 + (i % 5)), 0, 2 * Math.PI);
				ctx.fill();
			}
			const blob = await canvas.convertToBlob({ type: 'image/jpeg', quality: 0.92 });
			const data = new Uint8Array(await blob.arrayBuffer());
			let binary = '';
			for (let i = 0; i < data.length; i += 0x8000) {
				binary += String.fromCharCode(...data.subarray(i, i + 0x8000));
			}
			return btoa(binary);
		},
		[width, height] as const
	);
	return Uint8Array.from(atob(bytes), (c) => c.charCodeAt(0));
}

/**
 * A JPEG of seeded 4 × 4 px blocks of random colour at width × height: as much detail as a photo
 * can hold, so it stays large however it is compressed.
 */
async function detailedPhoto(page: Page, width: number, height: number): Promise<Uint8Array> {
	const bytes = await page.evaluate(
		async ([w, h]) => {
			const canvas = new OffscreenCanvas(w, h);
			const ctx = canvas.getContext('2d');
			if (!ctx) throw new Error('no 2d context');
			const image = ctx.createImageData(w, h);
			let seed = 7;
			const next = () => (seed = (Math.imul(seed, 1_103_515_245) + 12_345) >>> 0) >>> 24;
			for (let by = 0; by < h; by += 4) {
				for (let bx = 0; bx < w; bx += 4) {
					const colour = [next(), next(), next(), 255];
					for (let y = by; y < Math.min(by + 4, h); y++) {
						for (let x = bx; x < Math.min(bx + 4, w); x++) image.data.set(colour, (y * w + x) * 4);
					}
				}
			}
			ctx.putImageData(image, 0, 0);
			const blob = await canvas.convertToBlob({ type: 'image/jpeg', quality: 0.92 });
			const data = new Uint8Array(await blob.arrayBuffer());
			let binary = '';
			for (let i = 0; i < data.length; i += 0x8000) {
				binary += String.fromCharCode(...data.subarray(i, i + 0x8000));
			}
			return btoa(binary);
		},
		[width, height] as const
	);
	return Uint8Array.from(atob(bytes), (c) => c.charCodeAt(0));
}

const startOfImage = 0xffd8;
const app1Marker = 0xffe1;
const commentMarker = 0xfffe;
const maxSegmentData = 65_533;

function concat(parts: Uint8Array[]): Uint8Array {
	const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
	let at = 0;
	for (const part of parts) {
		out.set(part, at);
		at += part.length;
	}
	return out;
}

const view = (bytes: Uint8Array) => new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);

/** The JPEG with segments inserted right after its start-of-image marker. */
function withSegments(jpeg: Uint8Array, segments: Uint8Array[]): Uint8Array {
	expect(view(jpeg).getUint16(0), 'JPEG start-of-image').toBe(startOfImage);
	return concat([jpeg.subarray(0, 2), ...segments, jpeg.subarray(2)]);
}

/** A JPEG segment: its marker, its length and its data. */
function segment(marker: number, data: Uint8Array): Uint8Array {
	const header = new Uint8Array(4);
	view(header).setUint16(0, marker);
	view(header).setUint16(2, data.length + 2);
	return concat([header, data]);
}

/** An APP1 Exif segment holding only the orientation tag, in a big-endian TIFF block. */
function exifOrientation(orientation: number): Uint8Array {
	const tiffMagic = 42;
	const firstIFDOffset = 8;
	const orientationTag = 0x0112;
	const shortType = 3;
	const tiff = new Uint8Array(26);
	const fields = view(tiff);
	tiff.set(new TextEncoder().encode('MM'), 0);
	fields.setUint16(2, tiffMagic);
	fields.setUint32(4, firstIFDOffset);
	fields.setUint16(8, 1);
	fields.setUint16(10, orientationTag);
	fields.setUint16(12, shortType);
	fields.setUint32(14, 1);
	fields.setUint16(18, orientation);
	fields.setUint32(22, 0);
	return segment(app1Marker, concat([new TextEncoder().encode('Exif\0\0'), tiff]));
}

/** Comment segments that grow a JPEG by about `bytes` without changing its picture. */
function padding(bytes: number): Uint8Array[] {
	const out: Uint8Array[] = [];
	for (let left = bytes; left > 0; left -= maxSegmentData) {
		out.push(segment(commentMarker, new Uint8Array(Math.min(maxSegmentData, left)).fill(0x20)));
	}
	return out;
}

/** The pixel size a JPEG's frame header declares, EXIF orientation not applied. */
function jpegSize(jpeg: Uint8Array): { width: number; height: number } {
	const fields = view(jpeg);
	const notFrames = [0xc4, 0xc8, 0xcc];
	let at = 2;
	while (at + 9 < jpeg.length) {
		expect(jpeg[at], `JPEG marker at ${at}`).toBe(0xff);
		const marker = jpeg[at + 1];
		if (marker >= 0xc0 && marker <= 0xcf && !notFrames.includes(marker)) {
			return { height: fields.getUint16(at + 5), width: fields.getUint16(at + 7) };
		}
		at += 2 + fields.getUint16(at + 2);
	}
	throw new Error('JPEG without a frame header');
}

const isCoverPut = (req: Request) =>
	req.method() === 'PUT' && new URL(req.url()).pathname === `/api/editions/${editionID}/cover`;

/** Picks the file from "Wybierz z biblioteki" in the edition's "Zmień okładkę" sheet. */
async function pick(page: Page, name: string, bytes: Uint8Array, mimeType = 'image/jpeg') {
	await page.goto(`/editions/${editionID}`);
	return pickHere(page, name, bytes, mimeType);
}

/** Picks the file in the "Zmień okładkę" sheet of the edition page already shown. */
async function pickHere(page: Page, name: string, bytes: Uint8Array, mimeType = 'image/jpeg') {
	const sheet = page.getByRole('dialog', { name: 'Zmień okładkę' });
	if (!(await sheet.isVisible())) await page.getByRole('button', { name: 'Zmień okładkę' }).click();
	await expect(sheet).toContainText('JPEG lub PNG. Duże zdjęcia zostaną zmniejszone.');
	await sheet
		.locator('input[type="file"]:not([capture])')
		.setInputFiles({ name, mimeType, buffer: Buffer.from(bytes) });
	return sheet;
}

/**
 * Uploads the photo, waits for it to show as the cover and returns the body the browser sent, the
 * stored photo's size as decoded (EXIF applied) and as its JPEG frame declares it.
 */
async function upload(page: Page, request: APIRequestContext, file: Uint8Array) {
	const sent = page.waitForRequest(isCoverPut);
	const answered = page.waitForResponse((res) => isCoverPut(res.request()));
	const sheet = await pick(page, 'photo.jpg', file);
	const body = new Uint8Array((await sent).postDataBuffer() ?? []);
	expect((await answered).status()).toBe(204);
	await expect(sheet).toBeHidden();
	const cover = page.getByRole('img', { name: 'Własne zdjęcie okładki' });
	await expect(cover).toBeVisible();
	await expect
		.poll(() => cover.evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth > 0))
		.toBe(true);

	const path = `/api/editions/${editionID}/cover`;
	const stored = await request.get(path);
	expect(stored.ok()).toBe(true);
	const decoded = await page.evaluate(async (url) => {
		const bitmap = await createImageBitmap(await (await fetch(url)).blob());
		const size = { width: bitmap.width, height: bitmap.height };
		bitmap.close();
		return size;
	}, path);
	return { body, decoded, frame: jpegSize(new Uint8Array(await stored.body())) };
}

test('a 4032 × 3024 photo is stored at 1600 px under 1 MB', async ({ page, request }) => {
	const file = await photo(page, 4032, 3024);
	const { body, decoded, frame } = await upload(page, request, file);
	expect(body.length).toBeLessThan(1_000_000);
	expect(body.length).toBeLessThan(file.length);
	expect(decoded).toEqual({ width: 1600, height: 1200 });
	expect(frame).toEqual({ width: 1600, height: 1200 });
	await expectNoSourceRequests(request);
});

test('a 6000 × 4000 photo uploads and shows as the cover', async ({ page, request }) => {
	const { body, decoded } = await upload(page, request, await photo(page, 6000, 4000));
	expect(body.length).toBeLessThan(1_000_000);
	expect(decoded).toEqual({ width: 1600, height: 1067 });
});

test('a photo over 8 MiB uploads and shows as the cover', async ({ page, request }) => {
	const small = await photo(page, 3000, 2000);
	const file = withSegments(small, padding(8.5 * 1024 * 1024 - small.length));
	expect(file.length).toBeGreaterThan(8 * 1024 * 1024);
	const { body, decoded } = await upload(page, request, file);
	expect(body.length).toBeLessThan(1_000_000);
	expect(decoded).toEqual({ width: 1600, height: 1067 });
});

test('a photo within 1600 px keeps its size', async ({ page, request }) => {
	const { decoded, frame } = await upload(page, request, await photo(page, 800, 600));
	expect(decoded).toEqual({ width: 800, height: 600 });
	expect(frame).toEqual({ width: 800, height: 600 });
});

test('a photo turned by its EXIF orientation is stored upright', async ({ page, request }) => {
	const landscape = await photo(page, 1200, 900);
	const turned = withSegments(landscape, [exifOrientation(6)]);
	const { decoded, frame } = await upload(page, request, turned);
	expect(decoded).toEqual({ width: 900, height: 1200 });
	expect(frame, 'upright pixels, no EXIF needed').toEqual({ width: 900, height: 1200 });
});

test('a file the browser cannot read sends nothing', async ({ page }) => {
	const puts: string[] = [];
	page.on('request', (req) => {
		if (isCoverPut(req)) puts.push(req.url());
	});
	const noise = new Uint8Array(64 * 1024);
	for (let i = 0; i < noise.length; i++) noise[i] = Math.imul(i + 1, 2_654_435_761) >>> 24;
	const sheet = await pick(page, 'broken.jpg', noise);
	await expect(sheet).toContainText(
		'Wybierz prawidłowe zdjęcie JPEG lub PNG o rozdzielczości do 16 megapikseli.'
	);
	await expect(sheet).toBeVisible();
	expect(puts).toEqual([]);
});

test('a detailed 4032 × 3024 photo is lowered in quality to stay under 1 MB', async ({
	page,
	request
}) => {
	const file = await detailedPhoto(page, 4032, 3024);
	const { body, decoded } = await upload(page, request, file);
	expect(body.length).toBeLessThan(1_000_000);
	expect(decoded).toEqual({ width: 1600, height: 1200 });
});

test('a GIF sends nothing', async ({ page }) => {
	const puts: string[] = [];
	page.on('request', (req) => {
		if (isCoverPut(req)) puts.push(req.url());
	});
	const gif = Uint8Array.from(
		atob('R0lGODlhAQABAIAAAP///wAAACH5BAEAAAAALAAAAAABAAEAAAICRAEAOw=='),
		(c) => c.charCodeAt(0)
	);
	const sheet = await pick(page, 'photo.gif', gif, 'image/gif');
	await expect(sheet).toContainText(
		'Wybierz prawidłowe zdjęcie JPEG lub PNG o rozdzielczości do 16 megapikseli.'
	);
	expect(puts).toEqual([]);
});

test('a photo still shrinking when the edition is left is never sent', async ({
	page,
	request
}) => {
	await page.addInitScript(() => {
		const decode = window.createImageBitmap.bind(window);
		let held: Promise<void> | undefined = new Promise((resolve) => {
			Object.assign(window, { releaseFirstDecode: resolve });
		});
		Object.assign(window, {
			createImageBitmap: async (...args: Parameters<typeof createImageBitmap>) => {
				const wait = held;
				held = undefined;
				if (wait) await wait;
				return decode(...args);
			}
		});
	});
	const puts: string[] = [];
	page.on('request', (req) => {
		if (isCoverPut(req)) puts.push(req.url());
	});
	const older = await photo(page, 1200, 900);
	const newer = await photo(page, 800, 600);

	await page.goto(`/titles/${titleID}`);
	await page.locator(`a[href="/editions/${editionID}"]`).first().click();
	await expect(page).toHaveURL(new RegExp(`/editions/${editionID}$`));
	await pickHere(page, 'older.jpg', older);
	await page.goBack();
	await expect(page).toHaveURL(new RegExp(`/titles/${titleID}$`));
	await page.goForward();
	await expect(page).toHaveURL(new RegExp(`/editions/${editionID}$`));

	const answered = page.waitForResponse((res) => isCoverPut(res.request()));
	await pickHere(page, 'newer.jpg', newer);
	expect((await answered).status()).toBe(204);
	await page.evaluate(() =>
		(window as unknown as { releaseFirstDecode: () => void }).releaseFirstDecode()
	);
	await expect
		.poll(async () => {
			const stored = await request.get(`/api/editions/${editionID}/cover`);
			return jpegSize(new Uint8Array(await stored.body()));
		})
		.toEqual({ width: 800, height: 600 });
	await page.waitForTimeout(1000);
	expect(puts).toHaveLength(1);
	const stored = await request.get(`/api/editions/${editionID}/cover`);
	expect(jpegSize(new Uint8Array(await stored.body()))).toEqual({ width: 800, height: 600 });
});

/**
 * Playwright starts Chromium with the back-forward cache turned off, and its headless shell has
 * none, so this test launches full Chromium in headless mode with the cache on, shaped like the
 * phone project. A page restored from the cache fires no load, so going back waits for the commit.
 */
test('a photo shrinking in a page the browser restores from its cache is never sent', async ({
	playwright,
	request,
	baseURL
}) => {
	const browser = await playwright.chromium.launch({
		channel: 'chromium',
		ignoreDefaultArgs: ['--disable-back-forward-cache']
	});
	try {
		const page = await browser.newPage({
			baseURL,
			viewport: { width: 390, height: 844 },
			deviceScaleFactor: 3,
			isMobile: true,
			hasTouch: true,
			locale: 'pl-PL'
		});
		await bfcacheFlow(page, request);
	} finally {
		await browser.close();
	}
});

async function bfcacheFlow(page: Page, request: APIRequestContext) {
	await page.addInitScript(() => {
		addEventListener('pageshow', (event) => {
			Object.assign(window, { restored: event.persisted });
		});
		if (!new URLSearchParams(location.search).has('hold')) return;
		const decode = window.createImageBitmap.bind(window);
		let held: Promise<void> | undefined = new Promise((resolve) => {
			Object.assign(window, { releaseFirstDecode: resolve });
		});
		Object.assign(window, {
			createImageBitmap: async (...args: Parameters<typeof createImageBitmap>) => {
				const wait = held;
				held = undefined;
				if (wait) await wait;
				return decode(...args);
			}
		});
	});
	const puts: string[] = [];
	page.on('request', (req) => {
		if (isCoverPut(req)) puts.push(req.url());
	});
	const storedSize = async () => {
		const stored = await request.get(`/api/editions/${editionID}/cover`);
		return jpegSize(new Uint8Array(await stored.body()));
	};
	const older = await photo(page, 1200, 900);
	const newer = await photo(page, 800, 600);
	const later = await photo(page, 640, 480);

	await page.goto(`/editions/${editionID}?hold=1`);
	await pickHere(page, 'older.jpg', older);
	await page.goto(`/editions/${editionID}`);
	const answered = page.waitForResponse((res) => isCoverPut(res.request()));
	await pickHere(page, 'newer.jpg', newer);
	expect((await answered).status()).toBe(204);
	expect(await storedSize()).toEqual({ width: 800, height: 600 });

	await page.goBack({ waitUntil: 'commit' });
	await expect(page).toHaveURL(/\?hold=1$/);
	expect(
		await page.evaluate(() => (window as unknown as { restored?: boolean }).restored),
		'restored from the back-forward cache'
	).toBe(true);
	await page.evaluate(() =>
		(window as unknown as { releaseFirstDecode: () => void }).releaseFirstDecode()
	);
	await page.waitForTimeout(1000);
	expect(puts).toHaveLength(1);
	expect(await storedSize()).toEqual({ width: 800, height: 600 });

	const saved = page.waitForResponse((res) => isCoverPut(res.request()));
	await pickHere(page, 'later.jpg', later);
	expect((await saved).status()).toBe(204);
	expect(puts).toHaveLength(2);
	expect(await storedSize()).toEqual({ width: 640, height: 480 });
}
