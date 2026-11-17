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

<AppShell title={current?.label ?? 'Ohara'} label="Nawigacja" menuLabel="Menu">
	{#snippet sidebar()}
		<SideNav
			title="Ohara"
			kicker="Kolekcja"
			{sections}
			current={current?.href}
			label="Strony Ohary"
			closeLabel="Zamknij"
			collapseLabel="Zwiń"
			expandLabel="Rozwiń"
		>
			{#snippet foot()}
				<ThemeSwitch />
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
