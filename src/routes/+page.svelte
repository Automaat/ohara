<script lang="ts">
	import {
		Button,
		Chip,
		DataTable,
		EmptyState,
		FactList,
		Link,
		ListToolbar,
		PageHeader,
		Pill,
		type DataTableColumn
	} from '@smykla-skalski/sui';
	import { onMount, tick, untrack } from 'svelte';
	import { afterNavigate, replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import { errorMessage, request } from '$lib/api';
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
	} from '$lib/collection';
	import { databaseBadge, fetchStatus, type Status } from '$lib/status';
	import type { Snapshot } from './$types';

	type Row = CollectionItem & Record<string, unknown>;

	const emptyFilter: CollectionFilter = { q: '', kind: '', format: '', status: '' };
	let filter = $state(untrack(() => readCollectionFilter(page.url.searchParams)));
	let query = $state(untrack(() => filter.q));
	let view = $state<CollectionView | null>(null);
	let totals = $state<Totals | null>(null);
	let failure = $state<string | null>(null);
	let loading = $state(true);
	let info = $state<Status | null>(null);
	let seq = 0;
	let restoreScroll: [number, number] | undefined;
	let loadingTotals = false;

	export const snapshot: Snapshot<[number, number]> = {
		capture: () => [window.scrollX, window.scrollY],
		restore: (position) => (restoreScroll = position)
	};

	async function loadTotals() {
		if (totals || loadingTotals) return;
		loadingTotals = true;
		const full = await request<CollectionView>(fetch, 'GET', '/api/collection').catch(() => null);
		loadingTotals = false;
		if (full) totals ??= full.totals;
	}

	async function load() {
		const mine = ++seq;
		const url = collectionURL(filter);
		loading = true;
		if (url !== '/api/collection') void loadTotals();
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

	onMount(() => {
		fetchStatus(fetch).then(
			(s) => (info = s),
			() => (info = null)
		);
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
		{ key: 'kind', label: 'Typ' },
		{ key: 'edition_id', label: 'Wydanie' },
		{ key: 'status', label: 'Status' },
		{ key: 'price_cents', label: 'Cena', numeric: true },
		{ key: 'shop', label: 'Sklep' },
		{ key: 'purchased_on', label: 'Kupiono', nowrap: true }
	];

	const rows = $derived((view?.items ?? []) as Row[]);
	const count = new Intl.NumberFormat('pl-PL');
	const all = 'all';
	const badge = $derived(info ? databaseBadge(info.database) : null);
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
	description="Filmy i książki na półce i na liście życzeń, z tym, ile za nie zapłacono."
>
	{#snippet status()}
		{#if badge}
			<Pill tone={badge.tone} dot>Baza: {badge.label}</Pill>
		{/if}
	{/snippet}
	{#snippet facts()}
		<FactList
			layout="inline"
			missingLabel="—"
			facts={[
				{ label: 'Egzemplarze', value: totals ? String(totals.copies) : null, mono: true },
				{ label: 'Na półce', value: totals ? String(totals.owned) : null, mono: true },
				{ label: 'Lista życzeń', value: totals ? String(totals.wishlist) : null, mono: true },
				{
					label: 'Wydano',
					value: totals ? spentLabel(totals) : null,
					mono: true
				},
				{ label: 'Wersja', value: info?.version, mono: true }
			]}
		/>
	{/snippet}
	{#snippet actions()}
		<Button tone="signal" href="/titles/new">Dodaj tytuł</Button>
	{/snippet}
</PageHeader>

<div class="content">
	<ListToolbar
		searchLabel="Szukaj w kolekcji"
		placeholder="Tytuł, autor, sklep lub kod kreskowy"
		bind:query
		onsearch={(q: string) => set('q', q)}
		count={rows.length}
		countLabel={(_n: number, formatted: string) => `Wyniki: ${formatted}`}
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
		<p class="result-count">Wyniki: {count.format(rows.length)}</p>
	{/if}

	<DataTable
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
	>
		{#snippet cell(row, column)}
			{#if column.key === 'name'}
				<Link href={`/titles/${row.title_id}`}>{displayName(row)}</Link>
			{:else if column.key === 'kind'}
				{kindLabels[row.kind]}
			{:else if column.key === 'edition_id'}
				<Link href={`/editions/${row.edition_id}`}>{editionSummary(row)}</Link>
			{:else if column.key === 'status'}
				<Link href={`/copies/${row.copy_id}`}>
					<Pill tone={row.status === 'owned' ? 'success' : 'neutral'}
						>{statusLabels[row.status]}</Pill
					>
				</Link>
			{:else if column.key === 'price_cents'}
				{formatPLN(row.price_cents)}
			{:else if column.key === 'shop'}
				{row.shop}
			{:else if column.key === 'purchased_on'}
				{row.purchased_on ?? ''}
			{/if}
		{/snippet}
		{#snippet empty()}
			{#if activeFilters.length}
				<EmptyState title="Nic nie pasuje" description="Żaden egzemplarz nie spełnia filtrów.">
					{#snippet action()}
						<Button onclick={clearFilters}>Wyczyść filtry</Button>
					{/snippet}
				</EmptyState>
			{:else}
				<EmptyState
					title="Kolekcja jest pusta"
					description="Nie dodano jeszcze żadnego egzemplarza."
				>
					{#snippet action()}
						<Button tone="signal" href="/titles/new">Dodaj pierwszy tytuł</Button>
					{/snippet}
				</EmptyState>
			{/if}
		{/snippet}
	</DataTable>
</div>

<style>
	.content {
		display: grid;
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
</style>
