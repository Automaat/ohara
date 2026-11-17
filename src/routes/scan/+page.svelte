<script lang="ts">
	import {
		Button,
		Callout,
		Field,
		ObjectList,
		ObjectRow,
		PageHeader,
		Pill,
		Section
	} from '@smykla-skalski/sui';
	import { onMount, tick } from 'svelte';
	import { page } from '$app/state';
	import { ApiError, errorMessage, request } from '$lib/api';
	import { formatPLN, type FormIssue } from '$lib/collection';
	import BarcodeCamera from '$lib/components/BarcodeCamera.svelte';
	import ScanConfirm from '$lib/components/ScanConfirm.svelte';
	import {
		FormRejected,
		RepeatGuard,
		cleanBarcode,
		loadSticky,
		looksLikeBarcode,
		newRequestKey,
		quickForm,
		resolveScan,
		saveScan,
		saveSticky,
		wedgeBarcodeIn,
		type Candidate,
		type LookupResult,
		type QueueEntry,
		type QuickForm,
		type Resolution,
		type SaveTarget
	} from '$lib/scan';

	type Pending = {
		key: string;
		barcode: string;
		target: SaveTarget;
		candidates: Candidate[];
		picked: number;
		errors: LookupResult['errors'];
	};
	type Outcome = 'added' | 'queued' | 'skipped';
	type Recent = { id: number; barcode: string; outcome: Outcome; label: string; href?: string };
	type Notice = { tone: 'quiet' | 'warning'; text: string };

	const outcomeLabels: Record<Outcome, string> = {
		added: 'Dodano',
		queued: 'W kolejce',
		skipped: 'Pominięto'
	};
	const outcomeTones = { added: 'success', queued: 'warning', skipped: 'neutral' } as const;

	function storage(): Storage | undefined {
		try {
			return localStorage;
		} catch {
			return undefined;
		}
	}

	let code = $state('');
	let busy = $state<string | null>(null);
	let pending = $state<Pending | null>(null);
	let form = $state<QuickForm>(quickForm(null, loadSticky(undefined), ''));
	let issue = $state<FormIssue | null>(null);
	let saving = $state(false);
	let notice = $state<Notice | null>(null);
	let recent = $state<Recent[]>([]);
	let queued = $state<number | null>(null);
	let fromKeyboard = false;
	let sticky = loadSticky(storage());
	const guard = new RepeatGuard(1500);
	let nextID = 0;

	function log(barcode: string, outcome: Outcome, label: string, href?: string) {
		recent = [{ id: ++nextID, barcode, outcome, label, href }, ...recent].slice(0, 20);
	}

	async function focus(id: string) {
		await tick();
		document.getElementById(id)?.focus();
	}

	function finish(barcode: string) {
		guard.remember(barcode, performance.now());
		pending = null;
		issue = null;
		if (fromKeyboard) void focus('scan-code');
	}

	async function countQueue() {
		try {
			queued = (await request<QueueEntry[]>(fetch, 'GET', '/api/scan-queue')).length;
		} catch {
			queued = null;
		}
	}

	function open(r: Exclude<Resolution, { kind: 'queued' }>) {
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
		} else {
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
		}
		issue = null;
		void focus('scan-price');
	}

	function openManual(barcode: string) {
		pending = {
			key: newRequestKey(),
			barcode,
			target: { kind: 'new', barcode, candidate: null },
			candidates: [],
			picked: 0,
			errors: []
		};
		form = quickForm(null, sticky, barcode);
		issue = null;
		void focus('scan-price');
	}

	function pick(index: number) {
		if (!pending || pending.target.kind !== 'new') return;
		const candidate = pending.candidates[index];
		pending.picked = index;
		pending.target = { kind: 'new', barcode: pending.barcode, candidate };
		form = {
			...quickForm(candidate, sticky, pending.barcode),
			price: form.price,
			shop: form.shop,
			acquired_as: form.acquired_as
		};
	}

	async function handle(raw: string) {
		const barcode = cleanBarcode(raw);
		if (!barcode || busy || pending) return;
		if (!looksLikeBarcode(barcode)) {
			notice = { tone: 'warning', text: `„${raw.trim()}” nie wygląda na kod kreskowy.` };
			return;
		}
		busy = barcode;
		notice = null;
		code = '';
		try {
			const r = await resolveScan(fetch, barcode);
			if (r.kind === 'queued') {
				log(r.barcode, 'queued', 'Brak danych, do uzupełnienia', '/scan/queue');
				notice = {
					tone: 'warning',
					text: `Nie znaleziono ${r.barcode}. Kod czeka w kolejce do uzupełnienia.`
				};
				void countQueue();
				finish(r.barcode);
			} else {
				open(r);
			}
		} catch (err) {
			notice = { tone: 'warning', text: errorMessage(err) };
			finish(barcode);
		} finally {
			busy = null;
		}
	}

	function fromCamera(barcode: string) {
		if (!guard.accepts(barcode, performance.now())) return;
		fromKeyboard = false;
		void handle(barcode);
	}

	function fromInput() {
		fromKeyboard = true;
		void handle(code);
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
			const price =
				saved.copy.price_cents === null ? '' : ` · ${formatPLN(saved.copy.price_cents)}`;
			log(current.barcode, 'added', saved.titleName + price, `/copies/${saved.copy.id}`);
			notice = { tone: 'quiet', text: `Dodano: ${saved.titleName}.` };
			if (current.target.kind === 'new' && queued) void countQueue();
			finish(current.barcode);
			if (next) {
				fromKeyboard = true;
				void handle(next);
			}
		} catch (err) {
			issue =
				err instanceof FormRejected
					? { field: err.field, message: err.message }
					: {
							field: err instanceof ApiError ? (err.field ?? '') : '',
							message: errorMessage(err)
						};
			if (next) {
				notice = {
					tone: 'warning',
					text: `Kod ${next} nie został sprawdzony: zeskanuj go ponownie.`
				};
			}
		} finally {
			saving = false;
		}
	}

	function skip() {
		if (!pending) return;
		log(pending.barcode, 'skipped', 'Nie dodano');
		notice = null;
		finish(pending.barcode);
	}

	onMount(() => {
		sticky = loadSticky(storage());
		void countQueue();
		const preset = page.url.searchParams.get('barcode');
		if (!preset) return;
		if (page.url.searchParams.get('manual') === '1') {
			openManual(cleanBarcode(preset));
		} else {
			fromKeyboard = true;
			void handle(preset);
		}
	});
</script>

<svelte:head>
	<title>Skanuj · Ohara</title>
</svelte:head>

<PageHeader
	title="Skanuj"
	description="Aparat albo czytnik kodów: każdy kod trafia do kolekcji albo do kolejki."
>
	{#snippet actions()}
		<Button href="/scan/queue">
			Do uzupełnienia{queued ? ` (${queued})` : ''}
		</Button>
	{/snippet}
</PageHeader>

<div class="content">
	<BarcodeCamera paused={busy !== null || pending !== null} ondetect={fromCamera} />

	<form
		class="manual"
		novalidate
		onsubmit={(e) => {
			e.preventDefault();
			fromInput();
		}}
	>
		<Field
			id="scan-code"
			label="Kod kreskowy"
			hint="Wpisz kod albo użyj czytnika Bluetooth: kursor musi być w tym polu."
			inputmode="numeric"
			enterkeyhint="search"
			autocomplete="off"
			disabled={pending !== null}
			bind:value={code}
		/>
		<Button type="submit" loading={busy !== null} disabled={pending !== null}>Szukaj</Button>
	</form>

	{#if busy}
		<Callout tone="quiet" live>Szukam {busy}…</Callout>
	{/if}

	{#if pending}
		<ScanConfirm
			barcode={pending.barcode}
			target={pending.target}
			candidates={pending.candidates}
			picked={pending.picked}
			errors={pending.errors}
			bind:form
			{issue}
			{saving}
			onpick={pick}
			onsave={save}
			onskip={skip}
		/>
	{/if}

	{#if notice}
		<Callout tone={notice.tone} live>{notice.text}</Callout>
	{/if}

	{#if recent.length > 0}
		<Section title="Ostatnie skany">
			<ObjectList>
				{#each recent as r (r.id)}
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
	.manual {
		display: flex;
		gap: 0.5rem;
		align-items: end;
	}
	.manual :global(> :first-child) {
		flex: 1;
	}
	.code {
		font-family: var(--sui-mono);
		margin-inline-end: 0.5rem;
	}
</style>
