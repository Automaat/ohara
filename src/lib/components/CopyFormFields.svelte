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
	} from '#lib/collection.js';
	import { marketLabel, type MarketPrice } from '#lib/deals.js';

	let {
		form = $bindable(),
		issue,
		saving,
		submitLabel,
		market = null,
		onsubmit,
		oncancel
	}: {
		form: CopyForm;
		issue: FormIssue | null;
		saving: boolean;
		submitLabel: string;
		/** The used market price of the copy's title and format, shown under a wish's limit. */
		market?: MarketPrice | null;
		onsubmit: () => void;
		oncancel?: () => void;
	} = $props();

	const marketId = $props.id();
	const marketLine = $derived(marketLabel(market));

	const errorFor = (field: string) => (issue?.field === field ? issue.message : undefined);
	const fieldKeys = [
		'price_cents',
		'purchased_on',
		'shop',
		'acquired_as',
		'condition',
		'notes',
		'max_price_cents',
		'max_market_percent'
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
			aria-describedby={marketLine ? `${marketId}-market` : undefined}
		/>
		{#if marketLine}
			<p class="market" id="{marketId}-market">{marketLine}</p>
		{/if}
		<Field
			label="Procent ceny rynkowej (%)"
			hint="Powiadomienie także o ofercie za ten procent ceny rynkowej lub mniej, gdy rynek ma co najmniej 3 oferty."
			inputmode="numeric"
			placeholder="70"
			bind:value={form.market_percent}
			error={errorFor('max_market_percent')}
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
			<Button type="button" onclick={oncancel} disabled={saving}>Anuluj</Button>
		{/if}
	</div>
</form>

<style>
	.form {
		display: grid;
		gap: 1rem;
		max-width: 40rem;
	}
	.market {
		margin: -0.5rem 0 0;
		color: var(--sui-text-muted);
		font-size: 0.875rem;
		font-variant-numeric: tabular-nums;
	}
	.actions {
		display: flex;
		gap: 0.5rem;
	}
</style>
