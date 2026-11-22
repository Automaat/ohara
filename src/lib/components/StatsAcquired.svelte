<script lang="ts">
	import { formatPLN } from '#lib/collection.js';
	import { copyCount } from '#lib/polish.js';
	import { acquiredShareLabels, type Share } from '#lib/stats.js';
	import StatsCard from './StatsCard.svelte';

	let { shares }: { shares: Share[] } = $props();

	const paid = $derived(shares.every((s) => s.centsPercent !== null));
	const spoken = (pick: (s: Share) => number | null) =>
		shares.map((s) => `${acquiredShareLabels[s.key]} ${pick(s) ?? 0}%`).join(', ');
</script>

<StatsCard id="stats-acquired-title" title="Nowe czy używane">
	<div class="split">
		<h3>Według liczby egzemplarzy</h3>
		<div class="stack" role="img" aria-label={spoken((s) => s.copyPercent)}>
			{#each shares as s (s.key)}
				{#if s.copyPercent > 0}
					<span class={s.key} style:flex-grow={s.copyPercent}></span>
				{/if}
			{/each}
		</div>
		<ul class="legend">
			{#each shares as s (s.key)}
				<li>
					<i class={s.key}></i>
					<span class="label">{acquiredShareLabels[s.key]}</span>
					<strong>{s.copyPercent}%</strong>
					<span class="figure">{copyCount(s.copies)}</span>
				</li>
			{/each}
		</ul>
	</div>
	<div class="split">
		<h3>Według wydanej kwoty</h3>
		{#if paid}
			<div class="stack" role="img" aria-label={spoken((s) => s.centsPercent)}>
				{#each shares as s (s.key)}
					{#if (s.centsPercent ?? 0) > 0}
						<span class={s.key} style:flex-grow={s.centsPercent}></span>
					{/if}
				{/each}
			</div>
			<ul class="legend">
				{#each shares as s (s.key)}
					<li>
						<i class={s.key}></i>
						<span class="label">{acquiredShareLabels[s.key]}</span>
						<strong>{s.centsPercent}%</strong>
						<span class="figure">{formatPLN(s.cents)}</span>
					</li>
				{/each}
			</ul>
		{:else}
			<p class="note">Żaden posiadany egzemplarz nie ma ceny zakupu.</p>
		{/if}
	</div>
</StatsCard>

<style>
	.split {
		display: grid;
		gap: 0.5rem;
	}

	h3 {
		margin: 0;
		color: var(--sui-text-secondary);
		font-size: 0.8125rem;
		font-weight: 600;
	}

	.stack {
		display: flex;
		gap: 2px;
		block-size: 0.75rem;
		overflow: hidden;
		border-radius: 999px;
		background: var(--sui-surface-inset);
	}

	.stack span {
		flex-basis: 0;
		min-inline-size: 2px;
	}

	.new {
		background: var(--sui-chart-1);
	}

	.used {
		background: var(--sui-chart-2);
	}

	.unknown {
		background: var(--sui-border-strong);
	}

	.legend {
		display: grid;
		gap: 0.25rem;
		margin: 0;
		padding: 0;
		list-style: none;
		font-size: var(--sui-font-size-meta, 0.875rem);
	}

	.legend li {
		display: grid;
		grid-template-columns: auto minmax(0, 1fr) auto auto;
		align-items: baseline;
		gap: 0.5rem;
	}

	.legend i {
		display: inline-block;
		inline-size: 0.625rem;
		block-size: 0.625rem;
		border-radius: 3px;
	}

	.legend strong,
	.figure {
		font-variant-numeric: tabular-nums;
		text-align: end;
	}

	.figure {
		min-inline-size: 5.5rem;
		color: var(--sui-text-muted);
	}
</style>
