<script lang="ts">
	import { ErrorState, Link } from '@smykla-skalski/sui';
	import type { Snippet } from 'svelte';
	import { displayName, editionSummary, type CollectionItem } from '#lib/collection.js';
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
{#if loading && !failure}
	<p role="status">{rows.length ? 'Odświeżanie…' : 'Wczytywanie kolekcji…'}</p>
{/if}
{#if rows.length}
	<ul class="shelf" role="list" aria-label="Egzemplarze w kolekcji" aria-busy={loading}>
		{#each rows as row (row.copy_id)}
			<li>
				<div class="artwork">
					<CollectionCover kind={row.kind} url={row.cover_url} large />
				</div>
				<Link href={`/titles/${row.title_id}`}>{displayName(row)}</Link>
				{#if row.authors?.length}
					<span>{row.authors.join(', ')}</span>
				{/if}
				<span>{editionSummary({ ...row, barcode: null })}</span>
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
		display: flex;
		flex-direction: column;
		gap: var(--sui-space-1);
		overflow-wrap: anywhere;
	}

	.artwork {
		display: grid;
		align-items: end;
		aspect-ratio: 2 / 3;
		--collection-cover-width: 100%;
	}

	li > span {
		color: var(--sui-text-secondary);
		font-size: var(--sui-font-size-meta);
	}
</style>
