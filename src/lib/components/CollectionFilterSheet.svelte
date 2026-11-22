<script lang="ts">
	import { Button, Field, Modal, SegmentedControl, Select, Switch } from '@smykla-skalski/sui';
	import {
		collectionStatusLabels,
		formatLabels,
		loanedLabel,
		options,
		type CollectionFilter
	} from '#lib/collection.js';
	import { collectionSortOptions, type CollectionSort } from '#lib/collection-sort.js';

	let {
		open = $bindable(false),
		sort,
		format,
		status,
		loaned,
		onsort,
		onformat,
		onstatus,
		onloaned
	}: {
		open?: boolean;
		sort: CollectionSort;
		format: CollectionFilter['format'];
		status: CollectionFilter['status'];
		loaned: boolean;
		onsort: (sort: CollectionSort) => void;
		onformat: (format: CollectionFilter['format']) => void;
		onstatus: (status: CollectionFilter['status']) => void;
		onloaned: (loaned: boolean) => void;
	} = $props();

	const all = 'all';
	const formatOptions = [{ value: all, label: 'Każdy' }, ...options(formatLabels)];
	// "Bez egzemplarzy" lists titles instead of copies, and a title has no format of its own.
	const statusOptions = [{ value: all, label: 'Każdy' }, ...options(collectionStatusLabels)];
</script>

<Modal bind:open class="collection-filter-sheet" title="Sortowanie i filtry" closeLabel="Zamknij">
	<div class="fields">
		<Field label="Sortowanie">
			{#snippet control(c)}
				<Select
					{...c}
					options={collectionSortOptions}
					bind:value={() => sort, (v) => v !== null && onsort(v)}
				/>
			{/snippet}
		</Field>
		<Field
			label="Nośnik"
			hint={status === 'none' ? 'Nośnik mają egzemplarze, nie tytuły.' : undefined}
		>
			{#snippet control(c)}
				<Select
					{...c}
					disabled={status === 'none'}
					options={formatOptions}
					bind:value={
						() => format || all,
						(v) => onformat(v === all || v === null ? '' : (v as CollectionFilter['format']))
					}
				/>
			{/snippet}
		</Field>
		<SegmentedControl
			class="status-filter"
			label="Status"
			options={statusOptions}
			bind:value={
				() => status || all,
				(v) => onstatus(v === all || v === null ? '' : (v as CollectionFilter['status']))
			}
		/>
		<Switch
			label={loanedLabel}
			disabled={status === 'none'}
			checked={loaned}
			onchange={(e) => onloaned(e.currentTarget.checked)}
		/>
	</div>
	{#snippet footer()}
		<Button tone="signal" onclick={() => (open = false)}>Gotowe</Button>
	{/snippet}
</Modal>

<style>
	.fields {
		display: grid;
		gap: var(--sui-space-4);
	}

	/* A pick is the brand's selected colour, as on the chips beside the list, not the raspberry
	   accent; the ring of a focused pick takes the raspberry focus colour just outside the thumb. */
	.fields :global(.status-filter) {
		--sui-segment-thumb: var(--ohara-selected);
		--sui-segment-thumb-ink: var(--ohara-on-selected);
		--sui-brand-action-hover: var(--ohara-selected-hover);
		--sui-brand-action-pressed: var(--ohara-selected-pressed);
	}

	.fields :global(.status-filter .selected .sui-segmented-input:focus-visible) {
		outline-color: var(--sui-focus);
		outline-offset: 0;
	}

	/* On a phone the sheet rises from the bottom edge, within reach of the thumb that opened it. */
	@media (max-width: 640px) {
		:global(.collection-filter-sheet[open]) {
			margin-block: auto calc(env(safe-area-inset-bottom) + 0.5rem);
		}
	}
</style>
