<script lang="ts">
	import { Button, Field, FormError, SegmentedControl, Textarea } from '@smykla-skalski/sui';
	import {
		authorsLabel,
		kindLabels,
		options,
		type FormIssue,
		type TitleForm
	} from '$lib/collection';

	let {
		form = $bindable(),
		issue,
		saving,
		submitLabel,
		kindLocked = false,
		onsubmit,
		oncancel
	}: {
		form: TitleForm;
		issue: FormIssue | null;
		saving: boolean;
		submitLabel: string;
		kindLocked?: boolean;
		onsubmit: () => void;
		oncancel?: () => void;
	} = $props();

	const errorFor = (field: string) => (issue?.field === field ? issue.message : undefined);
	const fieldKeys = [
		'name',
		'year',
		'tmdb_id',
		'authors',
		'polish_title',
		'original_title',
		'bn_id',
		'openlibrary_id',
		'cover_url',
		'notes'
	];
	const general = $derived(issue && !fieldKeys.includes(issue.field) ? issue.message : null);
</script>

<form
	class="form"
	novalidate
	onsubmit={(e) => {
		e.preventDefault();
		onsubmit();
	}}
>
	<FormError message={general} />
	<SegmentedControl
		label="Typ"
		options={options(kindLabels)}
		bind:value={form.kind}
		disabled={kindLocked}
	/>
	<Field
		label="Tytuł"
		required
		requiredLabel="Wymagane"
		bind:value={form.name}
		error={errorFor('name')}
	/>
	<Field
		label="Polski tytuł"
		bind:value={form.polish_title}
		error={errorFor('polish_title')}
		hint="Potrzebny do wyszukiwania ofert."
	/>
	<Field
		label="Tytuł oryginalny"
		bind:value={form.original_title}
		error={errorFor('original_title')}
	/>
	<Field label="Rok" inputmode="numeric" bind:value={form.year} error={errorFor('year')} />
	{#if form.kind === 'movie'}
		<Field
			label="Identyfikator TMDB"
			inputmode="numeric"
			bind:value={form.tmdb_id}
			error={errorFor('tmdb_id')}
		/>
	{:else}
		<Field
			label={authorsLabel(form.kind)}
			hint={form.kind === 'music' ? 'Jeden wykonawca w wierszu.' : 'Jeden autor w wierszu.'}
			error={errorFor('authors')}
		>
			{#snippet control(c)}
				<Textarea {...c} rows={2} bind:value={form.authors} />
			{/snippet}
		</Field>
		{#if form.kind === 'book'}
			<Field label="Identyfikator BN" bind:value={form.bn_id} error={errorFor('bn_id')} />
			<Field
				label="Identyfikator Open Library"
				bind:value={form.openlibrary_id}
				error={errorFor('openlibrary_id')}
			/>
		{/if}
	{/if}
	<Field
		label="Adres okładki tytułu"
		inputmode="url"
		bind:value={form.cover_url}
		error={errorFor('cover_url')}
	/>
	<Field label="Notatki" error={errorFor('notes')}>
		{#snippet control(c)}
			<Textarea {...c} rows={3} bind:value={form.notes} />
		{/snippet}
	</Field>
	<div class="actions">
		<Button tone="signal" type="submit" loading={saving}>{submitLabel}</Button>
		{#if oncancel}
			<Button type="button" onclick={oncancel}>Anuluj</Button>
		{/if}
	</div>
</form>

<style>
	.form {
		display: grid;
		gap: 1rem;
		max-width: 40rem;
	}
	.actions {
		display: flex;
		gap: 0.5rem;
	}
</style>
