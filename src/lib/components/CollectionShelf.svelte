<script lang="ts">
	import { ErrorState, Link } from '@smykla-skalski/sui';
	import type { Snippet } from 'svelte';
	import {
		displayName,
		editionSummary,
		statusLabels,
		type CollectionItem
	} from '#lib/collection.js';
	import CollectionCover from './CollectionCover.svelte';
	import ShelfSkeleton from './ShelfSkeleton.svelte';

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
{#if loading && !failure}
	{#if rows.length}
		<p role="status">Odświeżanie…</p>
	{:else}
		<ShelfSkeleton label="Wczytywanie kolekcji…" />
	{/if}
{/if}
{#if rows.length}
	<ul class="shelf" role="list" aria-label="Egzemplarze w kolekcji" aria-busy={loading}>
		{#each rows as row (row.copy_id)}
			<li data-cover-title={row.title_id}>
				<div class="artwork">
					<CollectionCover kind={row.kind} url={row.cover_url} large />
				</div>
				<Link href={`/titles/${row.title_id}`}>{displayName(row)}</Link>
				<span class="meta">
					{#if row.status === 'wishlist'}<span class="wish">{statusLabels.wishlist}</span> ·
					{/if}{[row.authors?.join(', '), editionSummary({ ...row, barcode: null })]
						.filter(Boolean)
						.join(' · ')}
				</span>
			</li>
		{/each}
	</ul>
{:else if !loading && !failure}
	{@render empty()}
{/if}

<style>
	.shelf {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(9rem, 1fr));
		gap: var(--sui-space-4);
		margin: 0;
		padding: 0;
		list-style: none;
	}

	li {
		position: relative;
		display: flex;
		flex-direction: column;
		gap: var(--sui-space-1);
		overflow-wrap: anywhere;
	}

	/* The title's link covers the whole tile, so a tap on the cover opens the title too. */
	li :global(a)::after {
		content: '';
		position: absolute;
		inset: 0;
	}

	.artwork {
		display: grid;
		align-items: end;
		aspect-ratio: 2 / 3;
		--collection-cover-width: 100%;
	}

	.meta {
		color: var(--sui-text-secondary);
		font-size: var(--sui-font-size-meta);
	}

	.wish {
		color: var(--sui-brand-action-text);
		font-weight: var(--sui-weight-semibold);
	}

	/* Phones: three covers a row, a title of at most two lines and one line of details. */
	@media (max-width: 640px) {
		.shelf {
			grid-template-columns: repeat(3, minmax(0, 1fr));
			gap: 1.125rem 0.75rem;
		}

		li {
			gap: 0.125rem;
			overflow-wrap: normal;
		}

		.artwork {
			margin-block-end: 0.375rem;
		}

		.shelf li :global(.sui-link) {
			display: -webkit-box;
			color: var(--sui-text-primary);
			text-decoration: none;
			overflow: hidden;
			font-size: 0.8125rem;
			font-weight: var(--sui-weight-bold);
			line-height: 1.25;
			-webkit-box-orient: vertical;
			-webkit-line-clamp: 2;
			line-clamp: 2;
		}

		.meta {
			overflow: hidden;
			font-size: 0.75rem;
			text-overflow: ellipsis;
			white-space: nowrap;
		}
	}
</style>
