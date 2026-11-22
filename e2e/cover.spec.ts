import type { APIRequestContext, Page, Request } from '@playwright/test';
import { expect, expectNoSourceRequests, test } from './fixtures.js';

/** The edition each test changes the cover of, made through the API. */
let editionID: number;

test.beforeEach(async ({ request }) => {
	const title = await request.post('/api/titles', {
		data: { kind: 'movie', name: `Okładka ${Date.now()}` }
	});
	expect(title.ok()).toBe(true);
	const edition = await request.post('/api/editions', {
		data: { title_id: ((await title.json()) as { id: number }).id, format: 'bluray' }
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
async function pick(page: Page, name: string, bytes: Uint8Array) {
	await page.goto(`/editions/${editionID}`);
	await page.getByRole('button', { name: 'Zmień okładkę' }).click();
	const sheet = page.getByRole('dialog', { name: 'Zmień okładkę' });
	await expect(sheet).toContainText('JPEG lub PNG. Duże zdjęcia zostaną zmniejszone.');
	await sheet
		.locator('input[type="file"]:not([capture])')
		.setInputFiles({ name, mimeType: 'image/jpeg', buffer: Buffer.from(bytes) });
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
