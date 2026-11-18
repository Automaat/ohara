<script lang="ts">
	import {
		Button,
		Field,
		FormError,
		SegmentedControl,
		Select,
		Textarea
	} from '@smykla-skalski/sui';
	import {
		acquiredLabels,
		conditionLabels,
		options,
		priceHint,
		statusLabels,
		type CopyForm,
		type CopyStatus,
		type FormIssue
	} from '$lib/collection';

	let {
		form = $bindable(),
		issue,
		saving,
		submitLabel,
		onsubmit,
		oncancel
	}: {
		form: CopyForm;
		issue: FormIssue | null;
		saving: boolean;
		submitLabel: string;
		onsubmit: () => void;
		oncancel?: () => void;
	} = $props();

	const errorFor = (field: string) => (issue?.field === field ? issue.message : undefined);
	const fieldKeys = [
		'price_cents',
		'purchased_on',
		'shop',
		'acquired_as',
		'condition',
		'notes',
		'max_price_cents'
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
	<SegmentedControl label="Status" options={options(statusLabels)} bind:value={form.status} />
	{#if form.status === 'owned'}
		<Field
			label="Cena zakupu (zł)"
			hint={priceHint}
			inputmode="decimal"
			placeholder="49,99"
			bind:value={form.price}
			error={errorFor('price_cents')}
		/>
		<Field
			label="Data zakupu"
			placeholder="RRRR-MM-DD"
			bind:value={form.purchased_on}
			error={errorFor('purchased_on')}
		/>
	{:else}
		<Field
			label="Cena maksymalna (zł)"
			hint="Powiadomienie na Telegramie o każdej nowej ofercie w tej cenie lub taniej."
			inputmode="decimal"
			placeholder="60"
			bind:value={form.max_price}
			error={errorFor('max_price_cents')}
		/>
	{/if}
	<Field
		label={form.status === 'owned' ? 'Sklep lub sprzedawca' : 'Gdzie szukać'}
		bind:value={form.shop}
		error={errorFor('shop')}
	/>
	<Field label="Nowy czy używany" error={errorFor('acquired_as')}>
		{#snippet control(c)}
			<Select
				{...c}
				bind:value={form.acquired_as}
				options={[{ value: '', label: 'Nie podano' }, ...options(acquiredLabels)]}
			/>
		{/snippet}
	</Field>
	<Field label="Stan" error={errorFor('condition')}>
		{#snippet control(c)}
			<Select
				{...c}
				bind:value={form.condition}
				options={[{ value: '', label: 'Nie podano' }, ...options(conditionLabels)]}
			/>
		{/snippet}
	</Field>
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
