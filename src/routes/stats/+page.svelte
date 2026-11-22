<script lang="ts">
	import { Button, EmptyState, ErrorState, PageHeader } from '@smykla-skalski/sui';
	import { onMount } from 'svelte';
	import { errorMessage, request } from '#lib/api.js';
	import type { CollectionView } from '#lib/collection.js';
	import StatsAcquired from '#lib/components/StatsAcquired.svelte';
	import StatsGrowth from '#lib/components/StatsGrowth.svelte';
	import StatsSpending from '#lib/components/StatsSpending.svelte';
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

	let items = $state<StatsItem[] | null>(null);
	let failure = $state<string | null>(null);
	let loading = $state(true);
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

	onMount(() => {
		void load();
		return month.start();
	});

	const owned = $derived(items ? ownedCopies(items) : []);
	const spending = $derived(monthlySpending(owned, month.now));
	const growth = $derived(collectionGrowth(owned, month.now));
	const later = $derived(datedLater(owned, month.now));
	const shares = $derived(acquiredShare(owned));
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
		</div>
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
