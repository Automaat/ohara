<script lang="ts">
	import { Button, ErrorState, Link } from '@smykla-skalski/sui';
	import type { Snippet } from 'svelte';
	import { displayName, uncopiedLine, type Title } from '#lib/collection.js';
	import { addCopyHref } from '#lib/title-detail.js';
	import CollectionCover from './CollectionCover.svelte';
	import ShelfSkeleton from './ShelfSkeleton.svelte';

	let {
		titles,
		loading,
		failure,
		onretry,
		empty
	}: {
		titles: readonly Title[];
		loading: boolean;
		failure: string | null;
		onretry: () => void;
		empty: Snippet;
	} = $props();
</script>

{#if failure}
	<ErrorState
		title="Nie udało się wczytać tytułów"
		description={failure}
		{onretry}
		busy={loading}
		overContent={titles.length > 0}
		retryLabel="Spróbuj ponownie"
		retryingLabel="Ponawianie…"
	/>
{/if}
{#if loading && !failure}
	{#if titles.length}
		<p role="status">Odświeżanie…</p>
	{:else}
		<ShelfSkeleton label="Wczytywanie tytułów…" />
	{/if}
{/if}
{#if titles.length}
	<ul class="shelf" role="list" aria-label="Tytuły bez egzemplarzy" aria-busy={loading}>
		{#each titles as t (t.id)}
			{@const name = displayName(t)}
			<li data-cover-title={t.id}>
				<div class="artwork">
					<CollectionCover kind={t.kind} url={t.cover_url} large />
				</div>
				<span class="name"><Link href={`/titles/${t.id}`}>{name}</Link></span>
				<span class="meta">{uncopiedLine(t)}</span>
				<span class="add">
					<Button href={addCopyHref(`/titles/${t.id}`)} aria-label={`Dodaj egzemplarz: ${name}`}>
						Dodaj egzemplarz
					</Button>
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

	/* The title's link covers the tile, so a tap on the cover opens the title; the add button
	   sits above it. */
	.name :global(a)::after {
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

	.add {
		position: relative;
		display: grid;
		margin-block-start: auto;
		padding-block-start: var(--sui-space-1);
	}

	/* Phones: three covers a row, as in the collection, a title of at most two lines. */
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

		.name :global(.sui-link) {
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
