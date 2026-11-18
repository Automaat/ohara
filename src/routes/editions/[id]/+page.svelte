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
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { ApiError, errorMessage, request } from '$lib/api';
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
		statusLabels,
		triStateLabels,
		type Copy,
		type Edition,
		type EditionDetail,
		type FormIssue
	} from '$lib/collection';
	import ArtworkHeader from '$lib/components/ArtworkHeader.svelte';
	import CopyFormFields from '$lib/components/CopyFormFields.svelte';
	import EditionFormFields from '$lib/components/EditionFormFields.svelte';
	import EditionCoverForm from '$lib/components/EditionCoverForm.svelte';
	import { purchaseDate } from '$lib/polish';

	const id = $derived(Number(page.params.id));

	let detail = $state<EditionDetail | null>(null);
	let failure = $state<string | null>(null);
	let busy = $state(false);

	let editing = $state(false);
	let editionForm = $state(editionToForm(null));
	let editionIssue = $state<FormIssue | null>(null);
	let savingEdition = $state(false);

	let adding = $state(false);
	let copyForm = $state(copyToForm(null));
	let copyIssue = $state<FormIssue | null>(null);
	let savingCopy = $state(false);

	let confirmDelete = $state(false);
	let deleteError = $state<string | null>(null);

	async function load(editionID: number) {
		busy = true;
		try {
			detail = await request<EditionDetail>(fetch, 'GET', `/api/editions/${editionID}`);
			failure = null;
		} catch (err) {
			failure = errorMessage(err);
		} finally {
			busy = false;
		}
	}

	$effect(() => {
		void load(id);
	});

	function issueFrom(err: unknown): FormIssue {
		return { field: err instanceof ApiError ? (err.field ?? '') : '', message: errorMessage(err) };
	}

	async function saveEdition(d: EditionDetail) {
		const parsed = editionPayload(editionForm, d.title_id, d.title.kind);
		if (!parsed.ok) {
			editionIssue = parsed.issue;
			return;
		}
		savingEdition = true;
		try {
			await request<Edition>(fetch, 'PUT', `/api/editions/${id}`, parsed.value);
			editing = false;
			await load(id);
		} catch (err) {
			editionIssue = issueFrom(err);
		} finally {
			savingEdition = false;
		}
	}

	async function addCopy() {
		const parsed = copyPayload(copyForm, id);
		if (!parsed.ok) {
			copyIssue = parsed.issue;
			return;
		}
		savingCopy = true;
		try {
			await request<Copy>(fetch, 'POST', '/api/copies', parsed.value);
			adding = false;
			await load(id);
		} catch (err) {
			copyIssue = issueFrom(err);
		} finally {
			savingCopy = false;
		}
	}

	async function remove(d: EditionDetail) {
		try {
			await request(fetch, 'DELETE', `/api/editions/${id}`);
			await goto(`/titles/${d.title_id}`);
		} catch (err) {
			deleteError = errorMessage(err);
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
	>
		{#snippet facts()}
			<FactList
				layout="inline"
				facts={[
					{ label: 'Wydawca', value: d.publisher },
					{ label: 'Rok wydania', value: d.year?.toString(), mono: true }
				].filter((fact) => fact.value)}
			/>
		{/snippet}
	</ArtworkHeader>

	<div class="content">
		{#key d.id}
			<EditionCoverForm
				editionID={d.id}
				hasOverride={Boolean(d.cover_override_url)}
				onchange={() => (d.id === id ? load(d.id) : Promise.resolve())}
			/>
		{/key}

		{#if d.notes}
			<p class="notes">{d.notes}</p>
		{/if}

		<Section
			title="Egzemplarze"
			count={d.copies.length}
			countedName={(title: string, n: string) => `${title} (${n})`}
		>
			{#snippet actions()}
				{#if !adding}
					<Button
						tone="add"
						onclick={() => ((copyForm = copyToForm(null)), (copyIssue = null), (adding = true))}
						>Dodaj egzemplarz</Button
					>
				{/if}
			{/snippet}
			{#if adding}
				<Card title="Nowy egzemplarz" level={3}>
					<CopyFormFields
						bind:form={copyForm}
						issue={copyIssue}
						saving={savingCopy}
						submitLabel="Zapisz egzemplarz"
						onsubmit={addCopy}
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
									{ label: 'Stan', value: copy.condition && conditionLabels[copy.condition] }
								].filter((fact) => fact.value)}
							/>
						</Card>
					{/each}
				</div>
			{:else if !adding}
				<p class="muted">Brak egzemplarzy.</p>
			{/if}
		</Section>

		{#if d.barcode || d.discogs_release_id || d.title.kind === 'movie'}
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
				{#if d.title.kind === 'movie'}
					<FactList
						layout="inline"
						facts={[
							{ label: 'Napisy PL', value: triStateLabels[d.pl_subtitles] },
							{ label: 'Lektor/dubbing PL', value: triStateLabels[d.pl_voiceover] }
						]}
					/>
				{/if}
				{#if d.discogs_release_id}
					<Link href={`https://www.discogs.com/release/${d.discogs_release_id}`}
						>Discogs · {d.discogs_release_id}</Link
					>
				{/if}
			</Section>
		{/if}

		<Section title="Zarządzanie wydaniem">
			<div class="actions">
				<Button
					tone="ghost"
					onclick={() => (
						(editionForm = editionToForm(d)),
						(editionIssue = null),
						(editing = true)
					)}>Edytuj wydanie</Button
				>
				<Button tone="stop-quiet" onclick={() => ((deleteError = null), (confirmDelete = true))}
					>Usuń wydanie</Button
				>
			</div>
			<FormError message={deleteError} />
			{#if editing}
				<Card title="Edycja wydania" level={3}>
					<EditionFormFields
						bind:form={editionForm}
						kind={d.title.kind}
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
		gap: 1.5rem;
		padding: var(--sui-pane-gutter, 1.5rem);
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
</style>
