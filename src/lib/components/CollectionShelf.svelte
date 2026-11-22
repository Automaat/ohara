<script lang="ts">
	import { ErrorState, Link } from '@smykla-skalski/sui';
	import { tick, type Snippet } from 'svelte';
	import { displayName, shelfLine, type CollectionItem } from '#lib/collection.js';
	import { drawnCount, firstTiles, tilesPerStep, type ShelfPlace } from '#lib/shelf-drawing.js';
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

	/** The copies whose tiles the last draw left in the page. */
	let drawnKeys: ReadonlySet<number> = new Set();
	/** How many leading rows are drawn: set anew when the rows change, then grown a part a task. */
	let count = $derived(
		drawnCount(
			rows.map((row) => row.copy_id),
			drawnKeys,
			firstTiles
		)
	);
	const drawn = $derived(count >= rows.length ? rows : rows.slice(0, count));
	let list = $state<HTMLElement>();
	/**
	 * The height every tile will take, from the tiles drawn so far. The list keeps it while the
	 * rest are drawn, so the page scrolls as far as it will once they are all in it.
	 */
	let estimate = $state(0);
	/** The reader went to the end of the page while tiles were still being drawn. */
	let wentToEnd = false;

	/** Where the drawn tiles end, as a position in the document. */
	function drawnBottom(el: HTMLElement): number {
		return (el.lastElementChild ?? el).getBoundingClientRect().bottom + window.scrollY;
	}

	function listTop(el: HTMLElement): number {
		return el.getBoundingClientRect().top + window.scrollY;
	}

	$effect(() => {
		drawnKeys = new Set(drawn.map((row) => row.copy_id));
		if (!list || drawn.length >= rows.length) return;
		estimate = Math.ceil(((drawnBottom(list) - listTop(list)) * rows.length) / drawn.length);
	});

	$effect.pre(() => {
		void rows;
		wentToEnd = false;
	});

	function drawMore() {
		count = Math.min(rows.length, count + tilesPerStep);
	}

	$effect(() => {
		if (count >= rows.length) return;
		const timer = setTimeout(drawMore);
		return () => clearTimeout(timer);
	});

	/** Once the last part is drawn, a reader who went to the end of the page is taken to it. */
	$effect(() => {
		if (count < rows.length || !wentToEnd) return;
		wentToEnd = false;
		window.scrollTo(window.scrollX, document.documentElement.scrollHeight);
	});

	/** A reader who scrolls past the drawn tiles gets them drawn at once. */
	function onscroll() {
		if (!list || count >= rows.length) return;
		const page = document.documentElement;
		wentToEnd = window.scrollY + window.innerHeight >= page.scrollHeight - 1;
		void drawTo(window.scrollY);
	}

	/** The first tile at the top of the screen, or null when none is drawn there. */
	export function topTile(): ShelfPlace | null {
		for (const tile of list?.children ?? []) {
			const box = tile.getBoundingClientRect();
			const key = Number(tile.getAttribute('data-cover-key'));
			if (box.bottom > 0 && Number.isInteger(key)) return { key, offset: box.top };
		}
		return null;
	}

	/** Draws the list down to the tile of `place` and scrolls it back to where it was on screen. */
	export async function showTile(place: ShelfPlace): Promise<void> {
		const index = rows.findIndex((row) => row.copy_id === place.key);
		if (index < 0) return;
		if (count <= index) count = Math.min(rows.length, index + 1 + tilesPerStep);
		await tick();
		const tile = list?.querySelector(`[data-cover-key="${place.key}"]`);
		if (!tile) return;
		window.scrollTo(
			window.scrollX,
			window.scrollY + tile.getBoundingClientRect().top - place.offset
		);
	}

	/**
	 * Draws the list down to `y`, a position in the document, and a screen below it, so a scroll
	 * position kept further down can be restored. It draws what the tiles drawn so far say it
	 * needs, and a part more, at once: a cover carried back lands only if its tile is drawn
	 * within the transition's wait, and the reader waits on that transition anyway. It yields
	 * before measuring again, so the browser lays the new tiles out in a task of its own.
	 */
	export async function drawTo(y: number): Promise<void> {
		if (!list || count >= rows.length) return;
		const top = listTop(list);
		const height = drawnBottom(list) - top;
		const reach = y + window.innerHeight - top;
		if (height >= reach) return;
		const needed = Math.ceil((reach / Math.max(height, 1)) * count) + tilesPerStep;
		count = Math.min(rows.length, Math.max(needed, count + tilesPerStep));
		await tick();
		await new Promise((resolve) => setTimeout(resolve));
		return drawTo(y);
	}
</script>

<svelte:window {onscroll} />

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
	<!-- Each tile carries its place among all rows, so the list counts every copy while the
	     last parts are still being drawn. -->
	<ul
		class="shelf"
		role="list"
		aria-label="Egzemplarze w kolekcji"
		aria-busy={loading}
		style:min-height={count < rows.length && estimate ? `${estimate}px` : undefined}
		bind:this={list}
	>
		{#each drawn as row, i (row.copy_id)}
			{@const name = displayName(row)}
			{@const wish = row.status === 'wishlist'}
			<li
				data-cover-title={row.title_id}
				data-cover-key={row.copy_id}
				aria-setsize={rows.length}
				aria-posinset={i + 1}
			>
				<div class="artwork">
					<CollectionCover kind={row.kind} url={row.cover_url} large {name} {wish} />
				</div>
				<Link href={`/titles/${row.title_id}`}>{name}</Link>
				<span class="meta" class:wish>{shelfLine(row)}</span>
			</li>
		{/each}
	</ul>
{:else if !loading && !failure}
	{@render empty()}
{/if}

<style>
	/* Rows keep their own height while the list holds the space of tiles still to be drawn. */
	.shelf {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(9rem, 1fr));
		align-content: start;
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

	/* A wish's line, "Lista życzeń · do 80 zł", in the accent colour. */
	.meta.wish {
		color: var(--sui-brand-action-text);
		font-weight: var(--sui-weight-semibold);
		font-variant-numeric: tabular-nums;
	}

	/* Phones: three covers a row, a title of at most two lines and one line of details, two for
	   a wish and its limit. */
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

		.meta.wish {
			white-space: normal;
			overflow-wrap: anywhere;
		}
	}
</style>
