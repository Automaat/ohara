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
		SegmentedControl
	} from '@smykla-skalski/sui';
	import { onMount } from 'svelte';
	import { ApiError, errorMessage, request } from '#lib/api.js';
	import { badgePath, refreshDealBadge } from '#lib/deal-badge.svelte.js';
	import { options, type FormIssue } from '#lib/collection.js';
	import DealCard from '#lib/components/DealCard.svelte';
	import {
		dealsSummary,
		intervalLabel,
		isSent,
		keywordKindLabels,
		keywordPayload,
		keywordsOf,
		lastRunSummary,
		matchedPath,
		searchedFor,
		segmentLabel,
		type DealSegment,
		type DealsView,
		type HunterStatus,
		type Keyword,
		type KeywordKind,
		type Observation
	} from '#lib/deals.js';

	type Deals = {
		status: HunterStatus;
		wishes: number;
		under: Observation[];
		matched: Observation[];
	};

	let deals = $state<Deals | null>(null);
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
	 * "Pod limitem" asks the badge's own query, so the segment and the Okazje badge count the
	 * same listings. An answer that arrives after a later request's answer is dropped.
	 */
	async function load() {
		const ticket = ++issued;
		loading = true;
		try {
			const [under, matched] = await Promise.all([
				request<DealsView>(fetch, 'GET', badgePath),
				request<DealsView>(fetch, 'GET', matchedPath)
			]);
			if (ticket !== issued) return;
			const status = under.status.running ? under.status : matched.status;
			if (deals?.status.running && !status.running) void refreshDealBadge(fetch);
			deals = {
				status,
				wishes: matched.wishes,
				under: under.observations,
				matched: matched.observations
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

	const status = $derived(deals?.status);
	const lists = $derived<Record<DealSegment, Observation[]>>({
		under: deals?.under ?? [],
		all: deals?.matched ?? [],
		sent: (deals?.matched ?? []).filter(isSent)
	});
	const segments = $derived(
		(['under', 'all', 'sent'] as const).map((value) => ({
			value,
			label: segmentLabel(value, lists[value].length)
		}))
	);
	const shown = $derived(lists[segment]);
	const summary = $derived(
		deals ? dealsSummary(deals.wishes, deals.status, new Date()) : undefined
	);
	const searched = $derived(deals ? searchedFor(deals.wishes, deals.status, new Date()) : '');
	const runSummary = $derived(lastRunSummary(status?.last_result ?? null));
	const sourceErrors = $derived(status?.last_result?.errors ?? []);
	const busy = $derived(starting || !!status?.running);
	const nextSearch = $derived(
		status?.scheduled
			? `Kolejne wyszukiwanie ${intervalLabel(status)}.`
			: 'Wyszukaj oferty przyciskiem „Szukaj teraz”.'
	);
</script>

<svelte:head>
	<title>Okazje · Ohara</title>
</svelte:head>

{#snippet searchButton()}
	<Button tone="signal" loading={busy} onclick={runNow}>
		{status?.running ? 'Szukam…' : 'Szukaj teraz'}
	</Button>
{/snippet}

<PageHeader
	title="Okazje"
	description={summary}
	actions={deals && deals.wishes > 0 ? searchButton : undefined}
/>

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
					<Button tone="signal" href="/titles/new">Dodaj pozycję</Button>
				{/snippet}
			</EmptyState>
		{:else}
			<SegmentedControl label="Oferty" hideLabel options={segments} bind:value={segment} />
			{#if shown.length > 0}
				<ul class="deals" aria-label={segmentLabel(segment, shown.length)}>
					{#each shown as deal (deal.id)}
						<li><DealCard {deal} /></li>
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
</style>
