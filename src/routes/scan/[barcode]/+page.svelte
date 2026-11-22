<script lang="ts">
	import { BackLink, PageHeader } from '@smykla-skalski/sui';
	import { onDestroy, untrack } from 'svelte';
	import { goto, replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import ScanItem from '#lib/components/ScanItem.svelte';
	import { ScanFlow, phaseTitle, type Added } from '#lib/scan-flow.svelte.js';
	import { browserStore, cleanBarcode } from '#lib/scan.js';

	const flow = new ScanFlow(fetch, browserStore());
	let alive = true;

	const barcode = $derived(cleanBarcode(page.params.barcode ?? ''));
	const manual = $derived(page.url.searchParams.get('manual') === '1');
	const title = $derived(phaseTitle(flow.phase));

	$effect(() => {
		const code = barcode;
		const byHand = manual;
		untrack(() => void flow.load(code, byHand, takeRecheck()));
	});

	/**
	 * Whether the queue's "Szukaj ponownie" asked to ask the sources again. The request leaves the
	 * address, so a reload looks the code up from the cache.
	 */
	function takeRecheck(): boolean {
		if (page.url.searchParams.get('recheck') !== '1') return false;
		const url = new URL(page.url.href);
		url.searchParams.delete('recheck');
		replaceState(url, page.state);
		return true;
	}

	onDestroy(() => {
		alive = false;
	});

	async function added(a: Added) {
		if (!alive) return;
		if (a.next === barcode && !manual) await flow.load(a.next);
		else if (a.next) await goto(`/scan/${encodeURIComponent(a.next)}`);
		else await goto('/scan', { replace: true });
	}

	async function leave() {
		if (alive) await goto('/scan');
	}
</script>

<svelte:head>
	<title>{barcode} · Ohara</title>
</svelte:head>

<PageHeader {title}>
	{#snippet kicker()}
		<BackLink href="/scan" label="Skanuj" />
	{/snippet}
</PageHeader>

<div class="content">
	<ScanItem {flow} autofocus onadded={added} onclose={leave} onmanual={() => flow.manual()} />
</div>

<style>
	.content {
		padding: var(--sui-pane-gutter, 1.5rem);
		max-width: 40rem;
	}
</style>
