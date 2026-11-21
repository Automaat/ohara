<script lang="ts">
	import { BackLink, PageHeader } from '@smykla-skalski/sui';
	import { onDestroy } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import ScanItem from '#lib/components/ScanItem.svelte';
	import { ScanFlow, type Added } from '#lib/scan-flow.svelte.js';
	import { browserStore } from '#lib/scan.js';

	const requested = page.url.searchParams.get('kind');
	/** Opened from Okazje: the item starts as a wish and the page leads back there. */
	const fromDeals = page.url.searchParams.get('from') === 'deals';
	const back = fromDeals ? { href: '/deals', label: 'Okazje' } : { href: '/', label: 'Kolekcja' };
	const flow = new ScanFlow(fetch, browserStore());
	flow.start(requested === 'book' || requested === 'music' ? requested : 'movie');
	if (fromDeals) flow.wish(true);
	let alive = true;

	onDestroy(() => {
		alive = false;
		flow.detach();
	});

	/**
	 * Opens what went in, or Okazje for a wish added from there, or the scanner for the code a
	 * wedge scanner typed into the price.
	 */
	async function added(a: Added) {
		if (!alive) return;
		if (a.next) await goto(`/scan/${encodeURIComponent(a.next)}`);
		else await goto(fromDeals ? back.href : `/titles/${a.saved.titleID}`);
	}

	async function leave() {
		if (alive) await goto(back.href);
	}
</script>

<svelte:head>
	<title>Dodaj pozycję · Ohara</title>
</svelte:head>

<PageHeader title="Dodaj pozycję" description="Wyszukaj po tytule albo wpisz dane ręcznie.">
	{#snippet kicker()}
		<BackLink href={back.href} label={back.label} />
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
