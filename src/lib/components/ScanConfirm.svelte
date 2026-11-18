<script lang="ts">
	import {
		Button,
		Callout,
		Field,
		FormError,
		Link,
		SegmentedControl,
		Select
	} from '@smykla-skalski/sui';
	import {
		acquiredLabels,
		bindingLabels,
		displayName,
		editionSummary,
		formatOptions as formatOptionsFor,
		kindLabels,
		options,
		priceHint,
		type FormIssue
	} from '$lib/collection';
	import {
		candidateLabel,
		type Candidate,
		type LookupResult,
		type QuickForm,
		type SaveTarget
	} from '$lib/scan';

	let {
		target,
		candidates,
		picked,
		errors,
		form = $bindable(),
		issue,
		saving,
		onpick,
		onsave,
		onskip
	}: {
		target: SaveTarget;
		candidates: Candidate[];
		picked: number;
		errors: LookupResult['errors'];
		form: QuickForm;
		issue: FormIssue | null;
		saving: boolean;
		onpick: (index: number) => void;
		onsave: () => void;
		onskip: () => void;
	} = $props();

	const errorFor = (field: string) => (issue?.field === field ? issue.message : undefined);
	const ownFields = ['name', 'price_cents', 'shop'];
	const general = $derived(issue && !ownFields.includes(issue.field) ? issue.message : null);

	const candidateOptions = $derived(
		candidates.map((c, i) => ({ value: String(i), label: candidateLabel(c) }))
	);
	const cover = $derived(candidates[picked]?.cover_url ?? '');
	const candidateEdition = $derived(candidates[picked]?.edition);
	const ownedCount = $derived(
		target.kind === 'owned'
			? target.edition.copies.filter((copy) => copy.status === 'owned').length
			: 0
	);
	const formatOptions = $derived(formatOptionsFor(form.kind));
	const acquiredOptions = [{ value: '', label: 'Nie podano' }, ...options(acquiredLabels)];
	const bindingOptions = [{ value: '', label: 'Nie podano' }, ...options(bindingLabels)];
	const sources = $derived(errors.map((e) => e.provider).join(', '));
</script>

<form
	class="confirm"
	novalidate
	aria-label="Potwierdź pozycję"
	onsubmit={(e) => {
		e.preventDefault();
		onsave();
	}}
>
	<FormError message={general} />
	{#if target.kind === 'owned'}
		<div class="edition">
			<strong>
				<Link href={`/titles/${target.edition.title.id}`}>{displayName(target.edition.title)}</Link>
			</strong>
			<p>{editionSummary({ ...target.edition, barcode: null })}</p>
			<p>Egzemplarze w kolekcji: {ownedCount}</p>
		</div>
	{:else}
		{#if candidates.length > 1}
			<Field
				label={candidates.every((c) => c.type === 'music') ? 'Wydanie' : 'Propozycja'}
				hint={candidates.every((c) => c.type === 'music')
					? 'Ten kod ma kilka wydań: wybierz swoje.'
					: undefined}
			>
				{#snippet control(c)}
					<Select
						{...c}
						bind:value={() => String(picked), (v) => onpick(Number(v))}
						options={candidateOptions}
					/>
				{/snippet}
			</Field>
		{:else if candidates.length === 0}
			<p>Brak propozycji: uzupełnij pozycję ręcznie.</p>
		{/if}
		<div class="candidate">
			{#if cover}
				<img class="cover" src={cover} alt="Okładka" referrerpolicy="no-referrer" />
			{/if}
			<div class="edition">
				<Field
					id="scan-name"
					label="Tytuł"
					bind:value={form.name}
					error={errorFor('name')}
					autocomplete="off"
				/>
				{#if candidateEdition}
					{#if candidateEdition.label || candidateEdition.publisher || candidateEdition.year}
						<p>
							{[candidateEdition.label, candidateEdition.publisher, candidateEdition.year]
								.filter(Boolean)
								.join(' · ')}
						</p>
					{/if}
					<p>Egzemplarze tego wydania w kolekcji: 0</p>
				{/if}
			</div>
		</div>
		<SegmentedControl label="Typ" options={options(kindLabels)} bind:value={form.kind} />
		{#if formatOptions.length > 0}
			<SegmentedControl label="Nośnik" options={formatOptions} bind:value={form.format} />
		{:else}
			<Field label="Oprawa">
				{#snippet control(c)}
					<Select {...c} bind:value={form.binding} options={bindingOptions} />
				{/snippet}
			</Field>
		{/if}
	{/if}
	<Field
		id="scan-price"
		label="Cena zakupu (zł, opcjonalnie)"
		hint={priceHint}
		inputmode="decimal"
		placeholder="49,99"
		enterkeyhint="done"
		autocomplete="off"
		bind:value={form.price}
		error={errorFor('price_cents')}
	/>
	<div class="actions">
		<Button tone="signal" type="submit" loading={saving}>Dodaj egzemplarz</Button>
		<Button type="button" disabled={saving} onclick={onskip}>Pomiń</Button>
	</div>
	<details open={issue?.field === 'shop' || undefined}>
		<summary>Szczegóły zakupu</summary>
		<div class="purchase">
			<Field
				label="Sklep lub sprzedawca"
				bind:value={form.shop}
				error={errorFor('shop')}
				autocomplete="off"
			/>
			<SegmentedControl
				label="Nowy czy używany"
				options={acquiredOptions}
				bind:value={form.acquired_as}
			/>
		</div>
	</details>
	{#if sources}
		<Callout tone="warning">Nie odpowiedziały źródła: {sources}.</Callout>
	{/if}
</form>

<style>
	.confirm {
		font-family: var(--sui-sans);
		color: var(--sui-text-primary);
		display: grid;
		gap: 0.75rem;
		max-width: 40rem;
		padding: 1rem;
		border: 1px solid var(--sui-border, currentColor);
		border-radius: var(--sui-radius-md, 0.5rem);
	}
	.cover {
		width: 3.5rem;
		height: 4.5rem;
		object-fit: contain;
		border-radius: var(--sui-radius-sm, 0.25rem);
	}
	.candidate {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		align-items: center;
		gap: 0.75rem;
	}
	.candidate:has(.cover) {
		grid-template-columns: auto minmax(0, 1fr);
	}
	.edition,
	.purchase {
		display: grid;
		gap: 0.5rem;
	}
	.purchase {
		margin-block-start: 0.75rem;
	}
	summary {
		cursor: pointer;
	}
	p {
		margin: 0;
	}
	.actions {
		display: flex;
		gap: 0.5rem;
	}
</style>
