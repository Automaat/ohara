<script lang="ts">
	import { Button, EmptyState, ErrorState, PageHeader } from '@smykla-skalski/sui';
	import { onMount } from 'svelte';
	import { errorMessage, request } from '#lib/api.js';
	import type { CollectionView } from '#lib/collection.js';
	import StatsAcquired from '#lib/components/StatsAcquired.svelte';
	import StatsGrowth from '#lib/components/StatsGrowth.svelte';
	import StatsCard from '#lib/components/StatsCard.svelte';
	import StatsSpending from '#lib/components/StatsSpending.svelte';
	import StatsValue from '#lib/components/StatsValue.svelte';
	import { copyCount } from '#lib/polish.js';
	import { StatsMonth } from '#lib/stats-month.svelte.js';
	import {
		acquiredShare,
		collectionGrowth,
		datedLater,
		monthlySpending,
		ownedCopies,
		type StatsItem
	} from '#lib/stats.js';
	import { valueByFormat, valueByKind } from '#lib/stats-value.js';
	import type { ValuationView } from '#lib/valuation.js';

	let items = $state<StatsItem[] | null>(null);
	let failure = $state<string | null>(null);
	let loading = $state(true);
	let valuation = $state<ValuationView | null>(null);
	let valuationFailure = $state<string | null>(null);
	let valuationLoading = $state(true);
	const month = new StatsMonth();

	async function load() {
		loading = true;
		try {
			const view = await request<CollectionView>(fetch, 'GET', '/api/collection');
			items = view.items;
			failure = null;
		} catch (err) {
			failure = errorMessage(err);
		} finally {
			loading = false;
		}
	}

	async function loadValuation() {
		valuationLoading = true;
		try {
			valuation = await request<ValuationView>(fetch, 'GET', '/api/valuation');
			valuationFailure = null;
		} catch (err) {
			valuationFailure = errorMessage(err);
		} finally {
			valuationLoading = false;
		}
	}

	onMount(() => {
		void load();
		void loadValuation();
		return month.start();
	});

	const owned = $derived(items ? ownedCopies(items) : []);
	const spending = $derived(monthlySpending(owned, month.now));
	const growth = $derived(collectionGrowth(owned, month.now));
	const later = $derived(datedLater(owned, month.now));
	const shares = $derived(acquiredShare(owned));
	const byKind = $derived(valuation ? valueByKind(valuation.copies) : []);
	const byFormat = $derived(valuation ? valueByFormat(valuation.copies) : []);
	const description = $derived(
		items && owned.length > 0 ? `${copyCount(owned.length)} na półce` : undefined
	);
</script>

<svelte:head>
	<title>Statystyki · Ohara</title>
</svelte:head>

<PageHeader title="Statystyki" {description} />

<div class="content">
	{#if failure}
		<ErrorState
			title="Nie udało się wczytać kolekcji"
			description={failure}
			onretry={load}
			busy={loading}
			overContent={!!items}
			retryLabel="Spróbuj ponownie"
			retryingLabel="Ponawianie…"
		/>
	{/if}
	{#if !items}
		{#if !failure}
			<p role="status">Wczytywanie statystyk…</p>
		{/if}
	{:else if owned.length === 0}
		<EmptyState
			title="Brak egzemplarzy na półce"
			description="Statystyki pokażą wydatki i przyrost kolekcji, gdy dodasz pierwszy posiadany egzemplarz."
		>
			{#snippet action()}
				<Button tone="signal" href="/scan">Skanuj</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="cards">
			<StatsSpending {spending} />
			<StatsGrowth {growth} {later} />
			<StatsAcquired {shares} />
			{#if valuation?.last_valuation}
				<StatsValue
					id="stats-value-kind-title"
					title="Wartość według rodzaju"
					groups={byKind}
					day={valuation.last_valuation}
					minObservations={valuation.min_observations}
				/>
				<StatsValue
					id="stats-value-format-title"
					title="Wartość według nośnika"
					groups={byFormat}
					day={valuation.last_valuation}
					minObservations={valuation.min_observations}
				/>
			{:else if valuation}
				<StatsCard id="stats-value-title" title="Wartość kolekcji">
					<p class="note">
						Nie ma jeszcze wyceny. Wartość według rodzaju i nośnika pojawi się po pierwszej wycenie.
					</p>
					<div>
						<Button href="/valuation">Wartość</Button>
					</div>
				</StatsCard>
			{/if}
		</div>
		{#if valuationFailure}
			<ErrorState
				title="Nie udało się wczytać wyceny"
				description={valuationFailure}
				onretry={loadValuation}
				busy={valuationLoading}
				overContent={!!valuation}
				retryLabel="Spróbuj ponownie"
				retryingLabel="Ponawianie…"
			/>
		{/if}
	{/if}
</div>

<style>
	.content {
		display: grid;
		gap: 0.75rem;
		min-inline-size: 0;
		padding: var(--sui-pane-gutter, 1.5rem);
	}

	.cards {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(min(100%, 22rem), 1fr));
		align-items: start;
		gap: 0.75rem;
	}
</style>
