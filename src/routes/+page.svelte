<script lang="ts">
	import {
		Button,
		DataTable,
		EmptyState,
		FactList,
		Field,
		Link,
		ListToolbar,
		PageHeader,
		Pill,
		Select,
		type DataTableColumn
	} from '@smykla-skalski/sui';
	import { onMount } from 'svelte';
	import { errorMessage, request } from '$lib/api';
	import {
		collectionURL,
		displayName,
		editionSummary,
		formatPLN,
		kindLabels,
		spentLabel,
		statusLabels,
		type CollectionFilter,
		type CollectionItem,
		type CollectionView
	} from '$lib/collection';
	import { collectionSortOptions, sortCopies, type CollectionSort } from '$lib/collection-sort';
	import { databaseBadge, fetchStatus, type Status } from '$lib/status';

	let sort = $state<CollectionSort | null>('title');

	type Row = CollectionItem & Record<string, unknown>;

	let filter = $state<CollectionFilter>({ kind: '', format: '', status: '', q: '' });
	let view = $state<CollectionView | null>(null);
	let failure = $state<string | null>(null);
	let loading = $state(true);
	let info = $state<Status | null>(null);
	let seq = 0;

	async function load() {
		const mine = ++seq;
		loading = true;
		try {
			const got = await request<CollectionView>(fetch, 'GET', collectionURL(filter));
			if (mine !== seq) return;
			view = got;
			failure = null;
		} catch (err) {
			if (mine === seq) failure = errorMessage(err);
		} finally {
			if (mine === seq) loading = false;
		}
	}

	onMount(() => {
		void load();
		fetchStatus(fetch).then(
			(s) => (info = s),
			() => (info = null)
		);
	});

	function set<K extends keyof CollectionFilter>(key: K, value: CollectionFilter[K]) {
		filter[key] = value;
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
	const totals = $derived(view?.totals);
	const badge = $derived(info ? databaseBadge(info.database) : null);
	const filtered = $derived(
		filter.kind !== '' || filter.format !== '' || filter.status !== '' || filter.q.trim() !== ''
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
	<div class="collection-sort">
		<Field label="Sortowanie">
			{#snippet control(c)}
				<Select {...c} options={collectionSortOptions} bind:value={sort} />
			{/snippet}
		</Field>
	</div>

	<ListToolbar
		searchLabel="Szukaj w kolekcji"
		placeholder="Tytuł, autor, sklep lub kod kreskowy"
		query={filter.q}
		onsearch={(q: string) => set('q', q)}
		count={rows.length}
		countLabel={(_n: number, formatted: string) => `Pozycje: ${formatted}`}
		pending={loading}
		pendingLabel="Wczytywanie…"
		toolsLabel="Filtry"
		filter={{
			label: 'Typ',
			options: [
				{ value: '', label: 'Wszystko' },
				{ value: 'movie', label: 'Filmy' },
				{ value: 'book', label: 'Książki' },
				{ value: 'music', label: 'Muzyka' }
			],
			value: filter.kind,
			onchange: (v: string) => set('kind', v as CollectionFilter['kind'])
		}}
		tools={[
			{
				label: 'Nośnik',
				options: [
					{ value: '', label: 'Każdy' },
					{ value: 'dvd', label: 'DVD' },
					{ value: 'bluray', label: 'Blu-ray' },
					{ value: 'uhd', label: '4K UHD' },
					{ value: 'cd', label: 'CD' },
					{ value: 'vinyl', label: 'Winyl' },
					{ value: 'cassette', label: 'Kaseta' }
				],
				value: filter.format,
				onchange: (v: string) => set('format', v as CollectionFilter['format'])
			},
			{
				label: 'Status',
				options: [
					{ value: '', label: 'Każdy' },
					{ value: 'owned', label: 'Na półce' },
					{ value: 'wishlist', label: 'Lista życzeń' }
				],
				value: filter.status,
				onchange: (v: string) => set('status', v as CollectionFilter['status'])
			}
		]}
	/>

	<DataTable
		aria-label="Egzemplarze w kolekcji"
		{columns}
		rows={sortCopies(rows, sort ?? 'title')}
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
				{row.price_cents === null ? 'Nieznana' : formatPLN(row.price_cents)}
			{:else if column.key === 'shop'}
				{row.shop}
			{:else if column.key === 'purchased_on'}
				{row.purchased_on ?? ''}
			{/if}
		{/snippet}
		{#snippet empty()}
			{#if filtered}
				<EmptyState
					title="Nic nie pasuje"
					description="Żaden egzemplarz nie spełnia filtrów."
					step="Zmień filtry lub wyczyść wyszukiwanie."
				/>
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
	.collection-sort {
		width: min(100%, 16rem);
	}

	.content {
		display: grid;
		gap: 1rem;
		padding: var(--sui-pane-gutter, 1.5rem);
	}
</style>
