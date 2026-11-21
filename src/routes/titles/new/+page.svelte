<script lang="ts">
	import { BackLink, PageHeader } from '@smykla-skalski/sui';
	import { onDestroy } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import ScanItem from '#lib/components/ScanItem.svelte';
	import { ScanFlow, type Added } from '#lib/scan-flow.svelte.js';
	import { browserStore } from '#lib/scan.js';

	const requested = page.url.searchParams.get('kind');
	const flow = new ScanFlow(fetch, browserStore());
	flow.start(requested === 'book' || requested === 'music' ? requested : 'movie');
	let alive = true;

	onDestroy(() => {
		alive = false;
		flow.detach();
	});

	/** Opens what went in, or the scanner for the code a wedge scanner typed into the price. */
	async function added(a: Added) {
		if (!alive) return;
		await goto(a.next ? `/scan/${encodeURIComponent(a.next)}` : `/titles/${a.saved.titleID}`);
	}

	async function leave() {
		if (alive) await goto('/');
	}
</script>

<svelte:head>
	<title>Dodaj pozycję · Ohara</title>
</svelte:head>

<PageHeader title="Dodaj pozycję" description="Wyszukaj po tytule albo wpisz dane ręcznie.">
	{#snippet kicker()}
		<BackLink href="/" label="Kolekcja" />
	{/snippet}
</PageHeader>

<div class="content">
	<ScanItem {flow} onadded={added} onclose={leave} onmanual={() => flow.manual()} />
</div>

<style>
	.content {
		padding: var(--sui-pane-gutter, 1.5rem);
		max-width: 40rem;
	}
</style>
