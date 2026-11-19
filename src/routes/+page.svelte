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
		kindCounts,
		kindLabels,
		readCollectionFilter,
		shelfSummary,
		spentLabel,
		statusLabels,
		type CollectionFilter,
		type CollectionItem,
		type CollectionView,
		type Kind,
		type Totals
	} from '#lib/collection.js';
	import { collectionSortLabels, sortCopies, type CollectionSort } from '#lib/collection-sort.js';
	import { purchaseDate, queuedCodes, resultCount } from '#lib/polish.js';
	import type { QueueEntry } from '#lib/scan.js';
	import CollectionFilterSheet from '#lib/components/CollectionFilterSheet.svelte';
	import CollectionShelf from '#lib/components/CollectionShelf.svelte';
	import type { Snapshot } from './$types';

	type Row = CollectionItem & Record<string, unknown>;
	type Layout = 'shelf' | 'table';
	/** The whole collection's counts, whatever the filter. */
	type Overview = { totals: Totals; kinds: Record<Kind, number> };

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

	const defaultSort: CollectionSort = 'title';
	const sort = $derived(page.state.collectionSort ?? defaultSort);

	function setSort(value: CollectionSort) {
		replaceState(window.location.href, { ...page.state, collectionSort: value });
	}

	const emptyFilter: CollectionFilter = { q: '', kind: '', format: '', status: '' };
	let filter = $state(untrack(() => readCollectionFilter(page.url.searchParams)));
	let query = $state(untrack(() => filter.q));
	let view = $state<CollectionView | null>(null);
	let overview = $state<Overview | null>(null);
	let queued = $state(0);
	let failure = $state<string | null>(null);
	let loading = $state(true);
	let sheetOpen = $state(false);
	let seq = 0;
	let restoreScroll: [number, number] | undefined;
	let pendingOverview: Promise<void> | undefined;
	let pendingQueue: Promise<void> | undefined;

	let results = $state<HTMLElement>();

	/** Where the copies start on the page, so a position kept from them survives what loads above. */
	function resultsTop(): number {
		return results ? results.getBoundingClientRect().top + window.scrollY : 0;
	}

	export const snapshot: Snapshot<[number, number]> = {
		capture: () => [window.scrollX, window.scrollY - resultsTop()],
		restore: (position) => (restoreScroll = position)
	};

	function overviewOf(full: CollectionView): Overview {
		return { totals: full.totals, kinds: kindCounts(full.items) };
	}

	function loadOverview() {
		if (overview) return;
		pendingOverview ??= request<CollectionView>(fetch, 'GET', '/api/collection')
			.catch(() => null)
			.then((full) => {
				if (full) overview ??= overviewOf(full);
				pendingOverview = undefined;
			});
		return pendingOverview;
	}

	/** Counts the scan queue once per visit; a failed count is asked again on the next load. */
	function loadQueue() {
		pendingQueue ??= request<QueueEntry[]>(fetch, 'GET', '/api/scan-queue')
			.then((entries) => showQueue(entries.length))
			.catch(() => {
				pendingQueue = undefined;
			});
		return pendingQueue;
	}

	/**
	 * Shows the queue count. A notice that appears above a scrolled view pushes the view down by
	 * its height, so the copies on screen stay where the reader left them.
	 */
	async function showQueue(count: number) {
		const scrolled = window.scrollY;
		const before = document.documentElement.scrollHeight;
		queued = count;
		await tick();
		const grown = document.documentElement.scrollHeight - before;
		if (scrolled > 0 && grown !== 0) window.scrollTo(window.scrollX, scrolled + grown);
	}

	async function load() {
		const mine = ++seq;
		const url = collectionURL(filter);
		loading = true;
		const overviewReady = url === '/api/collection' ? undefined : loadOverview();
		void loadQueue();
		try {
			const got = await request<CollectionView>(fetch, 'GET', url);
			if (mine !== seq) return;
			view = got;
			if (url === '/api/collection') overview = overviewOf(got);
			failure = null;
		} catch (err) {
			if (mine === seq) failure = errorMessage(err);
		} finally {
			if (mine === seq) loading = false;
		}
		if (mine === seq && !failure && restoreScroll) {
			// The position is kept from the copies' top, so the header's counts and a queue notice
			// that arrives later cannot move the restored row.
			await overviewReady;
			await tick();
			if (mine === seq && restoreScroll) {
				const [x, fromResults] = restoreScroll;
				window.scrollTo(x, resultsTop() + fromResults);
				restoreScroll = undefined;
			}
		}
	}

	afterNavigate(() => {
		filter = readCollectionFilter(new URLSearchParams(window.location.search));
		query = filter.q;
		void load();
	});

	// The search asks once the reader pauses typing, as the list toolbar did.
	$effect(() => {
		const typed = query;
		if (typed === untrack(() => filter.q)) return;
		const timer = setTimeout(() => set('q', typed), 600);
		return () => clearTimeout(timer);
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

	const kindChips: { value: CollectionFilter['kind']; label: string }[] = [
		{ value: '', label: 'Wszystko' },
		{ value: 'movie', label: 'Filmy' },
		{ value: 'book', label: 'Książki' },
		{ value: 'music', label: 'Muzyka' }
	];

	function kindCount(kind: CollectionFilter['kind']): number | undefined {
		if (!overview) return undefined;
		return kind ? overview.kinds[kind] : overview.totals.copies;
	}

	// A no-break space keeps the line's height until the counts arrive, so nothing below moves.
	const summary = $derived(overview ? shelfSummary(overview.totals) : '\u00a0');
	const rows = $derived(sortCopies((view?.items ?? []) as Row[], sort));
	const emptyShelf = $derived(!failure && overview?.totals.copies === 0 && rows.length === 0);
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
			filter.status === 'owned' && {
				key: 'status',
				label: statusLabels.owned,
				remove: () => set('status', '')
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
		{/snippet}
	</PageHeader>

	<header class="phone-head phone-only">
		<h1>Kolekcja</h1>
		{#if !emptyShelf}
			<div class="phone-add">
				<Button href="/titles/new">Dodaj tytuł</Button>
			</div>
		{/if}
		<p class="summary">{summary}</p>
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
		onsort={setSort}
		onformat={(format) => set('format', format)}
		onstatus={(status) => set('status', status)}
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
					placeholder="Tytuł, autor, sklep, kod"
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
					onclick={() => set('status', filter.status === 'wishlist' ? '' : 'wishlist')}
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

			<div class="desktop-only layout-switch">
				<SegmentedControl
					label="Widok kolekcji"
					hideLabel
					options={layoutOptions}
					bind:value={() => layout, setLayout}
				/>
			</div>
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
			{#if view && !loading && !failure}{resultCount(rows.length)}{/if}
		</p>

		<div class="results" bind:this={results}>
			{#if layout === 'shelf'}
				<CollectionShelf {rows} {loading} {failure} onretry={load} {empty} />
			{:else}
				<div class="phone-only">
					<CollectionShelf {rows} {loading} {failure} onretry={load} {empty} />
				</div>
				<DataTable
					class="desktop-only"
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

	.title-details {
		display: grid;
		gap: var(--sui-space-1);
	}

	.price {
		font-variant-numeric: tabular-nums;
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
