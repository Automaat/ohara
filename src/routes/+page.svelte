<script lang="ts">
	import { EmptyState, ErrorState, FactList, PageHeader, Pill } from '@smykla-skalski/sui';
	import { onMount } from 'svelte';
	import { databaseBadge, fetchStatus, StatusError, type Status } from '$lib/status';

	let info = $state<Status | null>(null);
	let failure = $state<string | null>(null);
	let busy = $state(false);

	async function load() {
		busy = true;
		try {
			info = await fetchStatus(fetch);
			failure = null;
		} catch (err) {
			failure = err instanceof StatusError ? err.message : 'Nieznany błąd.';
		} finally {
			busy = false;
		}
	}

	onMount(load);

	const badge = $derived(info ? databaseBadge(info.database) : null);
</script>

<svelte:head>
	<title>Kolekcja · Ohara</title>
</svelte:head>

<PageHeader
	title="Kolekcja"
	description="Filmy i książki na półce: co masz, ile zapłaciłeś i ile są warte."
>
	{#snippet status()}
		{#if badge}
			<Pill tone={badge.tone} dot>Baza: {badge.label}</Pill>
		{:else}
			<Pill tone="neutral">Sprawdzanie…</Pill>
		{/if}
	{/snippet}
	{#snippet facts()}
		<FactList
			layout="inline"
			missingLabel="—"
			facts={[{ label: 'Wersja', value: info?.version, mono: true }]}
		/>
	{/snippet}
</PageHeader>

<div class="content">
	{#if failure}
		<ErrorState
			title="Nie udało się sprawdzić serwera"
			description={failure}
			onretry={load}
			{busy}
			retryLabel="Spróbuj ponownie"
			retryingLabel="Sprawdzanie…"
		/>
	{:else}
		<EmptyState
			title="Kolekcja jest pusta"
			description="Nie dodano jeszcze żadnej pozycji."
			step="Dodawanie i skanowanie kodów kreskowych pojawi się w kolejnych wersjach."
		/>
	{/if}
</div>

<style>
	.content {
		padding: var(--sui-pane-gutter, 1.5rem);
	}
</style>
