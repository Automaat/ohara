<script lang="ts">
	import { Disclosure } from '@smykla-skalski/sui';
	import { formatPLN } from '#lib/collection.js';
	import { copyCount } from '#lib/polish.js';
	import { bars, monthLabel, monthTick, roundZl, type Spending } from '#lib/stats.js';
	import StatsCard from './StatsCard.svelte';

	let { spending }: { spending: Spending } = $props();

	const width = 326;
	const height = 120;
	const months = $derived(spending.months);
	const drawn = $derived(
		bars(
			months.map((m) => m.cents),
			width,
			height
		)
	);
	const total = $derived(months.reduce((sum, m) => sum + m.cents, 0));
	const unpriced = $derived(months.reduce((sum, m) => sum + m.unpriced, 0));
	const dated = $derived(months.reduce((sum, m) => sum + m.copies, 0));
	const peak = $derived(
		months.reduce<(typeof months)[number] | null>(
			(best, m) => (m.cents > 0 && (!best || m.cents > best.cents) ? m : best),
			null
		)
	);
	const undated = $derived(spending.undated);
	const undatedLine = $derived(
		`Bez daty: ${formatPLN(undated.cents)} · ${copyCount(undated.copies)}` +
			(undated.unpriced > 0 ? `, w tym ${undated.unpriced} bez ceny.` : '.')
	);
</script>

<StatsCard
	id="stats-spending-title"
	title="Wydatki w miesiącach"
	aside={`${roundZl(total)} w ${months.length} mies.`}
>
	<svg
		class="chart"
		viewBox="0 0 {width} {height}"
		preserveAspectRatio="none"
		role="img"
		aria-label="Kwota wydana na egzemplarze w każdym z ostatnich {months.length} miesięcy"
	>
		<line class="ground" x1="0" y1={height} x2={width} y2={height} />
		{#each drawn as bar, i (months[i].month)}
			{#if bar.height > 0}
				<rect x={bar.x} y={bar.y} width={bar.width} height={bar.height} />
			{/if}
		{/each}
	</svg>
	<ol class="ticks" style:--months={months.length} aria-hidden="true">
		{#each months as m (m.month)}
			<li>{monthTick(m.month)}</li>
		{/each}
	</ol>
	{#if peak}
		<p class="note">Najwięcej: {formatPLN(peak.cents)} ({monthLabel(peak.month)}).</p>
	{:else if dated === 0}
		<p class="note">Brak zakupów z datą w ostatnich {months.length} miesiącach.</p>
	{/if}
	{#if unpriced > 0}
		<p class="note">{copyCount(unpriced)} z tego okresu bez ceny, poza sumą.</p>
	{/if}
	{#if undated.copies > 0}
		<p class="note">{undatedLine}</p>
	{/if}
	<Disclosure summary="Pokaż dane">
		<div class="table">
			<table>
				<caption>Wydatki w miesiącach</caption>
				<thead>
					<tr>
						<th scope="col">Miesiąc</th>
						<th scope="col">Wydano</th>
						<th scope="col">Egzemplarze</th>
						<th scope="col">Bez ceny</th>
					</tr>
				</thead>
				<tbody>
					{#each months as m (m.month)}
						<tr>
							<th scope="row">{monthLabel(m.month)}</th>
							<td>{formatPLN(m.cents)}</td>
							<td>{m.copies}</td>
							<td>{m.unpriced}</td>
						</tr>
					{/each}
					{#if undated.copies > 0}
						<tr>
							<th scope="row">Bez daty</th>
							<td>{formatPLN(undated.cents)}</td>
							<td>{undated.copies}</td>
							<td>{undated.unpriced}</td>
						</tr>
					{/if}
				</tbody>
			</table>
		</div>
	</Disclosure>
</StatsCard>

<style>
	rect {
		fill: var(--sui-chart-1);
	}

	line.ground {
		stroke: var(--sui-chart-grid, var(--sui-border-subtle));
		stroke-width: 1;
		vector-effect: non-scaling-stroke;
	}
</style>
