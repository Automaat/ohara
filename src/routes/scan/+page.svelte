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
		TextInput,
		Toaster,
		dismiss,
		toast
	} from '@smykla-skalski/sui';
	import { onDestroy, onMount, tick, untrack } from 'svelte';
	import { MediaQuery } from 'svelte/reactivity';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { AddToasts } from '#lib/add-toast.js';
	import { request } from '#lib/api.js';
	import { formatPLN } from '#lib/collection.js';
	import BarcodeCamera from '#lib/components/BarcodeCamera.svelte';
	import CollectionCover from '#lib/components/CollectionCover.svelte';
	import ScanItem from '#lib/components/ScanItem.svelte';
	import SessionTray from '#lib/components/SessionTray.svelte';
	import { phoneLayout, shortScreen } from '#lib/nav.js';
	import { queueDeletesSettled } from '#lib/queue-deletes.js';
	import { outcomeLabels, outcomeTones, recentScans } from '#lib/recent.svelte.js';
	import { ScanFlow, type Added } from '#lib/scan-flow.svelte.js';
	import { session, sessionSpent, undoAdd, type SessionAdd } from '#lib/session.svelte.js';
	import {
		RepeatGuard,
		barcodeInputError,
		cleanBarcode,
		looksLikeBarcode,
		type QueueEntry
	} from '#lib/scan.js';

	type Sheet = NonNullable<App.PageState['scanSheet']>;

	/** The phone layout, where the camera fills the screen and manual entry opens over it. */
	const phone = new MediaQuery(phoneLayout);
	/** A screen too short for the add toasts to grow taller than one line of each. */
	const short = new MediaQuery(shortScreen);
	/**
	 * A short screen wide enough for the tray beside the tab bar, which leaves the band its height;
	 * a narrower one keeps the tray above the bar.
	 */
	const beside = new MediaQuery(`${shortScreen} and (min-width: 600px)`);
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
	/** Where to go once the sheet's history entry is gone, as the add page for a code by hand. */
	let afterClose: string | null = null;
	let closing = 0;
	/** An add whose toast waits for the sheet to close, so the page's toasts announce it. */
	let addedOnClose: SessionAdd | null = null;
	let listOpen = $state(false);
	const guard = new RepeatGuard(repeatWindow);
	const flow = new ScanFlow(fetch, storage());
	const addToasts = new AddToasts(
		{
			toast,
			dismiss,
			holding: (id) => {
				const el = document.querySelector(`[data-toast="${id}"]`);
				return el !== null && (el.matches(':hover') || el.contains(document.activeElement));
			}
		},
		(add) => undoAdd(fetch, add)
	);

	const sheet = $derived(page.state.scanSheet);
	const trayShown = $derived(session.adds.length > 0);
	const spent = $derived(formatPLN(sessionSpent(session.adds)));
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

	/** Stands the phone sheet's top just below the top of the scan band, so the band stays in view. */
	function placeSheet() {
		const top = band?.getBoundingClientRect().top;
		sheetTop = phone.current && top !== undefined ? Math.round(top + bandPeek) : null;
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
		placeSheet();
		const scanSheet: Sheet = { barcode, id: ++sheetSeq, typed };
		void goto(page.url.href, {
			shallow: true,
			replace: !!page.state.scanSheet,
			state: { scanSheet }
		});
		return true;
	}

	/**
	 * Where focus goes when the sheet closes and the field that opened it is gone with the closed
	 * manual entry: the code field, else the button that types a code. Never the toast's
	 * "Cofnij", where a scanner's next Enter would undo the add.
	 */
	function sheetReturn(): HTMLElement | null {
		const visible = (el: HTMLElement | null) => (el?.checkVisibility() ? el : null);
		return (
			visible(document.getElementById('scan-code')) ??
			visible(document.querySelector<HTMLElement>('button[aria-label="Wpisz kod"]'))
		);
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

	/**
	 * After an add the sheet closes and the toast names what went in. A wedge scan's next code
	 * keeps the sheet open, so its toast shows in the sheet.
	 */
	function added(a: Added) {
		if (!page.state.scanSheet) return;
		if (a.next) {
			open(a.next, true);
			addToasts.show(a.add);
		} else {
			addedOnClose = a.add;
			closeSheet();
		}
	}

	/** Shows an add's toast once the closed sheet no longer hosts the page's toasts. */
	async function toastAfterClose(add: SessionAdd) {
		await tick();
		if (!page.state.scanSheet) addToasts.show(add);
	}

	/**
	 * Opens the add page for a code typed in by hand once the sheet has closed, so going back
	 * from that page lands on the scanner rather than reopening the sheet and queuing the code
	 * again.
	 */
	function fillByHand() {
		const s = page.state.scanSheet;
		if (!s) return;
		flow.dismiss();
		afterClose = `/scan/${encodeURIComponent(flow.item?.barcode ?? s.barcode)}?manual=1`;
		closeSheet();
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
		const next = afterClose;
		afterClose = null;
		const add = addedOnClose;
		addedOnClose = null;
		if (add) void toastAfterClose(add);
		if (next) void goto(next);
		else void countQueue();
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
			const inset = Math.max(0, Math.round(window.innerHeight - vv.height - vv.offsetTop));
			keyboard = inset;
			viewport = Math.round(vv.height);
			if (inset > 0) requestAnimationFrame(revealFocus);
		};
		untrack(follow);
		vv.addEventListener('resize', follow);
		vv.addEventListener('scroll', follow);
		return () => {
			vv.removeEventListener('resize', follow);
			vv.removeEventListener('scroll', follow);
			keyboard = 0;
			viewport = null;
		};
	});

	/* Turning the phone moves the band, so an open sheet follows it once the new layout is drawn. */
	$effect(() => {
		if (!sheet) return;
		let frame = 0;
		const onresize = () => {
			cancelAnimationFrame(frame);
			frame = requestAnimationFrame(placeSheet);
		};
		addEventListener('resize', onresize);
		return () => {
			cancelAnimationFrame(frame);
			removeEventListener('resize', onresize);
		};
	});

	function revealFocus() {
		const at = document.activeElement;
		if (at instanceof HTMLElement && at.closest('.scan-sheet')) {
			at.scrollIntoView({ block: 'nearest' });
		}
	}

	onDestroy(() => addToasts.destroy());

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

{#snippet tray(glass: boolean)}
	<SessionTray adds={session.adds} {glass} onopen={() => (listOpen = true)} />
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
		paused={!!sheet || leaving || listOpen || (phone.current && typing)}
		ondetect={fromCamera}
		{queued}
		ontype={() => (typing = true)}
		bind:band
	>
		{@render message()}
	</BarcodeCamera>
	{#if !phone.current}
		{@render manualEntry()}
		{#if trayShown}{@render tray(false)}{/if}
		{@render recent()}
	{/if}
</div>

{#if phone.current && trayShown}
	<div class={['tray-dock', { beside: beside.current }]} data-sui-theme="dark">
		{@render tray(true)}
	</div>
{/if}

{#if phone.current}
	<Modal bind:open={typing} title="Wpisz kod" closeLabel="Zamknij">
		<div class="typing">
			{@render manualEntry()}
			{@render recent()}
		</div>
	</Modal>
{/if}

<Modal bind:open={listOpen} class="session-list" title="Ta sesja" closeLabel="Zamknij">
	{#if trayShown}
		<div class="session">
			<p class="summary">Dodano: {session.adds.length} · Wydano {spent}</p>
			<ObjectList>
				{#each session.adds as add (add.id)}
					<ObjectRow href={`/copies/${add.copyID}`}>
						{#snippet main()}
							<span class="added">
								<CollectionCover kind={add.kind} url={add.cover} />
								<span class="added-text">
									<span class="added-name">{add.name}</span>
									{#if add.medium}<span class="metadata">{add.medium}</span>{/if}
								</span>
							</span>
						{/snippet}
						{#snippet side()}
							{#if add.status === 'owned'}
								<span class="price">
									{add.priceCents === null ? 'Bez ceny' : formatPLN(add.priceCents)}
								</span>
							{:else}
								<Pill tone="neutral">Lista życzeń</Pill>
							{/if}
						{/snippet}
					</ObjectRow>
				{/each}
			</ObjectList>
		</div>
	{:else}
		<p class="summary">Nic tu już nie ma: każde dodanie zostało cofnięte.</p>
	{/if}
</Modal>

<div
	class={[
		'scan-toasts',
		{ 'over-tray': phone.current && trayShown && !beside.current, short: short.current }
	]}
	data-sui-theme={phone.current ? 'dark' : undefined}
>
	<Toaster label="Powiadomienia" dismissLabel="Zamknij" />
</div>

<Modal
	open={!!sheet}
	class="scan-sheet"
	title={sheetTitle}
	closeLabel="Zamknij"
	beforeClose={() => !flow.saving}
	returnFocus={sheetReturn}
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

	.session {
		display: grid;
		gap: 0.75rem;
	}

	.summary {
		color: var(--sui-text-secondary);
		font-variant-numeric: tabular-nums;
		margin: 0;
	}

	.added {
		--collection-cover-width: 2.5rem;

		align-items: center;
		display: flex;
		gap: 0.75rem;
		min-inline-size: 0;
	}

	.added-text {
		display: grid;
		min-inline-size: 0;
	}

	.added-name {
		font-weight: 600;
	}

	/* On a phone the tray floats over the camera just above the tab bar, where the camera's
	   overlay keeps its foot clear, and hides with the tab bar under a dialog. */
	.tray-dock {
		--session-tray-height: 4.5rem;

		inset-block-end: calc(4rem + 2 * max(env(safe-area-inset-bottom, 0px), 0.75rem));
		inset-inline: max(env(safe-area-inset-left, 0px), 1rem)
			max(env(safe-area-inset-right, 0px), 1rem);
		position: fixed;
		z-index: 1;
	}

	/* On a short screen, as a phone held sideways, the tray stands beside a narrower tab bar, so
	   the scan band keeps the height between the camera's bar and the tab bar. Its two lines stay
	   one line each, cut short when a large sum does not fit. */
	:global(:root:has(.tray-dock.beside)) {
		--tray-beside-width: min(20rem, 40vw);
	}

	.tray-dock.beside {
		--session-tray-height: 4rem;

		inline-size: var(--tray-beside-width);
		inset-block-end: max(env(safe-area-inset-bottom, 0px), 0.75rem);
		inset-inline-start: auto;
	}

	.tray-dock.beside :global(:is(.count, .spent)) {
		min-inline-size: 0;
		overflow-x: clip;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	:global(:root:has(.tray-dock.beside) .ohara-phone .tab-bar) {
		margin-inline-end: calc(
			max(env(safe-area-inset-right, 0px), 1rem) + var(--tray-beside-width) + 0.625rem
		);
	}

	:global(:root:has(dialog:modal)) .tray-dock {
		visibility: hidden;
	}

	/* The add toast: a success tile before the copy and "Cofnij" beside it, as the mockup
	   draws it. A failure keeps SUI's own symbol and layout. */
	.scan-toasts :global(.sui-toast-body:not(:has(.sui-toast-mark))::before) {
		background: var(--sui-success)
			url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='none' stroke='%23ffffff' stroke-width='3' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpath d='M5 12.5 10 17.5 19 7'/%3E%3C/svg%3E")
			center / 1.25rem no-repeat;
		block-size: 2.5rem;
		border-radius: 50%;
		content: '';
		flex: none;
		inline-size: 2.5rem;
	}

	.scan-toasts
		:global(
			.sui-toaster[data-sui-theme='dark'] .sui-toast-body:not(:has(.sui-toast-mark))::before
		) {
		background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='none' stroke='%230e1b17' stroke-width='3' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpath d='M5 12.5 10 17.5 19 7'/%3E%3C/svg%3E");
	}

	.scan-toasts :global(.sui-toast-body:not(:has(.sui-toast-mark))) {
		align-items: center;
	}

	.scan-toasts :global(.sui-toast-body:not(:has(.sui-toast-mark)) .sui-toast-main) {
		align-items: center;
		grid-template-columns: minmax(0, 1fr) auto;
	}

	/* On a phone the toast stands over the camera above the tab bar, and above the tray when the
	   visit has one. */
	:global(.ohara-phone) .scan-toasts {
		--session-tray-height: 4.5rem;
	}

	/* The toast takes the width of the tray under it. */
	:global(.ohara-phone) .scan-toasts :global(.sui-toast) {
		max-inline-size: none;
	}

	/* A popover is as wide as its content; between both screen edges it spans the screen. */
	:global(.ohara-phone) .scan-toasts :global(.sui-toaster) {
		inline-size: auto;
		inset-inline: 0;
		inset-block-end: calc(
			4rem + 2 * max(env(safe-area-inset-bottom, 0px), 0.75rem) - var(--sui-space-4)
		);
	}

	/* On a short screen the toasts stand side by side above the tab bar, each at most SUI's own
	   width, so a second add's toast never stacks up over the scan band. */
	:global(.ohara-phone) .scan-toasts.short :global(.sui-toaster) {
		flex-direction: row;
		justify-content: flex-end;
	}

	:global(.ohara-phone) .scan-toasts.short :global(.sui-toast) {
		flex: 0 1 24rem;
		inline-size: auto;
		max-inline-size: 24rem;
		min-inline-size: 0;
	}

	/* A narrow toast keeps one line of title and one of description, cut short, so it never grows
	   up over the band; its live region still reads the whole text. */
	:global(.ohara-phone) .scan-toasts.short :global(.sui-toast-body) {
		flex-wrap: nowrap;
	}

	:global(.ohara-phone) .scan-toasts.short :global(:is(.sui-toast-title, .sui-toast-description)) {
		min-inline-size: 0;
		overflow-x: clip;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	:global(.ohara-phone) .scan-toasts.over-tray :global(.sui-toaster) {
		inset-block-end: calc(
			4rem + 2 * max(env(safe-area-inset-bottom, 0px), 0.75rem) + var(--session-tray-height) +
				0.75rem - var(--sui-space-4)
		);
	}

	:global(.scan-sheet) {
		--scan-gutter: var(--sui-local-modal-inset);
	}

	/* Every control in the sheet is a 44px target, its close button too, with a mouse as well. */
	:global(dialog.sui-modal.scan-sheet .sui-modal-close) {
		--sui-tier-quiet: max(44px, 2.75rem);
	}

	/* On a phone the sheet rises from the bottom edge over the live camera, leaving the top of the
	   scan band in view, yet never under half the screen, and stands on the keyboard while one is
	   open. Held sideways, it keeps its content clear of the notch. */
	:global(.ohara-phone dialog.sui-modal.scan-sheet[open]) {
		background: var(--sui-surface-base);
		border-block-end: 0;
		border-inline: 0;
		border-radius: 1.875rem 1.875rem 0 0;
		box-shadow: 0 -18px 50px rgb(0 0 0 / 35%);
		inline-size: 100%;
		margin: auto 0 var(--scan-keyboard, 0px);
		max-block-size: min(
			max(calc(100dvh - var(--scan-sheet-top, 6rem)), 50dvh),
			calc(var(--scan-viewport, 100dvh) - 0.5rem)
		);
		max-inline-size: 100%;
		padding-block: 0.625rem max(env(safe-area-inset-bottom, 0px), 1rem);
		padding-inline: env(safe-area-inset-left, 0px) env(safe-area-inset-right, 0px);
	}

	:global(.ohara-phone dialog.sui-modal.scan-sheet .sui-modal-head) {
		display: none;
	}

	/* While a sheet is open, its toasts stand at the top of the screen, over the camera, so a
	   toast from the last add never covers the sheet's buttons. */
	:global(.ohara-phone dialog.sui-modal:is(.scan-sheet, .session-list) .sui-toaster) {
		inline-size: auto;
		inset-block: env(safe-area-inset-top, 0px) auto;
		inset-inline: 0;
	}

	:global(.ohara-phone dialog.sui-modal.scan-sheet::backdrop) {
		-webkit-backdrop-filter: none;
		backdrop-filter: none;
		background: rgb(0 0 0 / 18%);
	}

	:global(.ohara-phone) .grabber {
		background: var(--sui-border-subtle);
		block-size: 5px;
		border-radius: 3px;
		display: block;
		inline-size: 40px;
		margin: 0 auto 0.375rem;
	}
</style>
