<script lang="ts">
	import {
		Button,
		Chip,
		DataTable,
		EmptyState,
		FactList,
		Field,
		Link,
		ListToolbar,
		PageHeader,
		Pill,
		SegmentedControl,
		Select,
		type DataTableColumn
	} from '@smykla-skalski/sui';
	import { onMount, tick, untrack } from 'svelte';
	import { afterNavigate, replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import { errorMessage, request } from '#lib/api.js';
	import {
		collectionSearch,
		collectionURL,
		displayName,
		editionSummary,
		formatLabels,
		formatPLN,
		kindLabels,
		options,
		readCollectionFilter,
		spentLabel,
		statusLabels,
		type CollectionFilter,
		type CollectionItem,
		type CollectionView,
		type Totals
	} from '#lib/collection.js';
	import { collectionSortOptions, sortCopies, type CollectionSort } from '#lib/collection-sort.js';
	import { purchaseDate, resultCount } from '#lib/polish.js';
	import CollectionRows from '#lib/components/CollectionRows.svelte';
	import CollectionShelf from '#lib/components/CollectionShelf.svelte';
	import type { Snapshot } from './$types';

	type Row = CollectionItem & Record<string, unknown>;
	type Layout = 'shelf' | 'table';

	const layoutKey = 'ohara.collection.layout';
	const layoutOptions: { value: Layout; label: string }[] = [
		{ value: 'shelf', label: 'Półka' },
		{ value: 'table', label: 'Tabela' }
	];
	let layout = $state<Layout>('shelf');

	onMount(() => {
		try {
			if (localStorage.getItem(layoutKey) === 'table') layout = 'table';
		} catch {
			// Storage can be unavailable in restricted browser contexts.
		}
	});

	function setLayout(value: Layout) {
		layout = value;
		try {
			localStorage.setItem(layoutKey, value);
		} catch {
			// Keep the current view usable for this visit.
		}
	}

	const sort = $derived(page.state.collectionSort ?? 'title');

	function setSort(value: CollectionSort | null) {
		if (value !== null) {
			replaceState(window.location.href, { ...page.state, collectionSort: value });
		}
	}

	const emptyFilter: CollectionFilter = { q: '', kind: '', format: '', status: '' };
	let filter = $state(untrack(() => readCollectionFilter(page.url.searchParams)));
	let query = $state(untrack(() => filter.q));
	let view = $state<CollectionView | null>(null);
	let totals = $state<Totals | null>(null);
	let failure = $state<string | null>(null);
	let loading = $state(true);
	let seq = 0;
	let restoreScroll: [number, number] | undefined;
	let pendingTotals: Promise<void> | undefined;

	export const snapshot: Snapshot<[number, number]> = {
		capture: () => [window.scrollX, window.scrollY],
		restore: (position) => (restoreScroll = position)
	};

	function loadTotals() {
		if (totals) return;
		pendingTotals ??= request<CollectionView>(fetch, 'GET', '/api/collection')
			.catch(() => null)
			.then((full) => {
				if (full) totals ??= full.totals;
				pendingTotals = undefined;
			});
		return pendingTotals;
	}

	async function load() {
		const mine = ++seq;
		const url = collectionURL(filter);
		loading = true;
		const totalsReady = url === '/api/collection' ? undefined : loadTotals();
		try {
			const got = await request<CollectionView>(fetch, 'GET', url);
			if (mine !== seq) return;
			view = got;
			if (url === '/api/collection') totals = got.totals;
			failure = null;
		} catch (err) {
			if (mine === seq) failure = errorMessage(err);
		} finally {
			if (mine === seq) loading = false;
		}
		if (mine === seq && !failure && restoreScroll) {
			// Shelf totals can wrap and change the height above the table.
			await totalsReady;
			await tick();
			if (mine === seq && restoreScroll) {
				window.scrollTo(...restoreScroll);
				restoreScroll = undefined;
			}
		}
	}

	afterNavigate(() => {
		filter = readCollectionFilter(new URLSearchParams(window.location.search));
		query = filter.q;
		void load();
	});

	function set<K extends keyof CollectionFilter>(key: K, value: CollectionFilter[K]) {
		if (key === 'q') query = value;
		updateFilter({ ...filter, [key]: value });
	}

	function clearFilters() {
		query = '';
		updateFilter(emptyFilter);
	}

	function updateFilter(next: CollectionFilter) {
		filter = next;
		const search = collectionSearch(next);
		const { pathname, search: current, hash } = window.location;
		if (current === search) return;
		replaceState(`${pathname}${search}${hash}`, page.state);
		restoreScroll = undefined;
		void load();
	}

	const columns: DataTableColumn<Row>[] = [
		{ key: 'name', label: 'Tytuł' },
		{ key: 'status', label: 'Status' },
		{ key: 'price_cents', label: 'Cena', numeric: true },
		{ key: 'shop', label: 'Sklep' },
		{ key: 'purchased_on', label: 'Kupiono', nowrap: true }
	];

	const rows = $derived(sortCopies((view?.items ?? []) as Row[], sort));
	const emptyShelf = $derived(!failure && totals?.copies === 0 && rows.length === 0);
	const all = 'all';
	const activeFilters = $derived(
		(
			[
				{ key: 'q', label: filter.q.trim() },
				{ key: 'kind', label: filter.kind && kindLabels[filter.kind] },
				{ key: 'format', label: filter.format && formatLabels[filter.format] },
				{ key: 'status', label: filter.status && statusLabels[filter.status] }
			] as const
		).filter((entry) => entry.label)
	);
</script>

<svelte:head>
	<title>Kolekcja · Ohara</title>
</svelte:head>

<PageHeader
	title="Kolekcja"
	description="Egzemplarze filmów, książek i muzyki na półce i na liście życzeń."
>
	{#snippet facts()}
		<FactList
			layout="inline"
			missingLabel="—"
			facts={[
				{ label: 'Egzemplarze', value: totals ? String(totals.copies) : null },
				{ label: 'Na półce', value: totals ? String(totals.owned) : null },
				{ label: 'Lista życzeń', value: totals ? String(totals.wishlist) : null },
				{
					label: 'Wydano',
					value: totals ? spentLabel(totals) : null
				}
			]}
		/>
	{/snippet}
	{#snippet actions()}
		{#if !emptyShelf}
			<Button tone="signal" href="/titles/new">Dodaj tytuł</Button>
		{/if}
	{/snippet}
</PageHeader>

{#if emptyShelf}
	<div class="content">
		{@render empty()}
	</div>
{:else}
	{@render collectionResults()}
{/if}

{#snippet collectionResults()}
	<div class="content">
		<div class="collection-sort">
			<Field label="Sortowanie">
				{#snippet control(c)}
					<Select {...c} options={collectionSortOptions} bind:value={() => sort, setSort} />
				{/snippet}
			</Field>
		</div>

		<ListToolbar
			class="collection-toolbar"
			searchLabel="Szukaj w kolekcji"
			placeholder="Tytuł, autor, sklep lub kod kreskowy"
			bind:query
			onsearch={(q: string) => set('q', q)}
			count={rows.length}
			countLabel={resultCount}
			pending={loading}
			pendingLabel="Wczytywanie…"
			toolsLabel="Filtry"
			filter={{
				label: 'Typ',
				options: [
					{ value: all, label: 'Wszystko' },
					{ value: 'movie', label: 'Filmy' },
					{ value: 'book', label: 'Książki' },
					{ value: 'music', label: 'Muzyka' }
				],
				value: filter.kind || all,
				onchange: (v: string) => set('kind', v === all ? '' : (v as CollectionFilter['kind']))
			}}
			tools={[
				{
					label: 'Nośnik',
					options: [{ value: all, label: 'Każdy' }, ...options(formatLabels)],
					value: filter.format || all,
					onchange: (v: string) => set('format', v === all ? '' : (v as CollectionFilter['format']))
				},
				{
					label: 'Status',
					options: [
						{ value: all, label: 'Każdy' },
						{ value: 'owned', label: 'Na półce' },
						{ value: 'wishlist', label: 'Lista życzeń' }
					],
					value: filter.status || all,
					onchange: (v: string) => set('status', v === all ? '' : (v as CollectionFilter['status']))
				}
			]}
		/>

		{#if activeFilters.length}
			<div class="active-filters" role="group" aria-label="Aktywne filtry">
				{#each activeFilters as { key, label } (key)}
					<Chip
						{label}
						onremove={() => set(key, '')}
						removeLabel={(value) => `Usuń filtr: ${value}`}
						removedLabel={(value) => `Usunięto filtr: ${value}`}
					/>
				{/each}
			</div>
		{/if}

		{#if view && !loading && !failure}
			<p class="metadata result-count" aria-hidden="true">{resultCount(rows.length)}</p>
		{/if}

		<div class="desktop-results">
			<SegmentedControl
				label="Widok kolekcji"
				options={layoutOptions}
				bind:value={() => layout, setLayout}
			/>
		</div>

		<div class="mobile-results">
			<CollectionRows {rows} {loading} {failure} onretry={load} {empty} />
		</div>
		{#if layout === 'shelf'}
			<div class="desktop-results">
				<CollectionShelf {rows} {loading} {failure} onretry={load} {empty} />
			</div>
		{:else}
			<DataTable
				class="desktop-results"
				aria-label="Egzemplarze w kolekcji"
				{columns}
				{rows}
				rowHeader
				rowKey={(r) => r.copy_id}
				loading={loading && !view}
				refreshing={loading && !!view}
				error={failure}
				onretry={load}
				retryLabel="Spróbuj ponownie"
				loadingLabel="Wczytywanie kolekcji…"
				refreshingLabel="Odświeżanie…"
				emptyLabel="Brak pozycji"
				scrollHint="Przewiń tabelę w bok"
				{empty}
			>
				{#snippet cell(row, column)}
					{#if column.key === 'name'}
						<div class="title-details">
							<Link href={`/titles/${row.title_id}`}>{displayName(row)}</Link>
							<span class="metadata">
								{[row.authors?.join(', '), kindLabels[row.kind]].filter(Boolean).join(' · ')}
								·
								<Link href={`/editions/${row.edition_id}`}>
									{editionSummary({ ...row, barcode: null })}
								</Link>
							</span>
						</div>
					{:else if column.key === 'status'}
						<Link href={`/copies/${row.copy_id}`}>
							<Pill tone={row.status === 'owned' ? 'success' : 'neutral'}>
								{statusLabels[row.status]}
							</Pill>
						</Link>
					{:else if column.key === 'price_cents'}
						{#if row.status === 'owned'}
							<span class="price">
								{row.price_cents === null ? 'Nieznana' : formatPLN(row.price_cents)}
							</span>
						{/if}
					{:else if column.key === 'shop'}
						{row.shop}
					{:else if column.key === 'purchased_on'}
						{#if row.purchased_on}
							<time class="metadata" datetime={row.purchased_on}>
								{purchaseDate(row.purchased_on)}
							</time>
						{/if}
					{/if}
				{/snippet}
			</DataTable>
		{/if}
	</div>
{/snippet}

{#snippet empty()}
	{#if emptyShelf}
		<EmptyState title="Kolekcja jest pusta" description="Dodaj pierwszy egzemplarz do kolekcji.">
			{#snippet action()}
				<div class="empty-actions">
					<Button tone="signal" href="/scan">Zeskanuj pierwszy kod</Button>
					<Button href="/titles/new">Dodaj ręcznie</Button>
				</div>
			{/snippet}
		</EmptyState>
	{:else if activeFilters.length}
		<EmptyState title="Nic nie pasuje" description="Żaden egzemplarz nie spełnia filtrów.">
			{#snippet action()}
				<Button onclick={clearFilters}>Wyczyść filtry</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<EmptyState title="Kolekcja jest pusta" description="Nie dodano jeszcze żadnego egzemplarza.">
			{#snippet action()}
				<Button tone="signal" href="/titles/new">Dodaj pierwszy tytuł</Button>
			{/snippet}
		</EmptyState>
	{/if}
{/snippet}

<style>
	.empty-actions {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
	}

	.collection-sort {
		width: min(100%, 16rem);
	}

	.content {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: 1rem;
		padding: var(--sui-pane-gutter, 1.5rem);
	}

	.active-filters {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
		min-width: 0;
	}

	.result-count {
		font-family: var(--sui-sans, sans-serif);
		margin: 0;
	}
	.mobile-results {
		display: none;
	}

	.title-details {
		display: grid;
		gap: var(--sui-space-1);
	}

	.price {
		font-variant-numeric: tabular-nums;
	}

	/* The collection's filters are chips: a pick is the brand's selected colour, not the
	   raspberry accent, at rest, on hover and while pressed. The tools popover follows the
	   toolbar and holds the type filter too once the bar folds it away. */
	.content :global(.collection-toolbar .sui-list-toolbar-segments),
	.content :global(.collection-toolbar + .sui-popover .sui-list-toolbar-panel) {
		--sui-segment-thumb: var(--ohara-selected);
		--sui-segment-thumb-ink: var(--ohara-on-selected);
		--sui-brand-action-hover: var(--ohara-selected-hover);
		--sui-brand-action-pressed: var(--ohara-selected-pressed);
	}

	/* SUI rings a focused pick inside its thumb in the thumb's ink, which on these chips is pink
	   or green. The ring takes the raspberry focus colour instead, on the track just outside the
	   thumb, where it stands out from the track rather than blending into the thumb. */
	.content
		:global(
			.collection-toolbar .sui-list-toolbar-segments .selected .sui-segmented-input:focus-visible
		),
	.content
		:global(
			.collection-toolbar
				+ .sui-popover
				.sui-list-toolbar-panel
				.selected
				.sui-segmented-input:focus-visible
		) {
		outline-color: var(--sui-focus);
		outline-offset: 0;
	}

	@media (max-width: 640px) {
		.content :global(.desktop-results) {
			display: none;
		}

		.mobile-results {
			display: block;
		}

		.content :global(.collection-toolbar .sui-list-toolbar-search) {
			flex-basis: 100%;
		}

		.content :global(.collection-toolbar .sui-list-toolbar-field) {
			flex-basis: 100%;
		}

		.content :global(.collection-toolbar .sui-list-toolbar-segments) {
			display: none;
		}

		.content :global(.collection-toolbar .sui-list-toolbar-folded),
		.content :global(.collection-toolbar .sui-list-toolbar-dot) {
			display: block;
		}
	}
</style>
