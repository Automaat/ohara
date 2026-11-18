<script lang="ts">
	import {
		Button,
		Callout,
		DataTable,
		FactList,
		FormError,
		LineChart,
		Link,
		PageHeader,
		Pill,
		RelativeTime,
		Section,
		type DataTableColumn,
		type Fact
	} from '@smykla-skalski/sui';
	import { onMount } from 'svelte';
	import { ApiError, errorMessage, request } from '$lib/api';
	import { formatLabels, formatPLN, kindLabels } from '$lib/collection';
	import {
		boundHints,
		boundKeys,
		boundLabels,
		boundTotal,
		estimateSource,
		estimateValue,
		formatDay,
		formatZl,
		historySeries,
		intervalLabel,
		lastRunSummary,
		offers,
		type CopyValue,
		type RunnerStatus,
		type ValuationView
	} from '$lib/valuation';

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
	const rows = $derived((view?.copies ?? []) as Row[]);
	const status = $derived(view?.status);
	const summary = $derived(view?.summary);
	const runSummary = $derived(lastRunSummary(status?.last_result ?? null));
	const sourceErrors = $derived(status?.last_result?.errors ?? []);
	const series = $derived(historySeries(view?.history ?? []));
	const facts = $derived<Fact[]>(
		summary
			? [
					{ label: 'Egzemplarzy', value: String(summary.copies) },
					{ label: 'Wydano', value: formatPLN(summary.spent_cents) },
					...boundKeys.map((k) => ({
						label: boundLabels[k],
						value: boundTotal(summary[k], summary.copies)
					})),
					{ label: 'Niska pewność', value: String(summary.low_confidence) }
				]
			: []
	);
</script>

<svelte:head>
	<title>Wartość · Ohara</title>
</svelte:head>

<PageHeader
	title="Wartość kolekcji"
	description="Przedział wartości każdego egzemplarza: skup, ceny używanych na OLX i Vinted oraz ceny nowych z Ceneo."
>
	{#snippet actions()}
		<Button
			tone="signal"
			loading={starting || !!status?.running}
			onclick={runNow}
			disabled={!status}>{status?.running ? 'Wyceniam…' : 'Wyceń teraz'}</Button
		>
	{/snippet}
</PageHeader>

<div class="content">
	<FormError message={runError} />
	{#if status}
		<p class="status metadata" role="status">
			{#if status.scheduled}
				Automatyczna wycena {intervalLabel(status)}.
			{:else}
				Automatyczna wycena wyłączona.
			{/if}
			{#if status.last_finished_at}
				Ostatnio: <RelativeTime value={status.last_finished_at} locale="pl" />.
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

	<Callout tone="quiet" title="Ceny ofertowe, nie transakcyjne">
		Ceny z OLX i Vinted to kwoty, za które sprzedający wystawiają egzemplarze, a nie ceny, za które
		je sprzedali. Liczą się oferty widziane w ostatnich {view?.window_days ?? 90} dniach; wycena z mniej
		niż {offers(view?.min_observations ?? 3)} ma niską pewność.
	</Callout>

	{#if summary}
		<FactList {facts} />
		<ul class="hints">
			{#each boundKeys as k (k)}
				<li><strong>{boundLabels[k]}:</strong> {boundHints[k]}</li>
			{/each}
		</ul>
	{/if}

	<Section title="Historia">
		<LineChart
			title="Wartość kolekcji w czasie"
			description="Suma przedziałów wartości z każdej wyceny na tle tego, ile wydano na kolekcję."
			level={3}
			{series}
			status={loading && !view ? 'loading' : failure && !view ? 'failed' : undefined}
			error={failure ?? undefined}
			onretry={load}
			xLabel="Dzień"
			yLabel="Wartość"
			seriesHeading="Seria"
			formatX={formatDay}
			formatY={formatZl}
			dataLabel="Pokaż dane"
			emptyTitle="Brak historii"
			emptyDescription="Pierwszy punkt pojawi się po pierwszej wycenie."
			emptyStep="Uruchom wycenę przyciskiem „Wyceń teraz”."
			loadingLabel="Wczytywanie historii…"
			refreshingLabel="Odświeżanie…"
			failedTitle="Nie udało się wczytać historii"
		/>
	</Section>

	<Section title="Egzemplarze">
		<DataTable
			aria-label="Wartość egzemplarzy"
			{columns}
			{rows}
			rowKey={(r) => r.copy_id}
			loading={loading && !view}
			refreshing={loading && !!view}
			error={failure}
			onretry={load}
			retryLabel="Spróbuj ponownie"
			loadingLabel="Wczytywanie wyceny…"
			refreshingLabel="Odświeżanie…"
			emptyLabel="Brak posiadanych egzemplarzy."
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
					<Pill tone={row.low_confidence ? 'warning' : 'success'}
						>{row.low_confidence ? 'Niska' : 'Dobra'}</Pill
					>
				{/if}
			{/snippet}
		</DataTable>
	</Section>
</div>

<style>
	.content {
		display: grid;
		gap: 1.5rem;
		padding: var(--sui-pane-gutter, 1.5rem);
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
</style>
