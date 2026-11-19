<script lang="ts">
	import { BackLink } from '@smykla-skalski/sui';
	import type { Snippet } from 'svelte';
	import type { Kind } from '#lib/collection.js';
	import CollectionCover from '#lib/components/CollectionCover.svelte';
	import { coversPlaced } from '#lib/cover-transition.js';

	let {
		title,
		overline,
		meta,
		byline,
		kind,
		artwork,
		backHref,
		backLabel,
		coverTitle,
		onback,
		actions
	}: {
		title: string;
		/** The small line above the title, such as "Film · 2014". */
		overline: string;
		/** The line under the title, such as the edition. */
		meta?: string;
		/** Who the title is by. */
		byline?: string;
		kind: Kind;
		artwork?: string;
		backHref: string;
		backLabel: string;
		/** The title whose cover a tap in the collection carries into this hero, and back. */
		coverTitle?: number;
		/** Handles a click on the back link before it follows `backHref`. */
		onback?: (event: MouseEvent) => void;
		/** Controls at the top end of the hero, such as a menu. */
		actions?: Snippet;
	} = $props();

	$effect(() => {
		if (coverTitle !== undefined) coversPlaced();
	});

	let failedArtwork = $state<string>();
	const backdrop = $derived(artwork && artwork !== failedArtwork ? artwork : undefined);
</script>

<header class="hero" class:flat={!backdrop} data-cover-title={coverTitle}>
	<div class="backdrop" aria-hidden="true">
		{#if backdrop}
			{@const url = backdrop}
			{#key url}
				<img src={url} alt="" referrerpolicy="no-referrer" onerror={() => (failedArtwork = url)} />
			{/key}
		{/if}
	</div>
	<div class="bar">
		<span class="plate"><BackLink href={backHref} label={backLabel} onclick={onback} /></span>
		{#if actions}
			<span class="plate round">{@render actions()}</span>
		{/if}
	</div>
	<div class="head">
		<div class="art">
			<CollectionCover {kind} url={artwork} large />
		</div>
		<div class="text">
			<p class="overline">{overline}</p>
			<h1>{title}</h1>
			{#if meta}
				<p class="meta">{meta}</p>
			{/if}
			{#if byline}
				<p class="meta">{byline}</p>
			{/if}
		</div>
	</div>
</header>

<style>
	/* The cover's width sets the hero's size: the backdrop's height and how far the cover reaches
	   up into it keep the phone's proportions, 16rem and 7.25rem to an 8.25rem cover. */
	.hero {
		--hero-cover: 8.25rem;
		--hero-backdrop: calc(var(--hero-cover) * 16 / 8.25);
		--hero-overlap: calc(var(--hero-cover) * 7.25 / 8.25);
		position: relative;
		isolation: isolate;
		padding: 1rem var(--sui-pane-gutter, 1.5rem) 0;
	}

	/* Wider than a phone the collection shelf's covers outgrow the phone's hero cover, so the
	   hero takes a 200 px cover and a cover opened from the shelf still grows into it. */
	@media not (max-width: 640px) {
		.hero {
			--hero-cover: 12.5rem;
		}
	}

	/* The artwork, blurred and dimmed at the top, fading into the page. */
	.backdrop {
		position: absolute;
		inset: 0 0 auto;
		block-size: var(--hero-backdrop);
		overflow: hidden;
		z-index: -1;
		background: var(--sui-surface-inset);
	}

	.backdrop img {
		position: absolute;
		inset: -2.5rem;
		inline-size: calc(100% + 5rem);
		block-size: calc(100% + 5rem);
		object-fit: cover;
		filter: blur(28px) saturate(1.2);
		transform: scale(1.1);
	}

	.backdrop::after {
		content: '';
		position: absolute;
		inset: 0;
		background: linear-gradient(
			to bottom,
			rgb(0 0 0 / 25%) 0%,
			transparent 35%,
			color-mix(in srgb, var(--sui-canvas) 55%, transparent) 62%,
			color-mix(in srgb, var(--sui-canvas) 88%, transparent) 82%,
			var(--sui-canvas) 100%
		);
	}

	/* Without artwork the hero is a flat inset band, still fading into the page. */
	.flat .backdrop::after {
		background: linear-gradient(
			to bottom,
			transparent 35%,
			color-mix(in srgb, var(--sui-canvas) 55%, transparent) 62%,
			color-mix(in srgb, var(--sui-canvas) 88%, transparent) 82%,
			var(--sui-canvas) 100%
		);
	}

	.bar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}

	/* Plates keep the back link and the menu legible over any artwork. */
	.plate {
		display: inline-flex;
		align-items: center;
		min-block-size: 2.25rem;
		padding-inline: 0.75rem;
		border-radius: 999px;
		background: color-mix(in srgb, var(--sui-surface-base) 72%, transparent);
		backdrop-filter: blur(12px);
	}

	.plate.round {
		padding-inline: 0.125rem;
	}

	.plate.round :global(button) {
		border-radius: 999px;
	}

	/* The cover's top stands `--hero-overlap` above the backdrop's foot, under the hero's 1rem
	   padding and a 2.25rem bar; a touch screen's taller bar lowers it a little, as before. */
	.head {
		display: flex;
		align-items: flex-end;
		gap: 1rem;
		margin-block-start: calc(var(--hero-backdrop) - var(--hero-overlap) - 3.25rem);
	}

	/* The cover keeps its place however tall the text beside it grows, so a cover carried in from
	   the collection stays where it landed when the title's lines arrive. */
	.art {
		flex: none;
		align-self: flex-start;
		--collection-cover-width: var(--hero-cover);
	}

	.art :global(.cover) {
		box-sizing: border-box;
		box-shadow:
			0 2px 4px rgb(0 0 0 / 18%),
			0 22px 40px -16px rgb(0 0 0 / 60%);
	}

	.text {
		display: grid;
		gap: 0.25rem;
		min-inline-size: 0;
		padding-block-end: 0.375rem;
	}

	.text p {
		margin: 0;
		overflow-wrap: anywhere;
	}

	.overline {
		font-size: 0.75rem;
		font-weight: 700;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--sui-text-muted);
	}

	h1 {
		margin: 0;
		font-family: var(--ohara-serif);
		font-size: clamp(1.625rem, 6vw, 2.25rem);
		font-weight: 600;
		line-height: 1.05;
		letter-spacing: -0.02em;
		color: var(--ohara-heading);
		overflow-wrap: anywhere;
	}

	.meta {
		font-size: 0.875rem;
		color: var(--sui-text-muted);
	}
</style>
