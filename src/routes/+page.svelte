<script lang="ts">
	import {
		Button,
		Chip,
		DataTable,
		EmptyState,
		FactList,
		Icon,
		IconButton,
		Link,
		PageHeader,
		Pill,
		SearchField,
		SegmentedControl,
		type DataTableColumn
	} from '@smykla-skalski/sui';
	import { onDestroy, onMount, tick, untrack } from 'svelte';
	import { MediaQuery } from 'svelte/reactivity';
	import { afterNavigate, replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import { CollectionLoad } from '#lib/collection-load.svelte.js';
	import SettingsSheet from '#lib/components/SettingsSheet.svelte';
	import {
		collectionSearch,
		collectionStatusLabels,
		displayName,
		editionSummary,
		formatLabels,
		formatPLN,
		isoDate,
		kindLabels,
		loanedLabel,
		orderLine,
		readCollectionFilter,
		shelfSummary,
		shelvedCount,
		spentLabel,
		statusLabels,
		uncopiedTitles,
		unpricedCount,
		unpricedLabel,
		type CollectionFilter,
		type CollectionItem
	} from '#lib/collection.js';
	import { filterCollection, locationsInUse } from '#lib/collection-filter.js';
	import {
		collectionSortLabels,
		sortCopies,
		sortTitles,
		type CollectionSort
	} from '#lib/collection-sort.js';
	import { purchaseDate, queuedCodes, resultCount } from '#lib/polish.js';
	import CollectionFilterSheet from '#lib/components/CollectionFilterSheet.svelte';
	import CollectionShelf from '#lib/components/CollectionShelf.svelte';
	import type { ShelfPlace } from '#lib/shelf-drawing.js';
	import TitleShelf from '#lib/components/TitleShelf.svelte';
	import type { Snapshot } from './$types';

	type Row = CollectionItem & Record<string, unknown>;
	type Layout = 'shelf' | 'table';

	const layoutKey = 'ohara.collection.layout';
	const layoutOptions: { value: Layout; label: string }[] = [
		{ value: 'shelf', label: 'Półka' },
		{ value: 'table', label: 'Tabela' }
	];
	let layout = $state<Layout>('shelf');
	/** A phone always gets the shelf; the same width as the `max-width: 640px` rules below. */
	const phone = new MediaQuery('(max-width: 640px)');

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

	const defaultSort: CollectionSort = 'title';
	const sort = $derived(page.state.collectionSort ?? defaultSort);

	function setSort(value: CollectionSort) {
		replaceState(window.location.href, { ...page.state, collectionSort: value });
	}

	const emptyFilter: CollectionFilter = { q: '', kind: '', format: '', status: '' };
	let sheetOpen = $state(false);

	let results = $state<HTMLElement>();
	let shelf = $state<CollectionShelf>();

	/** Where the copies start on the page, so a position kept from them survives what loads above. */
	function resultsTop(): number {
		return results ? results.getBoundingClientRect().top + window.scrollY : 0;
	}

	const loader = new CollectionLoad(
		untrack(() => readCollectionFilter(page.url.searchParams)),
		{
			screen: {
				scrollX: () => window.scrollX,
				scrollY: () => window.scrollY,
				scrollHeight: () => document.documentElement.scrollHeight,
				scrollTo: (x, y) => window.scrollTo(x, y),
				resultsTop,
				drawTo: (y) => shelf?.drawTo(y)
			}
		}
	);
	const filter = $derived(loader.filter);
	let query = $state(untrack(() => loader.filter.q));
	const collection = $derived(loader.collection);
	const uncopied = $derived(loader.uncopied);
	const overview = $derived(loader.overview);
	const queued = $derived(loader.queued);
	const failure = $derived(loader.failure);
	const loading = $derived(loader.loading);
	const load = loader.load;

	export const snapshot: Snapshot<[number, number]> = {
		capture: () => loader.capture(),
		restore: (position) => loader.restoreFrom(position)
	};

	onDestroy(() => loader.leave());

	$effect(() => {
		if (overview?.totals.copies === 0 && untrack(() => uncopied) === null) {
			loader.loadUncopied();
		}
	});

	afterNavigate((navigation) => {
		const read = readCollectionFilter(new URLSearchParams(window.location.search));
		query = read.q;
		void loader.navigated(read, navigation.type === 'popstate');
	});

	// The search filters once the reader pauses typing, so the shelf is not redrawn per key.
	$effect(() => {
		const typed = query;
		if (typed === untrack(() => filter.q)) return;
		const timer = setTimeout(() => set('q', typed), 200);
		return () => clearTimeout(timer);
	});

	function set<K extends keyof CollectionFilter>(key: K, value: CollectionFilter[K]) {
		if (key === 'q' && typeof value === 'string') query = value;
		updateFilter({ ...filter, [key]: value });
	}

	function clearFilters() {
		query = '';
		updateFilter(emptyFilter);
	}

	/**
	 * Applies a filter and writes it to the address. The search is kept as the address carries
	 * it, trimmed, so the shelf lists what the address lists after a reload.
	 */
	function updateFilter(changed: CollectionFilter) {
		loader.select({ ...changed, q: changed.q.trim() }, (next) => {
			const search = collectionSearch(next);
			const { pathname, search: current, hash } = window.location;
			if (current === search) return false;
			replaceState(`${pathname}${search}${hash}`, page.state);
			return true;
		});
	}

	const columns: DataTableColumn<Row>[] = [
		{ key: 'name', label: 'Tytuł' },
		{ key: 'status', label: 'Status' },
		{ key: 'price_cents', label: 'Cena', numeric: true },
		{ key: 'shop', label: 'Sklep' },
		{ key: 'purchased_on', label: 'Kupiono', nowrap: true }
	];

	const kindChips: { value: CollectionFilter['kind']; label: string }[] = [
		{ value: '', label: 'Wszystko' },
		{ value: 'movie', label: 'Filmy' },
		{ value: 'book', label: 'Książki' },
		{ value: 'music', label: 'Muzyka' }
	];

	/** A chip's count: copies of its kind, or titles without copies while those are listed. */
	function kindCount(kind: CollectionFilter['kind']): number | undefined {
		if (listsTitles) {
			if (!uncopied) return undefined;
			return kind ? uncopied.filter((t) => t.kind === kind).length : uncopied.length;
		}
		if (!overview) return undefined;
		return kind ? overview.kinds[kind] : shelvedCount(overview.totals);
	}

	function setStatus(status: CollectionFilter['status']) {
		// A title has no format, location or loan, so listing titles without copies drops those
		// filters.
		const titles = status === 'none';
		updateFilter({
			...filter,
			status,
			format: titles ? '' : filter.format,
			location: titles ? '' : filter.location,
			loaned: titles ? false : filter.loaned
		});
	}

	/** The places the loaded copies are stored at, for the sheet's "Miejsce". */
	const locations = $derived(locationsInUse(collection?.items ?? []));

	// A no-break space keeps the line's height until the counts arrive, so nothing below moves.
	const summary = $derived(overview ? shelfSummary(overview.totals) : '\u00a0');
	/** Owned copies without a purchase price; the phone's header links to them. */
	const unpriced = $derived(overview ? unpricedCount(overview.totals) : 0);
	const rows = $derived(
		sortCopies(filterCollection(collection?.items ?? [], filter) as Row[], sort)
	);
	/** "Bez egzemplarzy": the view lists titles that have no copy instead of copies. */
	const listsTitles = $derived(loader.listsTitles);

	/** Whether the copies showed as the shelf rather than the table. */
	let shelfBefore: boolean | undefined;
	/**
	 * The tile at the top of the screen when the table took over from the shelf, as when a phone
	 * turns sideways. The shelf that comes back starts with its first tiles only, so it draws down
	 * to that tile and puts it back where it was.
	 */
	let shelfPlace: ShelfPlace | null = null;

	$effect.pre(() => {
		const shown = layout === 'shelf' || phone.current;
		const was = shelfBefore;
		shelfBefore = shown;
		if (was === true && !shown) shelfPlace = shelf?.shownTile() ?? null;
		if (was !== false || !shown || !shelfPlace) return;
		const place = shelfPlace;
		shelfPlace = null;
		void tick().then(() => shelf?.showTile(place));
	});
	const titleRows = $derived(sortTitles(uncopiedTitles(uncopied ?? [], filter), sort));
	const listed = $derived(listsTitles ? uncopied : collection);
	/** Sold copies, which only "Sprzedane" lists. */
	const soldCount = $derived(overview ? overview.totals.copies - shelvedCount(overview.totals) : 0);
	/**
	 * An empty collection greets the reader whatever the filter, except "Bez ceny", which keeps
	 * "Nic nie pasuje" and its "Wyczyść filtry" like any filter that lists nothing. A collection
	 * whose every copy was sold is not empty: it keeps its filters, so "Sprzedane" stays in reach.
	 */
	const emptyShelf = $derived(
		!failure &&
			!listsTitles &&
			filter.status !== 'unpriced' &&
			overview?.totals.copies === 0 &&
			rows.length === 0
	);
	const activeFilters = $derived(
		(
			[
				{ key: 'q', label: filter.q.trim() },
				{ key: 'kind', label: filter.kind && kindLabels[filter.kind] },
				{ key: 'format', label: filter.format && formatLabels[filter.format] },
				{ key: 'location', label: filter.location },
				{ key: 'status', label: filter.status && collectionStatusLabels[filter.status] },
				{ key: 'loaned', label: filter.loaned ? loanedLabel : '' }
			] as const
		).filter((entry) => entry.label)
	);
	/** "Tytuł A–Z" as "tytuł A–Z", for a label that continues a sentence. */
	function lowerFirst(label: string): string {
		return label.charAt(0).toLocaleLowerCase('pl-PL') + label.slice(1);
	}

	/** What the sheet holds besides its defaults, each removable from beside the list. */
	const sheetChoices = $derived(
		[
			sort !== defaultSort && {
				key: 'sort',
				label: `Sortuj: ${lowerFirst(collectionSortLabels[sort])}`,
				remove: () => setSort(defaultSort)
			},
			filter.format && {
				key: 'format',
				label: formatLabels[filter.format],
				remove: () => set('format', '')
			},
			!!filter.location && {
				key: 'location',
				label: `Miejsce: ${filter.location}`,
				remove: () => set('location', '')
			},
			// The wishlist has its own chip beside the list.
			filter.status &&
				filter.status !== 'wishlist' && {
					key: 'status',
					label: collectionStatusLabels[filter.status],
					remove: () => set('status', '')
				},
			filter.loaned === true && {
				key: 'loaned',
				label: loanedLabel,
				remove: () => set('loaned', false)
			}
		].filter((choice) => choice !== false && choice !== '')
	);
</script>

<svelte:head>
	<title>Kolekcja · Ohara</title>
</svelte:head>

<div class="collection-page">
	<PageHeader class="desktop-only" title="Kolekcja" description={summary}>
		{#snippet facts()}
			<FactList
				layout="inline"
				missingLabel="—"
				facts={[{ label: 'Wydano', value: overview ? spentLabel(overview.totals) : null }]}
			/>
		{/snippet}
		{#snippet actions()}
			{#if !emptyShelf}
				<Button tone="signal" href="/titles/new">Dodaj tytuł</Button>
			{/if}
			<SettingsSheet />
		{/snippet}
	</PageHeader>

	<header class="phone-head phone-only">
		<h1>Kolekcja</h1>
		<div class="phone-add">
			{#if !emptyShelf}
				<Button href="/titles/new">Dodaj tytuł</Button>
			{/if}
			<SettingsSheet />
		</div>
		<p class="summary">
			{summary}
			{#if unpriced > 0}
				·
				<button
					type="button"
					class="unpriced"
					aria-pressed={filter.status === 'unpriced'}
					onclick={() => setStatus(filter.status === 'unpriced' ? '' : 'unpriced')}
				>
					{unpricedLabel(unpriced)}
				</button>
			{/if}
		</p>
	</header>

	{#if emptyShelf}
		<div class="content">
			{@render queueNotice()}
			{@render empty()}
		</div>
	{:else}
		{@render collectionResults()}
	{/if}

	<CollectionFilterSheet
		bind:open={sheetOpen}
		{sort}
		format={filter.format}
		status={filter.status}
		loaned={filter.loaned ?? false}
		location={filter.location ?? ''}
		{locations}
		onsort={setSort}
		onformat={(format) => set('format', format)}
		onstatus={setStatus}
		onloaned={(loaned) => set('loaned', loaned)}
		onlocation={(location) => set('location', location)}
	/>
</div>

{#snippet collectionResults()}
	<div class="content">
		<div class="tools">
			<div class="search">
				<SearchField
					class="search-field"
					label="Szukaj w kolekcji"
					hideLabel
					placeholder={listsTitles ? 'Tytuł lub autor' : 'Tytuł, autor, sklep, kod'}
					clearLabel="Wyczyść wyszukiwanie"
					bind:value={query}
					onclear={() => set('q', '')}
				/>
				<span class="sheet-button">
					<IconButton
						icon="sliders"
						toolbar
						label={sheetChoices.length
							? `Sortowanie i filtry (${sheetChoices.length})`
							: 'Sortowanie i filtry'}
						aria-haspopup="dialog"
						onclick={() => (sheetOpen = true)}
					/>
					{#if sheetChoices.length}
						<span class="sheet-dot" aria-hidden="true"></span>
					{/if}
				</span>
			</div>

			<div class="chips" role="group" aria-label="Typ">
				{#each kindChips as chip (chip.value)}
					{@const count = kindCount(chip.value)}
					<button
						type="button"
						class="chip"
						aria-pressed={filter.kind === chip.value}
						onclick={() => set('kind', chip.value)}
					>
						{chip.label}
						{#if count !== undefined}<span class="chip-count">{count}</span>{/if}
					</button>
				{/each}
				<button
					type="button"
					class="chip"
					aria-pressed={filter.status === 'wishlist'}
					onclick={() => setStatus(filter.status === 'wishlist' ? '' : 'wishlist')}
				>
					<svg class="heart" viewBox="0 0 24 24" aria-hidden="true">
						<path
							d="M12 20s-7.5-4.6-7.5-10.2A4.3 4.3 0 0 1 12 7.2a4.3 4.3 0 0 1 7.5 2.6C19.5 15.4 12 20 12 20z"
						/>
					</svg>
					<span class="visually-hidden">{statusLabels.wishlist}</span>
					{#if overview}<span class="chip-count">{overview.totals.wishlist}</span>{/if}
				</button>
			</div>

			{#if !listsTitles}
				<div class="desktop-only layout-switch">
					<SegmentedControl
						label="Widok kolekcji"
						hideLabel
						options={layoutOptions}
						bind:value={() => layout, setLayout}
					/>
				</div>
			{/if}
		</div>

		{#if sheetChoices.length}
			<div class="sheet-choices" role="group" aria-label="Sortowanie i filtry">
				{#each sheetChoices as { key, label, remove } (key)}
					<Chip
						{label}
						onremove={remove}
						removeLabel={(value) => `Usuń: ${value}`}
						removedLabel={(value) => `Usunięto: ${value}`}
					/>
				{/each}
			</div>
		{/if}

		{@render queueNotice()}

		<p class="metadata result-count" role="status">
			{#if listed && !loading && !failure}
				{resultCount(listsTitles ? titleRows.length : rows.length)}
			{/if}
		</p>

		<div class="results" bind:this={results}>
			{#if listsTitles}
				<TitleShelf titles={titleRows} {loading} {failure} onretry={load} {empty} />
			{:else if layout === 'shelf' || phone.current}
				<CollectionShelf bind:this={shelf} {rows} {loading} {failure} onretry={load} {empty} />
			{:else}
				<DataTable
					aria-label="Egzemplarze w kolekcji"
					{columns}
					{rows}
					rowHeader
					rowKey={(r) => r.copy_id}
					loading={loading && !collection}
					refreshing={loading && !!collection}
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
							<div class="status-details">
								<Link href={`/copies/${row.copy_id}`}>
									<Pill tone={row.status === 'owned' ? 'success' : 'neutral'}>
										{row.loan_borrower != null ? 'Wypożyczony' : statusLabels[row.status]}
									</Pill>
								</Link>
								{#if row.status === 'sold'}
									<span class="metadata price">
										{[purchaseDate(row.sold_on ?? null), formatPLN(row.sale_price_cents)]
											.filter(Boolean)
											.join(' · ')}
									</span>
								{:else if row.status === 'ordered' && row.expected_on}
									<span class="metadata price">
										{orderLine(row.expected_on, isoDate(new Date()))}
									</span>
								{/if}
							</div>
						{:else if column.key === 'price_cents'}
							{#if row.status !== 'wishlist'}
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
	</div>
{/snippet}

{#snippet queueNotice()}
	{#if queued > 0}
		<a class="queue-notice" href="/scan/queue">
			<span class="queue-mark" aria-hidden="true">
				<svg viewBox="0 0 24 24">
					<path d="M4 6v12M7 6v12M10 6v12M14 6v12M16.5 6v12M20 6v12" />
				</svg>
			</span>
			<span class="queue-text">{queuedCodes(queued)}</span>
			<span class="queue-go" aria-hidden="true"><Icon name="chevron-right" /></span>
		</a>
	{/if}
{/snippet}

{#snippet empty()}
	{#if emptyShelf}
		<EmptyState title="Kolekcja jest pusta" description="Dodaj pierwszy egzemplarz do kolekcji.">
			{#snippet action()}
				<div class="empty-actions">
					<Button tone="signal" href="/scan">Zeskanuj pierwszy kod</Button>
					<Button href="/titles/new">Dodaj ręcznie</Button>
					<!-- Offered until the count is known to be zero, so a failed count can be asked again. -->
					{#if uncopied === null || uncopied.length > 0}
						<Button onclick={() => setStatus('none')}>
							{uncopied
								? `${collectionStatusLabels.none} (${uncopied.length})`
								: collectionStatusLabels.none}
						</Button>
					{/if}
				</div>
			{/snippet}
		</EmptyState>
	{:else if listsTitles && uncopied?.length === 0}
		<EmptyState
			title="Każdy tytuł ma egzemplarz"
			description="Tu pojawią się tytuły zapisane bez egzemplarza."
		>
			{#snippet action()}
				<Button onclick={() => setStatus('')}>Pokaż egzemplarze</Button>
			{/snippet}
		</EmptyState>
	{:else if listsTitles}
		<EmptyState
			title="Nic nie pasuje"
			description="Żaden tytuł bez egzemplarzy nie spełnia filtrów."
		>
			{#snippet action()}
				<Button onclick={clearFilters}>Wyczyść filtry</Button>
			{/snippet}
		</EmptyState>
	{:else if activeFilters.length}
		<EmptyState title="Nic nie pasuje" description="Żaden egzemplarz nie spełnia filtrów.">
			{#snippet action()}
				<Button onclick={clearFilters}>Wyczyść filtry</Button>
			{/snippet}
		</EmptyState>
	{:else if soldCount > 0}
		<EmptyState title="Półka jest pusta" description="Wszystkie egzemplarze są sprzedane.">
			{#snippet action()}
				<div class="empty-actions">
					<Button tone="signal" onclick={() => setStatus('sold')}>
						{`${collectionStatusLabels.sold} (${soldCount})`}
					</Button>
					<Button href="/titles/new">Dodaj tytuł</Button>
				</div>
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

	.content {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: 1rem;
		padding: var(--sui-pane-gutter, 1.5rem);
	}

	.tools {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--sui-space-3);
		min-width: 0;
	}

	/* The search field with the sheet's button inside its end, as one inset bar. */
	.search {
		position: relative;
		flex: 1 1 18rem;
		max-width: 32rem;
		min-width: 0;
	}

	/* The sheet's button takes the field's end, so the clear button and the typed text stop
	   short of it, touch targets included. */
	.search :global(.sui-search-field-box > .search-input) {
		padding-inline-end: 5.5rem;
	}

	.search :global(.sui-search-field-box .sui-search-field-clear) {
		margin-inline-end: 3.25rem;
	}

	.sheet-button {
		/* The button sits inside the field, so it stays a step smaller; its touch target keeps 44px. */
		--sui-control-height-compact: 2.25rem;
		position: absolute;
		inset-block: 0;
		inset-inline-end: 0.25rem;
		display: grid;
		place-items: center;
	}

	.sheet-button :global(.sui-icon-button) {
		background: var(--sui-surface-base);
		color: var(--sui-text-primary);
	}

	.sheet-dot {
		position: absolute;
		top: 50%;
		inset-inline-end: 0;
		inline-size: 0.5rem;
		block-size: 0.5rem;
		margin-top: -1.125rem;
		border-radius: 50%;
		background: var(--sui-brand-action);
		pointer-events: none;
	}

	.chips {
		display: flex;
		gap: var(--sui-space-2);
		min-width: 0;
		/* Room above and below for each chip's 44px touch target. */
		padding-block: 5px;
		margin-block: -5px;
	}

	.chip {
		position: relative;
		display: inline-flex;
		flex: none;
		align-items: center;
		gap: 0.375rem;
		block-size: 2.125rem;
		padding-inline: 0.875rem;
		border: 1px solid var(--sui-border-subtle);
		border-radius: 1.0625rem;
		background: var(--sui-surface-base);
		color: var(--sui-text-primary);
		font-size: 0.875rem;
		font-weight: var(--sui-weight-semibold);
		line-height: 1;
		white-space: nowrap;
		cursor: pointer;
	}

	.chip::before {
		content: '';
		position: absolute;
		inset: -5px 0;
	}

	.chip:hover {
		background: color-mix(in srgb, var(--sui-surface-inset) 60%, var(--sui-surface-base));
	}

	.chip[aria-pressed='true'] {
		border-color: var(--ohara-selected);
		background: var(--ohara-selected);
		color: var(--ohara-on-selected);
	}

	.chip[aria-pressed='true']:hover {
		border-color: var(--ohara-selected-hover);
		background: var(--ohara-selected-hover);
	}

	.chip:active {
		transform: translateY(1px);
	}

	.chip:focus-visible {
		outline: 2px solid var(--sui-focus);
		outline-offset: 2px;
	}

	.chip-count {
		color: var(--sui-text-secondary);
		font-weight: 500;
		font-variant-numeric: tabular-nums;
	}

	.chip[aria-pressed='true'] .chip-count {
		color: inherit;
		opacity: 0.8;
	}

	.heart {
		inline-size: 0.875rem;
		block-size: 0.875rem;
		fill: var(--sui-brand-action);
	}

	.chip[aria-pressed='true'] .heart {
		fill: currentColor;
	}

	.layout-switch {
		margin-inline-start: auto;
	}

	.sheet-choices {
		display: flex;
		flex-wrap: wrap;
		gap: var(--sui-space-2);
		min-width: 0;
	}

	.queue-notice {
		box-sizing: border-box;
		display: flex;
		align-items: center;
		gap: 0.625rem;
		min-block-size: 2.75rem;
		padding: 0.5625rem 0.75rem;
		border: 1px solid var(--sui-border-subtle);
		border-radius: 0.9375rem;
		background: var(--sui-surface-base);
		color: var(--sui-text-primary);
		font-size: 0.875rem;
		font-weight: var(--sui-weight-semibold);
		text-decoration: none;
	}

	.queue-notice:hover {
		background: color-mix(in srgb, var(--sui-surface-inset) 50%, var(--sui-surface-base));
	}

	.queue-notice:focus-visible {
		outline: 2px solid var(--sui-focus);
		outline-offset: 2px;
	}

	.queue-go {
		display: flex;
		margin-inline-start: auto;
		color: var(--sui-text-secondary);
	}

	.queue-mark {
		display: grid;
		flex: none;
		place-items: center;
		inline-size: 1.875rem;
		block-size: 1.875rem;
		border-radius: 0.5625rem;
		background: var(--sui-brand-action-tint);
		color: var(--sui-brand-action-text);
	}

	.queue-mark svg {
		inline-size: 1.125rem;
		block-size: 1.125rem;
		fill: none;
		stroke: currentColor;
		stroke-width: 1.9;
		stroke-linecap: round;
	}

	.queue-text {
		min-width: 0;
	}

	.result-count {
		font-family: var(--sui-sans, sans-serif);
		margin: 0;
	}

	.results {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: inherit;
	}

	/* The queue notice keeps the view in place itself, so the browser's own scroll anchoring
	   would move it twice. */
	.collection-page {
		overflow-anchor: none;
	}

	.collection-page :global(.phone-only) {
		display: none;
	}

	.phone-head {
		grid-template-columns: minmax(0, 1fr) auto;
		align-items: center;
		column-gap: var(--sui-space-3);
		padding: var(--sui-space-3) var(--sui-space-4) 0;
	}

	.phone-add {
		/* Above the touch target of the summary's "bez ceny" toggle where the two meet. */
		position: relative;
		z-index: 1;
		align-items: center;
		display: flex;
		gap: var(--sui-space-2);
		grid-column: 2;
		grid-row: 1;
	}

	.phone-head h1 {
		margin: 0;
		color: var(--ohara-heading);
		font-family: var(--ohara-serif);
		font-size: 2.375rem;
		font-weight: 600;
		letter-spacing: -0.02em;
		line-height: 1.05;
	}

	.summary {
		grid-column: 1 / -1;
		margin: 0.25rem 0 0;
		color: var(--sui-text-secondary);
		font-size: 0.875rem;
	}

	/* A link-like toggle within the summary line, its touch target 44px tall. */
	.unpriced {
		position: relative;
		padding: 0;
		border: 0;
		background: none;
		color: var(--sui-brand-action-text);
		font: inherit;
		font-weight: var(--sui-weight-semibold);
		text-decoration: underline;
		text-underline-offset: 0.2em;
		cursor: pointer;
	}

	.unpriced::before {
		content: '';
		position: absolute;
		inset: -0.875rem -0.25rem;
	}

	.unpriced[aria-pressed='true'] {
		color: var(--sui-text-primary);
		text-decoration: none;
	}

	.unpriced:focus-visible {
		outline: 2px solid var(--sui-focus);
		outline-offset: 2px;
		border-radius: 2px;
	}

	.title-details {
		display: grid;
		gap: var(--sui-space-1);
	}

	.price {
		font-variant-numeric: tabular-nums;
	}

	.status-details {
		display: grid;
		justify-items: start;
		gap: var(--sui-space-1);
	}

	.visually-hidden {
		position: absolute;
		inline-size: 1px;
		block-size: 1px;
		overflow: hidden;
		clip-path: inset(50%);
		white-space: nowrap;
	}

	@media (max-width: 640px) {
		.content {
			gap: var(--sui-space-3);
			padding: var(--sui-space-3) var(--sui-space-4) var(--sui-space-6);
		}

		.collection-page :global(.desktop-only) {
			display: none;
		}

		.collection-page :global(.phone-only) {
			display: block;
		}

		.collection-page .phone-head {
			display: grid;
		}

		.search {
			flex-basis: 100%;
			max-width: none;
		}

		/* One row of chips that scrolls sideways, edge to edge. */
		.chips {
			flex-basis: 100%;
			margin-inline: calc(-1 * var(--sui-space-4));
			padding-inline: var(--sui-space-4);
			overflow-x: auto;
			scrollbar-width: none;
		}

		.result-count {
			position: absolute;
			inline-size: 1px;
			block-size: 1px;
			overflow: hidden;
			clip-path: inset(50%);
			white-space: nowrap;
		}
	}
</style>
