<script lang="ts">
	import {
		Button,
		Chip,
		Combobox,
		Field,
		FormError,
		SegmentedControl,
		Textarea
	} from '@smykla-skalski/sui';
	import { tick } from 'svelte';
	import {
		addTag,
		authorsLabel,
		kindLabels,
		options,
		seriesNames,
		type FormIssue,
		type Series,
		type TitleForm
	} from '#lib/collection.js';
	import { tagKey } from '#lib/tags.js';

	let {
		form = $bindable(),
		issue,
		saving,
		submitLabel,
		kindLocked = false,
		tagSuggestions = [],
		seriesSuggestions = [],
		onsubmit,
		oncancel
	}: {
		form: TitleForm;
		issue: FormIssue | null;
		saving: boolean;
		submitLabel: string;
		kindLocked?: boolean;
		/** The tags other titles carry, offered as the reader types one. */
		tagSuggestions?: readonly string[];
		/** The series in use, of every kind; the form offers those of the title's kind. */
		seriesSuggestions?: readonly Pick<Series, 'kind' | 'name'>[];
		onsubmit: () => void;
		oncancel?: () => void;
	} = $props();

	const errorFor = (field: string) => (issue?.field === field ? issue.message : undefined);
	const fieldKeys = [
		'name',
		'year',
		'authors',
		'tmdb_id',
		'polish_title',
		'original_title',
		'bn_id',
		'openlibrary_id',
		'cover_url',
		'notes',
		'tags',
		'series_name',
		'series_number'
	];
	const general = $derived(issue && !fieldKeys.includes(issue.field) ? issue.message : null);
	let titleForm: HTMLFormElement;

	/** The tags in use the title does not carry yet, case aside. */
	const tagChoices = $derived(
		tagSuggestions
			.filter((tag) => !form.tags.some((t) => tagKey(t) === tagKey(tag)))
			.map((tag) => ({ value: tag, label: tag }))
	);

	/**
	 * The tags in use that hold what the reader typed, compared by tag key as the server compares
	 * tags: the Combobox's own filter lower-cases by JavaScript's rules, which turn a final Σ into
	 * ς, so "οσ" would miss "ΟΣ".
	 */
	function suggestTags(typed: string) {
		const key = tagKey(typed);
		return Promise.resolve(tagChoices.filter((choice) => tagKey(choice.label).includes(key)));
	}

	/** The series of the title's kind, offered as the reader types its name. */
	const seriesChoices = $derived(
		seriesNames(seriesSuggestions, form.kind).map((name) => ({ value: name, label: name }))
	);

	/** The series whose names hold what was typed, compared by the key the server matches by. */
	function suggestSeries(typed: string) {
		const key = tagKey(typed.trim());
		return Promise.resolve(seriesChoices.filter((choice) => tagKey(choice.label).includes(key)));
	}

	let tagInput = $state<HTMLInputElement | null>(null);

	/** Adds a tag and empties the field for the next one. */
	function keepTag(tag: string) {
		form.tags = addTag(form.tags, tag);
		form.tag = '';
	}

	/**
	 * Adds the tag the reader picked or settled with Enter. A tag left typed when the field loses
	 * focus stays in it as a draft that saving adds: drawing its chip then would move the save
	 * button between the press and its release.
	 */
	function pickTag(tag: string) {
		if (tag && document.activeElement === tagInput) keepTag(tag);
	}

	function removeTag(tag: string) {
		form.tags = form.tags.filter((t) => t !== tag);
	}

	/** Whether Enter came while a tag was typed: it adds the tag instead of sending the form. */
	let enterAddsTag = false;

	$effect(() => {
		if (!issue?.field) return;
		void tick().then(() => titleForm.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
	});
</script>

<form
	class="form"
	bind:this={titleForm}
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
	<Field label="Rok" inputmode="numeric" bind:value={form.year} error={errorFor('year')} />
	{#if form.kind !== 'movie'}
		<Field
			label={authorsLabel(form.kind)}
			hint={form.kind === 'music' ? 'Jeden wykonawca w wierszu.' : 'Jeden autor w wierszu.'}
			error={errorFor('authors')}
		>
			{#snippet control(c)}
				<Textarea {...c} rows={2} bind:value={form.authors} />
			{/snippet}
		</Field>
	{/if}
	<div class="series">
		<Field
			label="Seria"
			hint="Np. „Wiedźmin”. Tytuły o tej samej nazwie serii trafiają do niej razem."
			error={errorFor('series_name')}
		>
			{#snippet control(c)}
				<Combobox
					{...c}
					free
					bind:value={form.series_name}
					options={seriesChoices}
					search={suggestSeries}
					searchingLabel="Szukam…"
					failedLabel="Nie udało się wczytać podpowiedzi"
					emptyLabel="Brak takiej serii"
					useLabel={(text) => `Nowa seria „${text}”`}
					countLabel={(n) => `Podpowiedzi: ${n}`}
					moreLabel={(n) => `i jeszcze ${n}`}
				/>
			{/snippet}
		</Field>
		<Field
			label="Tom"
			inputmode="numeric"
			bind:value={form.series_number}
			error={errorFor('series_number')}
		/>
	</div>
	{@render optionalFields()}
	<div class="actions">
		<Button tone="signal" type="submit" loading={saving}>{submitLabel}</Button>
		{#if oncancel}
			<Button type="button" onclick={oncancel} disabled={saving}>Anuluj</Button>
		{/if}
	</div>
</form>

{#snippet optionalFields()}
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
	{#if form.kind === 'movie'}
		<Field
			label="Identyfikator TMDB"
			inputmode="numeric"
			bind:value={form.tmdb_id}
			error={errorFor('tmdb_id')}
		/>
	{:else if form.kind === 'book'}
		<Field label="Identyfikator BN" bind:value={form.bn_id} error={errorFor('bn_id')} />
		<Field
			label="Identyfikator Open Library"
			bind:value={form.openlibrary_id}
			error={errorFor('openlibrary_id')}
		/>
	{/if}
	<Field
		label="Adres okładki tytułu"
		inputmode="url"
		bind:value={form.cover_url}
		error={errorFor('cover_url')}
	/>
	<div class="tags">
		<Field
			label="Tagi"
			hint="Twoje grupy, np. gatunek lub „do sprzedania”. Enter dodaje tag."
			error={errorFor('tags')}
		>
			{#snippet control(c)}
				<div
					role="presentation"
					onkeydowncapture={(e) => {
						if (e.key === 'Enter' && e.target instanceof HTMLInputElement) {
							enterAddsTag = e.target.value.trim() !== '';
						}
					}}
					onkeydown={(e) => {
						if (e.key === 'Enter' && enterAddsTag) {
							e.preventDefault();
							if (form.tag.trim()) keepTag(form.tag);
						}
						enterAddsTag = false;
					}}
				>
					<Combobox
						{...c}
						free
						bind:value={form.tag}
						bind:element={tagInput}
						options={tagChoices}
						search={suggestTags}
						onselect={pickTag}
						searchingLabel="Szukam…"
						failedLabel="Nie udało się wczytać podpowiedzi"
						emptyLabel="Brak takiego tagu"
						useLabel={(text) => `Dodaj tag „${text}”`}
						countLabel={(n) => `Podpowiedzi: ${n}`}
						moreLabel={(n) => `i jeszcze ${n}`}
					/>
				</div>
			{/snippet}
		</Field>
		{#if form.tags.length > 0}
			<div class="tag-list" role="group" aria-label="Tagi tytułu">
				{#each form.tags as tag (tag)}
					<Chip
						label={tag}
						onremove={() => removeTag(tag)}
						removeLabel={(t) => `Usuń tag ${t}`}
						removedLabel={(t) => `Usunięto tag ${t}`}
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
{/snippet}

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
	.tags {
		display: grid;
		gap: 0.5rem;
	}
	.series {
		display: grid;
		grid-template-columns: minmax(0, 1fr) 6rem;
		gap: 0.75rem;
		align-items: start;
	}
	.tag-list {
		display: flex;
		flex-wrap: wrap;
		gap: 0.375rem;
	}
</style>
