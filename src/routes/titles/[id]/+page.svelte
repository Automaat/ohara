<script lang="ts">
	import {
		BackLink,
		Button,
		Card,
		ConfirmDialog,
		ErrorState,
		FactList,
		FormError,
		PageHeader,
		Section,
		Skeleton
	} from '@smykla-skalski/sui';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { ApiError, errorMessage, request } from '$lib/api';
	import {
		displayName,
		editionPayload,
		editionSummary,
		editionToForm,
		formatPLN,
		kindLabels,
		statusLabels,
		titlePayload,
		titleToForm,
		type Edition,
		type FormIssue,
		type Title,
		type TitleDetail
	} from '$lib/collection';
	import EditionFormFields from '$lib/components/EditionFormFields.svelte';
	import TitleFormFields from '$lib/components/TitleFormFields.svelte';

	const id = $derived(Number(page.params.id));

	let detail = $state<TitleDetail | null>(null);
	let failure = $state<string | null>(null);
	let busy = $state(false);

	let editing = $state(false);
	let titleForm = $state(titleToForm(null));
	let titleIssue = $state<FormIssue | null>(null);
	let savingTitle = $state(false);

	let adding = $state(false);
	let editionForm = $state(editionToForm(null));
	let editionIssue = $state<FormIssue | null>(null);
	let savingEdition = $state(false);

	let confirmDelete = $state(false);
	let deleteError = $state<string | null>(null);

	async function load(titleID: number) {
		busy = true;
		try {
			detail = await request<TitleDetail>(fetch, 'GET', `/api/titles/${titleID}`);
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

	function startEdit(t: Title) {
		titleForm = titleToForm(t);
		titleIssue = null;
		editing = true;
	}

	async function saveTitle() {
		const parsed = titlePayload(titleForm);
		if (!parsed.ok) {
			titleIssue = parsed.issue;
			return;
		}
		savingTitle = true;
		try {
			await request<Title>(fetch, 'PUT', `/api/titles/${id}`, parsed.value);
			editing = false;
			await load(id);
		} catch (err) {
			titleIssue = issueFrom(err);
		} finally {
			savingTitle = false;
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
			const created = await request<Edition>(fetch, 'POST', '/api/editions', parsed.value);
			await goto(`/editions/${created.id}`);
		} catch (err) {
			editionIssue = issueFrom(err);
		} finally {
			savingEdition = false;
		}
	}

	async function remove() {
		try {
			await request(fetch, 'DELETE', `/api/titles/${id}`);
			await goto('/titles');
		} catch (err) {
			deleteError = errorMessage(err);
		}
	}

	function authorsLine(t: Title): string | null {
		return t.authors.length ? t.authors.join(', ') : null;
	}
</script>

<svelte:head>
	<title>{detail ? displayName(detail) : 'Tytuł'} · Ohara</title>
</svelte:head>

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
	<PageHeader title={displayName(t)} description={kindLabels[t.kind]}>
		{#snippet kicker()}
			<BackLink href="/titles" label="Tytuły" />
		{/snippet}
		{#snippet facts()}
			<FactList
				layout="inline"
				missingLabel="—"
				facts={[
					{ label: 'Rok', value: t.year?.toString(), mono: true },
					...(t.kind === 'book'
						? [
								{ label: 'Autorzy', value: authorsLine(t) },
								{ label: 'BN', value: t.bn_id, mono: true },
								{ label: 'Open Library', value: t.openlibrary_id, mono: true }
							]
						: t.kind === 'music'
							? [{ label: 'Wykonawcy', value: authorsLine(t) }]
							: [{ label: 'TMDB', value: t.tmdb_id?.toString(), mono: true }]),
					{ label: 'Egzemplarze', value: String(t.copy_count), mono: true }
				]}
			/>
		{/snippet}
		{#snippet actions()}
			<Button onclick={() => startEdit(t)}>Edytuj</Button>
			<Button tone="stop-quiet" onclick={() => ((deleteError = null), (confirmDelete = true))}
				>Usuń</Button
			>
		{/snippet}
	</PageHeader>

	<div class="content">
		<FormError message={deleteError} />
		{#if editing}
			<Section title="Edycja tytułu">
				<TitleFormFields
					bind:form={titleForm}
					issue={titleIssue}
					saving={savingTitle}
					kindLocked={t.edition_count > 0}
					submitLabel="Zapisz zmiany"
					onsubmit={saveTitle}
					oncancel={() => (editing = false)}
				/>
			</Section>
		{/if}
		{#if t.notes}
			<p class="notes">{t.notes}</p>
		{/if}

		<Section
			title="Wydania"
			count={t.editions.length}
			countedName={(title: string, n: string) => `${title} (${n})`}
		>
			{#snippet actions()}
				{#if !adding}
					<Button
						tone="add"
						onclick={() => (
							(editionForm = editionToForm(null)),
							(editionIssue = null),
							(adding = true)
						)}>Dodaj wydanie</Button
					>
				{/if}
			{/snippet}
			{#if adding}
				<Card title="Nowe wydanie" level={3}>
					<EditionFormFields
						bind:form={editionForm}
						kind={t.kind}
						issue={editionIssue}
						saving={savingEdition}
						submitLabel="Zapisz wydanie"
						onsubmit={() => addEdition(t)}
						oncancel={() => (adding = false)}
					/>
				</Card>
			{/if}
			{#if t.editions.length === 0 && !adding}
				<p class="muted">Brak wydań. Dodaj wydanie, aby zapisać egzemplarz.</p>
			{/if}
			<div class="cards">
				{#each t.editions as e (e.id)}
					<Card title={editionSummary(e)} level={3} href={`/editions/${e.id}`}>
						{#if e.copies.length === 0}
							<p class="muted">Brak egzemplarzy.</p>
						{:else}
							<ul class="copies">
								{#each e.copies as c (c.id)}
									<li>
										{statusLabels[c.status]}{c.price_cents != null
											? ` · ${formatPLN(c.price_cents)}`
											: ''}{c.shop ? ` · ${c.shop}` : ''}
									</li>
								{/each}
							</ul>
						{/if}
					</Card>
				{/each}
			</div>
		</Section>
	</div>

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

<style>
	.content {
		display: grid;
		gap: 1.5rem;
		padding: var(--sui-pane-gutter, 1.5rem);
	}
	.cards {
		display: grid;
		gap: 1rem;
		grid-template-columns: repeat(auto-fill, minmax(18rem, 1fr));
	}
	.copies {
		margin: 0;
		padding-inline-start: 1.25rem;
	}
	.muted {
		margin: 0;
		color: var(--sui-text-muted);
	}
	.notes {
		margin: 0;
		white-space: pre-wrap;
	}
</style>
