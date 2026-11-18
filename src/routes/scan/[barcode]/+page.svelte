<script lang="ts">
	import { BackLink, Button, Callout, Field, PageHeader, Skeleton } from '@smykla-skalski/sui';
	import { tick, untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { ApiError, errorMessage } from '#lib/api.js';
	import { formatPLN, type FormIssue } from '#lib/collection.js';
	import ScanConfirm from '#lib/components/ScanConfirm.svelte';
	import { logScan } from '#lib/recent.svelte.js';
	import {
		FormRejected,
		cleanBarcode,
		loadSticky,
		looksLikeBarcode,
		newRequestKey,
		quickForm,
		resolveScan,
		saveScan,
		saveSticky,
		searchHints,
		searchTitles,
		wedgeBarcodeIn,
		type Candidate,
		type LookupResult,
		type QuickForm,
		type SaveTarget
	} from '#lib/scan.js';

	type Pending = {
		key: string;
		barcode: string;
		target: SaveTarget;
		candidates: Candidate[];
		picked: number;
		errors: LookupResult['errors'];
	};
	/** found and owned have candidates or the edition; missing is filled in by hand. */
	type Status = 'found' | 'owned' | 'missing';
	type Phase =
		| { kind: 'loading' }
		| { kind: 'queued' }
		| { kind: 'ready'; status: Status }
		| { kind: 'saved'; name: string; price: string; href: string }
		| { kind: 'failed'; message: string };

	const statusTitles: Record<Status, string> = {
		found: 'Znaleziono',
		owned: 'Masz już to wydanie',
		missing: 'Dodaj ręcznie'
	};

	function storage(): Storage | undefined {
		try {
			return localStorage;
		} catch {
			return undefined;
		}
	}

	let phase = $state<Phase>({ kind: 'loading' });
	let pending = $state<Pending | null>(null);
	let form = $state<QuickForm>(quickForm(null, loadSticky(undefined), ''));
	let issue = $state<FormIssue | null>(null);
	let saving = $state(false);
	let notice = $state<string | null>(null);
	let albumQuery = $state('');
	let searching = $state(false);
	let nextCode = $state('');
	/** What the previous barcode came to, when a scanner moved straight on to this one. */
	let previous = $state<string | null>(null);
	let sticky = loadSticky(storage());

	const barcode = $derived(cleanBarcode(page.params.barcode ?? ''));
	const manual = $derived(page.url.searchParams.get('manual') === '1');

	$effect(() => {
		const code = barcode;
		const byHand = manual;
		untrack(() => void load(code, byHand));
	});

	async function focus(id: string) {
		await tick();
		document.getElementById(id)?.focus();
	}

	function blank(code: string) {
		pending = {
			key: newRequestKey(),
			barcode: code,
			target: { kind: 'new', barcode: code, candidate: null },
			candidates: [],
			picked: 0,
			errors: []
		};
		form = quickForm(null, sticky, code);
	}

	async function load(code: string, byHand: boolean) {
		phase = { kind: 'loading' };
		pending = null;
		issue = null;
		notice = null;
		albumQuery = '';
		nextCode = '';
		sticky = loadSticky(storage());
		if (!looksLikeBarcode(code)) {
			phase = { kind: 'failed', message: `„${code}” nie wygląda na kod kreskowy.` };
			return;
		}
		if (byHand) {
			blank(code);
			enterManually();
			return;
		}
		try {
			const r = await resolveScan(fetch, code);
			if (code !== barcode) return;
			if (r.kind === 'owned') {
				pending = {
					key: newRequestKey(),
					barcode: r.barcode,
					target: { kind: 'owned', edition: r.edition },
					candidates: [],
					picked: 0,
					errors: []
				};
				form = { ...quickForm(null, sticky, r.barcode), kind: r.edition.title.kind };
				phase = { kind: 'ready', status: 'owned' };
			} else if (r.kind === 'found') {
				const first = r.candidates[0];
				pending = {
					key: newRequestKey(),
					barcode: r.barcode,
					target: { kind: 'new', barcode: r.barcode, candidate: first },
					candidates: r.candidates,
					picked: 0,
					errors: r.errors
				};
				form = quickForm(first, sticky, r.barcode);
				phase = { kind: 'ready', status: 'found' };
			} else {
				blank(r.entry.barcode);
				phase = { kind: 'queued' };
				void focus('scan-next');
				return;
			}
			void focus('scan-price');
		} catch (err) {
			phase = { kind: 'failed', message: errorMessage(err) };
		}
	}

	function pick(index: number) {
		if (!pending || pending.target.kind !== 'new') return;
		const candidate = pending.candidates[index];
		pending.picked = index;
		pending.target = { kind: 'new', barcode: pending.barcode, candidate };
		const next = quickForm(candidate, sticky, pending.barcode);
		const sameKind = next.kind === form.kind;
		form = {
			...next,
			format: next.format || (sameKind ? form.format : ''),
			binding: next.binding || (sameKind ? form.binding : ''),
			price: form.price,
			shop: form.shop,
			acquired_as: form.acquired_as
		};
	}

	async function searchByTitle() {
		const query = albumQuery.trim();
		if (!pending || pending.target.kind !== 'new' || searching || !query) return;
		searching = true;
		try {
			const found = await searchTitles(fetch, form.kind, query, pending.barcode, form.format);
			if (found.length === 0) {
				notice = `Nic nie znaleziono dla „${query}”.`;
				return;
			}
			notice = null;
			pending.candidates = found;
			pending.errors = [];
			pick(0);
			void focus('scan-price');
		} catch (err) {
			notice = errorMessage(err);
		} finally {
			searching = false;
		}
	}

	async function save() {
		if (!pending || saving) return;
		const current = pending;
		const next = wedgeBarcodeIn(form.price);
		if (next) form.price = '';
		saving = true;
		issue = null;
		try {
			const saved = await saveScan(fetch, current.target, form, current.key);
			sticky = { shop: form.shop.trim(), acquired_as: form.acquired_as };
			saveSticky(storage(), sticky);
			const price = saved.copy.price_cents === null ? '' : formatPLN(saved.copy.price_cents);
			const href = `/copies/${saved.copy.id}`;
			logScan(current.barcode, 'added', saved.titleName + (price ? ` · ${price}` : ''), href);
			if (next) {
				previous = `Dodano: ${saved.titleName}.`;
				await openNext(next);
				return;
			}
			previous = null;
			phase = { kind: 'saved', name: saved.titleName, price, href };
			void focus('scan-next');
		} catch (err) {
			issue =
				err instanceof FormRejected
					? { field: err.field, message: err.message }
					: {
							field: err instanceof ApiError ? (err.field ?? '') : '',
							message: errorMessage(err)
						};
			if (next) notice = `Kod ${next} nie został sprawdzony: zeskanuj go ponownie.`;
		} finally {
			saving = false;
		}
	}

	function logSkippedScan() {
		if (pending && !manual) {
			const missing =
				phase.kind === 'queued' || (phase.kind === 'ready' && phase.status === 'missing');
			logScan(
				pending.barcode,
				missing ? 'queued' : 'skipped',
				missing ? 'Brak danych, do uzupełnienia' : 'Nie dodano',
				missing ? '/scan/queue' : undefined
			);
		}
	}

	async function skip() {
		logSkippedScan();
		previous = null;
		await goto('/scan');
	}

	function enterManually() {
		phase = { kind: 'ready', status: 'missing' };
		void focus('scan-name');
	}

	async function scanNext() {
		if (phase.kind === 'queued') logSkippedScan();
		previous = null;
		await openNext(cleanBarcode(nextCode));
	}

	async function openNext(code: string) {
		if (code && code === barcode && !manual) {
			await load(code, false);
		} else {
			await goto(code ? `/scan/${encodeURIComponent(code)}` : '/scan');
		}
	}
</script>

<svelte:head>
	<title>{barcode} · Ohara</title>
</svelte:head>

<PageHeader
	title={phase.kind === 'saved'
		? `Dodano: ${phase.name}`
		: phase.kind === 'queued'
			? 'Nie znaleziono'
			: phase.kind === 'ready'
				? statusTitles[phase.status]
				: phase.kind === 'failed'
					? 'Nie udało się sprawdzić kodu'
					: 'Sprawdzam kod…'}
	description={phase.kind === 'saved' ? phase.price || undefined : (pending?.barcode ?? barcode)}
>
	{#snippet kicker()}
		<BackLink href="/scan" label="Skanuj" />
	{/snippet}
</PageHeader>

{#snippet nextScanForm()}
	<form
		class="row"
		novalidate
		onsubmit={(e) => {
			e.preventDefault();
			void scanNext();
		}}
	>
		<Field
			id="scan-next"
			label="Następny kod"
			hint="Czytnik Bluetooth może skanować od razu; puste pole wraca do aparatu."
			inputmode="numeric"
			enterkeyhint="go"
			autocomplete="off"
			bind:value={nextCode}
		/>
		<Button type="submit" tone={phase.kind === 'queued' ? undefined : 'signal'}>
			Skanuj kolejny
		</Button>
	</form>
{/snippet}

<div class="content">
	{#if previous}
		<Callout tone="quiet" live>{previous}</Callout>
	{/if}

	{#if phase.kind === 'loading'}
		<Skeleton rows={3} />
	{:else if phase.kind === 'queued'}
		<Callout tone="warning" live>Kod trafił do kolejki do uzupełnienia.</Callout>
		<div class="actions">
			<Button tone="signal" onclick={enterManually}>Dodaj ręcznie</Button>
			<Button href="/scan/queue">Do uzupełnienia</Button>
		</div>
		{@render nextScanForm()}
	{:else if phase.kind === 'failed'}
		<Callout tone="warning" live>{phase.message}</Callout>
		<div class="actions">
			<Button tone="signal" onclick={() => load(barcode, false)}>Spróbuj ponownie</Button>
			<Button href={`/scan/${encodeURIComponent(barcode)}?manual=1`}>Uzupełnij ręcznie</Button>
			<Button href="/scan">Skanuj kolejny</Button>
		</div>
	{:else if phase.kind === 'saved'}
		{@render nextScanForm()}
		<div class="actions">
			<Button href={phase.href}>Zobacz egzemplarz</Button>
		</div>
	{:else if pending}
		{#snippet titleSearch()}
			<form
				class="row"
				novalidate
				aria-label="Szukaj po tytule"
				onsubmit={(e) => {
					e.preventDefault();
					void searchByTitle();
				}}
			>
				<Field
					id="scan-album"
					label="Szukaj po tytule"
					hint={searchHints[form.kind]}
					enterkeyhint="search"
					autocomplete="off"
					bind:value={albumQuery}
				/>
				<Button type="submit" loading={searching} disabled={!albumQuery.trim()}>Szukaj</Button>
			</form>
		{/snippet}

		{#if phase.status === 'missing' && pending.target.kind === 'new'}
			{@render titleSearch()}
		{/if}

		{#if notice}
			<Callout tone="warning" live>{notice}</Callout>
		{/if}

		<ScanConfirm
			target={pending.target}
			candidates={pending.candidates}
			picked={pending.picked}
			errors={pending.errors}
			bind:form
			{issue}
			{saving}
			skipLabel={phase.status === 'missing' ? 'Anuluj' : undefined}
			onpick={pick}
			onsave={save}
			onskip={skip}
		/>

		{#if phase.status === 'found' && pending.target.kind === 'new'}
			{@render titleSearch()}
		{/if}
	{/if}
</div>

<style>
	.content {
		display: grid;
		gap: 1rem;
		font-family: var(--sui-sans);
		color: var(--sui-text-primary);
		padding: var(--sui-pane-gutter, 1.5rem);
		max-width: 48rem;
	}
	.row {
		display: flex;
		gap: 0.5rem;
		align-items: end;
	}
	.row :global(> :first-child) {
		flex: 1;
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
	}
</style>
