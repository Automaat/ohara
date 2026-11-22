<script lang="ts">
	import { BackLink, EmptyState, ErrorState, PageHeader, Skeleton } from '@smykla-skalski/sui';
	import { onMount } from 'svelte';
	import { errorMessage, request } from '#lib/api.js';
	import { kindLabels, seriesSummary, type Series } from '#lib/collection.js';

	let series = $state<Series[] | null>(null);
	let failure = $state<string | null>(null);
	let busy = $state(false);

	async function load() {
		busy = true;
		try {
			series = await request<Series[]>(fetch, 'GET', '/api/series');
			failure = null;
		} catch (err) {
			failure = errorMessage(err);
		} finally {
			busy = false;
		}
	}

	onMount(() => {
		void load();
	});
</script>

<svelte:head>
	<title>Serie · Ohara</title>
</svelte:head>

<PageHeader title="Serie" description="Serie tytułów z kolekcji i tomy, których brakuje na półce.">
	{#snippet kicker()}
		<BackLink href="/" label="Kolekcja" />
	{/snippet}
</PageHeader>

<div class="content">
	{#if failure}
		<ErrorState
			title="Nie udało się wczytać serii"
			description={failure}
			onretry={() => load()}
			{busy}
			retryLabel="Spróbuj ponownie"
			retryingLabel="Wczytywanie…"
		/>
	{:else if series === null}
		<Skeleton rows={3} label="Wczytywanie serii" />
	{:else if series.length === 0}
		<EmptyState
			title="Brak serii"
			description="Seria pojawi się tutaj, gdy wpiszesz ją w edycji tytułu."
			step="Otwórz tytuł w kolekcji, wybierz „Edytuj” i wpisz serię oraz tom."
		/>
	{:else}
		<ul class="series" aria-label="Serie">
			{#each series as s (s.id)}
				<li>
					<a href={`/series/${s.id}`}>
						<span class="name">{s.name}</span>
						<span class="meta">{kindLabels[s.kind]} · {seriesSummary(s)}</span>
					</a>
				</li>
			{/each}
		</ul>
	{/if}
</div>

<style>
	.content {
		padding: var(--sui-pane-gutter, 1.5rem);
		font-family: var(--sui-sans);
		color: var(--sui-text-primary);
		max-width: 48rem;
	}
	.series {
		margin: 0;
		padding: 0;
		overflow: hidden;
		border: 1px solid var(--sui-border-subtle);
		border-radius: 1.375rem;
		background: var(--sui-surface-base);
		list-style: none;
	}
	.series li + li {
		border-top: 1px solid var(--sui-border-subtle);
	}
	.series a {
		display: grid;
		gap: 0.125rem;
		min-block-size: 2.75rem;
		padding: 0.75rem 1rem;
		color: inherit;
		text-decoration: none;
	}
	.series a:hover {
		background: var(--sui-row-hover);
	}
	.series a:focus-visible {
		outline: 2px solid var(--sui-focus);
		outline-offset: -2px;
	}
	.name {
		font-weight: 600;
		overflow-wrap: anywhere;
	}
	.meta {
		color: var(--sui-text-secondary);
		font-size: 0.875rem;
	}
</style>
