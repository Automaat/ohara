<script lang="ts">
	import {
		boundKeys,
		boundLine,
		heroFigures,
		rangeMarks,
		sparkline,
		type HistoryPoint,
		type Summary
	} from '#lib/valuation.js';

	let { summary, history }: { summary: Summary; history: HistoryPoint[] } = $props();

	const figures = $derived(heroFigures(summary));
	const marks = $derived(rangeMarks(summary));
	const spark = $derived(sparkline(history, 92, 36));
</script>

<section class="hero" aria-labelledby="valuation-hero-title">
	<div class="head">
		<h2 id="valuation-hero-title">Szacowana wartość</h2>
		{#if spark}
			<svg class="spark" width="92" height="36" viewBox="0 0 92 36" aria-hidden="true">
				<path d={spark.path} />
				<circle cx={spark.end.x} cy={spark.end.y} r="3.5" />
			</svg>
		{/if}
	</div>
	{#if figures.value}
		<p class="value">≈ {figures.value} <small>zł</small></p>
	{:else}
		<p class="value none">Brak wyceny</p>
	{/if}
	<p class="spent">
		<span>Wydano {figures.spent}</span>
		{#if figures.delta}
			<span class="delta">{figures.delta}</span>
		{/if}
	</p>
	{#if figures.coverage}
		<p class="coverage">{figures.coverage}</p>
	{/if}
	{#if marks}
		<div class="range" aria-hidden="true">
			<span class="paid" style:left="{marks.spent}%"></span>
			{#if marks.mid !== null}
				<span class="mark" style:left="{marks.mid}%"></span>
			{/if}
		</div>
	{/if}
	<p class="labels">
		{#each boundKeys as k (k)}
			<span>{boundLine(k, summary[k])}</span>
		{/each}
	</p>
</section>

<style>
	.hero {
		display: grid;
		gap: 0.375rem;
		padding: 1.125rem 1.125rem 1rem;
		border-radius: 1.625rem;
		background: #1f3b33;
		color: #f8e9e6;
	}

	.head {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 0.75rem;
		min-block-size: 1.25rem;
	}

	h2 {
		margin: 0;
		font-size: 0.8125rem;
		font-weight: 700;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: #f4c6cc;
	}

	.spark {
		flex: none;
		fill: none;
	}

	.spark path {
		stroke: #f4c6cc;
		stroke-width: 2.4;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.spark circle {
		fill: #f4c6cc;
	}

	.value {
		margin: 0;
		font-family: var(--ohara-serif, Georgia, serif);
		font-size: clamp(2.5rem, 12vw, 3.25rem);
		font-weight: 600;
		line-height: 1.05;
		letter-spacing: -0.03em;
		font-variant-numeric: tabular-nums;
	}

	.value small {
		font-size: 0.5em;
		font-weight: 500;
		opacity: 0.8;
	}

	.value.none {
		font-size: 2rem;
	}

	.spent {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem;
		margin: 0;
		font-size: 0.875rem;
		font-variant-numeric: tabular-nums;
	}

	.delta {
		padding: 0.1875rem 0.625rem;
		border-radius: var(--sui-radius-pill, 999px);
		background: rgb(244 198 204 / 18%);
		color: #f4c6cc;
		font-weight: 700;
	}

	.coverage {
		margin: 0;
		font-size: 0.75rem;
		color: rgb(248 233 230 / 75%);
		font-variant-numeric: tabular-nums;
	}

	.range {
		position: relative;
		block-size: 0.5rem;
		margin-block: 0.875rem 0.25rem;
		border-radius: 0.25rem;
		background: linear-gradient(90deg, rgb(248 233 230 / 18%), rgb(244 198 204 / 55%));
	}

	.mark {
		position: absolute;
		top: 50%;
		inline-size: 1.125rem;
		block-size: 1.125rem;
		border: 3px solid #1f3b33;
		border-radius: 50%;
		background: #f8e9e6;
		box-shadow: 0 1px 4px rgb(0 0 0 / 25%);
		transform: translate(-50%, -50%);
	}

	.paid {
		position: absolute;
		top: -0.3125rem;
		inline-size: 2px;
		block-size: 1.125rem;
		border-radius: 1px;
		background: #f2779f;
		transform: translateX(-50%);
	}

	.labels {
		display: flex;
		flex-wrap: wrap;
		justify-content: space-between;
		gap: 0.25rem 0.75rem;
		margin: 0;
		font-size: 0.75rem;
		color: rgb(248 233 230 / 75%);
		font-variant-numeric: tabular-nums;
	}
</style>
