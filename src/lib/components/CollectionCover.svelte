<script lang="ts">
	import { Icon } from '@smykla-skalski/sui';
	import type { Kind } from '#lib/collection.js';

	let { kind, url }: { kind: Kind; url?: string } = $props();
	let failedURL = $state<string>();
	let loadedURL = $state<string>();

	const thumbnail = $derived(
		url
			?.replace(/^https:\/\/image\.tmdb\.org\/t\/p\/w342\//, 'https://image.tmdb.org/t/p/w154/')
			.replace(/^(https:\/\/covers\.openlibrary\.org\/b\/id\/\d+)-L\.jpg$/, '$1-M.jpg')
	);
</script>

<span class="cover" class:square={kind === 'music'} aria-hidden="true">
	<Icon name="file-missing" />
	{#if thumbnail && thumbnail !== failedURL}
		{#key thumbnail}
			<img
				src={thumbnail}
				class:loaded={loadedURL === thumbnail}
				alt=""
				width="64"
				height={kind === 'music' ? 64 : 96}
				loading="lazy"
				decoding="async"
				referrerpolicy="no-referrer"
				onload={() => (loadedURL = thumbnail)}
				onerror={() => (failedURL = thumbnail)}
			/>
		{/key}
	{/if}
</span>

<style>
	.cover {
		position: relative;
		display: grid;
		place-items: center;
		inline-size: var(--collection-cover-width, 4rem);
		aspect-ratio: 2 / 3;
		border: 1px solid var(--sui-border-divider);
		border-radius: var(--sui-radius-control);
		background: var(--sui-surface-base);
		color: var(--sui-text-muted);
	}

	.square {
		aspect-ratio: 1;
	}

	img {
		position: absolute;
		inset: 0;
		inline-size: 100%;
		block-size: 100%;
		object-fit: contain;
		background: inherit;
		opacity: 0;
	}

	img.loaded {
		opacity: 1;
	}
</style>
