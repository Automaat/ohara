<script lang="ts">
	import {
		Button,
		DataTable,
		Link,
		ListToolbar,
		PageHeader,
		type DataTableColumn
	} from '@smykla-skalski/sui';
	import { onMount } from 'svelte';
	import { errorMessage, request } from '#lib/api.js';
	import { displayName, kindLabels, type Kind, type Title } from '#lib/collection.js';
	import { resultCount } from '#lib/polish.js';

	type Row = Title & Record<string, unknown>;

	let kind = $state<Kind | ''>('');
	let q = $state('');
	let titles = $state<Title[] | null>(null);
	let failure = $state<string | null>(null);
	let loading = $state(true);
	let seq = 0;

	async function load() {
		const mine = ++seq;
		loading = true;
		const params = new URLSearchParams();
		if (kind) params.set('kind', kind);
		if (q.trim()) params.set('q', q.trim());
		const qs = params.toString();
		try {
			const got = await request<Title[]>(fetch, 'GET', qs ? `/api/titles?${qs}` : '/api/titles');
			if (mine !== seq) return;
			titles = got;
			failure = null;
		} catch (err) {
			if (mine === seq) failure = errorMessage(err);
		} finally {
			if (mine === seq) loading = false;
		}
	}

	onMount(load);

	const columns: DataTableColumn<Row>[] = [
		{ key: 'name', label: 'Tytuł' },
		{ key: 'kind', label: 'Typ' },
		{ key: 'year', label: 'Rok', numeric: true },
		{ key: 'authors', label: 'Autorzy' },
		{ key: 'edition_count', label: 'Wydania', numeric: true },
		{ key: 'copy_count', label: 'Egzemplarze', numeric: true }
	];
	const rows = $derived((titles ?? []) as Row[]);
</script>

<svelte:head>
	<title>Katalog tytułów · Ohara</title>
</svelte:head>

<PageHeader
	title="Katalog tytułów"
	description="Tytuły filmów, książek i albumów muzycznych, niezależnie od wydania."
>
	{#snippet actions()}
		<Button tone="signal" href="/titles/new">Nowy tytuł</Button>
	{/snippet}
</PageHeader>

<div class="content">
	<ListToolbar
		searchLabel="Szukaj tytułów"
		placeholder="Tytuł lub autor"
		query={q}
		onsearch={(v: string) => {
			q = v;
			void load();
		}}
		count={rows.length}
		countLabel={resultCount}
		pending={loading}
		pendingLabel="Wczytywanie…"
		filter={{
			label: 'Typ',
			options: [
				{ value: '', label: 'Wszystko' },
				{ value: 'movie', label: 'Filmy' },
				{ value: 'book', label: 'Książki' },
				{ value: 'music', label: 'Muzyka' }
			],
			value: kind,
			onchange: (v: string) => {
				kind = v as Kind | '';
				void load();
			}
		}}
	/>
	{#if titles && !loading && !failure}
		<p class="metadata result-count" aria-hidden="true">{resultCount(rows.length)}</p>
	{/if}
	<DataTable
		aria-label="Tytuły"
		{columns}
		{rows}
		rowHeader
		rowKey={(r) => r.id}
		loading={loading && !titles}
		refreshing={loading && !!titles}
		error={failure}
		onretry={load}
		retryLabel="Spróbuj ponownie"
		loadingLabel="Wczytywanie tytułów…"
		refreshingLabel="Odświeżanie…"
		emptyLabel="Brak tytułów"
		scrollHint="Przewiń tabelę w bok"
	>
		{#snippet cell(row, column)}
			{#if column.key === 'name'}
				<Link href={`/titles/${row.id}`}>{displayName(row)}</Link>
			{:else if column.key === 'kind'}
				{kindLabels[row.kind]}
			{:else if column.key === 'authors'}
				{row.authors.join(', ')}
			{:else if column.key === 'year'}
				{row.year ?? ''}
			{:else if column.key === 'edition_count'}
				{row.edition_count}
			{:else if column.key === 'copy_count'}
				{row.copy_count}
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
	.result-count {
		margin: 0;
	}
</style>
