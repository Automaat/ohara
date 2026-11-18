<script lang="ts">
	import { Button, Callout, FactList, PageHeader, Pill, Section } from '@smykla-skalski/sui';
	import { onMount } from 'svelte';
	import { errorMessage } from '$lib/api';
	import { databaseBadge, fetchStatus, type Status } from '$lib/status';

	let info = $state<Status | null>(null);
	let failure = $state<string | null>(null);
	let loading = $state(true);
	const database = $derived(info ? databaseBadge(info.database) : null);

	async function load() {
		loading = true;
		failure = null;
		try {
			info = await fetchStatus(fetch);
		} catch (err) {
			failure = errorMessage(err);
		} finally {
			loading = false;
		}
	}

	onMount(load);
</script>

<svelte:head>
	<title>Informacje o aplikacji · Ohara</title>
</svelte:head>

<PageHeader title="Informacje o aplikacji" />

<div class="content">
	<Section title="Stan serwera">
		{#if loading}
			<p class="metadata" role="status">Sprawdzanie stanu serwera…</p>
		{:else if failure}
			<Callout tone="warning" title="Nie udało się sprawdzić stanu serwera">
				<p>{failure}</p>
				<Button onclick={load}>Spróbuj ponownie</Button>
			</Callout>
		{:else if info && database}
			<FactList layout="inline" missingLabel="—" facts={[{ label: 'Wersja', value: info.version }]}
			></FactList>
			<p>Baza danych <Pill tone={database.tone} dot>{database.label}</Pill></p>
		{/if}
	</Section>

	<Section title="Konfiguracja serwera">
		<dl>
			<dt>Harmonogram wyszukiwania okazji</dt>
			<dd><code>OHARA_DEALS_INTERVAL</code></dd>
			<dt>Harmonogram wyceny kolekcji</dt>
			<dd><code>OHARA_VALUATION_INTERVAL</code></dd>
		</dl>
	</Section>
</div>

<style>
	.content {
		display: grid;
		gap: var(--sui-space-5);
		padding: var(--sui-pane-gutter, 1.5rem);
	}
	dt {
		font-weight: var(--sui-weight-semibold);
	}
	dd {
		margin: 0 0 var(--sui-space-3);
		overflow-wrap: anywhere;
	}
</style>
