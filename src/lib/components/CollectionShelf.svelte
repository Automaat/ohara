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
	/** The tile at the top of the screen when the reader last scrolled. */
	let place: ShelfPlace | null = null;
	/** A resize is being laid out, so a scroll in its frame is the browser's, not the reader's. */
	let resizing = false;

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
		place = null;
	});

	function drawMore() {
		count = Math.min(rows.length, count + tilesPerStep);
	}

	$effect(() => {
		if (count >= rows.length) return;
		const timer = setTimeout(drawMore);
		return () => clearTimeout(timer);
	});

	/** Draws every tile left, then shows the end of the page. */
	async function drawToEnd() {
		count = rows.length;
		await tick();
		window.scrollTo(window.scrollX, document.documentElement.scrollHeight);
	}

	/**
	 * End, Ctrl+End or Cmd+Down while tiles are still being drawn draws them all before the
	 * browser scrolls, so it scrolls to the last copy rather than to the height kept for them.
	 */
	function onkeydown(event: KeyboardEvent) {
		if (count >= rows.length || event.defaultPrevented) return;
		const target = event.target;
		if (
			target instanceof HTMLElement &&
			target.closest('input, textarea, select, [contenteditable]')
		)
			return;
		if (event.key === 'End' || (event.metaKey && event.key === 'ArrowDown')) void drawToEnd();
	}

	/**
	 * Notes the tile at the top of the screen, and draws the tiles a scroll reaches: at the end of
	 * the page, as when the scroll bar is dragged down, every tile left.
	 */
	function onscroll() {
		if (!resizing) place = topTile();
		if (!list || count >= rows.length) return;
		const page = document.documentElement;
		if (window.scrollY + window.innerHeight >= page.scrollHeight - 1) void drawToEnd();
		else void drawTo(window.scrollY);
	}

	/** The browser runs a resize's scroll and animation frame steps in that order. */
	function onresize() {
		resizing = true;
		requestAnimationFrame(() => (resizing = false));
	}

	/** The first tile at the top of the screen, or null when none is drawn there. */
	function topTile(): ShelfPlace | null {
		const tiles = list?.children;
		if (!tiles?.length) return null;
		let low = 0;
		let high = tiles.length - 1;
		if (tiles[high].getBoundingClientRect().bottom <= 0) return null;
		while (low < high) {
			const middle = Math.floor((low + high) / 2);
			if (tiles[middle].getBoundingClientRect().bottom > 0) high = middle;
			else low = middle + 1;
		}
		const key = Number(tiles[low].getAttribute('data-cover-key'));
		return Number.isInteger(key) ? { key, offset: tiles[low].getBoundingClientRect().top } : null;
	}

	/**
	 * The tile at the top of the screen as the reader last saw it: the one noted at their last
	 * scroll, which a resize does not move, or else the one there now.
	 */
	export function shownTile(): ShelfPlace | null {
		return place ?? topTile();
	}

	/** Draws the list down to the tile of `place` and scrolls it back to where it was on screen. */
	export async function showTile(shown: ShelfPlace): Promise<void> {
		const index = rows.findIndex((row) => row.copy_id === shown.key);
		if (index < 0) return;
		if (count <= index) count = Math.min(rows.length, index + 1 + tilesPerStep);
		await tick();
		const tile = list?.querySelector(`[data-cover-key="${shown.key}"]`);
		if (!tile) return;
		window.scrollTo(
			window.scrollX,
			window.scrollY + tile.getBoundingClientRect().top - shown.offset
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

<svelte:window {onscroll} {onkeydown} {onresize} />

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
			{@const sold = row.status === 'sold'}
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
				<span class="meta" class:wish class:sold>{shelfLine(row)}</span>
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

	/* A sale's line, "Sprzedany 10 maj 2026 · 35 zł", in the details' colour. */
	.meta.sold {
		font-variant-numeric: tabular-nums;
	}

	/* Phones: three covers a row, a title of at most two lines and one line of details, two for
	   a wish and its limit or a sale. */
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

		.meta.wish,
		.meta.sold {
			white-space: normal;
			overflow-wrap: anywhere;
		}
	}
</style>
