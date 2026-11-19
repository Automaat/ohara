<script lang="ts">
	import { Disclosure } from '@smykla-skalski/sui';
	import {
		boundLabels,
		dayLabel,
		formatZl,
		historyPaths,
		historyRows,
		historySpan,
		type HistoryPoint
	} from '#lib/valuation.js';

	let { history, lastValuation }: { history: HistoryPoint[]; lastValuation: string | null } =
		$props();

	const width = 326;
	const height = 128;
	const paths = $derived(historyPaths(history, width, height));
	const rows = $derived(historyRows(history));
	const span = $derived(historySpan(history));
	const amount = (v: number | null) => (v === null ? '—' : formatZl(v));
</script>

<section class="history" aria-labelledby="valuation-history-title">
	<div class="head">
		<h2 id="valuation-history-title">Ostatnie 90 dni</h2>
		<span class="span">{span}</span>
	</div>
	{#if paths}
		<svg
			viewBox="0 0 {width} {height}"
			preserveAspectRatio="none"
			role="img"
			aria-label="Przedział od skupu do nowych, wartość używanych i kwota wydana w każdej wycenie"
		>
			<path class="band" d={paths.band} />
			<path class="mid" d={paths.mid} />
			<path class="spent" d={paths.spent} />
		</svg>
		<ul class="legend">
			<li><i class="band"></i>Przedział</li>
			<li><i class="mid"></i>{boundLabels.mid}</li>
			<li><i class="spent"></i>Wydano</li>
		</ul>
		<Disclosure summary="Pokaż dane">
			<div class="table">
				<table>
					<caption>Wartość kolekcji w czasie</caption>
					<thead>
						<tr>
							<th scope="col">Dzień</th>
							<th scope="col">{boundLabels.floor}</th>
							<th scope="col">{boundLabels.mid}</th>
							<th scope="col">{boundLabels.ceiling}</th>
							<th scope="col">Wydano</th>
						</tr>
					</thead>
					<tbody>
						{#each rows as r (r.x)}
							<tr>
								<th scope="row">{r.day}</th>
								<td>{amount(r.floor)}</td>
								<td>{amount(r.mid)}</td>
								<td>{amount(r.ceiling)}</td>
								<td>{amount(r.spent)}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		</Disclosure>
	{:else}
		<p class="none">
			Brak wycen z ostatnich 90 dni.
			{#if lastValuation}
				Ostatnia wycena: {dayLabel(lastValuation)}.
			{/if}
			Nowa wycena doda pierwszy punkt.
		</p>
	{/if}
</section>

<style>
	.history {
		display: grid;
		gap: 0.75rem;
		padding: 1rem 1.125rem;
		border: 1px solid var(--sui-border-subtle);
		border-radius: 1.375rem;
		background: var(--sui-surface-base);
	}

	.head {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.75rem;
	}

	h2 {
		margin: 0;
		font-size: 1rem;
		font-weight: 700;
	}

	.span {
		flex: none;
		color: var(--sui-text-muted);
		font-size: var(--sui-font-size-meta, 0.875rem);
		font-variant-numeric: tabular-nums;
	}

	svg {
		display: block;
		inline-size: 100%;
		block-size: 8rem;
		overflow: visible;
	}

	path {
		fill: none;
		vector-effect: non-scaling-stroke;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	path.band {
		fill: var(--sui-brand-action-tint);
		stroke: none;
	}

	path.mid {
		stroke: var(--sui-chart-1);
		stroke-width: 2.6;
	}

	path.spent {
		stroke: var(--sui-brand-action);
		stroke-width: 1.8;
		stroke-dasharray: 5 5;
		stroke-linecap: butt;
	}

	.legend {
		display: flex;
		flex-wrap: wrap;
		gap: 0.25rem 0.875rem;
		margin: 0;
		padding: 0;
		list-style: none;
		color: var(--sui-text-muted);
		font-size: 0.75rem;
	}

	.legend i {
		display: inline-block;
		inline-size: 0.875rem;
		margin-inline-end: 0.3125rem;
		vertical-align: middle;
	}

	.legend i.band {
		block-size: 0.5rem;
		border-radius: 3px;
		background: var(--sui-brand-action-tint);
	}

	.legend i.mid {
		block-size: 3px;
		border-radius: 2px;
		background: var(--sui-chart-1);
	}

	.legend i.spent {
		border-block-start: 2px dashed var(--sui-brand-action);
	}

	.table {
		overflow-x: auto;
	}

	table {
		inline-size: 100%;
		border-collapse: collapse;
		font-size: 0.8125rem;
		font-variant-numeric: tabular-nums;
	}

	.none {
		margin: 0;
		color: var(--sui-text-muted);
	}

	caption {
		position: absolute;
		inline-size: 1px;
		block-size: 1px;
		overflow: hidden;
		clip-path: inset(50%);
		white-space: nowrap;
	}

	th,
	td {
		padding: 0.25rem 0.375rem;
		text-align: end;
		vertical-align: top;
	}

	th[scope='col'] {
		font-size: 0.75rem;
	}

	th[scope='row'],
	th[scope='col']:first-child {
		text-align: start;
	}
</style>
