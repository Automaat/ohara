<script lang="ts">
	import { Button, CheckList, Field, FormError, Select, Textarea } from '@smykla-skalski/sui';
	import {
		bindingLabels,
		movieFormatLabels,
		musicFormatLabels,
		options,
		packagingLabels,
		triStateLabels,
		type EditionForm,
		type FormIssue,
		type Kind,
		type Packaging
	} from '#lib/collection.js';

	let {
		form = $bindable(),
		kind,
		issue,
		saving,
		submitLabel,
		onsubmit,
		oncancel
	}: {
		form: EditionForm;
		kind: Kind;
		issue: FormIssue | null;
		saving: boolean;
		submitLabel: string;
		onsubmit: () => void;
		oncancel?: () => void;
	} = $props();

	const errorFor = (field: string) => (issue?.field === field ? issue.message : undefined);
	const fieldKeys = [
		'barcode',
		'label',
		'publisher',
		'year',
		'format',
		'binding',
		'pl_subtitles',
		'pl_voiceover',
		'packaging',
		'discogs_release_id',
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
	<Field
		label="Kod kreskowy"
		hint="EAN, UPC lub ISBN z okładki."
		inputmode="numeric"
		bind:value={form.barcode}
		error={errorFor('barcode')}
	/>
	{#if kind === 'movie'}
		<Field label="Nośnik" required requiredLabel="Wymagane" error={errorFor('format')}>
			{#snippet control(c)}
				<Select
					{...c}
					bind:value={form.format}
					placeholder="Wybierz nośnik"
					options={options(movieFormatLabels)}
				/>
			{/snippet}
		</Field>
		<Field label="Polskie napisy" error={errorFor('pl_subtitles')}>
			{#snippet control(c)}
				<Select {...c} bind:value={form.pl_subtitles} options={options(triStateLabels)} />
			{/snippet}
		</Field>
		<Field label="Polski lektor lub dubbing" error={errorFor('pl_voiceover')}>
			{#snippet control(c)}
				<Select {...c} bind:value={form.pl_voiceover} options={options(triStateLabels)} />
			{/snippet}
		</Field>
		<CheckList
			label="Opakowanie"
			options={options(packagingLabels)}
			value={form.packaging}
			error={errorFor('packaging')}
			onchange={(v: string[]) => (form.packaging = v as Packaging[])}
		/>
	{:else if kind === 'music'}
		<Field label="Nośnik" required requiredLabel="Wymagane" error={errorFor('format')}>
			{#snippet control(c)}
				<Select
					{...c}
					bind:value={form.format}
					placeholder="Wybierz nośnik"
					options={options(musicFormatLabels)}
				/>
			{/snippet}
		</Field>
		<Field
			label="Wydanie na Discogs"
			hint="Numer wydania z adresu discogs.com/release/…; wycena czyta jego ceny."
			inputmode="numeric"
			bind:value={form.discogs_release_id}
			error={errorFor('discogs_release_id')}
		/>
	{:else}
		<Field label="Oprawa" error={errorFor('binding')}>
			{#snippet control(c)}
				<Select
					{...c}
					bind:value={form.binding}
					placeholder="Nie podano"
					options={[{ value: '', label: 'Nie podano' }, ...options(bindingLabels)]}
				/>
			{/snippet}
		</Field>
	{/if}
	<Field label="Wydawca" bind:value={form.publisher} error={errorFor('publisher')} />
	<Field label="Rok wydania" inputmode="numeric" bind:value={form.year} error={errorFor('year')} />
	<Field
		label="Opis wydania"
		hint="Np. edycja kolekcjonerska."
		bind:value={form.label}
		error={errorFor('label')}
	/>
	<Field
		label="Adres okładki wydania"
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
