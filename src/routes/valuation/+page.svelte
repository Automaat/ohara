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
		RelativeTime,
		type DataTableColumn
	} from '@smykla-skalski/sui';
	import { onMount } from 'svelte';
	import { ApiError, errorMessage, request } from '#lib/api.js';
	import { formatLabels, formatPLN, kindLabels, seasonName } from '#lib/collection.js';
	import DiscogsCredit from '#lib/components/DiscogsCredit.svelte';
	import ValuationHero from '#lib/components/ValuationHero.svelte';
	import ValuationHistory from '#lib/components/ValuationHistory.svelte';
	import ValuationMovers from '#lib/components/ValuationMovers.svelte';
	import ValuationRows from '#lib/components/ValuationRows.svelte';
	import {
		boundHints,
		boundKeys,
		boundLabels,
		byValue,
		copyGain,
		discogsRelease,
		estimateSource,
		estimateValue,
		intervalLabel,
		lastRunSummary,
		likelySold,
		likelySoldLabel,
		movers,
		offers,
		pageSummary,
		realizedLine,
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
			const started = await request<RunnerStatus>(fetch, 'POST', '/api/valuation/run');
			// The accepted run's status stands until a refresh succeeds, so a failed refresh
			// keeps polling for its result.
			if (view) view = { ...view, status: started };
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
		{ key: 'likely_sold', label: likelySoldLabel, numeric: true },
		{ key: 'gain', label: 'Względem zakupu', numeric: true },
		{ key: 'low_confidence', label: 'Pewność' }
	];
	const copies = $derived(byValue(view?.copies ?? []));
	const moved = $derived(movers(view?.copies ?? [], view?.moves ?? []));
	const rows = $derived(copies as Row[]);
	const status = $derived(view?.status);
	const valued = $derived(!!view?.last_valuation);
	const recent = $derived(view ? recentHistory(view.history, new Date()) : []);
	const description = $derived(view && valued ? pageSummary(view, new Date()) : undefined);
	const runSummary = $derived(lastRunSummary(status?.last_result ?? null));
	const sourceErrors = $derived(status?.last_result?.errors ?? []);
	const busy = $derived(starting || !!status?.running);
	// A sale's gain needs no market prices, so it shows before the first valuation too; after
	// one, the hero shows it.
	const realized = $derived(view ? realizedLine(view.summary) : null);
	const minObservations = $derived(view?.min_observations ?? 0);
	const fewOffers = $derived(offers(minObservations));
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
			{#if status.last_finished_at}
				Ostatni przebieg: <RelativeTime
					value={status.last_finished_at}
					locale="pl"
					exactLabel={(time) => `dokładnie ${time}`}
				/>.
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
	{:else if failure}
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
	{#if view && !valued}
		<EmptyState
			title="Kolekcja nie ma jeszcze wyceny"
			description="Wycena sprawdza ceny posiadanych egzemplarzy w skupie, w ogłoszeniach z OLX oraz na Discogs."
			action={runButton}
		/>
		{#if realized}
			<p class="status">{realized}</p>
		{/if}
		{#if status?.last_result}
			{@render runStatus()}
		{/if}
	{:else if view}
		<ValuationHero summary={view.summary} history={recent} />
		<ValuationHistory history={recent} lastValuation={view.last_valuation} />
		{#if view.moves_from && (moved.rising.length > 0 || moved.falling.length > 0)}
			<ValuationMovers rising={moved.rising} falling={moved.falling} from={view.moves_from} />
		{/if}

		{#if copies.length === 0}
			<p class="status">Brak posiadanych egzemplarzy.</p>
		{:else}
			<div class="mobile-results">
				<ValuationRows {copies} {minObservations} />
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
						<Link href={`/copies/${row.copy_id}`}>{seasonName(row.title_name, row.season)}</Link>
						<span class="meta">{row.format ? formatLabels[row.format] : kindLabels[row.kind]}</span>
						<DiscogsCredit release={discogsRelease(row)} />
					{:else if column.key === 'price_cents'}
						<span class="price">{row.price_cents == null ? '—' : formatPLN(row.price_cents)}</span>
					{:else if column.key === 'floor' || column.key === 'mid' || column.key === 'ceiling'}
						{estimateValue(row[column.key])}
						<span class="meta">{estimateSource(column.key, row[column.key], row.mid_grade)}</span>
					{:else if column.key === 'likely_sold'}
						{@const sold = likelySold(row, minObservations)}
						{#if sold}
							{estimateValue(row.likely_sold)}
							<span class="meta">{sold.note}</span>
						{:else}
							—
						{/if}
					{:else if column.key === 'gain'}
						{copyGain(row)}
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
				Dla wydań powiązanych z Discogs używamy najtańszej oferty, sugerowanej ceny dla stanu
				egzemplarza (Idealny: NM, Bardzo dobry: VG+, Dobry: VG, Dostateczny: G, Słaby: P; bez stanu
				lub bez sugestii dla niego: VG+) oraz sugerowanej ceny dla stanu Mint.
			</p>
			<p class="methodology">
				Ceny z OLX pochodzą z ogłoszeń. Nie znamy cen, po których zawarto transakcje. Uwzględniamy
				oferty z ostatnich {view.window_days} dni. Wycena używanych egzemplarzy na podstawie mniej niż
				{offers(view.min_observations)} ma niską pewność i jest oznaczona jako „mało danych”.
			</p>
			<p class="methodology">
				<strong>{likelySoldLabel}:</strong> mediana ostatnich cen używanych egzemplarzy tego samego
				tytułu i formatu z ogłoszeń OLX, które zniknęły w ciągu {view.likely_sold_days} dni od pierwszego
				wykrycia przez Oharę, bez wartości odstających. To nie są znane ceny sprzedaży: zakładamy tylko,
				że tak szybko zdjęte ogłoszenie zakończyło się sprzedażą. Cenę opartą na mniej niż {fewOffers}
				oznaczamy jako „mało danych”, a bez takich ogłoszeń jej nie pokazujemy. Nie wliczamy jej do sum
				ani do historii wartości.
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
