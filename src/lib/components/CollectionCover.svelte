<script lang="ts">
	import type { Kind } from '#lib/collection.js';

	let {
		kind,
		url,
		large = false,
		name,
		wish = false,
		lent = false,
		ordered = false,
		eager = false
	}: {
		kind: Kind;
		url?: string;
		large?: boolean;
		/** The title written on a cover without artwork; a small cover shows only the kind's icon. */
		name?: string;
		/** A wishlist copy: the cover carries a heart in the accent colour. */
		wish?: boolean;
		/** A copy out on loan: the cover carries an arrow leaving a box. */
		lent?: boolean;
		/** A copy on order: the cover carries a parcel. */
		ordered?: boolean;
		/** Load the image at once, as a page meant for printing needs every cover. */
		eager?: boolean;
	} = $props();
	let failedURL = $state<string>();
	let loadedURL = $state<string>();

	const thumbnail = $derived(
		large
			? url
			: url
					?.replace(/^https:\/\/image\.tmdb\.org\/t\/p\/w342\//, 'https://image.tmdb.org/t/p/w154/')
					.replace(/^(https:\/\/covers\.openlibrary\.org\/b\/id\/\d+)-L\.jpg$/, '$1-M.jpg')
	);
	/** No cover URL, or its image failed to load. */
	const blank = $derived(!thumbnail || thumbnail === failedURL);
</script>

<span
	class="cover"
	class:square={kind === 'music'}
	class:book={kind === 'book'}
	class:blank
	aria-hidden="true"
>
	{#if !blank}
		{#key thumbnail}
			<img
				src={thumbnail}
				class:loaded={loadedURL === thumbnail}
				alt=""
				loading={eager ? 'eager' : 'lazy'}
				decoding="async"
				referrerpolicy="no-referrer"
				onload={() => (loadedURL = thumbnail)}
				onerror={() => (failedURL = thumbnail)}
			/>
		{/key}
	{:else}
		<svg class="kind" viewBox="0 0 24 24">
			{#if kind === 'movie'}
				<rect x="3.5" y="4" width="17" height="16" rx="2" />
				<path d="M3.5 9h17M3.5 15h17M8 4v16M16 4v16" />
			{:else if kind === 'book'}
				<path d="M4 19.5V5.5A2.5 2.5 0 0 1 6.5 3H20v18H6.5A2.5 2.5 0 0 1 4 19.5Z" />
				<path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20" />
			{:else}
				<circle cx="12" cy="12" r="8.5" />
				<circle cx="12" cy="12" r="2.2" />
			{/if}
		</svg>
		{#if name}<span class="name">{name}</span>{/if}
	{/if}
	{#if wish}
		<span class="wish">
			<svg viewBox="0 0 24 24">
				<path
					d="M12 20s-7.5-4.6-7.5-10.2A4.3 4.3 0 0 1 12 7.2a4.3 4.3 0 0 1 7.5 2.6C19.5 15.4 12 20 12 20z"
				/>
			</svg>
		</span>
	{:else if lent}
		<span class="badge lent">
			<svg viewBox="0 0 24 24">
				<path d="M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5" />
			</svg>
		</span>
	{:else if ordered}
		<span class="badge ordered">
			<svg viewBox="0 0 24 24">
				<path d="M21 8 12 3 3 8v8l9 5 9-5V8Z" />
				<path d="m3 8 9 5 9-5M12 13v8" />
			</svg>
		</span>
	{/if}
</span>

<style>
	.cover {
		position: relative;
		/* The border and padding stay inside the 2:3 or square box, so a cover fills its slot
		   exactly and every title under a row of covers starts at the same height. */
		box-sizing: border-box;
		display: grid;
		place-items: center;
		inline-size: var(--collection-cover-width, 4rem);
		aspect-ratio: 2 / 3;
		overflow: hidden;
		border: 1px solid var(--sui-border-divider);
		border-radius: var(--sui-radius-control);
		background: var(--sui-surface-base);
		color: var(--sui-text-muted);
		/* The spine and the heart stack inside the cover, under whatever covers the tile. */
		isolation: isolate;
	}

	.square {
		aspect-ratio: 1;
	}

	/* No artwork: the kind's icon on the inset colour, with the title under it when given. */
	.blank {
		grid-template-rows: minmax(0, 1fr);
		background: var(--sui-surface-inset);
	}

	.kind {
		inline-size: min(1.375rem, 45%);
		aspect-ratio: 1;
		fill: none;
		stroke: currentColor;
		stroke-width: 1.75;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	/* Fixed margins: a percentage would follow the containing block, which is a whole row where
	   a small cover sits in a list. */
	.name {
		display: -webkit-box;
		max-inline-size: calc(100% - 1rem);
		margin: 0 0.5rem 0.625rem;
		overflow: hidden;
		color: var(--sui-text-primary);
		font-family: var(--ohara-serif);
		font-size: 0.75rem;
		line-height: 1.15;
		text-align: center;
		overflow-wrap: anywhere;
		-webkit-box-orient: vertical;
		-webkit-line-clamp: 3;
		line-clamp: 3;
	}

	/* A book's spine: a soft shadow along its left edge. */
	.book::before {
		content: '';
		position: absolute;
		inset-block: 0;
		left: 0;
		z-index: 1;
		inline-size: 5%;
		background: linear-gradient(90deg, rgb(0 0 0 / 22%), transparent);
		pointer-events: none;
	}

	img {
		position: absolute;
		inset: 0;
		inline-size: 100%;
		block-size: 100%;
		object-fit: contain;
		background: inherit;
		opacity: 0;
		filter: brightness(var(--ohara-cover-brightness, 1));
	}

	img.loaded {
		opacity: 1;
	}

	.wish,
	.badge {
		position: absolute;
		inset-block-start: 0.375rem;
		inset-inline-end: 0.375rem;
		z-index: 2;
		display: grid;
		place-items: center;
		inline-size: 1.5rem;
		block-size: 1.5rem;
		border-radius: 50%;
		background: color-mix(in srgb, var(--sui-surface-base) 94%, transparent);
		box-shadow: 0 2px 6px rgb(0 0 0 / 20%);
		color: var(--sui-brand-action);
	}

	.wish svg {
		inline-size: 0.875rem;
		block-size: 0.875rem;
		fill: currentColor;
	}

	.lent {
		color: var(--sui-text-primary);
	}

	.lent svg {
		inline-size: 0.875rem;
		block-size: 0.875rem;
		fill: none;
		stroke: currentColor;
		stroke-width: 2.25;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	/* A parcel in the muted ink: the copy is bought, not yet on the shelf. */
	.ordered {
		color: var(--sui-text-muted);
	}

	.ordered svg {
		inline-size: 0.875rem;
		block-size: 0.875rem;
		fill: none;
		stroke: currentColor;
		stroke-width: 2;
		stroke-linejoin: round;
	}
</style>
