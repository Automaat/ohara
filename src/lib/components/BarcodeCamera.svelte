<script lang="ts">
	import { Button, Callout } from '@smykla-skalski/sui';
	import { onMount, tick as flush } from 'svelte';
	import { prepareZXingModule, readBarcodes } from 'zxing-wasm/reader';
	import wasmURL from 'zxing-wasm/reader/zxing_reader.wasm?url';
	import { cameraErrorMessage, cropRect, scanFormats } from '$lib/scan';

	let {
		paused,
		ondetect
	}: {
		/** Stops decoding while a scan is being confirmed; the preview keeps running. */
		paused: boolean;
		ondetect: (code: string) => void;
	} = $props();

	const autoStartKey = 'ohara.scan.camera';
	const frameGap = 120;

	let video = $state<HTMLVideoElement>();
	let stream = $state<MediaStream | null>(null);
	let starting = $state(false);
	let error = $state<string | null>(null);
	let wanted = false;
	let timer: ReturnType<typeof setTimeout> | undefined;
	const canvas = document.createElement('canvas');

	function remember(on: boolean) {
		try {
			localStorage.setItem(autoStartKey, on ? 'on' : 'off');
		} catch {
			return;
		}
	}

	const readerOverrides = {
		locateFile: (path: string, prefix: string) => (path.endsWith('.wasm') ? wasmURL : prefix + path)
	};

	function loadReader() {
		return prepareZXingModule({ overrides: readerOverrides, fireImmediately: true });
	}

	async function start() {
		error = null;
		wanted = true;
		if (!navigator.mediaDevices?.getUserMedia) {
			error = 'Aparat działa tylko na stronie otwartej przez HTTPS.';
			return;
		}
		starting = true;
		const reader = loadReader().then(
			() => true,
			() => false
		);
		try {
			const media = await navigator.mediaDevices.getUserMedia({
				audio: false,
				video: {
					facingMode: { ideal: 'environment' },
					width: { ideal: 1920 },
					height: { ideal: 1080 }
				}
			});
			if (!(await reader)) {
				media.getTracks().forEach((t) => t.stop());
				error = 'Nie udało się wczytać czytnika kodów.';
				return;
			}
			if (!wanted || !video) {
				media.getTracks().forEach((t) => t.stop());
				return;
			}
			stream = media;
			video.srcObject = media;
			await flush();
			if (stream !== media) return;
			await video.play();
			remember(true);
			schedule();
		} catch (err) {
			halt();
			error = cameraErrorMessage(err);
		} finally {
			starting = false;
		}
	}

	function halt() {
		clearTimeout(timer);
		stream?.getTracks().forEach((t) => t.stop());
		stream = null;
		if (video) video.srcObject = null;
	}

	function stop() {
		wanted = false;
		remember(false);
		halt();
	}

	function schedule() {
		clearTimeout(timer);
		timer = setTimeout(tick, frameGap);
	}

	async function tick() {
		if (!stream || !video) return;
		if (!paused && video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA && video.videoWidth) {
			const r = cropRect(video.videoWidth, video.videoHeight);
			canvas.width = r.dw;
			canvas.height = r.dh;
			const ctx = canvas.getContext('2d', { willReadFrequently: true });
			if (ctx) {
				ctx.drawImage(video, r.sx, r.sy, r.sw, r.sh, 0, 0, r.dw, r.dh);
				const found = await readBarcodes(ctx.getImageData(0, 0, r.dw, r.dh), {
					formats: [...scanFormats],
					tryHarder: true,
					maxNumberOfSymbols: 1
				}).catch(() => []);
				const hit = found.find((b) => b.isValid && b.text);
				if (hit && !paused && stream) ondetect(hit.text);
			}
		}
		if (stream) schedule();
	}

	function onVisibility() {
		if (document.hidden) {
			halt();
		} else if (wanted && !stream && !starting) {
			void start();
		}
	}

	onMount(() => {
		let auto = false;
		try {
			auto = localStorage.getItem(autoStartKey) === 'on';
		} catch {
			auto = false;
		}
		if (auto) void start();
		document.addEventListener('visibilitychange', onVisibility);
		return () => {
			document.removeEventListener('visibilitychange', onVisibility);
			wanted = false;
			halt();
		};
	});
</script>

<div class="camera" class:live={stream !== null}>
	<!-- svelte-ignore a11y_media_has_caption -->
	<video bind:this={video} playsinline muted autoplay></video>
	{#if stream}
		<div class="guide" class:paused aria-hidden="true"></div>
	{/if}
</div>
{#if stream}
	<div class="controls">
		<Button type="button" tone="ghost" onclick={stop}>Wyłącz aparat</Button>
	</div>
{:else}
	<div>
		<Button tone="signal" type="button" loading={starting} onclick={() => void start()}>
			Włącz aparat
		</Button>
	</div>
{/if}
{#if error}
	<Callout tone="warning" live>{error}</Callout>
{/if}

<style>
	.camera {
		display: none;
	}
	.camera.live {
		display: block;
		position: relative;
		aspect-ratio: 4 / 3;
		max-height: 50vh;
		width: 100%;
		overflow: hidden;
		border-radius: var(--sui-radius-md, 0.5rem);
		background: #000;
	}
	video {
		width: 100%;
		height: 100%;
		object-fit: cover;
		display: block;
	}
	.guide {
		position: absolute;
		inset: 25% 6%;
		border: 3px solid rgb(255 255 255 / 85%);
		border-radius: 0.5rem;
		box-shadow: 0 0 0 100vmax rgb(0 0 0 / 35%);
	}
	.guide.paused {
		border-color: rgb(255 255 255 / 35%);
	}
	.controls {
		display: flex;
		justify-content: flex-end;
	}
</style>
