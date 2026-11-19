<script lang="ts">
	import { VisuallyHidden } from '@smykla-skalski/sui';
	import { onMount } from 'svelte';
	import { afterNavigate } from '$app/navigation';
	import { page } from '$app/state';
	import { dealBadge, refreshDealBadge } from '#lib/deal-badge.svelte.js';
	import { underLimitLabel } from '#lib/deals.js';
	import { currentTab, scanHref, tabs, type TabIcon } from '#lib/nav.js';

	const current = $derived(currentTab(page.url.pathname));
	const deals = $derived(dealBadge.count);

	afterNavigate(() => {
		void refreshDealBadge(fetch);
	});

	onMount(() => {
		const onvisibility = () => {
			if (document.visibilityState === 'visible') void refreshDealBadge(fetch);
		};
		document.addEventListener('visibilitychange', onvisibility);
		return () => document.removeEventListener('visibilitychange', onvisibility);
	});
</script>

{#snippet glyph(name: TabIcon | 'scan', size: number, stroke: number)}
	<svg
		width={size}
		height={size}
		viewBox="0 0 24 24"
		fill="none"
		stroke="currentColor"
		stroke-width={stroke}
		stroke-linecap="round"
		stroke-linejoin="round"
		aria-hidden="true"
		focusable="false"
	>
		{#if name === 'grid'}
			<rect x="3.5" y="3.5" width="7" height="7" rx="2" />
			<rect x="13.5" y="3.5" width="7" height="7" rx="2" />
			<rect x="3.5" y="13.5" width="7" height="7" rx="2" />
			<rect x="13.5" y="13.5" width="7" height="7" rx="2" />
		{:else if name === 'tag'}
			<path d="M3 12.2V4.5A1.5 1.5 0 0 1 4.5 3h7.7l8.8 8.8-9.2 9.2z" />
			<circle cx="8" cy="8" r="1.6" />
		{:else if name === 'trend'}
			<polyline points="3 17 9 11 13 15 21 7" />
			<polyline points="15 7 21 7 21 13" />
		{:else}
			<path
				d="M4 8V5.5A1.5 1.5 0 0 1 5.5 4H8M16 4h2.5A1.5 1.5 0 0 1 20 5.5V8M20 16v2.5a1.5 1.5 0 0 1-1.5 1.5H16M8 20H5.5A1.5 1.5 0 0 1 4 18.5V16"
			/>
			<path d="M8 8v8M11 8v8M14 8v8M16.5 8v8" />
		{/if}
	</svg>
{/snippet}

<nav class="tab-bar" aria-label="Główne strony">
	<ul class="pill">
		{#each tabs as tab (tab.href)}
			<li>
				<a
					class="tab"
					href={tab.href}
					aria-current={current === tab.href ? 'page' : undefined}
					data-sveltekit-preload-data="tap"
				>
					<span class="glyph">
						{@render glyph(tab.icon, 22, 1.9)}
						{#if tab.href === '/deals' && deals > 0}
							<span class="badge" aria-hidden="true">{deals > 99 ? '99+' : deals}</span>
						{/if}
					</span>
					<span class="label">{tab.label}</span>
					{#if tab.href === '/deals' && deals > 0}
						<VisuallyHidden>, {underLimitLabel(deals)}</VisuallyHidden>
					{/if}
				</a>
			</li>
		{/each}
	</ul>
	<a
		class="scan"
		href={scanHref}
		aria-label="Skanuj"
		aria-current={current === scanHref ? 'page' : undefined}
		data-sveltekit-preload-data="tap"
	>
		{@render glyph('scan', 28, 2)}
	</a>
</nav>

<style>
	.tab-bar {
		display: none;
	}

	/* The bar takes the place of SUI's phone bar, so it answers to the same container query on the
	   shell's width that shows the shell's menu button. */
	@container sui-shell (max-width: 47.9375rem) {
		.tab-bar {
			--tab-bar-gap: max(env(safe-area-inset-bottom, 0px), 0.75rem);

			align-items: center;
			box-sizing: border-box;
			display: flex;
			gap: 0.625rem;
			inset-block-end: var(--tab-bar-gap);
			margin: auto 1rem var(--tab-bar-gap);
			pointer-events: none;
			position: sticky;
			z-index: var(--sui-layer-top-bar);
		}
	}

	/* A dialog or sheet covers the page, and the bar would show through its scrim. Hiding it
	   keeps its space, so the page under the dialog does not move. */
	:global(:root:has(dialog:modal)) .tab-bar {
		visibility: hidden;
	}

	.pill {
		backdrop-filter: blur(24px) saturate(1.6);
		-webkit-backdrop-filter: blur(24px) saturate(1.6);
		background: color-mix(in srgb, var(--sui-surface-base) 72%, transparent);
		border: 1px solid color-mix(in srgb, var(--sui-text-primary) 10%, transparent);
		border-radius: 2rem;
		box-shadow: 0 14px 30px -14px var(--ohara-bar-shadow);
		box-sizing: border-box;
		display: grid;
		flex: 1;
		grid-template-columns: repeat(3, minmax(0, 1fr));
		list-style: none;
		margin: 0;
		min-block-size: 4rem;
		padding: 5px;
		pointer-events: auto;
	}

	.pill > li {
		display: grid;
	}

	.tab {
		align-items: center;
		block-size: 100%;
		border-radius: 1.6875rem;
		box-sizing: border-box;
		color: var(--sui-text-secondary);
		display: flex;
		flex-direction: column;
		font-size: 0.6875rem;
		font-weight: 700;
		gap: 3px;
		justify-content: center;
		line-height: 1.2;
		min-block-size: 2.75rem;
		min-inline-size: 2.75rem;
		padding: 0.25rem;
		text-align: center;
		text-decoration: none;
		-webkit-tap-highlight-color: transparent;
		touch-action: manipulation;
	}

	.tab[aria-current='page'] {
		background: color-mix(in srgb, var(--sui-text-primary) 9%, transparent);
		color: var(--sui-text-primary);
	}

	.tab:focus-visible,
	.scan:focus-visible {
		outline: var(--sui-focus-ring-width) solid var(--sui-focus);
		outline-offset: 2px;
	}

	.glyph {
		display: grid;
		position: relative;
	}

	.badge {
		align-items: center;
		background: var(--sui-brand-action);
		border-radius: 9px;
		box-sizing: border-box;
		color: var(--sui-on-brand-action);
		display: flex;
		font-size: 0.6875rem;
		font-weight: 800;
		inset-block-start: -7px;
		inset-inline-start: calc(100% - 4px);
		justify-content: center;
		line-height: 1;
		min-block-size: 18px;
		min-inline-size: 18px;
		padding-inline: 5px;
		position: absolute;
	}

	.scan {
		align-items: center;
		background: var(--sui-brand-action);
		block-size: 4rem;
		border-radius: 50%;
		box-shadow: 0 14px 26px -10px var(--sui-brand-action);
		color: var(--sui-on-brand-action);
		display: flex;
		flex: none;
		inline-size: 4rem;
		justify-content: center;
		pointer-events: auto;
		-webkit-tap-highlight-color: transparent;
		touch-action: manipulation;
	}

	.scan[aria-current='page'] {
		box-shadow:
			0 0 0 4px rgb(255 255 255 / 35%),
			0 14px 26px -10px var(--sui-brand-action);
	}

	@media (hover: hover) {
		.tab:hover:not([aria-current='page']) {
			color: var(--sui-text-primary);
		}

		.scan:hover {
			background: var(--sui-brand-action-hover);
		}
	}

	.scan:active {
		background: var(--sui-brand-action-pressed);
	}

	/* On a short screen, as at a large zoom, a stuck bar would cover much of the page, so it rides
	   with the content instead (WCAG 1.4.10). */
	@media (max-height: 30rem) {
		.tab-bar {
			position: relative;
			inset-block-end: auto;
		}
	}

	@media (prefers-reduced-transparency: reduce) {
		.pill {
			backdrop-filter: none;
			-webkit-backdrop-filter: none;
			background: var(--sui-surface-base);
		}
	}

	@media (forced-colors: active) {
		.pill,
		.scan {
			border: 1px solid CanvasText;
		}

		.tab[aria-current='page'] {
			outline: 2px solid Highlight;
		}
	}
</style>
