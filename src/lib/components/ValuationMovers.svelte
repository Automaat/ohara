<script lang="ts">
	import { copyMeta, dayLabel, type Mover } from '#lib/valuation.js';
	import CollectionCover from './CollectionCover.svelte';

	let { rising, falling, from }: { rising: Mover[]; falling: Mover[]; from: string } = $props();
</script>

{#snippet group(id: string, label: string, list: Mover[], tone: 'up' | 'down')}
	{#if list.length > 0}
		<div class="group">
			<h3 {id}>{label}</h3>
			<ul class="list" aria-labelledby={id}>
				{#each list as m (m.copy.copy_id)}
					<li>
						<a href={`/copies/${m.copy.copy_id}`}>
							<CollectionCover kind={m.copy.kind} url={m.copy.cover_url} />
							<span class="details">
								<span class="title">{m.copy.title_name}</span>
								<span class="meta">{copyMeta(m.copy)}</span>
							</span>
							<span class="change {tone}">{m.change}</span>
						</a>
					</li>
				{/each}
			</ul>
		</div>
	{/if}
{/snippet}

<section class="movers" aria-labelledby="valuation-movers-title">
	<div class="head">
		<h2 id="valuation-movers-title">Największe zmiany</h2>
		<span class="span">Wartość używanych od {dayLabel(from)}</span>
	</div>
	<div class="groups">
		{@render group('valuation-movers-rising', 'Wzrosty', rising, 'up')}
		{@render group('valuation-movers-falling', 'Spadki', falling, 'down')}
	</div>
</section>

<style>
	.movers {
		display: grid;
		gap: 0.625rem;
	}

	.head {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.25rem 0.75rem;
	}

	h2 {
		margin: 0;
		font-size: 1rem;
	}

	h3 {
		margin: 0 0 0.375rem;
		color: var(--sui-text-muted);
		font-size: 0.75rem;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.span {
		color: var(--sui-text-muted);
		font-size: 0.75rem;
	}

	.groups {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(min(100%, 18rem), 1fr));
		gap: 0.75rem;
	}

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
		flex: 1 1 auto;
		gap: 0.0625rem;
		min-inline-size: 0;
	}

	.title {
		font-size: 0.875rem;
		font-weight: 700;
		overflow-wrap: anywhere;
	}

	.meta {
		color: var(--sui-text-muted);
		font-size: 0.75rem;
		font-variant-numeric: tabular-nums;
	}

	/* A long change wraps at its spaces, or anywhere, within half the row, never widening it. */
	.change {
		flex: 0 1 auto;
		min-inline-size: 0;
		max-inline-size: 50%;
		margin-inline-start: auto;
		font-size: 0.875rem;
		font-weight: 800;
		font-variant-numeric: tabular-nums;
		text-align: end;
		overflow-wrap: anywhere;
	}

	.up {
		color: var(--sui-success);
	}

	.down {
		color: var(--sui-danger);
	}
</style>
