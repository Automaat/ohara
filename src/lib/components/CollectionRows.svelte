<script lang="ts">
	import { ErrorState, ObjectList, ObjectRow, Pill } from '@smykla-skalski/sui';
	import type { Snippet } from 'svelte';
	import {
		bindingLabels,
		displayName,
		formatLabels,
		formatPLN,
		kindLabels,
		statusLabels,
		type CollectionItem
	} from '#lib/collection.js';
	import CollectionCover from './CollectionCover.svelte';

	let {
		rows,
		loading,
		failure,
		onretry,
		empty
	}: {
		rows: readonly CollectionItem[];
		loading: boolean;
		failure: string | null;
		onretry: () => void;
		empty: Snippet;
	} = $props();
</script>

{#if failure}
	<ErrorState
		title="Nie udało się wczytać kolekcji"
		description={failure}
		{onretry}
		busy={loading}
		overContent={rows.length > 0}
		retryLabel="Spróbuj ponownie"
		retryingLabel="Ponawianie…"
	/>
{/if}
{#if loading && !failure && rows.length === 0}
	<p role="status">Wczytywanie kolekcji…</p>
{/if}
{#if rows.length > 0}
	<ObjectList aria-label="Egzemplarze w kolekcji" aria-busy={loading}>
		{#each rows as row (row.copy_id)}
			<ObjectRow href={`/copies/${row.copy_id}`}>
				{#snippet main()}
					<div class="row">
						<CollectionCover kind={row.kind} url={row.cover_url} />
						<div class="details">
							<strong>{displayName(row)}</strong>
							{#if row.authors?.length}
								<span class="creator">{row.authors.join(', ')}</span>
							{/if}
							<span class="format">
								{row.format
									? formatLabels[row.format]
									: row.binding
										? bindingLabels[row.binding]
										: kindLabels[row.kind]}
							</span>
							<div class="facts">
								<Pill tone={row.status === 'owned' ? 'success' : 'neutral'}>
									{statusLabels[row.status]}
								</Pill>
								<span class="price">{formatPLN(row.price_cents) || 'Cena: —'}</span>
							</div>
						</div>
					</div>
				{/snippet}
			</ObjectRow>
		{/each}
	</ObjectList>
{:else if !loading && !failure}
	{@render empty()}
{/if}

<style>
	.row {
		display: grid;
		grid-template-columns: auto minmax(0, 1fr);
		gap: var(--sui-space-3);
		align-items: center;
		font-family: var(--sui-sans);
	}

	.details {
		display: grid;
		gap: var(--sui-space-1);
		overflow-wrap: anywhere;
	}

	.creator,
	.format {
		color: var(--sui-text-secondary);
		font-size: var(--sui-font-size-meta);
	}

	.facts {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--sui-space-1) var(--sui-space-2);
	}

	.price {
		font-size: var(--sui-font-size-meta);
		font-variant-numeric: tabular-nums;
	}
</style>
