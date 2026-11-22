<script lang="ts">
	import {
		Button,
		Callout,
		Chip,
		Disclosure,
		EmptyState,
		ErrorState,
		Field,
		FormError,
		PageHeader,
		Section,
		SegmentedControl,
		Toaster,
		dismiss,
		toast
	} from '@smykla-skalski/sui';
	import { onMount, tick } from 'svelte';
	import { ApiError, errorMessage, request } from '#lib/api.js';
	import { dealBadge, forgetDealListing, loadUnderLimit } from '#lib/deal-badge.svelte.js';
	import { options, type FormIssue } from '#lib/collection.js';
	import DealCard from '#lib/components/DealCard.svelte';
	import { WantlistSync } from '#lib/wantlist-sync.svelte.js';
	import WishlistShare from '#lib/components/WishlistShare.svelte';
	import { DealDismissals } from '#lib/deal-dismissals.svelte.js';
	import {
		dealsSummary,
		dismissalPath,
		intervalLabel,
		keywordKindLabels,
		keywordPayload,
		keywordsOf,
		lastRunSummary,
		marketFor,
		marketPath,
		matchedPath,
		searchedFor,
		segmentLabel,
		sentPath,
		wantlistPath,
		wantlistSummary,
		withoutDismissed,
		type DealSegment,
		type DealsView,
		type HunterStatus,
		type Keyword,
		type KeywordKind,
		type MarketPrice,
		type MarketView,
		type Observation,
		type WantlistStatus
	} from '#lib/deals.js';

	type Deals = {
		status: HunterStatus;
		wishes: number;
		/** The wishes the hunter looks for: TV series are left out. */
		searchedWishes: number;
		all: Observation[];
		sent: Observation[];
	};

	let deals = $state<Deals | null>(null);
	let markets = $state<MarketPrice[]>([]);
	let now = $state(new Date());
	let segment = $state<DealSegment>('under');
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
	let issued = 0;

	/**
	 * "Pod limitem" lists the listings the Okazje badge last counted, so a refresh from this page,
	 * a navigation or a returning tab moves the segment and the badge together. The page asks again
	 * when the tab becomes visible, so the other segments follow. An answer that arrives after a
	 * later request's answer is dropped. The search counts as running while any answer says so.
	 * The wishes' market prices load beside them without holding them back.
	 */
	async function load() {
		const ticket = ++issued;
		loading = true;
		try {
			void loadMarkets();
			const [under, all, sent] = await Promise.all([
				loadUnderLimit(fetch),
				request<DealsView>(fetch, 'GET', matchedPath),
				request<DealsView>(fetch, 'GET', sentPath)
			]);
			if (ticket !== issued) return;
			now = new Date();
			deals = {
				status: [under, all, sent].find((v) => v.status.running)?.status ?? all.status,
				wishes: all.wishes,
				searchedWishes: all.searched_wishes,
				all: all.observations,
				sent: sent.observations
			};
			failure = null;
		} catch (err) {
			if (ticket !== issued) return;
			failure = errorMessage(err);
		}
		loading = false;
		clearTimeout(poll);
		if (deals?.status.running) poll = setTimeout(load, 3000);
	}

	/**
	 * The wishes' market prices, asked beside the listings and drawn when they come, so a slow
	 * answer never holds the listings back. A failed request keeps the last prices, and an answer
	 * older than one already drawn is dropped.
	 */
	let marketsAsked = 0;
	let marketsDrawn = 0;
	async function loadMarkets() {
		const ticket = ++marketsAsked;
		const view = await request<MarketView>(fetch, 'GET', marketPath()).catch(() => null);
		if (view && ticket > marketsDrawn) {
			marketsDrawn = ticket;
			markets = view.markets;
		}
	}

	const dismissals = new DealDismissals({
		toast,
		dismiss,
		send: (method, id) => request(fetch, method, dismissalPath(id)),
		forget: forgetDealListing,
		reload: load
	});

	/**
	 * The card's "Ignoruj" goes with its card, so focus moves to the next card's "Ignoruj", or the
	 * previous one's, or the segments when the list is empty.
	 */
	async function dismissDeal(deal: Observation) {
		const at = shown.findIndex((o) => o.id === deal.id);
		const next = shown[at + 1] ?? shown[at - 1];
		const done = dismissals.dismiss(deal);
		await tick();
		const target = next
			? document.querySelector<HTMLElement>(`[data-dismiss="${next.id}"]`)
			: document.querySelector<HTMLElement>('.content input[type="radio"]:checked');
		target?.focus();
		await done;
	}

	async function loadKeywords() {
		try {
			keywords = await request<Keyword[]>(fetch, 'GET', '/api/deals/keywords');
			keywordFailure = null;
		} catch (err) {
			keywordFailure = errorMessage(err);
		}
	}

	/**
	 * A visible page asks for its lists again every 10 minutes, so a listing a scheduled run found
	 * gone leaves the page, and a Discogs card never stays longer than the API terms' six hours: the
	 * server shows one only while its seller offered it within 5 h 45 min (deals.discogsShown).
	 */
	const reloadEvery = 10 * 60_000;

	/** The Discogs wantlist sync; without a Discogs token the server says it is disabled. */
	const wantlist = new WantlistSync({
		status: () => request<WantlistStatus>(fetch, 'GET', wantlistPath),
		start: () => request<WantlistStatus>(fetch, 'POST', `${wantlistPath}/sync`)
	});

	onMount(() => {
		void load();
		void loadKeywords();
		void wantlist.load();
		const clock = setInterval(() => (now = new Date()), 30_000);
		const reload = setInterval(() => {
			if (document.visibilityState === 'visible') void load();
		}, reloadEvery);
		const onvisibility = () => {
			if (document.visibilityState === 'visible') void load();
		};
		document.addEventListener('visibilitychange', onvisibility);
		return () => {
			dismissals.destroy();
			clearTimeout(poll);
			wantlist.destroy();
			clearInterval(clock);
			clearInterval(reload);
			document.removeEventListener('visibilitychange', onvisibility);
		};
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

	const status = $derived(deals?.status);
	const lists = $derived<Record<DealSegment, Observation[]>>({
		under: withoutDismissed(deals ? (dealBadge.listings ?? []) : [], dismissals.hidden),
		all: withoutDismissed(deals?.all ?? [], dismissals.hidden),
		sent: withoutDismissed(deals?.sent ?? [], dismissals.hidden)
	});
	const segments = $derived(
		(['under', 'all', 'sent'] as const).map((value) => ({
			value,
			label: segmentLabel(value, lists[value].length)
		}))
	);
	const shown = $derived(lists[segment]);
	const summary = $derived(deals ? dealsSummary(deals.wishes, deals.status, now) : undefined);
	const searched = $derived(deals ? searchedFor(deals.searchedWishes, deals.status, now) : '');
	const runSummary = $derived(lastRunSummary(status?.last_result ?? null));
	const sourceErrors = $derived(status?.last_result?.errors ?? []);
	const busy = $derived(starting || !!status?.running);
	const nextSearch = $derived(
		status?.scheduled
			? `Kolejne wyszukiwanie ${intervalLabel(status)}.`
			: 'Wyszukaj oferty przyciskiem „Szukaj teraz”.'
	);

	const syncSummary = $derived(wantlistSummary(wantlist.status?.last_result ?? null));

	/** The add page started as a wish, searching titles the same way the scanner does. */
	const addWish = '/titles/new?from=deals';
</script>

<svelte:head>
	<title>Okazje · Ohara</title>
</svelte:head>

{#snippet headerActions()}
	<Button href={addWish}>Dodaj pozycję</Button>
	{#if deals && deals.wishes > 0}
		<Button tone="signal" loading={busy} onclick={runNow}>
			{status?.running ? 'Szukam…' : 'Szukaj teraz'}
		</Button>
	{/if}
{/snippet}

<PageHeader title="Okazje" description={summary} actions={headerActions} />

<div class="content">
	<FormError message={runError} />
	{#if !deals}
		{#if failure}
			<ErrorState
				title="Nie udało się wczytać ofert"
				description={failure}
				onretry={load}
				busy={loading}
				retryLabel="Spróbuj ponownie"
				retryingLabel="Ponawianie…"
			/>
		{:else}
			<p role="status">Wczytywanie ofert…</p>
		{/if}
	{:else}
		{#if failure}
			<ErrorState
				title="Nie udało się odświeżyć ofert"
				description={failure}
				onretry={load}
				busy={loading}
				overContent
				retryLabel="Spróbuj ponownie"
				retryingLabel="Ponawianie…"
			/>
		{/if}
		{#if deals.wishes === 0}
			<EmptyState
				title="Lista życzeń jest pusta"
				description="Okazji szukamy dla pozycji z listy życzeń."
			>
				{#snippet action()}
					<Button tone="signal" href={addWish}>Dodaj pozycję</Button>
				{/snippet}
			</EmptyState>
		{:else}
			<SegmentedControl label="Oferty" hideLabel options={segments} bind:value={segment} />
			{#if shown.length > 0}
				<ul class="deals" aria-label={segmentLabel(segment, shown.length)}>
					{#each shown as deal (deal.id)}
						<li>
							<DealCard
								{deal}
								market={marketFor(markets, deal.title_id, deal.format)}
								ondismiss={() => void dismissDeal(deal)}
							/>
						</li>
					{/each}
				</ul>
			{:else if segment === 'under' && lists.all.length > 0}
				<EmptyState title="Brak ofert pod limitem" description={searched}>
					{#snippet action()}
						<Button onclick={() => (segment = 'all')}>Pokaż wszystkie</Button>
					{/snippet}
				</EmptyState>
			{:else if segment === 'under'}
				<EmptyState title="Brak ofert pod limitem" description={searched} step={nextSearch} />
			{:else if segment === 'all'}
				<EmptyState title="Brak ofert z listy życzeń" description={searched} step={nextSearch} />
			{:else}
				<EmptyState
					title="Brak wysłanych powiadomień"
					description={status?.telegram
						? 'Powiadomienie wysyłamy, gdy oferta spadnie do limitu pozycji z listy życzeń.'
						: 'Powiadomienia Telegram są wyłączone.'}
					step="Ustaw limit ceny w pozycji z listy życzeń."
				/>
			{/if}
		{/if}

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
				{#if runSummary}
					{runSummary}.
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
	{/if}

	{#if wantlist.status?.enabled}
		<Section
			title="Lista życzeń Discogs"
			description="Dodaje albumy z listy życzeń, które mają wydanie Discogs, do Twojej listy życzeń na Discogs. Usuwa tylko te, które dodała Ohara, gdy znikną z listy życzeń."
		>
			<FormError message={wantlist.error ?? wantlist.failure} />
			<div>
				<Button loading={wantlist.syncing} onclick={() => void wantlist.sync()}>
					{wantlist.status.running ? 'Synchronizuję…' : 'Synchronizuj z Discogs'}
				</Button>
			</div>
			{#if syncSummary}
				<p class="status metadata" role="status">{syncSummary}</p>
			{/if}
		</Section>
	{:else if wantlist.failure}
		<ErrorState
			title="Nie udało się sprawdzić synchronizacji z Discogs"
			description={wantlist.failure}
			onretry={() => void wantlist.load()}
			busy={wantlist.loading}
			retryLabel="Spróbuj ponownie"
			retryingLabel="Ponawianie…"
		/>
	{/if}

	<WishlistShare />

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

<div class="toasts">
	<Toaster label="Powiadomienia" dismissLabel="Zamknij" />
</div>

<style>
	.content {
		display: grid;
		gap: 0.75rem;
		padding: var(--sui-pane-gutter, 1.5rem);
	}
	.deals {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(min(100%, 24rem), 1fr));
		gap: 0.75rem;
		margin: 0;
		padding: 0;
		list-style: none;
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
	/* On a phone the undo toast stands above the floating tab bar instead of covering it. */
	:global(.ohara-phone) .toasts :global(.sui-toaster) {
		inset-block-end: calc(4rem + max(env(safe-area-inset-bottom, 0px), 0.75rem));
	}
</style>
