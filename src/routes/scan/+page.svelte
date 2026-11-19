<script module lang="ts">
	/** Numbers each sheet, so a barcode read again opens a fresh one. */
	let sheetSeq = 0;
</script>

<script lang="ts">
	import {
		Button,
		Callout,
		Field,
		Modal,
		ObjectList,
		ObjectRow,
		PageHeader,
		Pill,
		Section,
		TextInput
	} from '@smykla-skalski/sui';
	import { onMount, untrack } from 'svelte';
	import { MediaQuery } from 'svelte/reactivity';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { request } from '#lib/api.js';
	import BarcodeCamera from '#lib/components/BarcodeCamera.svelte';
	import ScanItem from '#lib/components/ScanItem.svelte';
	import { queueDeletesSettled } from '#lib/queue-deletes.js';
	import { outcomeLabels, outcomeTones, recentScans } from '#lib/recent.svelte.js';
	import { ScanFlow, type Added } from '#lib/scan-flow.svelte.js';
	import {
		RepeatGuard,
		barcodeInputError,
		cleanBarcode,
		looksLikeBarcode,
		type QueueEntry
	} from '#lib/scan.js';

	type Sheet = NonNullable<App.PageState['scanSheet']>;

	/** The tab bar's width, where the camera fills the screen and manual entry opens over it. */
	const phone = new MediaQuery('max-width: 47.9375rem');
	/** How long a code must stay out of view before the camera takes it again. */
	const repeatWindow = 3000;
	/** How much of the scan band stays in view above the sheet. */
	const bandPeek = 56;

	function storage(): Storage | undefined {
		try {
			return localStorage;
		} catch {
			return undefined;
		}
	}

	let code = $state('');
	let leaving = $state(false);
	let typing = $state(false);
	let notice = $state<string | null>(null);
	let codeError = $state<string>();
	let queued = $state<number | null>(null);
	let band = $state<HTMLElement>();
	let sheetTop = $state<number | null>(null);
	let keyboard = $state(0);
	let viewport = $state<number | null>(null);
	let shown: Sheet | null = null;
	/** Every code the open sheet has shown, a wedge scan's next codes included. */
	let handled: string[] = [];
	let closing = 0;
	const guard = new RepeatGuard(repeatWindow);
	const flow = new ScanFlow(fetch, storage());

	const sheet = $derived(page.state.scanSheet);
	const sheetTitle = $derived(titleFor());
	const sheetStyle = $derived(
		[
			sheetTop === null ? '' : `--scan-sheet-top: ${sheetTop}px`,
			`--scan-keyboard: ${keyboard}px`,
			viewport === null ? '' : `--scan-viewport: ${viewport}px`
		]
			.filter(Boolean)
			.join('; ')
	);

	function titleFor(): string {
		const p = flow.phase;
		switch (p.kind) {
			case 'loading':
				return 'Sprawdzam kod…';
			case 'queued':
				return 'Nie znaleziono';
			case 'invalid':
				return 'Nieprawidłowy kod';
			case 'failed':
				return 'Nie udało się sprawdzić kodu';
		}
		if (p.status === 'owned') return 'Masz już to wydanie';
		return p.status === 'missing' ? 'Dodaj ręcznie' : 'Znaleziono';
	}

	async function countQueue() {
		try {
			await queueDeletesSettled();
			queued = (await request<QueueEntry[]>(fetch, 'GET', '/api/scan-queue')).length;
		} catch {
			queued = null;
		}
	}

	/**
	 * Opens the sheet for a code over the camera, in a history entry of its own so going back
	 * closes it. A sheet already open gives way to the next code in the same entry. Returns
	 * whether the code was taken.
	 */
	function open(raw: string, typed: boolean): boolean {
		const barcode = cleanBarcode(raw);
		if (!barcode || leaving) return false;
		if (!looksLikeBarcode(barcode)) {
			notice = `„${raw.trim()}” nie wygląda na kod kreskowy.`;
			return false;
		}
		notice = null;
		code = '';
		typing = false;
		const top = band?.getBoundingClientRect().top;
		sheetTop = phone.current && top !== undefined ? Math.round(top + bandPeek) : null;
		const scanSheet: Sheet = { barcode, id: ++sheetSeq, typed };
		void goto(page.url.href, {
			shallow: true,
			replace: !!page.state.scanSheet,
			state: { scanSheet }
		});
		return true;
	}

	function closeSheet() {
		const s = page.state.scanSheet;
		if (!s || closing === s.id) return;
		closing = s.id;
		history.back();
	}

	function fromCamera(barcode: string): boolean {
		if (sheet || !guard.accepts(barcode, performance.now())) return false;
		return open(barcode, false);
	}

	function submitCode() {
		if (leaving) return;
		codeError = barcodeInputError(cleanBarcode(code));
		if (codeError) {
			document.getElementById('scan-code')?.focus();
			return;
		}
		open(code, true);
	}

	function added(a: Added) {
		if (!page.state.scanSheet) return;
		if (a.next) open(a.next, true);
		else closeSheet();
	}

	async function fillByHand() {
		const s = page.state.scanSheet;
		if (!s) return;
		flow.dismiss();
		await goto(`/scan/${encodeURIComponent(flow.item?.barcode ?? s.barcode)}?manual=1`, {
			replace: true
		});
	}

	function sheetOpened(s: Sheet) {
		if (shown?.id === s.id) return;
		shown = s;
		if (!handled.includes(s.barcode)) handled.push(s.barcode);
		void flow.load(s.barcode);
	}

	function sheetClosed() {
		const now = performance.now();
		for (const barcode of handled) guard.remember(barcode, now);
		handled = [];
		flow.dismiss();
		shown = null;
		closing = 0;
		sheetTop = null;
		void countQueue();
	}

	$effect(() => {
		const s = sheet;
		untrack(() => {
			if (s) sheetOpened(s);
			else if (shown) sheetClosed();
		});
	});

	$effect(() => {
		if (flow.phase.kind === 'queued') untrack(() => void countQueue());
	});

	$effect(() => {
		const vv = window.visualViewport;
		if (!sheet || !phone.current || !vv) return;
		const follow = () => {
			keyboard = Math.max(0, Math.round(window.innerHeight - vv.height - vv.offsetTop));
			viewport = Math.round(vv.height);
			if (keyboard > 0) requestAnimationFrame(revealFocus);
		};
		follow();
		vv.addEventListener('resize', follow);
		vv.addEventListener('scroll', follow);
		return () => {
			vv.removeEventListener('resize', follow);
			vv.removeEventListener('scroll', follow);
			keyboard = 0;
			viewport = null;
		};
	});

	function revealFocus() {
		const at = document.activeElement;
		if (at instanceof HTMLElement && at.closest('.scan-sheet')) {
			at.scrollIntoView({ block: 'nearest' });
		}
	}

	onMount(() => {
		void countQueue();
		// Links from before the add page existed: /scan?barcode=…[&manual=1].
		const preset = cleanBarcode(page.url.searchParams.get('barcode') ?? '');
		if (preset) {
			leaving = true;
			const manual = page.url.searchParams.get('manual') === '1' ? '?manual=1' : '';
			void goto(`/scan/${encodeURIComponent(preset)}${manual}`, { replace: true });
		} else if (!phone.current) {
			document.getElementById('scan-code')?.focus();
		}
	});
</script>

<svelte:head>
	<title>Skanuj · Ohara</title>
</svelte:head>

{#snippet manualEntry()}
	<form
		novalidate
		onsubmit={(e) => {
			e.preventDefault();
			submitCode();
		}}
	>
		<Field
			id="scan-code"
			label="Kod kreskowy"
			hint="Wpisz kod albo użyj czytnika Bluetooth: kursor musi być w tym polu."
			error={codeError}
		>
			{#snippet control({ id, describedby, invalid })}
				<div class="manual">
					<TextInput
						{id}
						aria-describedby={describedby}
						{invalid}
						inputmode="numeric"
						enterkeyhint="search"
						autocomplete="off"
						disabled={leaving}
						bind:value={code}
						oninput={() => (codeError = undefined)}
					/>
					<Button type="submit" loading={leaving}>Szukaj</Button>
				</div>
			{/snippet}
		</Field>
	</form>
{/snippet}

{#snippet recent()}
	{#if recentScans.list.length > 0}
		<Section title="Ostatnie skany">
			<ObjectList>
				{#each recentScans.list as r (r.id)}
					<ObjectRow href={r.href}>
						{#snippet main()}
							<span class="code">{r.barcode}</span>
							{r.label}
						{/snippet}
						{#snippet side()}
							<Pill tone={outcomeTones[r.outcome]}>{outcomeLabels[r.outcome]}</Pill>
						{/snippet}
					</ObjectRow>
				{/each}
			</ObjectList>
		</Section>
	{/if}
{/snippet}

{#snippet message()}
	{#if notice}
		<Callout tone="warning" live>{notice}</Callout>
	{/if}
{/snippet}

{#if phone.current}
	<h1 class="title">Skanuj</h1>
{:else}
	<PageHeader
		title="Skanuj"
		description="Aparat albo czytnik kodów: każdy kod otwiera okno, w którym go dodasz."
	>
		{#snippet actions()}
			<Button href="/scan/queue">
				Do uzupełnienia{queued ? ` (${queued})` : ''}
			</Button>
		{/snippet}
	</PageHeader>
{/if}

<div class={['content', { phone: phone.current }]}>
	<BarcodeCamera
		full={phone.current}
		paused={!!sheet || leaving || (phone.current && typing)}
		ondetect={fromCamera}
		{queued}
		ontype={() => (typing = true)}
		bind:band
	>
		{@render message()}
	</BarcodeCamera>
	{#if !phone.current}
		{@render manualEntry()}
		{@render recent()}
	{/if}
</div>

{#if phone.current}
	<Modal bind:open={typing} title="Wpisz kod" closeLabel="Zamknij">
		<div class="typing">
			{@render manualEntry()}
			{@render recent()}
		</div>
	</Modal>
{/if}

<Modal
	open={!!sheet}
	class="scan-sheet"
	title={sheetTitle}
	closeLabel="Zamknij"
	beforeClose={() => !flow.saving}
	onclose={closeSheet}
	style={sheetStyle}
>
	<span class="grabber" aria-hidden="true"></span>
	<ScanItem
		{flow}
		autofocus={sheet?.typed ?? false}
		onadded={added}
		onclose={closeSheet}
		onmanual={fillByHand}
	/>
</Modal>

<style>
	.title {
		block-size: 1px;
		clip-path: inset(50%);
		inline-size: 1px;
		margin: 0;
		overflow: hidden;
		position: absolute;
		white-space: nowrap;
	}

	.content {
		display: grid;
		gap: 1rem;
		font-family: var(--sui-sans);
		color: var(--sui-text-primary);
		padding: var(--sui-pane-gutter, 1.5rem);
		max-width: 48rem;
	}

	.content.phone {
		padding: 0;
	}

	.typing {
		display: grid;
		gap: 1rem;
	}

	.manual {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: 0.5rem;
		align-items: center;
	}

	.code {
		font-family: var(--sui-mono);
		margin-inline-end: 0.5rem;
	}

	.grabber {
		display: none;
	}

	:global(.scan-sheet) {
		--scan-gutter: var(--sui-local-modal-inset);
	}

	/* On a phone the sheet rises from the bottom edge over the live camera, leaving the top of the
	   scan band in view, and stands on the keyboard while one is open. */
	@media (max-width: 47.9375rem) {
		:global(.scan-sheet[open]) {
			background: var(--sui-surface-base);
			border-block-end: 0;
			border-inline: 0;
			border-radius: 1.875rem 1.875rem 0 0;
			box-shadow: 0 -18px 50px rgb(0 0 0 / 35%);
			inline-size: 100%;
			margin: auto 0 var(--scan-keyboard, 0px);
			max-block-size: min(
				calc(100dvh - var(--scan-sheet-top, 6rem)),
				calc(var(--scan-viewport, 100dvh) - 0.5rem)
			);
			max-inline-size: 100%;
			padding-block: 0.625rem max(env(safe-area-inset-bottom, 0px), 1rem);
		}

		:global(.scan-sheet .sui-modal-head) {
			display: none;
		}

		:global(.scan-sheet::backdrop) {
			-webkit-backdrop-filter: none;
			backdrop-filter: none;
			background: rgb(0 0 0 / 18%);
		}

		.grabber {
			background: var(--sui-border-subtle);
			block-size: 5px;
			border-radius: 3px;
			display: block;
			inline-size: 40px;
			margin: 0 auto 0.375rem;
		}
	}
</style>
