<script lang="ts">
	import '@smykla-skalski/sui/styles.css';
	import { AppShell, SideNav, ThemeSwitch } from '@smykla-skalski/sui';
	import type { Snippet } from 'svelte';
	import { page } from '$app/state';
	import { currentItem, sections } from '$lib/nav';

	let { children }: { children: Snippet } = $props();

	const current = $derived(currentItem(page.url.pathname, sections));
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
