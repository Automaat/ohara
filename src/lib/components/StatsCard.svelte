<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		id,
		title,
		aside,
		children
	}: { id: string; title: string; aside?: string; children: Snippet } = $props();
</script>

<!-- One statistic on its card: a heading, a figure beside it and the chart with its data. -->
<section class="card" aria-labelledby={id}>
	<div class="head">
		<h2 {id}>{title}</h2>
		{#if aside}
			<span class="aside">{aside}</span>
		{/if}
	</div>
	{@render children()}
</section>

<style>
	.card {
		display: grid;
		gap: 0.75rem;
		min-inline-size: 0;
		padding: 1rem 1.125rem;
		border: 1px solid var(--sui-border-subtle);
		border-radius: 1.375rem;
		background: var(--sui-surface-base);
	}

	.head {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.25rem 0.75rem;
	}

	h2 {
		margin: 0;
		font-size: 1rem;
		font-weight: 700;
	}

	.aside {
		color: var(--sui-text-muted);
		font-size: var(--sui-font-size-meta, 0.875rem);
		font-variant-numeric: tabular-nums;
	}

	.card :global(svg.chart) {
		display: block;
		inline-size: 100%;
		block-size: 7.5rem;
		overflow: visible;
	}

	/* A month's short name under each bar or point, in a grid as wide as the chart's slots. */
	.card :global(.ticks) {
		display: grid;
		grid-template-columns: repeat(var(--months, 12), minmax(0, 1fr));
		margin: -0.375rem 0 0;
		padding: 0;
		list-style: none;
		color: var(--sui-text-muted);
		font-size: 0.6875rem;
		text-align: center;
	}

	.card :global(.ticks li) {
		overflow: hidden;
		white-space: nowrap;
	}

	.card :global(.note) {
		margin: 0;
		color: var(--sui-text-muted);
		font-size: var(--sui-font-size-meta, 0.875rem);
	}

	.card :global(.table) {
		overflow-x: auto;
	}

	.card :global(table) {
		inline-size: 100%;
		border-collapse: collapse;
		font-size: 0.8125rem;
		font-variant-numeric: tabular-nums;
	}

	.card :global(caption) {
		position: absolute;
		inline-size: 1px;
		block-size: 1px;
		overflow: hidden;
		clip-path: inset(50%);
		white-space: nowrap;
	}

	.card :global(th),
	.card :global(td) {
		padding: 0.25rem 0.375rem;
		text-align: end;
		vertical-align: top;
	}

	.card :global(th[scope='col']) {
		font-size: 0.75rem;
	}

	.card :global(th[scope='row']),
	.card :global(th[scope='col']:first-child) {
		text-align: start;
	}
</style>
