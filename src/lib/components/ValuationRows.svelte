<script lang="ts">
	import {
		copyGain,
		copyMeta,
		copyNote,
		discogsRelease,
		likelySoldLine,
		missingCeiling,
		wholeZl,
		type CopyValue
	} from '#lib/valuation.js';
	import { seasonName } from '#lib/collection.js';
	import CollectionCover from './CollectionCover.svelte';
	import DiscogsCredit from './DiscogsCredit.svelte';

	let { copies, minObservations }: { copies: readonly CopyValue[]; minObservations: number } =
		$props();
</script>

<ul class="list" aria-label="Wartość egzemplarzy">
	{#each copies as c (c.copy_id)}
		<li>
			<a href={`/copies/${c.copy_id}`}>
				<CollectionCover kind={c.kind} url={c.cover_url} />
				<span class="details">
					<span class="title">
						{seasonName(c.title_name, c.season)}
						{#if c.low_confidence}
							<span class="low">mało danych</span>
						{/if}
					</span>
					<span class="meta">{copyMeta(c)}</span>
					{#if likelySoldLine(c, minObservations)}
						<span class="meta">{likelySoldLine(c, minObservations)}</span>
					{/if}
				</span>
				<span class="value">
					{c.mid ? wholeZl(c.mid.cents) : '—'}
					<small><span class="visually-hidden">Względem zakupu: </span>{copyGain(c)}</small>
					<small>{copyNote(c)}</small>
					{#if missingCeiling(c)}
						<small>{missingCeiling(c)}</small>
					{/if}
				</span>
			</a>
			<DiscogsCredit class="row-credit" release={discogsRelease(c)} />
		</li>
	{/each}
</ul>

<style>
	.list {
		margin: 0;
		padding: 0;
		overflow: hidden;
		border: 1px solid var(--sui-border-subtle);
		border-radius: 1.375rem;
		background: var(--sui-surface-base);
		list-style: none;
	}

	li + li {
		border-block-start: 1px solid var(--sui-border-subtle);
	}

	a {
		--collection-cover-width: 2.375rem;

		display: flex;
		align-items: center;
		gap: 0.75rem;
		min-block-size: 2.75rem;
		padding: 0.625rem 0.875rem;
		color: inherit;
		text-decoration: none;
	}

	a:hover {
		background: var(--sui-interactive-hover-layer, transparent);
	}

	a:focus-visible {
		outline: 2px solid var(--sui-focus);
		outline-offset: -2px;
	}

	a > :global(.cover) {
		flex: none;
	}

	.details {
		display: grid;
		gap: 0.0625rem;
		min-inline-size: 0;
	}

	.title {
		font-size: 0.875rem;
		font-weight: 700;
		overflow-wrap: anywhere;
	}

	.low {
		display: inline-block;
		margin-inline-start: 0.25rem;
		padding: 0.125rem 0.4375rem;
		border-radius: 0.5rem;
		background: var(--sui-brand-action-tint);
		color: var(--sui-brand-action-text);
		font-size: 0.6875rem;
		font-weight: 700;
		white-space: nowrap;
	}

	/* Under the copy's details: past the row's padding, the cover and the gap. */
	li > :global(.row-credit) {
		padding: 0 0.875rem 0.625rem calc(0.875rem + 2.375rem + 0.75rem);
	}

	.meta {
		color: var(--sui-text-muted);
		font-size: 0.75rem;
		font-variant-numeric: tabular-nums;
	}

	.value {
		display: grid;
		margin-inline-start: auto;
		flex: none;
		font-size: 0.9375rem;
		font-weight: 800;
		font-variant-numeric: tabular-nums;
		text-align: end;
		white-space: nowrap;
	}

	.visually-hidden {
		position: absolute;
		inline-size: 1px;
		block-size: 1px;
		overflow: hidden;
		clip-path: inset(50%);
		white-space: nowrap;
	}

	.value small {
		color: var(--sui-text-muted);
		font-size: 0.6875rem;
		font-weight: 600;
	}
</style>
