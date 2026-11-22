<script lang="ts">
	import {
		Button,
		CheckList,
		Chip,
		Combobox,
		Field,
		FormError,
		Select,
		Textarea
	} from '@smykla-skalski/sui';
	import { request } from '#lib/api.js';
	import {
		bindingLabels,
		displayName,
		kindLabels,
		movieFormatLabels,
		musicFormatLabels,
		options,
		packagingLabels,
		triStateLabels,
		type ContentTitle,
		type EditionForm,
		type FormIssue,
		type Kind,
		type Packaging,
		type Title
	} from '#lib/collection.js';

	let {
		form = $bindable(),
		kind,
		titleID,
		issue,
		saving,
		submitLabel,
		onsubmit,
		oncancel
	}: {
		form: EditionForm;
		kind: Kind;
		/** The edition's own title, which its box set contents cannot hold. */
		titleID: number;
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
		'contents',
		'notes'
	];
	const general = $derived(issue && !fieldKeys.includes(issue.field) ? issue.message : null);

	/** The title typed into "Zawartość zestawu" before one is picked. */
	let contentPick = $state('');
	/** The titles the last searches offered, by option value, for the pick to add. */
	const offered = new Map<string, ContentTitle>();

	/**
	 * The titles whose names or authors hold what the reader typed, as `GET /api/titles` searches,
	 * without the edition's own title and those the box set already holds.
	 */
	async function searchContents(typed: string) {
		const q = typed.trim();
		if (!q) return [];
		const titles = await request<Title[]>(
			fetch,
			'GET',
			`/api/titles?${new URLSearchParams({ q })}`
		);
		const held = new Set([titleID, ...(form.contents ?? []).map((c) => c.id)]);
		return titles
			.filter((t) => !held.has(t.id))
			.map((t) => {
				const { id, name, original_title, polish_title, year } = t;
				offered.set(String(id), { id, kind: t.kind, name, original_title, polish_title, year });
				return {
					value: String(id),
					label: displayName(t),
					description: [kindLabels[t.kind], year].filter(Boolean).join(' · ')
				};
			});
	}

	/** Adds the picked title to the box set and empties the field for the next one. */
	function pickContent(value: string) {
		const title = offered.get(value);
		if (!title || (form.contents ?? []).some((c) => c.id === title.id)) return;
		form.contents = [...(form.contents ?? []), title];
		contentPick = '';
	}

	function removeContent(id: number) {
		form.contents = (form.contents ?? []).filter((c) => c.id !== id);
	}
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
	<div class="contents">
		<Field
			label="Zawartość zestawu"
			hint="Tytuły w tym zestawie, np. filmy trylogii. Brakujący tytuł dodaj najpierw w Kolekcji."
			error={errorFor('contents')}
		>
			{#snippet control(c)}
				<Combobox
					{...c}
					bind:value={contentPick}
					search={searchContents}
					onselect={pickContent}
					searchingLabel="Szukam…"
					failedLabel="Nie udało się wyszukać tytułów"
					emptyLabel="Brak takiego tytułu"
					countLabel={(n) => `Tytuły: ${n}`}
					moreLabel={(n) => `i jeszcze ${n}`}
					draftMessage="Wybierz tytuł z listy albo wyczyść pole."
				/>
			{/snippet}
		</Field>
		{#if form.contents?.length}
			<div class="content-list" role="group" aria-label="Tytuły w zestawie">
				{#each form.contents as content (content.id)}
					<Chip
						label={displayName(content)}
						onremove={() => removeContent(content.id)}
						removeLabel={(t) => `Usuń z zestawu: ${t}`}
						removedLabel={(t) => `Usunięto z zestawu: ${t}`}
					/>
				{/each}
			</div>
		{/if}
	</div>
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
	.contents {
		display: grid;
		gap: 0.5rem;
	}
	.content-list {
		display: flex;
		flex-wrap: wrap;
		gap: 0.375rem;
	}
</style>
