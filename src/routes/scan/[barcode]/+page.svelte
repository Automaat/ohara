<script lang="ts">
	import { BackLink, PageHeader } from '@smykla-skalski/sui';
	import { onDestroy, untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import ScanItem from '#lib/components/ScanItem.svelte';
	import { ScanFlow, type Added } from '#lib/scan-flow.svelte.js';
	import { browserStore, cleanBarcode } from '#lib/scan.js';

	const flow = new ScanFlow(fetch, browserStore());
	let alive = true;

	const barcode = $derived(cleanBarcode(page.params.barcode ?? ''));
	const manual = $derived(page.url.searchParams.get('manual') === '1');
	const title = $derived(pageTitle());

	function pageTitle(): string {
		const p = flow.phase;
		switch (p.kind) {
			case 'loading':
				return 'Sprawdzam kod…';
			case 'queued':
				return 'Nie znaleziono';
			case 'invalid':
				return 'Nieprawidłowy kod';
			case 'failed':
				return 'Nie udało się sprawdzić kodu';
			case 'offline':
				return 'Zapisano bez połączenia';
			case 'expired':
				return 'Sesja wygasła';
		}
		if (p.status === 'owned') return 'Masz już to wydanie';
		return p.status === 'missing' ? 'Dodaj ręcznie' : 'Znaleziono';
	}

	$effect(() => {
		const code = barcode;
		const byHand = manual;
		untrack(() => void flow.load(code, byHand));
	});

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
