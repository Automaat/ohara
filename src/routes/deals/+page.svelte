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
	import { onMount, tick } from 'svelte';
	import { ApiError, errorMessage, request } from '#lib/api.js';
	import { showDeals } from '#lib/deal-badge.svelte.js';
	import {
		acquiredLabels,
		collectionURL,
		formatLabels,
		formatPLN,
		options,
		type CollectionView,
		type FormIssue
	} from '#lib/collection.js';
	import {
		alertLabels,
		intervalLabel,
		keywordKindLabels,
		keywordPayload,
		keywordsOf,
		lastRunSummary,
		limitComparison,
		sourceLabels,
		type AlertState,
		type DealsView,
		type HunterStatus,
		type Keyword,
		type KeywordKind,
		type Observation
	} from '#lib/deals.js';

	type Row = Observation & Record<string, unknown>;

	let view = $state<DealsView | null>(null);
	let hasWishlist = $state<boolean | null>(null);
	let wishlistFailure = $state<string | null>(null);
	let emptySearch = $state<HTMLButtonElement | HTMLAnchorElement | null>(null);
	let headerSearch = $state<HTMLButtonElement | HTMLAnchorElement | null>(null);
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
			showDeals(next.observations);
			const restoreFocus = emptySearch !== null && document.activeElement === emptySearch;
			view = next;
			failure = null;
			if (next.observations.length === 0) {
				await loadWishlist();
			} else if (restoreFocus) {
				await tick();
				if (document.activeElement === document.body) headerSearch?.focus();
			}
		} catch (err) {
			failure = errorMessage(err);
		} finally {
			loading = false;
		}
		clearTimeout(poll);
		if (view?.status.running) poll = setTimeout(load, 3000);
	}

	async function loadWishlist() {
		try {
			const wishlist = await request<CollectionView>(
				fetch,
				'GET',
				collectionURL({ kind: '', format: '', status: 'wishlist', q: '' })
			);
			hasWishlist = wishlist.totals.wishlist > 0;
			wishlistFailure = null;
		} catch (err) {
			wishlistFailure = errorMessage(err);
		}
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
		{ key: 'listing_title', label: 'Oferta' },
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

<PageHeader
	title="Okazje"
	description="Oferty z OLX, Vinted, Pepper i Ceneo pasujące do pozycji z listy życzeń."
>
	{#snippet actions()}
		{#if rows.length > 0}
			<Button
				bind:element={headerSearch}
				tone="signal"
				loading={starting || !!status?.running}
				onclick={runNow}
			>
				{status?.running ? 'Szukam…' : 'Szukaj teraz'}
			</Button>
		{/if}
	{/snippet}
</PageHeader>

<div class="content">
	<FormError message={runError} />
	{#if status}
		<p class="status metadata" role="status">
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
		{#if wishlistFailure}
			<Callout tone="warning" title="Nie udało się sprawdzić listy życzeń">
				<p>{wishlistFailure}</p>
				<Button {loading} onclick={load}>Spróbuj ponownie</Button>
			</Callout>
		{/if}
		<EmptyState
			title={hasWishlist === false ? 'Lista życzeń jest pusta' : 'Brak pasujących ofert'}
			description={hasWishlist === null
				? ''
				: hasWishlist
					? 'Uruchom wyszukiwanie, aby sprawdzić, czy pojawiły się nowe oferty.'
					: 'Dodaj pozycję do listy życzeń, aby szukać pasujących ofert.'}
		>
			{#snippet action()}
				{#if hasWishlist}
					<Button
						bind:element={emptySearch}
						tone="signal"
						loading={starting || !!status?.running}
						onclick={runNow}
					>
						{status?.running ? 'Szukam…' : 'Szukaj teraz'}
					</Button>
				{:else if hasWishlist === false}
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
				{#if column.key === 'listing_title'}
					<div class="offer">
						<div>
							<Link href={row.url} external externalLabel="otwiera się w nowej karcie">
								{row.listing_title || row.url}
							</Link>
							<div class="metadata">
								{sourceLabels[row.source]}{row.acquired_as
									? ` · ${acquiredLabels[row.acquired_as]}`
									: ''}
							</div>
						</div>
						<div>
							<strong class="price">{formatPLN(row.price_cents)}</strong>
							<div>{limitComparison(row.price_cents, row.max_price_cents)}</div>
							{#if row.max_price_cents != null}
								<div class="metadata">Limit {formatPLN(row.max_price_cents)}</div>
							{/if}
						</div>
						<div>
							{#if row.title_id}
								<Link
									href={row.edition_id ? `/editions/${row.edition_id}` : `/titles/${row.title_id}`}
								>
									{row.title_name ?? 'Tytuł'}
								</Link>
							{:else}
								Usunięty tytuł
							{/if}
							{#if row.format}
								<span class="metadata"> · {formatLabels[row.format]}</span>
							{/if}
						</div>
						{#if row.listing_photo_url || row.cover_url}
							<div class="artwork">
								{#each [{ url: row.listing_photo_url, label: 'Zdjęcie oferty' }, { url: row.cover_url, label: 'Okładka wydania' }] as image (image.label)}
									{#if image.url}
										<figure>
											<img src={image.url} alt="" loading="lazy" referrerpolicy="no-referrer" />
											<figcaption>{image.label}</figcaption>
										</figure>
									{/if}
								{/each}
							</div>
						{/if}
					</div>
				{:else if column.key === 'alert'}
					<Pill tone={alertTones[row.alert]} title={row.alert_error || undefined}>
						{alertLabels[row.alert]}
					</Pill>
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
	.offer {
		display: grid;
		gap: 0.5rem;
		overflow-wrap: anywhere;
		/* The table cell's `font` resets the figures; the price and limit lines take them back. */
		font-variant-numeric: tabular-nums;
	}
	.artwork {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 6rem));
		gap: 0.5rem;
	}
	.artwork figure {
		margin: 0;
		font-size: 0.75rem;
	}
	.artwork img {
		display: block;
		width: 100%;
		aspect-ratio: 1;
		object-fit: contain;
		object-position: center;
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
