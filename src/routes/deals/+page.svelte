<script lang="ts">
	import {
		Button,
		Callout,
		Chip,
		DataTable,
		Disclosure,
		EmptyState,
		Field,
		FormError,
		Link,
		PageHeader,
		Pill,
		RelativeTime,
		Section,
		SegmentedControl,
		type DataTableColumn,
		type PillTone
	} from '@smykla-skalski/sui';
	import { onMount } from 'svelte';
	import { ApiError, errorMessage, request } from '$lib/api';
	import {
		acquiredLabels,
		collectionURL,
		formatPLN,
		options,
		type CollectionView,
		type FormIssue
	} from '$lib/collection';
	import {
		alertLabels,
		intervalLabel,
		keywordKindLabels,
		keywordPayload,
		keywordsOf,
		lastRunSummary,
		sourceLabels,
		type AlertState,
		type DealsView,
		type HunterStatus,
		type Keyword,
		type KeywordKind,
		type Observation
	} from '$lib/deals';

	type Row = Observation & Record<string, unknown>;

	let view = $state<DealsView | null>(null);
	let hasWishlist = $state(false);
	let failure = $state<string | null>(null);
	let loading = $state(true);
	let runError = $state<string | null>(null);
	let starting = $state(false);

	let keywords = $state<Keyword[]>([]);
	let keywordFailure = $state<string | null>(null);
	let kind = $state<KeywordKind>('reject');
	let phrase = $state('');
	let issue = $state<FormIssue | null>(null);
	let adding = $state(false);

	let poll: ReturnType<typeof setTimeout> | undefined;

	async function load() {
		loading = true;
		try {
			const next = await request<DealsView>(fetch, 'GET', '/api/deals');
			if (next.observations.length === 0) {
				const wishlist = await request<CollectionView>(
					fetch,
					'GET',
					collectionURL({ kind: '', format: '', status: 'wishlist', q: '' })
				);
				hasWishlist = wishlist.totals.wishlist > 0;
			}
			view = next;
			failure = null;
		} catch (err) {
			failure = errorMessage(err);
		} finally {
			loading = false;
		}
		clearTimeout(poll);
		if (view?.status.running) poll = setTimeout(load, 3000);
	}

	async function loadKeywords() {
		try {
			keywords = await request<Keyword[]>(fetch, 'GET', '/api/deals/keywords');
			keywordFailure = null;
		} catch (err) {
			keywordFailure = errorMessage(err);
		}
	}

	onMount(() => {
		void load();
		void loadKeywords();
		return () => clearTimeout(poll);
	});

	async function runNow() {
		starting = true;
		runError = null;
		try {
			await request<HunterStatus>(fetch, 'POST', '/api/deals/run');
		} catch (err) {
			runError =
				err instanceof ApiError && err.status === 409
					? 'Wyszukiwanie już trwa.'
					: errorMessage(err);
		}
		await load();
		starting = false;
	}

	async function addKeyword() {
		const parsed = keywordPayload(kind, phrase);
		if (!parsed.ok) {
			issue = parsed.issue;
			return;
		}
		adding = true;
		try {
			await request<Keyword>(fetch, 'POST', '/api/deals/keywords', parsed.value);
			phrase = '';
			issue = null;
			await loadKeywords();
		} catch (err) {
			issue = { field: 'phrase', message: errorMessage(err) };
		} finally {
			adding = false;
		}
	}

	async function removeKeyword(k: Keyword) {
		try {
			await request(fetch, 'DELETE', `/api/deals/keywords/${k.id}`);
			keywordFailure = null;
		} catch (err) {
			keywordFailure = errorMessage(err);
		}
		await loadKeywords();
	}

	const alertTones: Record<AlertState, PillTone> = {
		none: 'neutral',
		pending: 'info',
		sent: 'success',
		failed: 'danger'
	};

	const columns: DataTableColumn<Row>[] = [
		{ key: 'title_name', label: 'Pozycja' },
		{ key: 'listing_title', label: 'Oferta' },
		{ key: 'price_cents', label: 'Cena', numeric: true },
		{ key: 'source', label: 'Źródło' },
		{ key: 'alert', label: 'Powiadomienie' },
		{ key: 'observed_at', label: 'Znaleziono' }
	];
	const rows = $derived((view?.observations ?? []) as Row[]);
	const status = $derived(view?.status);
	const summary = $derived(lastRunSummary(status?.last_result ?? null));
	const sourceErrors = $derived(status?.last_result?.errors ?? []);
</script>

<svelte:head>
	<title>Okazje · Ohara</title>
</svelte:head>

{#snippet searchButton()}
	<Button tone="signal" loading={starting || !!status?.running} onclick={runNow}
		>{status?.running ? 'Szukam…' : 'Szukaj teraz'}</Button
	>
{/snippet}

<PageHeader
	title="Okazje"
	description="Oferty z OLX, Vinted, Pepper i Ceneo pasujące do pozycji z listy życzeń."
>
	{#snippet actions()}
		{#if rows.length > 0}
			{@render searchButton()}
		{/if}
	{/snippet}
</PageHeader>

<div class="content">
	<FormError message={runError} />
	{#if status}
		<p class="status" role="status">
			{#if status.scheduled}
				Szukamy ofert {intervalLabel(status)}.
			{:else}
				Oferty wyszukujesz ręcznie.
			{/if}
			{#if !status.telegram}
				Powiadomienia Telegram wyłączone.
			{/if}
			{#if status.last_finished_at}
				Ostatnio: <RelativeTime value={status.last_finished_at} locale="pl" />.
			{/if}
			{#if summary}
				{summary}.
			{/if}
		</p>
		{#if sourceErrors.length > 0}
			<Callout tone="warning" title="Niektóre źródła nie odpowiedziały">
				<ul>
					{#each sourceErrors as e, i (i)}
						<li>{e}</li>
					{/each}
				</ul>
			</Callout>
		{/if}
	{/if}

	{#if view && !failure && rows.length === 0}
		<EmptyState
			title={hasWishlist ? 'Brak pasujących ofert' : 'Lista życzeń jest pusta'}
			description={hasWishlist
				? 'Uruchom wyszukiwanie, aby sprawdzić, czy pojawiły się nowe oferty.'
				: 'Dodaj pozycję do listy życzeń, aby szukać pasujących ofert.'}
		>
			{#snippet action()}
				{#if hasWishlist}
					{@render searchButton()}
				{:else}
					<Button tone="signal" href="/titles/new">Dodaj pozycję</Button>
				{/if}
			{/snippet}
		</EmptyState>
	{:else}
		<DataTable
			aria-label="Znalezione oferty"
			{columns}
			{rows}
			rowKey={(r) => r.id}
			loading={loading && !view}
			refreshing={loading && !!view}
			error={failure}
			onretry={load}
			retryLabel="Spróbuj ponownie"
			loadingLabel="Wczytywanie ofert…"
			refreshingLabel="Odświeżanie…"
			emptyLabel="Brak ofert"
			scrollHint="Przewiń tabelę w bok"
		>
			{#snippet cell(row, column)}
				{#if column.key === 'title_name'}
					{#if row.title_id}
						<Link href={`/titles/${row.title_id}`}>{row.title_name ?? 'Tytuł'}</Link>
					{:else}
						Usunięty tytuł
					{/if}
				{:else if column.key === 'listing_title'}
					<Link href={row.url} external externalLabel="otwiera się w nowej karcie"
						>{row.listing_title || row.url}</Link
					>
				{:else if column.key === 'price_cents'}
					{formatPLN(row.price_cents)}
				{:else if column.key === 'source'}
					{sourceLabels[row.source]}{row.acquired_as ? ` · ${acquiredLabels[row.acquired_as]}` : ''}
				{:else if column.key === 'alert'}
					<Pill tone={alertTones[row.alert]} title={row.alert_error || undefined}
						>{alertLabels[row.alert]}</Pill
					>
				{:else if column.key === 'observed_at'}
					<RelativeTime value={row.observed_at} locale="pl" />
				{/if}
			{/snippet}
		</DataTable>
	{/if}

	<Disclosure summary="Ustawienia wyszukiwania">
		<Section
			title="Słowa kluczowe"
			description="Oferta z frazą odrzucaną w tytule jest pomijana, chyba że fraza należy do dopuszczanej. Fraza pasuje też do dłuższych słów: „uszkodzon” odrzuca „uszkodzona” i „uszkodzony”."
		>
			<FormError message={keywordFailure} />
			{#each ['reject', 'accept'] as const as k (k)}
				<div class="keywords">
					<h3>{keywordKindLabels[k]}</h3>
					<div class="chips">
						{#each keywordsOf(keywords, k) as word (word.id)}
							<Chip
								label={word.phrase}
								onremove={() => removeKeyword(word)}
								removeLabel={(l) => `Usuń „${l}”`}
								removedLabel={(l) => `Usunięto „${l}”`}
							/>
						{:else}
							<p class="empty">Brak fraz.</p>
						{/each}
					</div>
				</div>
			{/each}
			<form
				class="add"
				novalidate
				onsubmit={(e) => {
					e.preventDefault();
					void addKeyword();
				}}
			>
				<SegmentedControl label="Lista" options={options(keywordKindLabels)} bind:value={kind} />
				<Field
					label="Fraza"
					placeholder="np. sama płyta"
					bind:value={phrase}
					error={issue?.message}
				/>
				<div>
					<Button tone="add" type="submit" loading={adding}>Dodaj frazę</Button>
				</div>
			</form>
		</Section>
	</Disclosure>
</div>

<style>
	.content {
		display: grid;
		gap: 1.5rem;
		padding: var(--sui-pane-gutter, 1.5rem);
	}
	.status {
		margin: 0;
	}
	.keywords h3 {
		margin: 0 0 0.5rem;
		font-size: 1rem;
	}
	.keywords + .keywords {
		margin-top: 1rem;
	}
	.chips {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
	}
	.empty {
		margin: 0;
	}
	.add {
		display: grid;
		gap: 1rem;
		max-width: 40rem;
		margin-top: 1.5rem;
	}
</style>
