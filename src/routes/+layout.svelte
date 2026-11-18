<script lang="ts">
	import '@smykla-skalski/sui/styles.css';
	import {
		AppShell,
		ServiceWorkerStatus,
		SideNav,
		ThemeSwitch,
		registerServiceWorker
	} from '@smykla-skalski/sui';
	import type { Snippet } from 'svelte';
	import { dev } from '$app/environment';
	import { page } from '$app/state';
	import { currentItem, sections } from '$lib/nav';

	let { children }: { children: Snippet } = $props();

	const current = $derived(currentItem(page.url.pathname, sections));
	const worker = registerServiceWorker('/service-worker.js', { dev });
</script>

<AppShell title="Ohara" label="Nawigacja" menuLabel="Menu">
	{#snippet sidebar()}
		<SideNav
			title="Ohara"
			{sections}
			current={current?.href}
			label="Strony Ohary"
			closeLabel="Zamknij"
			collapseLabel="Zwiń"
			expandLabel="Rozwiń"
		>
			{#snippet foot()}
				<ThemeSwitch label="Motyw" lightLabel="Jasny" darkLabel="Ciemny" systemLabel="Systemowy" />
			{/snippet}
		</SideNav>
	{/snippet}
	{@render children()}
</AppShell>
<ServiceWorkerStatus
	{worker}
	readyTitle="Nowa wersja jest gotowa"
	readyDescription="Przeładuj, aby jej użyć."
	reloadLabel="Przeładuj"
	reloadingLabel="Przeładowuję…"
	laterLabel="Później"
	offlineTitle="Brak połączenia"
	offlineDescription="Skanowanie wymaga połączenia z serwerem."
	onlineTitle="Połączenie wróciło"
	dismissLabel="Zamknij"
/>

<style>
	:global(:root) {
		--sui-primitive-light-canvas: #f8f5ee;
		--sui-primitive-light-inset: #eeeae1;
	}

	:global(body) {
		background: var(--sui-canvas);
		color: var(--sui-text-primary);
		font: var(--sui-font-size-body) / var(--sui-leading-body) var(--sui-sans);
	}

	:global(button),
	:global(input),
	:global(select),
	:global(textarea) {
		font-family: inherit;
	}

	:global(.metadata) {
		color: var(--sui-text-secondary);
		font-size: var(--sui-font-size-meta);
		line-height: var(--sui-leading-meta);
	}

	:global(.price) {
		font-weight: var(--sui-weight-semibold);
		font-variant-numeric: tabular-nums;
	}
</style>
