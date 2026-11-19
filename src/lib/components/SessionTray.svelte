<script lang="ts">
	import CollectionCover from '#lib/components/CollectionCover.svelte';
	import { formatPLN } from '#lib/collection.js';
	import { sessionSpent, type SessionAdd } from '#lib/session.svelte.js';

	let {
		adds,
		glass = false,
		onopen
	}: {
		/** The visit's adds, newest first. */
		adds: readonly SessionAdd[];
		/** Drawn as dark glass over the full-screen camera. */
		glass?: boolean;
		/** Opens the list of the visit's copies. */
		onopen: () => void;
	} = $props();

	const covers = $derived(adds.slice(0, 3));
	const spent = $derived(formatPLN(sessionSpent(adds)));
</script>

<button type="button" class={['tray', { glass }]} aria-haspopup="dialog" onclick={onopen}>
	<span class="covers" aria-hidden="true">
		{#each covers as add, i (add.id)}
			<span class="cover" style:z-index={covers.length - i}>
				<CollectionCover kind={add.kind} url={add.cover} />
			</span>
		{/each}
	</span>
	<span class="text">
		<span class="count">Ta sesja: {adds.length}</span>
		<span class="spent">Wydano {spent}</span>
	</span>
	<svg
		class="chevron"
		width="20"
		height="20"
		viewBox="0 0 24 24"
		fill="none"
		stroke="currentColor"
		stroke-width="2.25"
		stroke-linecap="round"
		stroke-linejoin="round"
		aria-hidden="true"
		focusable="false"
	>
		<path d="m9 6 6 6-6 6" />
	</svg>
</button>

<style>
	.tray {
		--collection-cover-width: 2.125rem;

		align-items: center;
		background: var(--sui-surface-base);
		block-size: var(--session-tray-height, 4.5rem);
		border: 1px solid var(--sui-border-subtle);
		border-radius: 1.375rem;
		box-sizing: border-box;
		color: var(--sui-text-primary);
		cursor: pointer;
		display: flex;
		font: inherit;
		gap: 0.875rem;
		inline-size: 100%;
		padding: 0 1rem 0 0.75rem;
		text-align: start;
		-webkit-tap-highlight-color: transparent;
		touch-action: manipulation;
	}

	/* Over the camera the tray is the same dark glass as the camera's own controls. */
	.glass {
		-webkit-backdrop-filter: blur(20px) saturate(1.5);
		backdrop-filter: blur(20px) saturate(1.5);
		background: rgb(20 24 22 / 55%);
		border-color: rgb(255 255 255 / 14%);
		color: #ffffff;
	}

	.covers {
		display: flex;
		flex: none;
	}

	/* The covers overlap like a short stack, the newest in front. */
	.cover {
		display: grid;
		position: relative;
	}

	.cover + .cover {
		margin-inline-start: -0.75rem;
	}

	.text {
		display: grid;
		flex: 1;
		gap: 0.125rem;
		min-inline-size: 0;
	}

	.count {
		font-size: 1rem;
		font-weight: 700;
		line-height: 1.25;
	}

	.spent {
		color: var(--sui-text-secondary);
		font-size: 0.875rem;
		font-variant-numeric: tabular-nums;
		line-height: 1.25;
	}

	.glass .spent {
		color: rgb(255 255 255 / 72%);
	}

	.chevron {
		flex: none;
		opacity: 0.8;
	}

	.tray:focus-visible {
		outline: var(--sui-focus-ring-width) solid var(--sui-focus);
		outline-offset: 2px;
	}

	.glass:focus-visible {
		outline-color: #ffffff;
	}

	@media (hover: hover) {
		.tray:not(.glass):hover {
			background: var(--sui-field-hover);
		}

		.glass:hover {
			background: rgb(20 24 22 / 70%);
		}
	}

	@media (prefers-reduced-transparency: reduce) {
		.glass {
			-webkit-backdrop-filter: none;
			backdrop-filter: none;
			background: rgb(20 24 22 / 92%);
		}
	}

	@media (forced-colors: active) {
		.tray {
			border-color: CanvasText;
		}
	}
</style>
