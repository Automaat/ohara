<script lang="ts">
	import { Button, Icon, Pill, RelativeTime, VisuallyHidden } from '@smykla-skalski/sui';
	import { acquiredLabels } from '#lib/collection.js';
	import {
		alertLabels,
		dealPrice,
		isUnderLimit,
		limitComparison,
		sourceLabels,
		wishLimit,
		type Observation
	} from '#lib/deals.js';

	let { deal, ondismiss }: { deal: Observation; ondismiss?: () => void } = $props();
	let failed = $state<string[]>([]);

	const image = $derived(
		[deal.listing_photo_url, deal.cover_url].find((url) => url && !failed.includes(url))
	);
	const under = $derived(isUnderLimit(deal));
	const wishHref = $derived(
		deal.edition_id
			? `/editions/${deal.edition_id}`
			: deal.title_id
				? `/titles/${deal.title_id}`
				: null
	);
</script>

<article class="deal">
	<div class="top">
		<span class="photo">
			<Icon name="file-missing" />
			{#if image}
				{#key image}
					<img
						src={image}
						alt=""
						loading="lazy"
						decoding="async"
						referrerpolicy="no-referrer"
						onerror={() => (failed = [...failed, image])}
					/>
				{/key}
			{/if}
		</span>
		<div class="body">
			<h2 class="name">{deal.listing_title || 'Oferta bez tytułu'}</h2>
			<p class="meta">
				{sourceLabels[deal.source]}{deal.acquired_as
					? ` · ${acquiredLabels[deal.acquired_as].toLowerCase()}`
					: ''} · <RelativeTime value={deal.observed_at} locale="pl" />
			</p>
			<p class="price">
				<span class="amount">{dealPrice(deal.price_cents)}</span>
				<Pill tone={under ? 'success' : 'neutral'}>
					{limitComparison(deal.price_cents, deal.max_price_cents)}
				</Pill>
			</p>
		</div>
	</div>
	<div class="foot">
		<span class="facts">
			<span class="wish">
				{#if wishHref}
					<a href={wishHref}>{deal.title_name ?? 'Tytuł'}</a>
				{:else}
					Usunięty tytuł
				{/if}
				· {wishLimit(deal.max_price_cents)}
			</span>
			<span
				class="alert"
				class:sent={deal.alert === 'sent'}
				class:failed={deal.alert === 'failed'}
				title={deal.alert_error || undefined}
			>
				{#if deal.alert === 'sent'}
					<Icon name="check" />
				{/if}
				{alertLabels[deal.alert]}
			</span>
		</span>
		<span class="actions">
			{#if ondismiss}
				<Button tone="ghost" data-dismiss={deal.id} onclick={ondismiss}>
					Ignoruj<VisuallyHidden>
						ofertę {deal.listing_title || deal.url}
					</VisuallyHidden>
				</Button>
			{/if}
			<span class="open">
				<Button href={deal.url} target="_blank" rel="noopener noreferrer" tone="signal">
					Otwórz<VisuallyHidden>
						ofertę {deal.listing_title || deal.url} w nowej karcie
					</VisuallyHidden>
					{#snippet trailing()}
						<Icon name="arrow-up-right" />
					{/snippet}
				</Button>
			</span>
		</span>
	</div>
</article>

<style>
	.deal {
		display: grid;
		gap: 0.625rem;
		padding: 0.75rem;
		border: 1px solid var(--sui-border-subtle);
		border-radius: 1.375rem;
		background: var(--sui-surface-base);
	}

	.top {
		display: flex;
		gap: 0.75rem;
	}

	.photo {
		position: relative;
		display: grid;
		place-items: center;
		flex: none;
		inline-size: 5.75rem;
		aspect-ratio: 1;
		overflow: hidden;
		border-radius: 0.875rem;
		background: var(--sui-surface-inset);
		color: var(--sui-text-muted);
	}

	.photo img {
		position: absolute;
		inset: 0.5rem;
		inline-size: calc(100% - 1rem);
		block-size: calc(100% - 1rem);
		object-fit: contain;
	}

	.body {
		display: grid;
		align-content: start;
		gap: 0.1875rem;
		min-inline-size: 0;
	}

	.name {
		margin: 0;
		font-size: 0.875rem;
		font-weight: 700;
		line-height: 1.3;
		overflow-wrap: anywhere;
	}

	.meta {
		margin: 0;
		color: var(--sui-text-muted);
		font-size: 0.75rem;
	}

	.price {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem;
		margin: 0.3125rem 0 0;
	}

	.amount {
		font-family: var(--ohara-serif);
		font-size: 1.5rem;
		font-weight: 600;
		font-variant-numeric: tabular-nums;
		line-height: 1.2;
	}

	.foot {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.625rem;
		padding-block-start: 0.625rem;
		border-block-start: 1px solid var(--sui-border-subtle);
		color: var(--sui-text-muted);
		font-size: 0.75rem;
		font-variant-numeric: tabular-nums;
	}

	.facts {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.25rem 0.625rem;
		min-inline-size: 0;
	}

	.wish {
		min-inline-size: 0;
		overflow-wrap: anywhere;
	}

	.wish a {
		color: inherit;
	}

	.alert {
		display: inline-flex;
		align-items: center;
		gap: 0.25rem;
		font-weight: 700;
	}

	.alert.sent {
		color: var(--sui-success);
	}

	.alert.failed {
		color: var(--sui-danger);
	}

	/* The mockup's "Otwórz" is bottle green in light and powder pink in dark: the selected
	   chip's colours, lent to the signal tone inside this card only. */
	.open {
		--sui-brand-action: var(--ohara-selected);
		--sui-brand-action-hover: var(--ohara-selected-hover);
		--sui-brand-action-pressed: var(--ohara-selected-pressed);
		--sui-on-brand-action: var(--ohara-on-selected);
	}

	.actions {
		display: flex;
		flex: none;
		gap: 0.5rem;
		margin-inline-start: auto;
	}
</style>
