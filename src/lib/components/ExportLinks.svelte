<script lang="ts">
	import { Button } from '@smykla-skalski/sui';
	import { exportChoices, exportHref } from '#lib/export.js';

	const id = $props.id();
</script>

<section class="export" aria-labelledby={`${id}-export`}>
	<h2 id={`${id}-export`}>Eksportuj kolekcję</h2>
	<div class="choices">
		{#each exportChoices as choice (choice.format)}
			<Button href={exportHref(choice.format)} download="">{choice.label}</Button>
		{/each}
	</div>
</section>

<style>
	.export {
		display: grid;
		gap: var(--sui-space-2, 0.5rem);
	}

	h2 {
		margin: 0;
		font-size: var(--sui-text-sm, 0.875rem);
		font-weight: 600;
	}

	.choices {
		display: flex;
		flex-wrap: wrap;
		gap: var(--sui-space-2, 0.5rem);
	}
</style>
