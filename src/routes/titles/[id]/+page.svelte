<script lang="ts">
	import {
		ActionMenu,
		Button,
		Card,
		ConfirmDialog,
		ErrorState,
		FactList,
		Field,
		FormError,
		Icon,
		Link,
		Modal,
		Section,
		Select,
		Skeleton
	} from '@smykla-skalski/sui';
	import { onDestroy } from 'svelte';
	import { afterNavigate, goto, replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import { errorMessage, issueFrom, request } from '#lib/api.js';
	import { titlePreview } from '#lib/collection-cache.js';
	import {
		copyPayload,
		copyToForm,
		displayName,
		editionPayload,
		editionToForm,
		seriesLine,
		tagHref,
		titlePayload,
		titleToForm,
		type Copy,
		type CopyStatus,
		type Edition,
		type FormIssue,
		type RatingPayload,
		type Series,
		type TagCount,
		type Title,
		type TitleDetail
	} from '#lib/collection.js';
	import CollectionCover from '#lib/components/CollectionCover.svelte';
	import CopyFormFields from '#lib/components/CopyFormFields.svelte';
	import EditionFormFields from '#lib/components/EditionFormFields.svelte';
	import TitleFormFields from '#lib/components/TitleFormFields.svelte';
	import TitleHero from '#lib/components/TitleHero.svelte';
	import { openedTile } from '#lib/cover-transition.js';
	import {
		addCopyHref,
		addCopyStart,
		applyFinish,
		copyFacts,
		copyLine,
		copyRequest,
		editionArtwork,
		editionName,
		finishAction,
		finishDay,
		finishedLabel,
		finishedLine,
		finishPayload,
		noRating,
		openedFromCollection,
		plainClick,
		previewHero,
		ratingOptions,
		soleCopy,
		titleHero,
		todayInPoland,
		withoutFinish,
		trendHeight,
		trendWidth,
		valueCard,
		type Hero
	} from '#lib/title-detail.js';
	import { newRequestKey } from '#lib/scan.js';
	import { RatingSaver } from '#lib/title-rating.js';
	import { TitleMarkets } from '#lib/title-market.svelte.js';
	import type { CopyValue, CopyValueDay, CopyValueHistory, TitleValues } from '#lib/valuation.js';

	/**
	 * The copy sheet edits a copy or adds one to an edition. An add keeps one request key for every
	 * attempt, so a retry after a lost answer returns the copy instead of adding a second one.
	 */
	type CopyTarget =
		{ mode: 'edit'; copy: Copy } | { mode: 'add'; editionID: number; requestKey: string };

	const id = $derived(Number(page.params.id));
	/** The heading of the card of a title's one copy. */
	const soleCopyHeading: Record<CopyStatus, string> = {
		owned: 'Twój egzemplarz',
		ordered: 'Zamówiony egzemplarz',
		wishlist: 'Na liście życzeń',
		sold: 'Sprzedany egzemplarz'
	};

	let detail = $state<TitleDetail | null>(null);
	/** The title as the collection showed it, drawn in the hero until the title loads. */
	let preview = $state<Hero | null>(null);
	const hero = $derived(detail ? titleHero(detail) : preview);
	let values = $state<CopyValue[]>([]);
	let valueDays = $state<CopyValueDay[]>([]);
	let failure = $state<string | null>(null);
	/** A failed refresh of the title already on the page, which stays in view. */
	let refreshError = $state<string | null>(null);
	let busy = $state(false);
	/** Counts loads, so a slower answer for an earlier load never replaces a newer one. */
	let loads = 0;
	/** The title the page was last asked for; moving to another one closes this one's forms. */
	let requested: number | undefined;

	/** Stores the open title's rating picks; replaced when another title opens. */
	let ratings: RatingSaver | null = null;
	let ratingError = $state<string | null>(null);

	let titleSheet = $state(false);
	let titleForm = $state(titleToForm(null));
	let titleIssue = $state<FormIssue | null>(null);
	let savingTitle = $state(false);
	/** The tags in use, read when the title form opens; without them the form takes typed tags. */
	let tagSuggestions = $state<string[]>([]);
	/** The series in use, read when the title form opens, for its "Seria" suggestions. */
	let seriesSuggestions = $state<Series[]>([]);

	/** The sheet that changes or removes a finished title's day, or takes the mark back. */
	let finishSheet = $state(false);
	let finishText = $state('');
	let finishIssue = $state<FormIssue | null>(null);
	let finishing = $state(false);

	let copySheet = $state(false);
	let copyTarget = $state<CopyTarget | null>(null);
	let copyForm = $state(copyToForm(null));
	let copyIssue = $state<FormIssue | null>(null);
	let savingCopy = $state(false);

	let adding = $state(false);
	/** A link's request to add a copy, used by the first answer for the title it opened. */
	const addRequest = copyRequest();
	onDestroy(addRequest.close);
	/** Set while a new edition is the first step of adding a copy, so its page opens the copy form. */
	let copyAfterEdition = false;
	/** Set when a request to add a copy leaves the choice among several editions to the reader. */
	let pickEdition = $state(false);
	let editionForm = $state(editionToForm(null));
	let editionIssue = $state<FormIssue | null>(null);
	let savingEdition = $state(false);
	/**
	 * The open edition form's key, sent with every attempt to save it, so a retry after a lost
	 * answer opens the edition instead of adding a second one.
	 */
	let editionKey = '';

	let confirmDelete = $state(false);
	let deleteError = $state<string | null>(null);

	const sole = $derived(detail ? soleCopy(detail) : null);
	const copyEdition = $derived.by(() => {
		const target = copyTarget;
		if (!target) return undefined;
		const editionID = target.mode === 'edit' ? target.copy.edition_id : target.editionID;
		return detail?.editions.find((e) => e.id === editionID);
	});
	const value = $derived(sole ? valueCard(sole.copy, values, valueDays) : null);

	/** The wish's market price, asked once the copy sheet shows a wish's limit. */
	const markets = new TitleMarkets(fetch);
	$effect(() => {
		if (detail && copySheet && copyForm.status === 'wishlist') void markets.load(detail.id);
	});

	async function load(titleID: number) {
		if (titleID !== requested) {
			requested = titleID;
			detail = null;
			preview = null;
			values = [];
			valueDays = [];
			failure = null;
			refreshError = null;
			ratings = null;
			ratingError = null;
			titleSheet = false;
			copySheet = false;
			adding = false;
			copyAfterEdition = false;
			pickEdition = false;
			addRequest.open(titleID, page.url.searchParams);
			confirmDelete = false;
			deleteError = null;
		}
		const current = ++loads;
		busy = true;
		const mark = ratings?.mark() ?? 0;
		try {
			const next = await request<TitleDetail>(fetch, 'GET', `/api/titles/${titleID}`);
			if (current !== loads) return;
			next.rating = ratingFrom(titleID, next.rating ?? null, mark);
			detail = next;
			failure = null;
			refreshError = null;
			void loadValues(next, current);
			if (addRequest.take(titleID)) startCopyFromLink(next);
		} catch (err) {
			if (current !== loads) return;
			if (detail?.id === titleID) {
				refreshError = `Nie udało się odświeżyć tytułu: ${errorMessage(err)}`;
			} else {
				failure = errorMessage(err);
				preview = null;
			}
		} finally {
			if (current === loads) busy = false;
		}
	}

	/**
	 * Today's values and the copy's daily ones, asked only when the page leads with one owned copy.
	 * A failed request for today's leaves the copy without a value card, as a copy the valuation has
	 * not priced yet; a failed one for the days leaves the card without its trend.
	 */
	async function loadValues(t: TitleDetail, current: number) {
		if (soleCopy(t)?.copy.status !== 'owned') {
			values = [];
			valueDays = [];
			return;
		}
		void loadValueDays(t, current);
		try {
			const answer = await request<TitleValues>(
				fetch,
				'GET',
				`/api/valuation/copies?title_id=${t.id}`
			);
			if (current === loads) values = answer.copies;
		} catch {
			if (current === loads) values = [];
		}
	}

	async function loadValueDays(t: TitleDetail, current: number) {
		try {
			const answer = await request<CopyValueHistory>(
				fetch,
				'GET',
				`/api/valuation/copies/history?title_id=${t.id}`
			);
			if (current === loads) valueDays = answer.history;
		} catch {
			if (current === loads) valueDays = [];
		}
	}

	$effect(() => {
		void load(id);
	});

	afterNavigate((navigation) => {
		if (openedFromCollection(navigation) && !page.state.fromCollection) {
			replaceState('', { ...page.state, fromCollection: true });
		}
		// A title opened from the collection shows its hero at once, so the cover carried from
		// the tapped tile has a place to land however slowly the title answers.
		if (navigation.from?.url.pathname === '/' && !detail) {
			const shown = titlePreview(id, openedTile(id));
			preview = shown ? previewHero(shown) : null;
		}
	});

	/**
	 * Goes back to the collection by stepping back in history when the collection opened this
	 * page, so it returns filtered and scrolled as the reader left it and the cover lands on its
	 * tile. Otherwise, and for a click that opens a new tab, the link goes to `/` as usual.
	 */
	function backToCollection(event: MouseEvent) {
		if (!page.state.fromCollection || !plainClick(event)) return;
		event.preventDefault();
		history.back();
	}

	function editTitle(t: Title) {
		titleForm = titleToForm(t);
		titleIssue = null;
		titleSheet = true;
		request<TagCount[]>(fetch, 'GET', '/api/tags').then(
			(tags) => (tagSuggestions = tags.map((tag) => tag.name)),
			() => {
				// Suggestions only help; a tag can still be typed.
			}
		);
		request<Series[]>(fetch, 'GET', '/api/series').then(
			(series) => (seriesSuggestions = series),
			() => {
				// Suggestions only help; a series can still be typed.
			}
		);
	}

	async function saveTitle() {
		const parsed = titlePayload(titleForm);
		if (!parsed.ok) {
			titleIssue = parsed.issue;
			return;
		}
		savingTitle = true;
		const mark = ratings?.mark() ?? 0;
		try {
			const saved = await request<Title>(fetch, 'PUT', `/api/titles/${id}`, parsed.value);
			if (detail?.id === saved.id) {
				Object.assign(detail, withoutFinish(saved));
				detail.rating = ratingFrom(saved.id, saved.rating ?? null, mark);
			}
			titleSheet = false;
			await load(id);
		} catch (err) {
			titleIssue = issueFrom(err);
		} finally {
			savingTitle = false;
		}
	}

	function openFinish(t: Title) {
		finishText = t.finished_on ?? '';
		finishIssue = null;
		finishSheet = true;
	}

	/**
	 * Records the title as finished on a day (null when unknown) or not finished, sending only that
	 * state, and shows the answer at once.
	 */
	async function saveFinished(t: Title, finished: boolean, day: string | null) {
		if (finishing) return;
		finishing = true;
		finishIssue = null;
		try {
			const saved = await request<Title>(
				fetch,
				'PUT',
				`/api/titles/${t.id}/finished`,
				finishPayload(finished, day)
			);
			if (detail?.id === saved.id) applyFinish(detail, saved);
			finishSheet = false;
			// A refresh started before this answer would bring the old state back; a newer one wins.
			if (requested === saved.id) void load(saved.id);
		} catch (err) {
			finishIssue = issueFrom(err);
		} finally {
			finishing = false;
		}
	}

	function submitFinish(t: Title) {
		const day = finishDay(finishText, todayInPoland(new Date()));
		if (!day.ok) {
			finishIssue = day.issue;
			return;
		}
		void saveFinished(t, true, day.value);
	}

	/**
	 * The rating a read of title `titleID` begun at `mark` shows: the one it read, unless a pick
	 * made since then, or still on its way, says otherwise.
	 */
	function ratingFrom(titleID: number, read: number | null, mark: number): number | null {
		if (ratings?.titleID !== titleID) {
			const saver = new RatingSaver(
				titleID,
				read,
				async (ratedID, rating) => {
					const body: RatingPayload = { rating };
					const saved = await request<Title>(fetch, 'PUT', `/api/titles/${ratedID}/rating`, body);
					return saved.rating ?? null;
				},
				(view) => {
					if (ratings !== saver || detail?.id !== titleID) return;
					detail.rating = view.rating;
					ratingError =
						view.failed === null
							? null
							: `Nie udało się zapisać oceny: ${errorMessage(view.failed)}`;
				}
			);
			ratings = saver;
			return read;
		}
		return ratings.loaded(read, mark);
	}

	/** Stores a picked rating without the edit form; the select shows it at once. */
	function rate(picked: number | null) {
		void ratings?.pick(picked === null || picked === noRating ? null : picked);
	}

	/** Shows a saved copy at once, so it stays in view even when the refresh after it fails. */
	function applyCopy(saved: Copy) {
		const edition = detail?.editions.find((e) => e.id === saved.edition_id);
		if (!edition) return;
		const i = edition.copies.findIndex((c) => c.id === saved.id);
		if (i === -1) edition.copies.push(saved);
		else edition.copies[i] = saved;
	}

	function openCopy(target: CopyTarget) {
		copyTarget = target;
		copyForm = copyToForm(target.mode === 'edit' ? target.copy : null);
		copyIssue = null;
		copySheet = true;
	}

	async function saveCopy(target: CopyTarget) {
		const editionID = target.mode === 'edit' ? target.copy.edition_id : target.editionID;
		const parsed = copyPayload(copyForm, editionID);
		if (!parsed.ok) {
			copyIssue = parsed.issue;
			return;
		}
		savingCopy = true;
		try {
			const saved =
				target.mode === 'edit'
					? await request<Copy>(fetch, 'PUT', `/api/copies/${target.copy.id}`, parsed.value)
					: await request<Copy>(fetch, 'POST', '/api/copies', {
							...parsed.value,
							request_key: target.requestKey
						});
			applyCopy(saved);
			copySheet = false;
			await load(id);
		} catch (err) {
			copyIssue = issueFrom(err);
		} finally {
			savingCopy = false;
		}
	}

	function startEdition() {
		editionForm = editionToForm(null);
		editionIssue = null;
		editionKey = newRequestKey();
		copyAfterEdition = false;
		adding = true;
	}

	/**
	 * Opens what a link to add a copy asked for: the copy form of the only edition, a new edition
	 * first when there is none, or edition links that open their copy form. The request leaves the
	 * address, so a reload does not repeat it.
	 */
	function startCopyFromLink(t: TitleDetail) {
		const url = new URL(page.url.href);
		url.searchParams.delete('add');
		replaceState(url, page.state);
		const start = addCopyStart(t);
		if (start?.form === 'edition') {
			startEdition();
			copyAfterEdition = true;
		} else if (start?.form === 'copy') {
			openCopy({ mode: 'add', editionID: start.editionID, requestKey: newRequestKey() });
		} else {
			pickEdition = true;
		}
	}

	async function addEdition(t: Title) {
		const parsed = editionPayload(editionForm, t.id, t.kind);
		if (!parsed.ok) {
			editionIssue = parsed.issue;
			return;
		}
		savingEdition = true;
		try {
			const created = await request<Edition>(fetch, 'POST', '/api/editions', {
				...parsed.value,
				request_key: editionKey
			});
			const edition = `/editions/${created.id}`;
			await goto(copyAfterEdition ? addCopyHref(edition) : edition);
		} catch (err) {
			editionIssue = issueFrom(err);
		} finally {
			savingEdition = false;
		}
	}

	async function remove() {
		try {
			await request(fetch, 'DELETE', `/api/titles/${id}`);
			await goto('/');
		} catch (err) {
			deleteError = errorMessage(err);
		}
	}
</script>

<svelte:head>
	<title>{detail ? displayName(detail) : 'Tytuł'} · Ohara</title>
</svelte:head>

{#if hero}
	<TitleHero
		{...hero}
		backHref="/"
		backLabel="Kolekcja"
		coverTitle={id}
		onback={backToCollection}
		actions={titleActions}
	/>
{/if}

{#if failure}
	<div class="content">
		<ErrorState
			title="Nie udało się wczytać tytułu"
			description={failure}
			onretry={() => load(id)}
			{busy}
			retryLabel="Spróbuj ponownie"
			retryingLabel="Wczytywanie…"
		/>
	</div>
{:else if !detail}
	<div class="content"><Skeleton /></div>
{:else}
	{@const t = detail}
	{@const ids = [
		{ label: 'BN', value: t.bn_id ?? undefined },
		{ label: 'Open Library', value: t.openlibrary_id ?? undefined },
		{ label: 'TMDB', value: t.tmdb_id?.toString() }
	].filter((fact) => fact.value)}
	<div class="content">
		<FormError message={refreshError} />
		<FormError message={deleteError} />

		<div class="finished">
			{#if t.finished}
				<p class="finished-line">{finishedLine(t)}</p>
				<Button tone="ghost" onclick={() => openFinish(t)}>Zmień datę</Button>
			{:else}
				<Button
					loading={finishing}
					onclick={() => saveFinished(t, true, todayInPoland(new Date()))}
				>
					{finishAction(t.kind)}
				</Button>
			{/if}
		</div>
		{#if !finishSheet}
			<FormError message={finishIssue?.message ?? null} />
		{/if}

		{#if t.box_sets?.length}
			<ul class="box-sets" aria-label="Zestawy z tym tytułem">
				{#each t.box_sets as box (box.edition_id)}
					<li>
						W zestawie: <a href={`/titles/${box.title_id}`}>{box.name}</a>
						{#if box.owned}<span class="muted"> · na półce</span>{/if}
					</li>
				{/each}
			</ul>
		{/if}

		{#if sole}
			{@const s = sole}
			<Card title={soleCopyHeading[s.copy.status]} level={2}>
				{#snippet actions()}
					<Button tone="ghost" onclick={() => openCopy({ mode: 'edit', copy: s.copy })}>
						Edytuj
					</Button>
				{/snippet}
				<dl class="facts">
					{#each copyFacts(s.copy) as fact (fact.label)}
						<div>
							<dt>{fact.label}</dt>
							<dd>{fact.value}</dd>
						</div>
					{/each}
				</dl>
			</Card>

			{#if value}
				<Card title="Wartość dziś" level={2}>
					<div class="worth">
						{#if value.usedAt == null}
							<p class="muted">Brak cen używanych egzemplarzy.</p>
						{:else}
							<p class="used">≈ {value.used}</p>
						{/if}
						{#if value.delta}
							<p class="delta">{value.delta}</p>
						{/if}
						{#if value.trend}
							<svg
								class="trend"
								width={trendWidth}
								height={trendHeight}
								viewBox={`0 0 ${trendWidth} ${trendHeight}`}
								aria-hidden="true"
							>
								<path d={value.trend.path} />
								{#each value.trend.dots as dot (dot.x)}
									<circle cx={dot.x} cy={dot.y} r="0.9" />
								{/each}
							</svg>
						{/if}
					</div>
					<div class="range" aria-hidden="true">
						{#if value.usedAt != null}
							<span class="mark" style:inset-inline-start={`${value.usedAt}%`}></span>
						{/if}
						{#if value.paidAt != null}
							<span class="paid" style:inset-inline-start={`${value.paidAt}%`}></span>
						{/if}
					</div>
					<dl class="range-labels">
						<div>
							<dt>Skup</dt>
							<dd>{value.floor}</dd>
						</div>
						<div>
							<dt>Używane</dt>
							<dd>{value.used}</dd>
						</div>
						<div>
							<dt>Nowe</dt>
							<dd>{value.ceiling}</dd>
						</div>
					</dl>
				</Card>
			{/if}

			{#if !adding}
				<div class="quiet">
					<Button
						onclick={() =>
							openCopy({ mode: 'add', editionID: s.edition.id, requestKey: newRequestKey() })}
					>
						{#snippet icon()}<Icon name="plus" />{/snippet}
						Kolejny egzemplarz
					</Button>
					<Button onclick={startEdition}>Inne wydanie</Button>
				</div>
			{/if}
		{:else}
			<Section
				title="Wydania"
				count={t.editions.length}
				countedName={(title: string, n: string) => `${title} (${n})`}
			>
				{#snippet actions()}
					{#if !adding}
						<Button tone={t.editions.length === 0 ? 'signal' : 'add'} onclick={startEdition}>
							Dodaj wydanie
						</Button>
					{/if}
				{/snippet}
				{#if t.editions.length === 0 && !adding}
					<p class="muted">Brak wydań. Dodaj wydanie, aby zapisać egzemplarz.</p>
				{:else if pickEdition}
					<p class="muted">Wybierz wydanie nowego egzemplarza.</p>
				{/if}
				<div class="cards">
					{#each t.editions as e (e.id)}
						<Card
							title={editionName(e)}
							description={[e.publisher, e.year].filter(Boolean).join(' · ') || undefined}
							level={3}
							href={pickEdition ? addCopyHref(`/editions/${e.id}`) : `/editions/${e.id}`}
						>
							<div class="edition">
								<CollectionCover kind={t.kind} url={editionArtwork(e, t)} />
								{#if e.copies.length === 0}
									<p class="muted">Brak egzemplarzy.</p>
								{:else}
									<ul class="copies">
										{#each e.copies as c (c.id)}
											<li><a href={`/copies/${c.id}`}>{copyLine(c)}</a></li>
										{/each}
									</ul>
								{/if}
							</div>
						</Card>
					{/each}
				</div>
			</Section>
		{/if}

		{#if adding}
			<Card title="Nowe wydanie" level={2}>
				<EditionFormFields
					bind:form={editionForm}
					kind={t.kind}
					titleID={t.id}
					issue={editionIssue}
					saving={savingEdition}
					submitLabel="Zapisz wydanie"
					onsubmit={() => addEdition(t)}
					oncancel={() => ((adding = false), (copyAfterEdition = false))}
				/>
			</Card>
		{/if}

		{#if t.series_id != null && seriesLine(t)}
			<p class="series">
				<a href={`/series/${t.series_id}`}>{seriesLine(t)}</a>
			</p>
		{/if}

		{#if t.tags.length > 0}
			<ul class="tags" aria-label="Tagi">
				{#each t.tags as tag (tag)}
					<li><a href={tagHref(tag)} aria-label={`Kolekcja z tagiem ${tag}`}>{tag}</a></li>
				{/each}
			</ul>
		{/if}

		<div class="rating">
			<Field label="Ocena" error={ratingError ?? undefined}>
				{#snippet control(c)}
					<Select
						{...c}
						options={ratingOptions}
						bind:value={() => t.rating ?? noRating, (v) => rate(v)}
					/>
				{/snippet}
			</Field>
		</div>

		{#if t.notes}
			<p class="notes">{t.notes}</p>
		{/if}

		{#if sole || ids.length > 0}
			<Section title="Szczegóły">
				<div class="details">
					{#if sole}
						<Link href={`/editions/${sole.edition.id}`}>Szczegóły wydania</Link>
					{/if}
					{#if ids.length > 0}
						<FactList layout="inline" facts={ids} />
					{/if}
				</div>
			</Section>
		{/if}
	</div>

	<Modal
		bind:open={titleSheet}
		title="Edycja tytułu"
		closeLabel="Zamknij"
		busy={savingTitle}
		size="inspector"
	>
		<TitleFormFields
			bind:form={titleForm}
			issue={titleIssue}
			saving={savingTitle}
			kindLocked={t.edition_count > 0}
			{tagSuggestions}
			{seriesSuggestions}
			submitLabel="Zapisz zmiany"
			onsubmit={saveTitle}
			oncancel={() => (titleSheet = false)}
		/>
	</Modal>

	<Modal
		bind:open={finishSheet}
		title={finishedLabel(t.kind)}
		closeLabel="Zamknij"
		busy={finishing}
	>
		<form
			id="finish-form"
			novalidate
			onsubmit={(e) => {
				e.preventDefault();
				submitFinish(t);
			}}
		>
			<FormError
				message={finishIssue && finishIssue.field !== 'finished_on' ? finishIssue.message : null}
			/>
			<Field
				label="Data"
				hint="Zostaw puste, jeśli nie pamiętasz dnia."
				placeholder="RRRR-MM-DD"
				bind:value={finishText}
				error={finishIssue?.field === 'finished_on' ? finishIssue.message : undefined}
			/>
		</form>
		{#snippet footer()}
			<Button tone="stop-quiet" disabled={finishing} onclick={() => saveFinished(t, false, null)}>
				Usuń oznaczenie
			</Button>
			<Button tone="signal" type="submit" form="finish-form" loading={finishing}>Zapisz</Button>
		{/snippet}
	</Modal>

	<Modal
		bind:open={copySheet}
		title={copyTarget?.mode === 'add' ? 'Nowy egzemplarz' : 'Edycja egzemplarza'}
		description={copyEdition ? editionName(copyEdition) : undefined}
		closeLabel="Zamknij"
		busy={savingCopy}
		size="inspector"
	>
		{#if copyTarget}
			{@const target = copyTarget}
			<CopyFormFields
				bind:form={copyForm}
				issue={copyIssue}
				saving={savingCopy}
				submitLabel={target.mode === 'add' ? 'Dodaj egzemplarz' : 'Zapisz zmiany'}
				market={copyEdition ? markets.for(copyEdition.title_id, copyEdition.format) : null}
				kind={t.kind}
				onsubmit={() => saveCopy(target)}
				oncancel={() => (copySheet = false)}
			/>
		{/if}
	</Modal>

	<ConfirmDialog
		bind:open={confirmDelete}
		title="Usunąć tytuł?"
		consequence={t.edition_count > 0
			? 'Najpierw usuń wszystkie wydania tego tytułu.'
			: 'Tytuł zniknie z kolekcji. Tego nie można cofnąć.'}
		confirmLabel="Usuń tytuł"
		cancelLabel="Anuluj"
		closeLabel="Zamknij"
		tone="stop"
		onconfirm={remove}
	/>
{/if}

<!-- The menu stands in the hero from its first frame, disabled until the title loads, so the
     hero keeps its height and the cover stays where it landed. -->
{#snippet titleActions()}
	<ActionMenu
		label="Więcej działań"
		icon="more"
		toolbar
		align="end"
		disabled={!detail}
		items={[
			{
				label: 'Edytuj tytuł',
				icon: 'editor',
				onselect: () => {
					if (detail) editTitle(detail);
				}
			},
			{ separator: true },
			{
				label: 'Usuń tytuł',
				icon: 'trash',
				tone: 'stop',
				onselect: () => ((deleteError = null), (confirmDelete = true))
			}
		]}
	/>
{/snippet}

<style>
	.content {
		display: grid;
		gap: 1rem;
		padding: var(--sui-pane-gutter, 1.5rem);
	}
	.finished {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem 0.75rem;
	}
	.finished-line {
		margin: 0;
		font-weight: 600;
	}
	.rating {
		justify-self: start;
		min-inline-size: 10rem;
	}
	.cards {
		display: grid;
		gap: 1rem;
		grid-template-columns: repeat(auto-fill, minmax(min(100%, 18rem), 1fr));
	}
	.facts {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.625rem 0.875rem;
		margin: 0;
	}
	.facts dt {
		font-size: 0.75rem;
		font-weight: 600;
		color: var(--sui-text-muted);
	}
	.facts dd {
		margin: 0;
		font-size: 0.9375rem;
		font-weight: 600;
		font-variant-numeric: tabular-nums;
		overflow-wrap: anywhere;
	}
	.worth {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.25rem 0.75rem;
	}
	.used {
		margin: 0;
		font-family: var(--ohara-serif);
		font-size: 2.125rem;
		font-weight: 600;
		letter-spacing: -0.02em;
		font-variant-numeric: tabular-nums;
	}
	.trend {
		flex: none;
		margin-inline-start: auto;
		fill: none;
	}
	.trend path {
		stroke: var(--sui-brand-action);
		stroke-width: 2.4;
		stroke-linecap: butt;
		stroke-linejoin: round;
	}
	.trend circle {
		fill: var(--sui-brand-action);
	}
	.delta {
		display: inline-flex;
		align-items: center;
		min-block-size: 1.625rem;
		margin: 0;
		padding: 0 0.625rem;
		border-radius: 999px;
		font-size: 0.8125rem;
		font-weight: 700;
		background: var(--sui-surface-inset);
		color: var(--sui-text-primary);
		font-variant-numeric: tabular-nums;
	}
	.range {
		position: relative;
		block-size: 0.5rem;
		margin: 0.875rem 0 0.5rem;
		border-radius: 0.25rem;
		background: linear-gradient(
			90deg,
			var(--sui-surface-inset),
			color-mix(in srgb, var(--sui-brand-action) 30%, var(--sui-surface-inset))
		);
	}
	.mark {
		position: absolute;
		inset-block-start: 50%;
		inline-size: 1.125rem;
		block-size: 1.125rem;
		box-sizing: border-box;
		border: 3px solid var(--sui-surface-base);
		border-radius: 50%;
		background: var(--ohara-selected);
		box-shadow: 0 1px 4px rgb(0 0 0 / 25%);
		translate: -50% -50%;
	}
	.paid {
		position: absolute;
		inset-block-start: -0.3125rem;
		inline-size: 2px;
		block-size: 1.125rem;
		border-radius: 1px;
		background: var(--sui-brand-action);
		translate: -50% 0;
	}
	.range-labels {
		display: grid;
		grid-template-columns: repeat(3, minmax(0, 1fr));
		gap: 0.5rem;
		margin: 0;
		font-size: 0.75rem;
		color: var(--sui-text-muted);
		font-variant-numeric: tabular-nums;
	}
	.range-labels dd {
		margin: 0;
		font-weight: 600;
		color: var(--sui-text-primary);
	}
	.range-labels div:nth-child(2) {
		text-align: center;
	}
	.range-labels div:last-child {
		text-align: end;
	}
	.quiet {
		display: flex;
		flex-wrap: wrap;
		gap: 0.625rem;
	}
	.quiet > :global(*) {
		flex: 1 1 auto;
		white-space: nowrap;
	}
	.details {
		display: grid;
		justify-items: start;
		gap: 0.75rem;
	}
	.edition {
		display: flex;
		align-items: flex-start;
		gap: 0.75rem;
		--collection-cover-width: 3.5rem;
	}
	.copies {
		display: grid;
		flex: 1;
		gap: 0.25rem;
		min-inline-size: 0;
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.copies a {
		display: block;
		overflow-wrap: anywhere;
		padding: 0.375rem 0.5rem;
		border-radius: var(--sui-radius-control);
		color: var(--sui-link);
		font-variant-numeric: tabular-nums;
		text-decoration: none;
	}
	.copies a:hover {
		background: var(--sui-row-hover);
	}
	.muted {
		margin: 0;
		color: var(--sui-text-muted);
	}
	.box-sets {
		display: grid;
		gap: 0.25rem;
		margin: 0;
		padding: 0;
		list-style: none;
		min-inline-size: 0;
		overflow-wrap: anywhere;
	}
	.notes {
		margin: 0;
		white-space: pre-wrap;
	}
	.series {
		margin: 0;
	}
	.series a {
		color: var(--sui-link);
		overflow-wrap: anywhere;
	}
	.series a:focus-visible {
		outline: 2px solid var(--sui-focus);
		outline-offset: 2px;
	}
	.tags {
		display: flex;
		flex-wrap: wrap;
		gap: 0.375rem;
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.tags a {
		display: inline-flex;
		align-items: center;
		min-block-size: 2.75rem;
		padding: 0 0.75rem;
		border: 1px solid var(--sui-border-control);
		border-radius: 999px;
		color: var(--sui-link);
		overflow-wrap: anywhere;
		text-decoration: none;
	}
	.tags a:hover {
		background: var(--sui-row-hover);
	}
	.tags a:focus-visible {
		outline: 2px solid var(--sui-focus);
		outline-offset: 2px;
	}
</style>
