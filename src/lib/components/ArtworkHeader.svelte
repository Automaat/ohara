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
		coverAction,
		facts
	}: {
		title: string;
		description?: string;
		backHref: string;
		backLabel: string;
		cover?: string;
		coverLabel: string;
		/** Makes the cover a button, marked with a camera badge, that opens a dialog to change it. */
		coverAction?: { label: string; onclick: () => void };
		facts?: Snippet;
	} = $props();

	const uid = $props.id();
	const captionID = `${uid}-cover`;
	let failedCover = $state<string>();
	const shown = $derived(cover && cover !== failedCover ? cover : undefined);
</script>

{#snippet art()}
	{#if shown}
		{@const imageURL = shown}
		{#key shown}
			<img
				src={imageURL}
				alt={coverLabel}
				referrerpolicy="no-referrer"
				onerror={() => (failedCover = imageURL)}
			/>
		{/key}
	{:else}
		<span class="placeholder" id={captionID}>Brak okładki</span>
	{/if}
{/snippet}

<div class="band">
	<BackLink href={backHref} label={backLabel} />
	<div class="hero">
		<PageHeader class="artwork-heading" {title} {description} {facts} />
		<figure>
			{#if coverAction}
				<button
					type="button"
					class="change"
					aria-label={coverAction.label}
					aria-describedby={captionID}
					aria-haspopup="dialog"
					onclick={coverAction.onclick}
				>
					{@render art()}
					<span class="badge" aria-hidden="true">
						<svg
							viewBox="0 0 24 24"
							width="16"
							height="16"
							fill="none"
							stroke="currentColor"
							stroke-width="1.75"
							stroke-linecap="round"
							stroke-linejoin="round"
						>
							<path
								d="M4 8.5A1.5 1.5 0 0 1 5.5 7h2.2l1.5-2h5.6l1.5 2h2.2A1.5 1.5 0 0 1 20 8.5v9a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 17.5z"
							/>
							<circle cx="12" cy="13" r="3.25" />
						</svg>
					</span>
				</button>
			{:else}
				{@render art()}
			{/if}
			{#if shown}
				<figcaption id={captionID} aria-hidden="true">{coverLabel}</figcaption>
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
	.change {
		position: relative;
		display: block;
		inline-size: 100%;
		padding: 0;
		border: 0;
		border-radius: var(--sui-radius-control);
		background: none;
		color: inherit;
		font: inherit;
		cursor: pointer;
	}
	.change:focus-visible {
		outline: var(--sui-focus-ring-width) solid var(--sui-focus);
		outline-offset: 2px;
	}
	.change .placeholder {
		background: var(--sui-surface-inset);
	}
	.badge {
		position: absolute;
		inset-block-end: -0.5rem;
		inset-inline-end: -0.5rem;
		display: grid;
		place-items: center;
		inline-size: 1.75rem;
		block-size: 1.75rem;
		border-radius: 50%;
		background: var(--sui-brand-action);
		color: var(--sui-on-brand-action);
		box-shadow: 0 0 0 2px var(--sui-surface-base);
	}
	.change:hover .badge {
		background: var(--sui-brand-action-hover);
	}
	.change:active .badge {
		background: var(--sui-brand-action-pressed);
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
