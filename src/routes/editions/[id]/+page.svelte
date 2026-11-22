<script lang="ts">
	import {
		Button,
		Card,
		ConfirmDialog,
		ErrorState,
		FactList,
		FormError,
		Link,
		CopyButton,
		Section,
		Skeleton
	} from '@smykla-skalski/sui';
	import { onDestroy } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import { goto, replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import { errorMessage, issueFrom, request } from '#lib/api.js';
	import {
		acquiredLabels,
		conditionLabels,
		copyPayload,
		copyToForm,
		displayName,
		editionPayload,
		editionSummary,
		editionToForm,
		formatPLN,
		gradeLine,
		kindLabels,
		screenKind,
		statusLabels,
		triStateLabels,
		type Copy,
		type Edition,
		type EditionDetail,
		type FormIssue
	} from '#lib/collection.js';
	import ArtworkHeader from '#lib/components/ArtworkHeader.svelte';
	import CopyFormFields from '#lib/components/CopyFormFields.svelte';
	import EditionFormFields from '#lib/components/EditionFormFields.svelte';
	import EditionCoverSheet from '#lib/components/EditionCoverSheet.svelte';
	import { coverUndo } from '#lib/edition-cover.js';
	import { purchaseDate } from '#lib/polish.js';
	import { LatestLoad, newRequestKey } from '#lib/scan.js';
	import { copyRequest } from '#lib/title-detail.js';
	import { TitleMarkets } from '#lib/title-market.svelte.js';

	const id = $derived(Number(page.params.id));

	let detail = $state<EditionDetail | null>(null);
	let failure = $state<string | null>(null);
	let busy = $state(false);

	let editing = $state(false);
	let editionForm = $state(editionToForm(null));
	let editionIssue = $state<FormIssue | null>(null);
	/**
	 * Editions whose save is still on its way, kept across a jump to another edition and back, so
	 * a second save of the same edition cannot start until the first one has landed.
	 */
	const pendingSaves = new SvelteSet<number>();
	const savingEdition = $derived(detail !== null && pendingSaves.has(detail.id));
	/** Counts opened edit forms, so a save's answer closes only the form it was sent from. */
	let editionEdits = 0;

	let adding = $state(false);
	let copyForm = $state(copyToForm(null));
	let copyIssue = $state<FormIssue | null>(null);
	let savingCopy = $state(false);
	/**
	 * The open copy form's key, sent with every attempt to save it, so a retry after a lost answer
	 * returns the copy instead of adding a second one.
	 */
	let copyKey = '';

	let coverSheet = $state(false);

	let confirmDelete = $state(false);
	let deleteError = $state<string | null>(null);

	/**
	 * The edition the page was last asked for, and a link's request to add a copy to it. Another
	 * edition's answers and forms are dropped.
	 */
	let requested: number | undefined;
	const addRequest = copyRequest();
	onDestroy(addRequest.close);
	/** A slower answer for an earlier load never replaces a newer one. */
	const loads = new LatestLoad();

	async function load(editionID: number) {
		if (editionID !== requested) {
			requested = editionID;
			detail = null;
			failure = null;
			editing = false;
			editionForm = editionToForm(null);
			editionIssue = null;
			editionEdits++;
			adding = false;
			copyForm = copyToForm(null);
			copyIssue = null;
			savingCopy = false;
			copyKey = '';
			coverSheet = false;
			confirmDelete = false;
			deleteError = null;
			addRequest.open(editionID, page.url.searchParams);
		}
		const isLatest = loads.start();
		busy = true;
		try {
			const next = await request<EditionDetail>(fetch, 'GET', `/api/editions/${editionID}`);
			if (!isLatest()) return;
			detail = next;
			failure = null;
			if (addRequest.take(editionID)) startCopyFromLink();
		} catch (err) {
			if (!isLatest()) return;
			failure = errorMessage(err);
		} finally {
			if (isLatest()) busy = false;
		}
	}

	$effect(() => {
		void load(id);
	});

	/** The wish's market price, asked once the new copy's form shows a wish's limit. */
	const markets = new TitleMarkets(fetch);
	$effect(() => {
		if (detail && adding && copyForm.status === 'wishlist') void markets.load(detail.title_id);
	});

	function startCopy() {
		copyForm = copyToForm(null);
		copyIssue = null;
		copyKey = newRequestKey();
		savingCopy = false;
		adding = true;
	}

	/** Opens the copy form a link asked for; the request leaves the address. */
	function startCopyFromLink() {
		const url = new URL(page.url.href);
		url.searchParams.delete('add');
		replaceState(url, page.state);
		startCopy();
	}

	/**
	 * Opened only once a pending save and every load have landed: a newer load drops the save's own
	 * reload, so only the newest answer holds what the save stored.
	 */
	function startEdit(d: EditionDetail) {
		if (pendingSaves.has(d.id) || busy) return;
		editionForm = editionToForm(d);
		editionIssue = null;
		editionEdits++;
		editing = true;
	}

	async function saveEdition(d: EditionDetail) {
		const parsed = editionPayload(editionForm, d.title_id, d.title.kind);
		if (!parsed.ok) {
			editionIssue = parsed.issue;
			return;
		}
		if (pendingSaves.has(d.id)) return;
		const edit = editionEdits;
		pendingSaves.add(d.id);
		try {
			await request<Edition>(fetch, 'PUT', `/api/editions/${d.id}`, parsed.value);
			if (d.id !== requested) return;
			if (edit === editionEdits) editing = false;
			await load(d.id);
		} catch (err) {
			if (edit === editionEdits) editionIssue = issueFrom(err);
		} finally {
			pendingSaves.delete(d.id);
		}
	}

	async function addCopy(d: EditionDetail) {
		const parsed = copyPayload(copyForm, d.id);
		if (!parsed.ok) {
			copyIssue = parsed.issue;
			return;
		}
		// The key names the open form: another form has its own key, so this answer leaves it alone.
		const key = copyKey;
		savingCopy = true;
		try {
			await request<Copy>(fetch, 'POST', '/api/copies', { ...parsed.value, request_key: key });
			if (d.id !== requested) return;
			if (copyKey === key) adding = false;
			await load(d.id);
		} catch (err) {
			if (copyKey === key) copyIssue = issueFrom(err);
		} finally {
			if (copyKey === key) savingCopy = false;
		}
	}

	async function remove(d: EditionDetail) {
		try {
			await request(fetch, 'DELETE', `/api/editions/${d.id}`);
			if (d.id === requested) await goto(`/titles/${d.title_id}`);
		} catch (err) {
			if (d.id === requested) deleteError = errorMessage(err);
		}
	}
</script>

<svelte:head>
	<title
		>{detail ? `${displayName(detail.title)} · ${editionSummary(detail)}` : 'Wydanie'} · Ohara</title
	>
</svelte:head>

{#if failure}
	<div class="content">
		<ErrorState
			title="Nie udało się wczytać wydania"
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
	{@const d = detail}
	<ArtworkHeader
		title={displayName(d.title)}
		description={editionSummary({ ...d, barcode: null })}
		backHref={`/titles/${d.title_id}`}
		backLabel="Tytuł"
		cover={d.cover_override_url || d.cover_url || d.title.cover_url}
		coverLabel={d.cover_override_url
			? 'Własne zdjęcie okładki'
			: d.cover_url
				? 'Okładka wydania'
				: 'Okładka poglądowa tytułu'}
		coverAction={{ label: 'Zmień okładkę', onclick: () => (coverSheet = true) }}
	>
		{#snippet facts()}
			<FactList
				layout="inline"
				facts={[
					{ label: 'Wydawca', value: d.publisher },
					{ label: 'Rok wydania', value: d.year?.toString() }
				].filter((fact) => fact.value)}
			/>
		{/snippet}
	</ArtworkHeader>

	<div class="content">
		<Section
			class="copies"
			title="Egzemplarze"
			count={d.copies.length}
			countedName={(title: string, n: string) => `${title} (${n})`}
		>
			{#snippet actions()}
				{#if !adding}
					<Button tone="add" onclick={startCopy}>Dodaj egzemplarz</Button>
				{/if}
			{/snippet}
			{#if adding}
				<Card title="Nowy egzemplarz" level={3}>
					<CopyFormFields
						bind:form={copyForm}
						issue={copyIssue}
						saving={savingCopy}
						submitLabel="Zapisz egzemplarz"
						market={markets.for(d.title_id, d.format)}
						kind={d.title.kind}
						onsubmit={() => addCopy(d)}
						oncancel={() => (adding = false)}
					/>
				</Card>
			{/if}
			{#if d.copies.length}
				<div class="cards">
					{#each d.copies as copy (copy.id)}
						<Card title={statusLabels[copy.status]} level={3} href={`/copies/${copy.id}`}>
							<FactList
								layout="inline"
								facts={[
									{
										label: 'Cena',
										value: copy.price_cents == null ? null : formatPLN(copy.price_cents)
									},
									{
										label: 'Cena maksymalna',
										value: copy.max_price_cents == null ? null : formatPLN(copy.max_price_cents)
									},
									{ label: 'Kupiono', value: purchaseDate(copy.purchased_on) },
									{ label: 'Sklep', value: copy.shop },
									{
										label: 'Nowy/używany',
										value: copy.acquired_as && acquiredLabels[copy.acquired_as]
									},
									{ label: 'Stan', value: copy.condition && conditionLabels[copy.condition] },
									{ label: 'Gradacja', value: gradeLine(copy) },
									{ label: 'Miejsce', value: copy.location }
								].filter((fact) => fact.value)}
							/>
						</Card>
					{/each}
				</div>
			{:else if !adding}
				<p class="muted">Brak egzemplarzy.</p>
			{/if}
		</Section>

		{#if d.contents?.length}
			<Section
				title="Zawartość zestawu"
				count={d.contents.length}
				countedName={(title: string, n: string) => `${title} (${n})`}
			>
				<ul class="contents" aria-label="Tytuły w zestawie">
					{#each d.contents as content (content.id)}
						<li>
							<a href={`/titles/${content.id}`}>{displayName(content)}</a>
							<span class="muted">
								{[kindLabels[content.kind], content.year].filter(Boolean).join(' · ')}
							</span>
						</li>
					{/each}
				</ul>
			</Section>
		{/if}

		{#if d.notes}
			<p class="notes">{d.notes}</p>
		{/if}

		{#if d.barcode || d.discogs_release_id || screenKind(d.title.kind)}
			<Section title="Szczegóły wydania">
				{#if d.barcode}
					<div class="barcode">
						<FactList
							layout="inline"
							facts={[{ label: 'Kod kreskowy', value: d.barcode, mono: true }]}
						/>
						{#key d.barcode}
							<CopyButton
								text={d.barcode}
								label="Kopiuj kod kreskowy"
								copiedLabel="Skopiowano"
								failedLabel="Nie skopiowano"
							/>
						{/key}
					</div>
				{/if}
				{#if screenKind(d.title.kind)}
					<FactList
						layout="inline"
						facts={[
							{ label: 'Napisy PL', value: triStateLabels[d.pl_subtitles] },
							{ label: 'Lektor/dubbing PL', value: triStateLabels[d.pl_voiceover] }
						]}
					/>
				{/if}
				{#if d.discogs_release_id}
					<Link href={`https://www.discogs.com/release/${d.discogs_release_id}`}>
						Discogs · {d.discogs_release_id}
					</Link>
				{/if}
			</Section>
		{/if}

		<Section title="Zarządzanie wydaniem">
			<div class="actions">
				<Button tone="ghost" disabled={savingEdition || busy} onclick={() => startEdit(d)}>
					Edytuj wydanie
				</Button>
				<Button tone="stop-quiet" onclick={() => ((deleteError = null), (confirmDelete = true))}>
					Usuń wydanie
				</Button>
			</div>
			<FormError message={deleteError} />
			{#if editing}
				<Card title="Edycja wydania" level={3}>
					<EditionFormFields
						bind:form={editionForm}
						kind={d.title.kind}
						titleID={d.title_id}
						issue={editionIssue}
						saving={savingEdition}
						submitLabel="Zapisz zmiany"
						onsubmit={() => saveEdition(d)}
						oncancel={() => (editing = false)}
					/>
				</Card>
			{/if}
		</Section>
	</div>

	{#key d.id}
		<EditionCoverSheet
			bind:open={coverSheet}
			editionID={d.id}
			undo={coverUndo(d)}
			onchange={() => (d.id === id ? load(d.id) : Promise.resolve())}
		/>
	{/key}

	<ConfirmDialog
		bind:open={confirmDelete}
		title="Usunąć wydanie?"
		consequence={d.copies.length > 0
			? 'Najpierw usuń wszystkie egzemplarze tego wydania.'
			: 'Wydanie zniknie z kolekcji. Tego nie można cofnąć.'}
		confirmLabel="Usuń wydanie"
		cancelLabel="Anuluj"
		closeLabel="Zamknij"
		tone="stop"
		onconfirm={() => remove(d)}
	/>
{/if}

<style>
	.content {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: 1.5rem;
		padding: var(--sui-pane-gutter, 1.5rem);
	}
	@container (max-width: 19rem) {
		.content :global(.copies > .sui-section-head) {
			flex-wrap: wrap;
			row-gap: 0.75rem;
		}
		.content :global(.copies > .sui-section-head > .sui-section-seat) {
			block-size: auto;
			flex-basis: 100%;
			margin-block-start: 0;
		}
		.content :global(.copies > .sui-section-head .sui-section-actions) {
			justify-content: flex-start;
		}
	}
	.notes {
		margin: 0;
		white-space: pre-wrap;
	}
	.cards {
		display: grid;
		gap: 1rem;
		grid-template-columns: repeat(auto-fill, minmax(min(100%, 18rem), 1fr));
	}
	.actions,
	.barcode {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.5rem;
	}
	.muted {
		margin: 0;
		color: var(--sui-text-muted);
	}
	.contents {
		display: grid;
		gap: 0.5rem;
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.contents li {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 0.5rem;
		min-inline-size: 0;
		overflow-wrap: anywhere;
	}
	.contents a {
		min-inline-size: 0;
	}
</style>
