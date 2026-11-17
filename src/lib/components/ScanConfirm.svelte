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
		formatLabels,
		kindLabels,
		options,
		type FormIssue
	} from '$lib/collection';
	import type { Candidate, LookupResult, QuickForm, SaveTarget } from '$lib/scan';

	let {
		barcode,
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
		barcode: string;
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
		candidates.map((c, i) => ({
			value: String(i),
			label: `${displayName({ name: c.title.name, polish_title: c.title.polish_title ?? null, original_title: c.title.original_title ?? null })} · ${c.provider} · ${Math.round(c.confidence * 100)}%`
		}))
	);
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
	<p class="code">{barcode}</p>
	<FormError message={general} />
	{#if target.kind === 'owned'}
		<p>
			Masz już to wydanie:
			<Link href={`/titles/${target.edition.title.id}`}>{displayName(target.edition.title)}</Link>
			· {editionSummary(target.edition)} · egzemplarzy: {target.edition.copies.length}. Dodać
			kolejny?
		</p>
	{:else}
		{#if candidates.length > 1}
			<Field label="Propozycja">
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
		{#if sources}
			<Callout tone="warning">Nie odpowiedziały źródła: {sources}.</Callout>
		{/if}
		<SegmentedControl label="Typ" options={options(kindLabels)} bind:value={form.kind} />
		<Field label="Tytuł" bind:value={form.name} error={errorFor('name')} autocomplete="off" />
		{#if form.kind === 'movie'}
			<SegmentedControl label="Nośnik" options={options(formatLabels)} bind:value={form.format} />
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
		label="Cena zakupu (zł)"
		inputmode="decimal"
		placeholder="49,99"
		enterkeyhint="done"
		autocomplete="off"
		bind:value={form.price}
		error={errorFor('price_cents')}
	/>
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
	<div class="actions">
		<Button tone="signal" type="submit" loading={saving}>Dodaj</Button>
		<Button type="button" disabled={saving} onclick={onskip}>Pomiń</Button>
	</div>
</form>

<style>
	.confirm {
		font-family: var(--sui-sans);
		color: var(--sui-text-primary);
		display: grid;
		gap: 1rem;
		max-width: 40rem;
		padding: 1rem;
		border: 1px solid var(--sui-border, currentColor);
		border-radius: var(--sui-radius-md, 0.5rem);
	}
	.code {
		margin: 0;
		font-family: var(--sui-mono);
		font-size: 1.125rem;
	}
	p {
		margin: 0;
	}
	.actions {
		display: flex;
		gap: 0.5rem;
	}
</style>
