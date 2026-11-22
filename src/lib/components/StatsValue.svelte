<script lang="ts">
	import { groupNotes, groupValue, valueShare, type ValueGroup } from '#lib/stats-value.js';
	import { dayLabel } from '#lib/valuation.js';
	import StatsCard from './StatsCard.svelte';

	let {
		id,
		title,
		groups,
		day,
		minObservations
	}: {
		id: string;
		title: string;
		groups: ValueGroup[];
		/** The day of the last valuation. */
		day: string;
		/** How many prices a used value needs to be more than "mało danych". */
		minObservations: number;
	} = $props();
</script>

<StatsCard {id} {title}>
	{#if groups.length > 0}
		<ul class="groups">
			{#each groups as g (g.key)}
				<li>
					<div class="line">
						<span class="label">{g.label}</span>
						<strong>{groupValue(g)}</strong>
					</div>
					<div class="bar" aria-hidden="true">
						<span style:inline-size="{valueShare(g, groups)}%"></span>
					</div>
					<p class="notes">{groupNotes(g).join(' · ')}</p>
				</li>
			{/each}
		</ul>
		<p class="note">
			Wartość używanych egzemplarzy z wyceny z {dayLabel(day)}. „Mało danych”: wartość z OLX oparta
			na mniej niż {minObservations} ofertach.
		</p>
	{:else}
		<p class="note">Ostatnia wycena nie objęła żadnego posiadanego egzemplarza.</p>
	{/if}
</StatsCard>

<style>
	.groups {
		display: grid;
		gap: 0.75rem;
		margin: 0;
		padding: 0;
		list-style: none;
	}

	.groups li {
		display: grid;
		gap: 0.25rem;
		min-inline-size: 0;
	}

	.line {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		justify-content: space-between;
		gap: 0 0.75rem;
	}

	.label {
		font-weight: 600;
	}

	.line strong {
		font-variant-numeric: tabular-nums;
	}

	.bar {
		block-size: 0.5rem;
		overflow: hidden;
		border-radius: 999px;
		background: var(--sui-surface-inset);
	}

	.bar span {
		display: block;
		block-size: 100%;
		border-radius: inherit;
		background: var(--sui-chart-1);
	}

	.notes {
		margin: 0;
		color: var(--sui-text-muted);
		font-size: var(--sui-font-size-meta, 0.875rem);
		font-variant-numeric: tabular-nums;
		overflow-wrap: anywhere;
	}
</style>
