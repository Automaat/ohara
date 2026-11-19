<script lang="ts">
	import { Button, Field, FormError, SegmentedControl, Select } from '@smykla-skalski/sui';
	import { tick } from 'svelte';
	import {
		acquiredLabels,
		conditionLabels,
		formatOptions as formatOptionsFor,
		kindLabels,
		options,
		packagingLabels,
		type Binding,
		type FormIssue,
		type Packaging
	} from '#lib/collection.js';
	import { isoDate, type QuickForm, type SaveTarget } from '#lib/scan.js';

	let {
		target,
		form = $bindable(),
		issue,
		saving,
		shops,
		editTitle = false,
		skipLabel = 'Pomiń',
		onsave,
		onskip,
		onwish
	}: {
		target: SaveTarget;
		form: QuickForm;
		issue: FormIssue | null;
		saving: boolean;
		/** Shops used lately, newest first, offered as chips. */
		shops: string[];
		/** Shows the title and kind of a new item as fields. */
		editTitle?: boolean;
		skipLabel?: string;
		onsave: () => void;
		onskip: () => void;
		onwish: (on: boolean) => void;
	} = $props();

	const bindingShort: Record<Binding, string> = {
		hardcover: 'Twarda',
		paperback: 'Miękka',
		other: 'Inna'
	};
	const packagingOptions = options(packagingLabels);
	const acquiredOptions = options(acquiredLabels);
	const conditionOptions = [{ value: '', label: 'Nie podano' }, ...options(conditionLabels)];
	const priceFields = ['price_cents', 'max_price_cents', 'purchased_on'];
	const ownFields = ['name', 'shop', ...priceFields];

	let typingShop = $state(false);

	const wish = $derived(form.status === 'wishlist');
	const formatOptions = $derived(formatOptionsFor(form.kind));
	const today = $derived(isoDate(new Date()));
	const errorFor = (field: string) => (issue?.field === field ? issue.message : undefined);
	const general = $derived(issue && !ownFields.includes(issue.field) ? issue.message : null);
	const priceError = $derived(issue && priceFields.includes(issue.field) ? issue.message : null);
	const shopChips = $derived(
		!typingShop && form.shop.trim() && !shops.includes(form.shop.trim())
			? [form.shop.trim(), ...shops]
			: shops
	);

	$effect(() => {
		if (issue?.field === 'shop') typingShop = true;
	});

	function togglePackaging(p: Packaging) {
		form.packaging = form.packaging.includes(p)
			? form.packaging.filter((x) => x !== p)
			: [...form.packaging, p];
	}

	function pickShop(shop: string) {
		form.shop = form.shop.trim() === shop ? '' : shop;
		typingShop = false;
	}

	async function typeShop() {
		typingShop = !typingShop;
		if (!typingShop) return;
		if (shops.includes(form.shop.trim())) form.shop = '';
		await tick();
		document.getElementById('scan-shop')?.focus();
	}
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
	{#if target.kind === 'new'}
		{#if editTitle}
			<Field
				id="scan-name"
				label="Tytuł"
				bind:value={form.name}
				error={errorFor('name')}
				autocomplete="off"
			/>
			<SegmentedControl
				class="seg"
				label="Typ"
				options={options(kindLabels)}
				bind:value={form.kind}
			/>
		{/if}
		{#if formatOptions.length > 0}
			<SegmentedControl
				class="seg"
				label="Nośnik"
				options={formatOptions}
				bind:value={form.format}
			/>
		{:else}
			<SegmentedControl
				class="seg"
				label="Oprawa"
				options={options(bindingShort)}
				bind:value={form.binding}
			/>
		{/if}
		{#if form.kind === 'movie'}
			<div class="toggles" role="group" aria-label="Opakowanie">
				{#each packagingOptions as p (p.value)}
					<button
						type="button"
						class="chip toggle"
						aria-pressed={form.packaging.includes(p.value)}
						onclick={() => togglePackaging(p.value)}
					>
						<svg
							class="tick"
							width="16"
							height="16"
							viewBox="0 0 24 24"
							fill="none"
							stroke="currentColor"
							stroke-width="2.5"
							stroke-linecap="round"
							stroke-linejoin="round"
							aria-hidden="true"
							focusable="false"
						>
							<path d="M5 12.5 10 17.5 19 7" />
						</svg>
						{p.label}
					</button>
				{/each}
			</div>
		{/if}
	{/if}

	<fieldset class="purchase">
		<legend class="caption">{wish ? 'Lista życzeń' : 'Zakup'}</legend>
		<div class={['row', { wish, 'with-today': !wish && form.purchased_on !== today }]}>
			<div class={['box', { invalid: errorFor('price_cents') || errorFor('max_price_cents') }]}>
				<label for="scan-price">{wish ? 'Limit ceny' : 'Cena'}</label>
				<span class="value">
					<input
						id="scan-price"
						inputmode="decimal"
						enterkeyhint="done"
						autocomplete="off"
						placeholder="0,00"
						aria-invalid={errorFor('price_cents') || errorFor('max_price_cents') ? true : undefined}
						aria-describedby={priceError ? 'scan-price-error' : undefined}
						bind:value={form.price}
					/>
					<span class="unit" aria-hidden="true">zł</span>
				</span>
			</div>
			{#if !wish}
				<div
					class={[
						'box',
						'date',
						{
							invalid: errorFor('purchased_on'),
							today: form.purchased_on === today,
							empty: !form.purchased_on
						}
					]}
				>
					<label for="scan-date">Data zakupu</label>
					<input
						id="scan-date"
						type="date"
						aria-invalid={errorFor('purchased_on') ? true : undefined}
						aria-describedby={priceError ? 'scan-price-error' : undefined}
						bind:value={form.purchased_on}
					/>
					<span class="shown" aria-hidden="true">
						{form.purchased_on === today ? 'Dziś' : 'Brak'}
					</span>
				</div>
				{#if form.purchased_on !== today}
					<button type="button" class="chip" onclick={() => (form.purchased_on = today)}>
						Dziś
					</button>
				{/if}
			{/if}
		</div>
		{#if priceError}
			<p class="error" id="scan-price-error">{priceError}</p>
		{/if}

		{#if !wish}
			<div class="shops" role="group" aria-labelledby="scan-shops">
				<span class="caption" id="scan-shops">Sklep</span>
				<div class="chips">
					{#each shopChips as shop (shop)}
						<button
							type="button"
							class="chip"
							aria-pressed={form.shop.trim() === shop}
							onclick={() => pickShop(shop)}
						>
							{shop}
						</button>
					{/each}
					<button
						type="button"
						class="chip add"
						aria-label="Inny sklep"
						aria-expanded={typingShop}
						onclick={typeShop}
					>
						<svg
							width="18"
							height="18"
							viewBox="0 0 24 24"
							fill="none"
							stroke="currentColor"
							stroke-width="2.2"
							stroke-linecap="round"
							aria-hidden="true"
							focusable="false"
						>
							<path d="M12 5v14M5 12h14" />
						</svg>
					</button>
				</div>
				{#if typingShop}
					<Field
						id="scan-shop"
						label="Sklep lub sprzedawca"
						bind:value={form.shop}
						error={errorFor('shop')}
						autocomplete="off"
					/>
				{/if}
			</div>
			<SegmentedControl
				class="seg"
				label="Nowy czy używany"
				options={acquiredOptions}
				bind:value={form.acquired_as}
			/>
			{#if form.acquired_as === 'used'}
				<Field label="Stan">
					{#snippet control(c)}
						<Select {...c} bind:value={form.condition} options={conditionOptions} />
					{/snippet}
				</Field>
			{/if}
		{/if}
	</fieldset>

	<div class="actions">
		<Button class="primary" tone="signal" type="submit" loading={saving}>
			{wish ? 'Dodaj do listy życzeń' : 'Dodaj do kolekcji'}
		</Button>
		<div class="quiet">
			<Button tone="ghost" type="button" disabled={saving} onclick={() => onwish(!wish)}>
				{#snippet icon()}
					{#if !wish}
						<svg
							width="18"
							height="18"
							viewBox="0 0 24 24"
							fill="currentColor"
							aria-hidden="true"
							focusable="false"
						>
							<path d="M12 20.5 4.2 12.9a4.8 4.8 0 0 1 6.8-6.8l1 1 1-1a4.8 4.8 0 0 1 6.8 6.8z" />
						</svg>
					{/if}
				{/snippet}
				{wish ? 'Do kolekcji' : 'Na listę życzeń'}
			</Button>
			<Button tone="ghost" type="button" disabled={saving} onclick={onskip}>{skipLabel}</Button>
		</div>
	</div>
</form>

<style>
	.confirm {
		color: var(--sui-text-primary);
		display: grid;
		font-family: var(--sui-sans);
		gap: 0.875rem;
		min-inline-size: 0;
	}

	/* The mockup's segments share the sheet's width, with the pick on a raised surface. */
	.confirm :global(.seg) {
		--sui-local-control-height: 2.75rem;
		--sui-segment-thumb: var(--sui-surface-base);
		--sui-segment-thumb-ink: var(--sui-text-primary);

		display: grid;
		grid-template-columns: minmax(0, 1fr);
	}

	.confirm :global(.seg .sui-segmented-track) {
		inline-size: 100%;
	}

	.confirm :global(.seg .sui-segmented-track .sui-segmented-option) {
		flex: 1 1 0;
		justify-content: center;
	}

	.purchase {
		border: 0;
		display: grid;
		gap: 0.75rem;
		margin: 0;
		min-inline-size: 0;
		padding: 0;
	}

	.caption {
		color: var(--sui-text-secondary);
		font-size: 0.8125rem;
		font-weight: 700;
		padding: 0;
	}

	.row {
		display: grid;
		gap: 0.625rem;
		grid-template-columns: minmax(0, 1.3fr) minmax(0, 1fr);
	}

	.row.with-today {
		grid-template-columns: minmax(0, 1.3fr) minmax(0, 1fr) auto;
	}

	.row.wish {
		grid-template-columns: minmax(0, 1fr);
	}

	.box {
		background: var(--sui-surface-inset);
		border-radius: 0.875rem;
		box-sizing: border-box;
		display: flex;
		flex-direction: column;
		justify-content: center;
		min-block-size: 3.375rem;
		min-inline-size: 0;
		padding: 0.375rem 0.875rem;
		position: relative;
	}

	.box:focus-within {
		background: var(--sui-surface-base);
		box-shadow: 0 0 0 2px var(--sui-brand-action);
	}

	.box.invalid {
		box-shadow: 0 0 0 2px var(--sui-danger);
	}

	.box label {
		color: var(--sui-text-secondary);
		font-size: 0.6875rem;
		font-weight: 700;
		letter-spacing: 0.02em;
	}

	.box input {
		-webkit-appearance: none;
		appearance: none;
		background: transparent;
		border: 0;
		box-sizing: border-box;
		color: var(--sui-text-primary);
		font: 700 1.125rem / 1.3 var(--sui-sans);
		font-variant-numeric: tabular-nums;
		inline-size: 100%;
		margin: 0;
		min-block-size: 1.5rem;
		min-inline-size: 0;
		outline: none;
		padding: 0;
	}

	.box input::placeholder {
		color: var(--sui-text-secondary);
		font-weight: 600;
	}

	.value {
		align-items: baseline;
		display: flex;
		gap: 0.25rem;
	}

	.unit {
		color: var(--sui-text-secondary);
		font-weight: 700;
	}

	/* The date field draws "Dziś" for today and "Brak" when empty over the native date,
	   which shows again while the field has focus. */
	.date .shown {
		display: none;
		font-weight: 700;
		inset-block-end: 0.5rem;
		inset-inline: 0.875rem 2.25rem;
		overflow: hidden;
		pointer-events: none;
		position: absolute;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.date.today:not(:focus-within) .shown,
	.date.empty:not(:focus-within) .shown {
		display: block;
	}

	.date.empty .shown {
		color: var(--sui-text-secondary);
		font-size: 0.9375rem;
		font-weight: 600;
	}

	.date.today .shown {
		font-size: 1.125rem;
	}

	.date.today:not(:focus-within) input,
	.date.empty:not(:focus-within) input {
		color: transparent;
	}

	.date input::-webkit-calendar-picker-indicator {
		opacity: 0.6;
	}

	.error {
		color: var(--sui-danger);
		font-size: 0.8125rem;
		font-weight: 600;
		margin: 0;
	}

	.shops {
		display: grid;
		gap: 0.375rem;
		min-inline-size: 0;
	}

	.chips,
	.toggles {
		display: flex;
		gap: 0.5rem;
		margin-inline: calc(-1 * var(--scan-gutter, 0px));
		overflow-x: auto;
		padding-block: 2px;
		padding-inline: var(--scan-gutter, 0px);
		scrollbar-width: none;
	}

	.chip {
		align-items: center;
		background: var(--sui-surface-base);
		border: 1px solid var(--sui-border-subtle);
		border-radius: 1.375rem;
		box-sizing: border-box;
		color: var(--sui-text-primary);
		cursor: pointer;
		display: inline-flex;
		flex: none;
		font: 600 0.875rem / 1 var(--sui-sans);
		gap: 0.375rem;
		justify-content: center;
		min-block-size: 2.75rem;
		min-inline-size: 2.75rem;
		padding: 0 0.875rem;
		-webkit-tap-highlight-color: transparent;
		touch-action: manipulation;
		white-space: nowrap;
	}

	.row > .chip {
		align-self: center;
	}

	.chip[aria-pressed='true'] {
		background: var(--sui-brand-action-tint);
		border-color: transparent;
		color: var(--sui-brand-action-text);
	}

	.chip.add {
		padding: 0;
	}

	.toggle .tick {
		display: none;
	}

	.toggle[aria-pressed='true'] .tick {
		display: block;
	}

	.chip:focus-visible {
		outline: var(--sui-focus-ring-width, 2px) solid var(--sui-focus);
		outline-offset: 2px;
	}

	@media (hover: hover) {
		.chip:hover:not([aria-pressed='true']) {
			border-color: var(--sui-text-secondary);
		}
	}

	.actions {
		display: grid;
		gap: 0.25rem;
		margin-block-start: 0.25rem;
	}

	.actions :global(.primary) {
		--sui-local-button-seat: 3.375rem;

		border-radius: 1.0625rem;
		font-size: 1rem;
		font-weight: 800;
		inline-size: 100%;
		max-inline-size: none;
	}

	.quiet {
		display: grid;
		grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
	}

	.quiet :global(.sui-button) {
		inline-size: 100%;
		max-inline-size: none;
	}

	@media (forced-colors: active) {
		.chip[aria-pressed='true'] {
			border: 2px solid Highlight;
		}
	}
</style>
