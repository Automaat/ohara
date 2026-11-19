<script lang="ts">
	import { VisuallyHidden } from '@smykla-skalski/sui';

	let { label }: { label: string } = $props();

	/** A load answered within this many milliseconds draws no slots, so they never flash. */
	const patience = 1000;
	let late = $state(false);

	$effect(() => {
		const timer = setTimeout(() => (late = true), patience);
		return () => clearTimeout(timer);
	});
</script>

<!--
@component
A shelf's first load: empty cover slots in the shelf's own grid, drawn still, once the wait
passes a second; a quicker answer draws nothing. The status says what is loading either way.
-->

<VisuallyHidden role="status">{label}</VisuallyHidden>
{#if late}
	<ul class="slots" aria-hidden="true">
		{#each { length: 12 }, index (index)}
			<li>
				<span class="cover"></span>
				<span class="bar"></span>
				<span class="bar short"></span>
			</li>
		{/each}
	</ul>
{/if}

<style>
	/* The grid of `CollectionShelf` and `TitleShelf`, so the covers arrive where the slots were. */
	.slots {
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
		gap: var(--sui-space-2);
	}

	.cover {
		aspect-ratio: 2 / 3;
		border-radius: var(--sui-radius-control);
		background: var(--sui-placeholder);
	}

	.bar {
		block-size: 0.625rem;
		inline-size: 80%;
		border-radius: var(--sui-radius-pill);
		background: var(--sui-placeholder);
	}

	.bar.short {
		inline-size: 55%;
	}

	/* Phones: three covers a row, as the shelves lay them out. */
	@media (max-width: 640px) {
		.slots {
			grid-template-columns: repeat(3, minmax(0, 1fr));
			gap: 1.125rem 0.75rem;
		}

		.cover {
			margin-block-end: 0.25rem;
		}
	}

	@media (forced-colors: active) {
		.cover,
		.bar {
			outline: 1px solid GrayText;
			outline-offset: -1px;
		}
	}
</style>
