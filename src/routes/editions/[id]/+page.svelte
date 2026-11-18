<script lang="ts">
	import {
		BackLink,
		Button,
		Card,
		ConfirmDialog,
		DataTable,
		ErrorState,
		FactList,
		FormError,
		Link,
		PageHeader,
		Pill,
		Section,
		Skeleton,
		type DataTableColumn
	} from '@smykla-skalski/sui';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { ApiError, errorMessage, request } from '$lib/api';
	import {
		acquiredLabels,
		bindingLabels,
		conditionLabels,
		copyPayload,
		copyToForm,
		displayName,
		editionPayload,
		editionSummary,
		editionToForm,
		formatLabels,
		formatPLN,
		packagingLabels,
		statusLabels,
		triStateLabels,
		type Copy,
		type Edition,
		type EditionDetail,
		type FormIssue
	} from '$lib/collection';
	import CopyFormFields from '$lib/components/CopyFormFields.svelte';
	import EditionFormFields from '$lib/components/EditionFormFields.svelte';

	type Row = Copy & Record<string, unknown>;

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

	const columns: DataTableColumn<Row>[] = [
		{ key: 'status', label: 'Status' },
		{ key: 'price_cents', label: 'Cena', numeric: true },
		{ key: 'purchased_on', label: 'Kupiono', nowrap: true },
		{ key: 'shop', label: 'Sklep' },
		{ key: 'acquired_as', label: 'Nowy/używany' },
		{ key: 'condition', label: 'Stan' }
	];
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
	{@const cover = d.cover_url || d.title.cover_url}
	<PageHeader title={editionSummary(d)} description={displayName(d.title)}>
		{#snippet kicker()}
			<BackLink href={`/titles/${d.title_id}`} label={displayName(d.title)} />
		{/snippet}
		{#snippet facts()}
			<FactList
				layout="inline"
				missingLabel="—"
				facts={[
					{ label: 'Kod kreskowy', value: d.barcode, mono: true },
					...(d.title.kind === 'movie'
						? [
								{ label: 'Nośnik', value: d.format ? formatLabels[d.format] : null },
								{ label: 'Napisy PL', value: triStateLabels[d.pl_subtitles] },
								{ label: 'Lektor/dubbing PL', value: triStateLabels[d.pl_voiceover] },
								{
									label: 'Opakowanie',
									value: d.packaging.map((p) => packagingLabels[p]).join(', ')
								}
							]
						: d.title.kind === 'music'
							? [
									{ label: 'Nośnik', value: d.format ? formatLabels[d.format] : null },
									{
										label: 'Discogs',
										value: d.discogs_release_id?.toString(),
										mono: true,
										href: d.discogs_release_id
											? `https://www.discogs.com/release/${d.discogs_release_id}`
											: undefined
									}
								]
							: [{ label: 'Oprawa', value: d.binding ? bindingLabels[d.binding] : null }]),
					{ label: 'Wydawca', value: d.publisher },
					{ label: 'Rok', value: d.year?.toString(), mono: true }
				]}
			/>
		{/snippet}
		{#snippet actions()}
			<Button
				onclick={() => ((editionForm = editionToForm(d)), (editionIssue = null), (editing = true))}
				>Edytuj</Button
			>
			<Button tone="stop-quiet" onclick={() => ((deleteError = null), (confirmDelete = true))}
				>Usuń</Button
			>
		{/snippet}
	</PageHeader>

	<div class="content">
		<FormError message={deleteError} />
		{#if cover}
			<figure class="artwork">
				<img
					src={cover}
					alt={d.cover_url ? 'Okładka wydania' : 'Okładka tytułu'}
					referrerpolicy="no-referrer"
				/>
				{#if !d.cover_url}
					<figcaption>Okładka poglądowa tytułu</figcaption>
				{/if}
			</figure>
		{/if}
		{#if editing}
			<Section title="Edycja wydania">
				<EditionFormFields
					bind:form={editionForm}
					kind={d.title.kind}
					issue={editionIssue}
					saving={savingEdition}
					submitLabel="Zapisz zmiany"
					onsubmit={() => saveEdition(d)}
					oncancel={() => (editing = false)}
				/>
			</Section>
		{/if}
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
			<DataTable
				aria-label="Egzemplarze wydania"
				{columns}
				rows={d.copies as Row[]}
				rowKey={(r) => r.id}
				emptyLabel="Brak egzemplarzy"
				scrollHint="Przewiń tabelę w bok"
			>
				{#snippet cell(row, column)}
					{#if column.key === 'status'}
						<Link href={`/copies/${row.id}`}>
							<Pill tone={row.status === 'owned' ? 'success' : 'neutral'}
								>{statusLabels[row.status]}</Pill
							>
						</Link>
					{:else if column.key === 'price_cents'}
						{formatPLN(row.price_cents)}
					{:else if column.key === 'purchased_on'}
						{row.purchased_on ?? ''}
					{:else if column.key === 'shop'}
						{row.shop}
					{:else if column.key === 'acquired_as'}
						{row.acquired_as ? acquiredLabels[row.acquired_as] : ''}
					{:else if column.key === 'condition'}
						{row.condition ? conditionLabels[row.condition] : ''}
					{/if}
				{/snippet}
			</DataTable>
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
	.artwork {
		margin: 0;
	}
	.artwork img {
		display: block;
		max-width: min(100%, 12rem);
		max-height: 16rem;
	}
	.artwork figcaption {
		margin-top: 0.5rem;
	}
</style>
