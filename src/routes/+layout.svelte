<script lang="ts">
	import '@smykla-skalski/sui/styles.css';
	import '../app.css';
	import {
		AppShell,
		ServiceWorkerStatus,
		SideNav,
		ThemeSwitch,
		registerServiceWorker
	} from '@smykla-skalski/sui';
	import type { Snippet } from 'svelte';
	import { dev } from '$app/env';
	import { page } from '$app/state';
	import { currentItem, sections } from '#lib/nav.js';

	let { children }: { children: Snippet } = $props();

	const current = $derived(currentItem(page.url.pathname, sections));
	const worker = registerServiceWorker('/service-worker.js', { dev });
</script>

<AppShell class="ohara-shell" title="Ohara" label="Nawigacja" menuLabel="Menu">
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
	:global(body) {
		background: var(--sui-canvas);
		color: var(--sui-text-primary);
		font: var(--sui-font-size-body) / var(--sui-leading-body) var(--sui-sans);
		/* Years and prices in the app's own text use tabular figures. */
		font-variant-numeric: tabular-nums;
	}

	:global(button),
	:global(input),
	:global(select),
	:global(textarea) {
		font-family: inherit;
	}

	:global(.ohara-shell .sui-shell-side[aria-modal='true']:popover-open) {
		inset: 0;
		inline-size: 100%;
		max-inline-size: none;
		block-size: 100dvh;
		max-block-size: none;
		padding: env(safe-area-inset-top) env(safe-area-inset-right) env(safe-area-inset-bottom)
			env(safe-area-inset-left);
	}

	:global(.ohara-shell .sui-shell-side[aria-modal='true']:popover-open > .sui-shell-pages) {
		inline-size: 100%;
	}

	:global(:root:has(.ohara-shell .sui-shell-side[aria-modal='true']:popover-open)) {
		overflow: hidden;
	}

	/* WebKit hides native select text when SUI supplies a selectedcontent button. */
	@supports (appearance: base-select) {
		@media not (pointer: fine) {
			:global(.sui-select > select.sui-select-control) {
				appearance: base-select;
				align-items: center;
				display: flex;
			}

			:global(.sui-select-control::picker-icon),
			:global(.sui-select-value .sui-select-check),
			:global(.sui-select-value .sui-select-note) {
				display: none;
			}

			:global(.sui-select-value) {
				min-inline-size: 0;
				overflow: hidden;
				text-overflow: ellipsis;
				white-space: nowrap;
			}
		}
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
