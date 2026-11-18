<script lang="ts">
	import { BackLink, PageHeader } from '@smykla-skalski/sui';
	import type { Snippet } from 'svelte';

	let {
		title,
		description,
		backHref,
		backLabel,
		cover,
		coverLabel,
		facts
	}: {
		title: string;
		description?: string;
		backHref: string;
		backLabel: string;
		cover?: string;
		coverLabel: string;
		facts?: Snippet;
	} = $props();

	let failedCover = $state<string>();
</script>

<div class="band">
	<BackLink href={backHref} label={backLabel} />
	<div class="hero">
		<PageHeader class="artwork-heading" {title} {description} {facts} />
		<figure>
			{#if cover && cover !== failedCover}
				{@const imageURL = cover}
				{#key cover}
					<img
						src={imageURL}
						alt={coverLabel}
						referrerpolicy="no-referrer"
						onerror={() => (failedCover = imageURL)}
					/>
				{/key}
				<figcaption aria-hidden="true">{coverLabel}</figcaption>
			{:else}
				<p class="placeholder">Brak okładki</p>
			{/if}
		</figure>
	</div>
</div>

<style>
	.band {
		padding: var(--sui-pane-gutter, 1.5rem);
		background: var(--sui-surface-base);
		border-block-end: var(--sui-stroke) solid var(--sui-border-band);
	}
	.hero {
		display: grid;
		grid-template-columns: clamp(5rem, 23%, 13rem) minmax(0, 1fr);
		align-items: center;
		gap: clamp(0.75rem, 4vw, 2rem);
		margin-block-start: 1rem;
	}
	.hero :global(.artwork-heading) {
		grid-column: 2;
		grid-row: 1;
		padding: 0;
		margin: 0;
		border: 0;
	}
	figure {
		grid-column: 1;
		grid-row: 1;
		margin: 0;
		min-inline-size: 0;
	}
	img,
	.placeholder {
		display: block;
		inline-size: 100%;
		aspect-ratio: 4 / 5;
		object-fit: contain;
		border-radius: var(--sui-radius-control);
		background: var(--sui-surface-raised, var(--sui-row-hover));
	}
	.placeholder {
		display: grid;
		place-items: center;
		box-sizing: border-box;
		padding: 0.25rem;
		margin: 0;
		text-align: center;
	}
	figcaption {
		margin-block-start: 0.25rem;
	}
	figcaption,
	.placeholder {
		color: var(--sui-text-muted);
		font-size: var(--sui-font-size-meta);
	}
</style>
