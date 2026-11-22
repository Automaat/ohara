<script lang="ts">
	import {
		Button,
		Combobox,
		Field,
		FormError,
		SegmentedControl,
		Select,
		Switch,
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
	import { request } from '#lib/api.js';

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

	/** The places copies are stored at, the most used first, asked once an owned copy shows. */
	let locations = $state<string[] | null>(null);
	$effect(() => {
		if (form.status !== 'owned' || locations !== null) return;
		locations = [];
		request<string[]>(fetch, 'GET', '/api/copies/locations').then(
			(found) => (locations = found),
			() => {
				// Without suggestions the field still takes any place typed.
			}
		);
	});
	const locationOptions = $derived((locations ?? []).map((l) => ({ value: l, label: l })));

	// An ordered copy is a purchase still on its way: "Na półce" with the "Zamówiony" switch on.
	// The segments keep three options, which fit a phone's width before they turn into a select.
	const { ordered: orderedLabel, ...segmentLabels } = statusLabels;
	const statusOptions = options(segmentLabels);

	const errorFor = (field: string) => (issue?.field === field ? issue.message : undefined);
	const fieldKeys = [
		'price_cents',
		'purchased_on',
		'shop',
		'acquired_as',
		'condition',
		'notes',
		'max_price_cents',
		'max_market_percent',
		'sold_on',
		'sale_price_cents',
		'expected_on',
		'location'
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
		label="Status"
		options={statusOptions}
		bind:value={
			() => (form.status === 'ordered' ? 'owned' : form.status),
			(v) => (form.status = v ?? form.status)
		}
	/>
	{#if form.status === 'owned' || form.status === 'ordered'}
		<Switch
			label={orderedLabel}
			bind:checked={
				() => form.status === 'ordered', (on) => (form.status = on ? 'ordered' : 'owned')
			}
		/>
	{/if}
	{#if form.status !== 'wishlist'}
		<Field
			label="Cena zakupu (zł)"
			hint={priceHint}
			inputmode="decimal"
			placeholder="49,99"
			bind:value={form.price}
			error={errorFor('price_cents')}
		/>
		<Field
			label={form.status === 'ordered' ? 'Data zamówienia' : 'Data zakupu'}
			placeholder="RRRR-MM-DD"
			bind:value={form.purchased_on}
			error={errorFor('purchased_on')}
		/>
		{#if form.status === 'ordered'}
			<Field
				label="Spodziewana dostawa"
				hint="Nie wiesz? Zostaw puste."
				placeholder="RRRR-MM-DD"
				bind:value={form.expected_on}
				error={errorFor('expected_on')}
			/>
		{:else if form.status === 'sold'}
			<Field
				label="Data sprzedaży"
				placeholder="RRRR-MM-DD"
				bind:value={form.sold_on}
				error={errorFor('sold_on')}
			/>
			<Field
				label="Cena sprzedaży (zł)"
				hint="Nie pamiętasz? Zostaw puste."
				inputmode="decimal"
				placeholder="35"
				bind:value={form.sale_price}
				error={errorFor('sale_price_cents')}
			/>
		{/if}
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
		label={form.status === 'wishlist' ? 'Gdzie szukać' : 'Sklep lub sprzedawca'}
		bind:value={form.shop}
		error={errorFor('shop')}
	/>
	{#if form.status === 'owned'}
		<Field label="Miejsce" hint="Gdzie stoi egzemplarz, np. Regał 2." error={errorFor('location')}>
			{#snippet control(c)}
				<Combobox
					{...c}
					free
					options={locationOptions}
					bind:value={form.location}
					emptyLabel="Brak zapisanych miejsc"
					useLabel={(text) => `Użyj „${text}”`}
					countLabel={(count) => (count === 1 ? '1 podpowiedź' : `Podpowiedzi: ${count}`)}
					moreLabel={(count) => `Jeszcze ${count}, pisz dalej, aby zawęzić`}
				/>
			{/snippet}
		</Field>
	{/if}
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
