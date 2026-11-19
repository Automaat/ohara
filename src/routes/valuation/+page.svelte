<script lang="ts">
	import {
		Button,
		Callout,
		DataTable,
		Disclosure,
		EmptyState,
		ErrorState,
		FormError,
		Link,
		PageHeader,
		Pill,
		type DataTableColumn
	} from '@smykla-skalski/sui';
	import { onMount } from 'svelte';
	import { ApiError, errorMessage, request } from '#lib/api.js';
	import { formatLabels, formatPLN, kindLabels } from '#lib/collection.js';
	import ValuationHero from '#lib/components/ValuationHero.svelte';
	import ValuationHistory from '#lib/components/ValuationHistory.svelte';
	import ValuationRows from '#lib/components/ValuationRows.svelte';
	import {
		boundHints,
		boundKeys,
		boundLabels,
		byValue,
		estimateSource,
		estimateValue,
		intervalLabel,
		lastRunSummary,
		offers,
		pageSummary,
		recentHistory,
		type CopyValue,
		type RunnerStatus,
		type ValuationView
	} from '#lib/valuation.js';

	type Row = CopyValue & Record<string, unknown>;

	let view = $state<ValuationView | null>(null);
	let failure = $state<string | null>(null);
	let loading = $state(true);
	let runError = $state<string | null>(null);
	let starting = $state(false);

	let poll: ReturnType<typeof setTimeout> | undefined;

	async function load() {
		loading = true;
		try {
			view = await request<ValuationView>(fetch, 'GET', '/api/valuation');
			failure = null;
		} catch (err) {
			failure = errorMessage(err);
		} finally {
			loading = false;
		}
		clearTimeout(poll);
		if (view?.status.running) poll = setTimeout(load, 5000);
	}

	onMount(() => {
		void load();
		return () => clearTimeout(poll);
	});

	async function runNow() {
		starting = true;
		runError = null;
		try {
			await request<RunnerStatus>(fetch, 'POST', '/api/valuation/run');
		} catch (err) {
			runError =
				err instanceof ApiError && err.status === 409 ? 'Wycena już trwa.' : errorMessage(err);
		} finally {
			starting = false;
		}
		await load();
	}

	const columns: DataTableColumn<Row>[] = [
		{ key: 'title_name', label: 'Pozycja' },
		{ key: 'price_cents', label: 'Zapłacono', numeric: true },
		{ key: 'floor', label: boundLabels.floor, numeric: true },
		{ key: 'mid', label: boundLabels.mid, numeric: true },
		{ key: 'ceiling', label: boundLabels.ceiling, numeric: true },
		{ key: 'low_confidence', label: 'Pewność' }
	];
	const copies = $derived(byValue(view?.copies ?? []));
	const rows = $derived(copies as Row[]);
	const status = $derived(view?.status);
	const valued = $derived(!!view && view.history.length > 0);
	const recent = $derived(recentHistory(view?.history ?? []));
	const description = $derived(view && valued ? pageSummary(view, new Date()) : undefined);
	const runSummary = $derived(lastRunSummary(status?.last_result ?? null));
	const sourceErrors = $derived(status?.last_result?.errors ?? []);
	const busy = $derived(starting || !!status?.running);
</script>

<svelte:head>
	<title>Wartość · Ohara</title>
</svelte:head>

{#snippet runButton()}
	<Button tone="signal" loading={busy} onclick={runNow} disabled={!status}>
		{status?.running ? 'Wyceniam…' : 'Wyceń teraz'}
	</Button>
{/snippet}

{#snippet runStatus()}
	{#if status}
		<p class="status metadata" role="status">
			{#if status.scheduled}
				Automatyczna wycena {intervalLabel(status)}.
			{:else}
				Automatyczna wycena wyłączona.
			{/if}
			{#if runSummary}
				{runSummary}.
			{/if}
		</p>
		{#if sourceErrors.length > 0}
			<Callout tone="warning" title="Niektóre źródła nie odpowiedziały">
				<ul>
					{#each sourceErrors as e, i (i)}
						<li>{e}</li>
					{/each}
				</ul>
			</Callout>
		{/if}
	{/if}
{/snippet}

<PageHeader title="Wartość" {description} actions={valued ? runButton : undefined} />

<div class="content">
	<FormError message={runError} />
	{#if !view}
		{#if failure}
			<ErrorState
				title="Nie udało się wczytać wyceny"
				description={failure}
				onretry={load}
				busy={loading}
				retryLabel="Spróbuj ponownie"
				retryingLabel="Ponawianie…"
			/>
		{:else}
			<p role="status">Wczytywanie wyceny…</p>
		{/if}
	{:else if !valued}
		<EmptyState
			title="Kolekcja nie ma jeszcze wyceny"
			description="Wycena sprawdza ceny posiadanych egzemplarzy w skupie, w ogłoszeniach z OLX i Vinted oraz w sklepach z Ceneo."
			action={runButton}
		/>
		{#if status?.last_result}
			{@render runStatus()}
		{/if}
	{:else}
		{#if failure}
			<ErrorState
				title="Nie udało się odświeżyć wyceny"
				description={failure}
				onretry={load}
				busy={loading}
				overContent
				retryLabel="Spróbuj ponownie"
				retryingLabel="Ponawianie…"
			/>
		{/if}
		<ValuationHero summary={view.summary} history={recent} />
		<ValuationHistory history={recent} />

		{#if copies.length === 0}
			<p class="status">Brak posiadanych egzemplarzy.</p>
		{:else}
			<div class="mobile-results">
				<ValuationRows {copies} />
			</div>
			<DataTable
				class="desktop-results"
				aria-label="Wartość egzemplarzy"
				{columns}
				{rows}
				rowKey={(r) => r.copy_id}
				refreshing={loading}
				refreshingLabel="Odświeżanie…"
				scrollHint="Przewiń tabelę w bok"
			>
				{#snippet cell(row, column)}
					{#if column.key === 'title_name'}
						<Link href={`/copies/${row.copy_id}`}>{row.title_name}</Link>
						<span class="meta">{row.format ? formatLabels[row.format] : kindLabels[row.kind]}</span>
					{:else if column.key === 'price_cents'}
						<span class="price">{row.price_cents == null ? '—' : formatPLN(row.price_cents)}</span>
					{:else if column.key === 'floor' || column.key === 'mid' || column.key === 'ceiling'}
						{estimateValue(row[column.key])}
						<span class="meta">{estimateSource(column.key, row[column.key])}</span>
					{:else if column.key === 'low_confidence'}
						<Pill tone={row.low_confidence ? 'warning' : 'success'}>
							{row.low_confidence ? 'Niska' : 'Dobra'}
						</Pill>
					{/if}
				{/snippet}
			</DataTable>
		{/if}

		{@render runStatus()}

		<Disclosure summary="Jak liczymy wartość?">
			<ul class="hints">
				{#each boundKeys as k (k)}
					<li><strong>{boundLabels[k]}:</strong> {boundHints[k]}</li>
				{/each}
			</ul>
			<p class="methodology">
				Dla wydań powiązanych z Discogs używamy najtańszej oferty oraz sugerowanych cen dla stanów
				VG+ i Mint.
			</p>
			<p class="methodology">
				Ceny z OLX i Vinted pochodzą z ogłoszeń. Nie znamy cen, po których zawarto transakcje.
				Uwzględniamy oferty z ostatnich {view.window_days} dni. Wycena używanych egzemplarzy na podstawie
				mniej niż {offers(view.min_observations)} ma niską pewność i jest oznaczona jako „mało danych”.
			</p>
		</Disclosure>
	{/if}
</div>

<style>
	.content {
		display: grid;
		gap: 0.75rem;
		padding: var(--sui-pane-gutter, 1.5rem);
	}
	.methodology {
		font-family: 'SUI Plus Jakarta Sans', system-ui, sans-serif;
		font-size: var(--sui-font-size-body, 0.875rem);
	}
	.status {
		margin: 0;
	}
	.hints {
		margin: 0;
		padding-inline-start: 1.25rem;
		color: var(--sui-text-muted, inherit);
	}
	.meta {
		display: block;
		font-size: 0.8125rem;
		color: var(--sui-text-muted, inherit);
	}
	.mobile-results {
		display: none;
	}
	@media (max-width: 640px) {
		.content :global(.desktop-results) {
			display: none;
		}
		.mobile-results {
			display: block;
		}
	}
</style>
