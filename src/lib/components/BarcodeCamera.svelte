<script lang="ts">
	import { Button, Callout } from '@smykla-skalski/sui';
	import { onMount, tick as flush, type Snippet } from 'svelte';
	import { prepareZXingModule, readBarcodes } from 'zxing-wasm/reader';
	import wasmURL from 'zxing-wasm/reader/zxing_reader.wasm?url';
	import { beep, primeBeep } from '#lib/beep.js';
	import { bandCrop, cameraErrorMessage, scanFormats, torchSupported } from '#lib/scan.js';

	let {
		paused,
		ondetect,
		full = false,
		queued = null,
		ontype,
		children
	}: {
		/** Stops decoding while a read is being handled; the preview keeps running. */
		paused: boolean;
		/** Gets a code read inside the band; true when the page takes it, which beeps and flashes. */
		ondetect: (code: string) => boolean;
		/** Fills the screen behind the tab bar, with the queue link and manual entry over it. */
		full?: boolean;
		/** Codes waiting in the queue, shown on the queue link of the full-screen camera. */
		queued?: number | null;
		/** Opens manual entry from the full-screen camera's keyboard button. */
		ontype?: () => void;
		/** Messages under the band. */
		children?: Snippet;
	} = $props();

	type TorchConstraints = MediaTrackConstraints & {
		advanced: (MediaTrackConstraintSet & { torch: boolean })[];
	};

	const autoStartKey = 'ohara.scan.camera';
	const frameGap = 120;
	const flashLength = 700;

	let video = $state<HTMLVideoElement>();
	let band = $state<HTMLElement>();
	let stream = $state<MediaStream | null>(null);
	let starting = $state(false);
	let error = $state<string | null>(null);
	let flashing = $state(false);
	let torch = $state(false);
	let torchOn = $state(false);
	let wanted = false;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let flashTimer: ReturnType<typeof setTimeout> | undefined;
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
			torch = canTorch(media);
			schedule();
		} catch (err) {
			halt();
			error = cameraErrorMessage(err);
		} finally {
			starting = false;
		}
	}

	function canTorch(media: MediaStream): boolean {
		const track = media.getVideoTracks()[0];
		if (!track || typeof track.getCapabilities !== 'function') return false;
		try {
			return torchSupported(track.getCapabilities());
		} catch {
			return false;
		}
	}

	async function toggleTorch() {
		const track = stream?.getVideoTracks()[0];
		if (!track) return;
		const on = !torchOn;
		const constraints: TorchConstraints = { advanced: [{ torch: on }] };
		try {
			await track.applyConstraints(constraints);
			if (stream?.getVideoTracks()[0] === track) torchOn = on;
		} catch {
			torch = false;
			torchOn = false;
		}
	}

	function halt() {
		clearTimeout(timer);
		stream?.getTracks().forEach((t) => t.stop());
		stream = null;
		torch = false;
		torchOn = false;
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

	function flash() {
		clearTimeout(flashTimer);
		flashing = true;
		flashTimer = setTimeout(() => (flashing = false), flashLength);
	}

	async function tick() {
		if (!stream || !video) return;
		if (
			!paused &&
			band &&
			video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA &&
			video.videoWidth
		) {
			const r = bandCrop(
				video.videoWidth,
				video.videoHeight,
				video.getBoundingClientRect(),
				band.getBoundingClientRect()
			);
			const ctx = r ? canvas.getContext('2d', { willReadFrequently: true }) : null;
			if (r && ctx) {
				canvas.width = r.dw;
				canvas.height = r.dh;
				ctx.drawImage(video, r.sx, r.sy, r.sw, r.sh, 0, 0, r.dw, r.dh);
				const found = await readBarcodes(ctx.getImageData(0, 0, r.dw, r.dh), {
					formats: [...scanFormats],
					tryHarder: true,
					maxNumberOfSymbols: 1
				}).catch(() => []);
				const hit = found.find((b) => b.isValid && b.text);
				if (hit && !paused && stream && ondetect(hit.text)) {
					flash();
					void beep();
				}
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
		document.addEventListener('pointerdown', primeBeep, true);
		document.addEventListener('keydown', primeBeep, true);
		return () => {
			document.removeEventListener('visibilitychange', onVisibility);
			document.removeEventListener('pointerdown', primeBeep, true);
			document.removeEventListener('keydown', primeBeep, true);
			clearTimeout(flashTimer);
			wanted = false;
			halt();
		};
	});
</script>

{#snippet startButton()}
	<Button tone="signal" type="button" loading={starting} onclick={() => void start()}>
		Włącz aparat
	</Button>
{/snippet}

{#snippet problem()}
	{#if error}
		<Callout tone="warning" live>{error}</Callout>
	{/if}
{/snippet}

<div class={['stage', { full, 'ohara-full-camera': full, live: stream !== null }]}>
	<!-- svelte-ignore a11y_media_has_caption -->
	<video bind:this={video} playsinline muted autoplay></video>
	<div class="overlay">
		{#if full || torch}
			<div class="bar">
				{#if full}
					<a
						class="glass chip"
						href="/scan/queue"
						aria-label={queued ? `Do uzupełnienia: ${queued}` : undefined}
					>
						{#if queued}<span class="count" aria-hidden="true">{queued}</span>{/if}
						Do uzupełnienia
					</a>
				{/if}
				<span class="spacer"></span>
				{#if torch}
					<button
						type="button"
						class="glass round"
						aria-label="Latarka"
						aria-pressed={torchOn}
						onclick={() => void toggleTorch()}
					>
						<svg
							width="20"
							height="20"
							viewBox="0 0 24 24"
							fill="none"
							stroke="currentColor"
							stroke-width="2"
							stroke-linecap="round"
							stroke-linejoin="round"
							aria-hidden="true"
							focusable="false"
						>
							<path d="M13 2 4.5 13.5H12L11 22l8.5-11.5H12z" />
						</svg>
					</button>
				{/if}
				{#if full && ontype}
					<button
						type="button"
						class="glass round"
						aria-label="Wpisz kod"
						aria-haspopup="dialog"
						onclick={ontype}
					>
						<svg
							width="20"
							height="20"
							viewBox="0 0 24 24"
							fill="none"
							stroke="currentColor"
							stroke-width="2"
							stroke-linecap="round"
							stroke-linejoin="round"
							aria-hidden="true"
							focusable="false"
						>
							<rect x="2.5" y="5.5" width="19" height="13" rx="2.5" />
							<path d="M6.5 9.5h.01M10 9.5h.01M13.5 9.5h.01M17 9.5h.01M8 14.5h8" />
						</svg>
					</button>
				{/if}
			</div>
		{/if}
		<div class="middle">
			{#if stream}
				<div bind:this={band} class={['band', { ok: flashing, paused }]} aria-hidden="true">
					<span class="laser"></span>
				</div>
				<p class="hint">Nakieruj na kod kreskowy</p>
			{:else if full}
				{@render startButton()}
			{/if}
			{#if full}
				<div class="notes">
					{@render problem()}
					{@render children?.()}
				</div>
			{/if}
		</div>
	</div>
</div>
{#if !full}
	{#if stream}
		<div class="controls">
			<Button type="button" tone="ghost" onclick={stop}>Wyłącz aparat</Button>
		</div>
	{:else}
		<div>{@render startButton()}</div>
	{/if}
	{@render problem()}
	{@render children?.()}
{/if}

<style>
	/* The camera is always dark, so it takes the dark theme's accent and success colours. */
	.stage {
		--camera-accent: #f2779f;
		--camera-ok: #6fbf97;
		--camera-ink: #ffffff;

		background: #000000;
		box-sizing: border-box;
		color: var(--camera-ink);
		display: none;
		overflow: hidden;
		position: relative;
	}

	.stage.live {
		aspect-ratio: 4 / 3;
		border-radius: var(--sui-radius-md, 0.5rem);
		display: block;
		max-height: 50vh;
		width: 100%;
	}

	/* On a phone the camera takes the whole screen, under the status bar and the tab bar, and the
	   page under it does not scroll. */
	:global(:root:has(.ohara-full-camera)) {
		overflow: hidden;
	}

	.stage.full {
		aspect-ratio: auto;
		border-radius: 0;
		display: block;
		inset: 0;
		max-height: none;
		position: fixed;
		width: auto;
	}

	video {
		display: block;
		height: 100%;
		inset: 0;
		object-fit: cover;
		position: absolute;
		width: 100%;
	}

	.overlay {
		box-sizing: border-box;
		display: flex;
		flex-direction: column;
		inset: 0;
		padding: 0.75rem;
		position: absolute;
	}

	.full .overlay {
		padding-block: max(env(safe-area-inset-top, 0px), 0.75rem)
			calc(4rem + 2 * max(env(safe-area-inset-bottom, 0px), 0.75rem));
		padding-inline: max(env(safe-area-inset-left, 0px), 1rem)
			max(env(safe-area-inset-right, 0px), 1rem);
	}

	.bar {
		align-items: center;
		display: flex;
		gap: 0.625rem;
		position: relative;
		z-index: 2;
	}

	.spacer {
		flex: 1;
	}

	.glass {
		-webkit-backdrop-filter: blur(20px) saturate(1.5);
		backdrop-filter: blur(20px) saturate(1.5);
		background: rgb(20 24 22 / 50%);
		border: 1px solid rgb(255 255 255 / 14%);
		box-sizing: border-box;
		color: var(--camera-ink);
		font: inherit;
		-webkit-tap-highlight-color: transparent;
		touch-action: manipulation;
	}

	.chip {
		align-items: center;
		border-radius: 1.375rem;
		display: inline-flex;
		font-size: 0.875rem;
		font-weight: 700;
		gap: 0.5rem;
		min-block-size: 2.75rem;
		min-width: 0;
		padding: 0 0.875rem 0 0.5rem;
		text-decoration: none;
	}

	.count {
		align-items: center;
		background: var(--camera-accent);
		border-radius: 0.75rem;
		box-sizing: border-box;
		color: #0e1b17;
		display: inline-flex;
		font-size: 0.75rem;
		font-weight: 800;
		justify-content: center;
		min-block-size: 1.5rem;
		min-inline-size: 1.5rem;
		padding: 0 0.375rem;
	}

	.round {
		align-items: center;
		block-size: 2.75rem;
		border-radius: 50%;
		cursor: pointer;
		display: inline-flex;
		flex: none;
		inline-size: 2.75rem;
		justify-content: center;
		padding: 0;
	}

	.round[aria-pressed='true'] {
		background: rgb(255 255 255 / 90%);
		color: #0e1b17;
	}

	.glass:focus-visible {
		outline: 2px solid var(--camera-ink);
		outline-offset: 2px;
	}

	.middle {
		align-items: center;
		display: flex;
		flex: 1;
		flex-direction: column;
		justify-content: center;
		min-block-size: 0;
	}

	/* The band sits a little above the middle of the free space, as the mockup draws it. */
	.full .middle::before,
	.full .middle::after {
		content: '';
		flex: 1;
	}

	.full .middle::after {
		flex-grow: 1.6;
	}

	.band {
		block-size: 8rem;
		border-radius: 1.375rem;
		box-shadow: 0 0 0 100vmax rgb(8 6 6 / 52%);
		flex: none;
		inline-size: min(100% - 2.5rem, 26rem);
		position: relative;
		z-index: 1;
	}

	.band::before {
		border: 3px solid rgb(255 255 255 / 92%);
		border-radius: inherit;
		content: '';
		inset: 0;
		mask:
			linear-gradient(#000 0 0) top left / 2.125rem 2.125rem no-repeat,
			linear-gradient(#000 0 0) top right / 2.125rem 2.125rem no-repeat,
			linear-gradient(#000 0 0) bottom left / 2.125rem 2.125rem no-repeat,
			linear-gradient(#000 0 0) bottom right / 2.125rem 2.125rem no-repeat;
		position: absolute;
		transition: border-color 120ms ease-out;
	}

	.laser {
		background: var(--camera-accent);
		block-size: 2px;
		border-radius: 2px;
		box-shadow:
			0 0 10px var(--camera-accent),
			0 0 22px rgb(242 119 159 / 60%);
		inset-block-start: 50%;
		inset-inline: 1.125rem;
		position: absolute;
		transition:
			background-color 120ms ease-out,
			box-shadow 120ms ease-out;
	}

	.band.paused .laser {
		opacity: 0.35;
	}

	.band.ok::before {
		border-color: var(--camera-ok);
	}

	.band.ok .laser {
		background: var(--camera-ok);
		box-shadow:
			0 0 10px var(--camera-ok),
			0 0 22px rgb(111 191 151 / 60%);
		opacity: 1;
	}

	.hint {
		font-size: 0.9375rem;
		font-weight: 600;
		margin: 1.125rem 0 0;
		position: relative;
		text-align: center;
		text-shadow: 0 1px 6px rgb(0 0 0 / 50%);
		z-index: 1;
	}

	.notes {
		box-sizing: border-box;
		display: grid;
		gap: 0.75rem;
		inline-size: min(100%, 26rem);
		margin-block-start: 1rem;
		position: relative;
		z-index: 1;
	}

	.notes:empty {
		display: none;
	}

	.controls {
		display: flex;
		justify-content: flex-end;
	}

	@media (prefers-reduced-motion: reduce) {
		.band::before,
		.laser {
			transition: none;
		}
	}

	@media (prefers-reduced-transparency: reduce) {
		.glass {
			-webkit-backdrop-filter: none;
			backdrop-filter: none;
			background: rgb(20 24 22 / 92%);
		}
	}

	@media (forced-colors: active) {
		.glass,
		.band::before {
			border-color: CanvasText;
		}
	}
</style>
