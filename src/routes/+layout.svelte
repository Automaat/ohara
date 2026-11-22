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
	import { onMount, type Snippet } from 'svelte';
	import { MediaQuery } from 'svelte/reactivity';
	import { dev } from '$app/env';
	import { onNavigate } from '$app/navigation';
	import { page } from '$app/state';
	import TabBar from '#lib/components/TabBar.svelte';
	import { carryCover } from '#lib/cover-transition.js';
	import { currentItem, phoneLayout, sections } from '#lib/nav.js';
	import { pendingScans } from '#lib/pending-scans.svelte.js';

	let { children }: { children: Snippet } = $props();

	const phone = new MediaQuery(phoneLayout);
	const current = $derived(currentItem(page.url.pathname, sections));
	const worker = registerServiceWorker('/service-worker.js', { dev });

	onNavigate((navigation) => carryCover(navigation));
	onMount(() => pendingScans.start(window, document));
</script>

<AppShell
	class={['ohara-shell', { 'ohara-phone': phone.current }]}
	title="Ohara"
	label="Nawigacja"
	menuLabel="Menu"
>
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
	<div class="ohara-page">{@render children()}</div>
	<TabBar />
</AppShell>
<ServiceWorkerStatus
	{worker}
	class="ohara-sw-status"
	readyTitle="Nowa wersja jest gotowa"
	readyDescription="Przeładuj, aby jej użyć."
	reloadLabel="Przeładuj"
	reloadingLabel="Przeładowuję…"
	laterLabel="Później"
	offlineTitle="Brak połączenia"
	offlineDescription={pendingScans.available
		? 'Zeskanowane kody trafią do kolejki, gdy połączenie wróci.'
		: 'Skanowanie wymaga połączenia z serwerem.'}
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

	/* In the phone layout the tab bar replaces SUI's top bar, whose menu button it makes redundant,
	   and the side navigation, which SUI keeps on a phone held sideways because it answers to the
	   shell's width alone. The pane becomes a column so the bar sits at the foot of the screen
	   even on a short page. */
	:global(.ohara-shell.ohara-phone > .sui-shell-bar),
	:global(.ohara-shell.ohara-phone .sui-shell-side:not(:popover-open)) {
		display: none;
	}

	:global(.ohara-shell.ohara-phone .sui-shell-grid) {
		grid-template-columns: minmax(0, 1fr);
	}

	:global(.ohara-shell.ohara-phone .sui-shell-pane) {
		display: flex;
		flex-direction: column;
	}

	.ohara-page {
		flex: 1 0 auto;
	}

	/* A control focused near the foot of the screen scrolls clear of the floating tab bar. */
	:global(:root:has(.ohara-phone)) {
		scroll-padding-block-end: calc(4rem + 2 * max(env(safe-area-inset-bottom, 0px), 0.75rem));
	}

	/* SUI floats the offline and update notice at the foot of a phone's screen, where it caught
	   taps meant for the tab bar (#303), and just above the bar stand the pages' toasts with their
	   "Cofnij". On a phone the notice stands at the top of the screen, clear of both, and comes
	   down into place instead of rising. */
	:global(:root:has(.ohara-phone) .ohara-sw-status:not(.preview)) {
		inset-block-start: max(var(--sui-space-4), env(safe-area-inset-top));
		inset-block-end: auto;
	}

	@media (prefers-reduced-motion: no-preference) {
		:global(:root:has(.ohara-phone) .ohara-sw-status:not(.preview):not(:popover-open)) {
			translate: 0 calc(-1 * var(--sui-space-2));
		}

		@starting-style {
			:global(:root:has(.ohara-phone) .ohara-sw-status:popover-open) {
				translate: 0 calc(-1 * var(--sui-space-2));
			}
		}
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
