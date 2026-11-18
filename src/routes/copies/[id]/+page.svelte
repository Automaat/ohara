<script lang="ts">
	import {
		BackLink,
		Button,
		ConfirmDialog,
		ErrorState,
		FormError,
		PageHeader,
		Skeleton
	} from '@smykla-skalski/sui';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { ApiError, errorMessage, request } from '#lib/api.js';
	import {
		copyPayload,
		copyToForm,
		displayName,
		editionSummary,
		statusLabels,
		type Copy,
		type CopyDetail,
		type FormIssue
	} from '#lib/collection.js';
	import CopyFormFields from '#lib/components/CopyFormFields.svelte';

	const id = $derived(Number(page.params.id));

	let detail = $state<CopyDetail | null>(null);
	let failure = $state<string | null>(null);
	let busy = $state(false);
	let form = $state(copyToForm(null));
	let issue = $state<FormIssue | null>(null);
	let saving = $state(false);
	let saved = $state(false);
	let confirmDelete = $state(false);
	let deleteError = $state<string | null>(null);

	async function load(copyID: number) {
		busy = true;
		try {
			detail = await request<CopyDetail>(fetch, 'GET', `/api/copies/${copyID}`);
			form = copyToForm(detail);
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

	async function save(d: CopyDetail) {
		const parsed = copyPayload(form, d.edition_id);
		if (!parsed.ok) {
			issue = parsed.issue;
			return;
		}
		saving = true;
		saved = false;
		try {
			await request<Copy>(fetch, 'PUT', `/api/copies/${id}`, parsed.value);
			issue = null;
			saved = true;
			await load(id);
		} catch (err) {
			issue = {
				field: err instanceof ApiError ? (err.field ?? '') : '',
				message: errorMessage(err)
			};
		} finally {
			saving = false;
		}
	}

	async function remove(d: CopyDetail) {
		try {
			await request(fetch, 'DELETE', `/api/copies/${id}`);
			await goto(`/editions/${d.edition_id}`);
		} catch (err) {
			deleteError = errorMessage(err);
		}
	}
</script>

<svelte:head>
	<title>Egzemplarz · Ohara</title>
</svelte:head>

{#if failure}
	<div class="content">
		<ErrorState
			title="Nie udało się wczytać egzemplarza"
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
	<PageHeader
		title={`${statusLabels[d.status]}: ${displayName(d.title)}`}
		description={editionSummary(d.edition)}
	>
		{#snippet kicker()}
			<BackLink href={`/editions/${d.edition_id}`} label={editionSummary(d.edition)} />
		{/snippet}
		{#snippet actions()}
			<Button tone="stop-quiet" onclick={() => ((deleteError = null), (confirmDelete = true))}>
				Usuń
			</Button>
		{/snippet}
	</PageHeader>

	<div class="content">
		<FormError message={deleteError} />
		{#if saved}
			<p role="status">Zapisano.</p>
		{/if}
		<CopyFormFields
			bind:form
			{issue}
			{saving}
			submitLabel="Zapisz zmiany"
			onsubmit={() => save(d)}
		/>
	</div>

	<ConfirmDialog
		bind:open={confirmDelete}
		title="Usunąć egzemplarz?"
		consequence="Egzemplarz i jego cena znikną z kolekcji. Tego nie można cofnąć."
		confirmLabel="Usuń egzemplarz"
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
</style>
