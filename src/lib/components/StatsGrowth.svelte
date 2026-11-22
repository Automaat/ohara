<script lang="ts">
	import { Disclosure } from '@smykla-skalski/sui';
	import { copyCount } from '#lib/polish.js';
	import { growthPaths, monthLabel, monthTick, type MonthCount } from '#lib/stats.js';
	import StatsCard from './StatsCard.svelte';

	let { growth }: { growth: MonthCount[] } = $props();

	const width = 326;
	const height = 120;
	const paths = $derived(
		growthPaths(
			growth.map((g) => g.copies),
			width,
			height
		)
	);
	const first = $derived(growth.at(0));
	const last = $derived(growth.at(-1));
</script>

<StatsCard
	id="stats-growth-title"
	title="Przyrost kolekcji"
	aside={last ? copyCount(last.copies) : undefined}
>
	{#if paths}
		<svg
			viewBox="0 0 {width} {height}"
			preserveAspectRatio="none"
			role="img"
			aria-label="Liczba posiadanych egzemplarzy na koniec każdego z ostatnich {growth.length} miesięcy"
		>
			<path class="area" d={paths.area} />
			<path class="line" d={paths.line} />
		</svg>
		<ol class="ticks" style:--months={growth.length} aria-hidden="true">
			{#each growth as g (g.month)}
				<li>{monthTick(g.month)}</li>
			{/each}
		</ol>
	{/if}
	{#if first && last}
		<p class="note">
			Na koniec miesiąca {monthLabel(first.month)}: {copyCount(first.copies)}; teraz: {copyCount(
				last.copies
			)}. Egzemplarze bez daty zakupu liczą się od dnia dodania.
		</p>
	{/if}
	<Disclosure summary="Pokaż dane">
		<div class="table">
			<table>
				<caption>Posiadane egzemplarze na koniec miesiąca</caption>
				<thead>
					<tr>
						<th scope="col">Miesiąc</th>
						<th scope="col">Egzemplarze</th>
					</tr>
				</thead>
				<tbody>
					{#each growth as g (g.month)}
						<tr>
							<th scope="row">{monthLabel(g.month)}</th>
							<td>{g.copies}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	</Disclosure>
</StatsCard>

<style>
	path {
		vector-effect: non-scaling-stroke;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	path.area {
		fill: var(--sui-brand-action-tint);
		stroke: none;
	}

	path.line {
		fill: none;
		stroke: var(--sui-chart-1);
		stroke-width: 2.6;
	}
</style>
