<script lang="ts">
	import { BackLink, Button, EmptyState, ErrorState, PageHeader } from '@smykla-skalski/sui';
	import { onMount } from 'svelte';
	import { errorMessage, request } from '#lib/api.js';
	import { isoDate, type CollectionView } from '#lib/collection.js';
	import CollectionCover from '#lib/components/CollectionCover.svelte';
	import {
		boundCell,
		coverage,
		paidCell,
		reportDay,
		reportRows,
		reportTitle,
		reportTotals,
		totalCell
	} from '#lib/insurance-report.js';
	import type { ValuationView } from '#lib/valuation.js';

	type Report = { view: ValuationView; items: CollectionView['items'] };

	let report = $state<Report | null>(null);
	let failure = $state<string | null>(null);
	let loading = $state(true);

	/** Reads the values and, for their condition, creators and binding, the owned copies. */
	async function load() {
		loading = true;
		try {
			const [view, collection] = await Promise.all([
				request<ValuationView>(fetch, 'GET', '/api/valuation'),
				request<CollectionView>(fetch, 'GET', '/api/collection?status=owned')
			]);
			report = { view, items: collection.items };
			failure = null;
		} catch (err) {
			failure = errorMessage(err);
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		void load();
	});

	const rows = $derived(report ? reportRows(report.view.copies, report.items) : []);
	const totals = $derived(reportTotals(report?.view.copies ?? []));
	const lastValuation = $derived(report?.view.last_valuation ?? null);
	const today = isoDate(new Date());
	const description = $derived(
		lastValuation
			? `Wycena z dnia ${reportDay(lastValuation)}. Sporządzono ${reportDay(today)}.`
			: undefined
	);
</script>

<svelte:head>
	<title>{reportTitle} · Ohara</title>
</svelte:head>

{#snippet printButton()}
	<Button tone="signal" onclick={() => window.print()}>Drukuj</Button>
{/snippet}

<div class="report">
	<div class="back">
		<BackLink href="/valuation" label="Wartość" />
	</div>
	<PageHeader
		title={reportTitle}
		{description}
		actions={lastValuation && rows.length > 0 ? printButton : undefined}
	/>

	<div class="content">
		{#if !report}
			{#if failure}
				<ErrorState
					title="Nie udało się wczytać raportu"
					description={failure}
					onretry={load}
					busy={loading}
					retryLabel="Spróbuj ponownie"
					retryingLabel="Ponawianie…"
				/>
			{:else}
				<p role="status">Wczytywanie raportu…</p>
			{/if}
		{:else if rows.length === 0}
			<EmptyState
				title="Brak posiadanych egzemplarzy"
				description="Raport wymienia egzemplarze z kolekcji, a żaden nie jest teraz na półce."
			>
				{#snippet action()}
					<Button href="/">Przejdź do kolekcji</Button>
				{/snippet}
			</EmptyState>
		{:else if !lastValuation}
			<EmptyState
				title="Kolekcja nie ma jeszcze wyceny"
				description="Raport podaje wartość egzemplarzy z wyceny, a żadna jeszcze się nie odbyła."
			>
				{#snippet action()}
					<Button href="/valuation">Przejdź do wyceny</Button>
				{/snippet}
			</EmptyState>
		{:else}
			<table class="copies" aria-label="Egzemplarze w raporcie">
				<thead>
					<tr>
						<th scope="col"><span class="visually-hidden">Okładka</span></th>
						<th scope="col">Tytuł</th>
						<th scope="col">Twórcy</th>
						<th scope="col">Wydanie</th>
						<th scope="col">Kod kreskowy</th>
						<th scope="col">Stan</th>
						<th scope="col" class="num">Zapłacono</th>
						<th scope="col" class="num">Wartość (używane)</th>
						<th scope="col" class="num">Wartość (nowe)</th>
					</tr>
				</thead>
				<tbody>
					{#each rows as row (row.copy.copy_id)}
						<tr>
							<td class="cover-cell">
								<CollectionCover kind={row.copy.kind} url={row.copy.cover_url} eager />
							</td>
							<th scope="row" class="title">{row.copy.title_name}</th>
							<td data-label="Twórcy" class="creators">{row.creators || '—'}</td>
							<td data-label="Wydanie">{row.edition}</td>
							<td data-label="Kod kreskowy" class="code">{row.copy.barcode ?? '—'}</td>
							<td data-label="Stan">{row.condition || '—'}</td>
							<td data-label="Zapłacono" class="num">{paidCell(row.copy.price_cents)}</td>
							<td data-label="Wartość (używane)" class="num">{boundCell(row.copy.mid)}</td>
							<td data-label="Wartość (nowe)" class="num">{boundCell(row.copy.ceiling)}</td>
						</tr>
					{/each}
				</tbody>
			</table>

			<section class="totals" aria-labelledby="report-totals">
				<h2 id="report-totals">Podsumowanie</h2>
				<dl>
					<div>
						<dt>Egzemplarze</dt>
						<dd>{totals.copies}</dd>
					</div>
					<div>
						<dt>Zapłacono</dt>
						<dd>
							{totalCell(totals.paid, '—')}
							<span class="covers">{coverage(totals.paid, totals.copies)}</span>
						</dd>
					</div>
					<div>
						<dt>Wartość (używane)</dt>
						<dd>
							{totalCell(totals.mid)}
							<span class="covers">{coverage(totals.mid, totals.copies)}</span>
						</dd>
					</div>
					<div>
						<dt>Wartość (nowe)</dt>
						<dd>
							{totalCell(totals.ceiling)}
							<span class="covers">{coverage(totals.ceiling, totals.copies)}</span>
						</dd>
					</div>
					<div>
						<dt>Data wyceny</dt>
						<dd>{reportDay(lastValuation)}</dd>
					</div>
				</dl>
				<p class="note">
					Wartość używanych egzemplarzy to mediana cen ofertowych z OLX, a dla płyt powiązanych z
					Discogs sugerowana cena dla stanu egzemplarza. Wartość nowych to sugerowana cena Discogs
					dla stanu Mint. Ceny ofertowe nie są cenami transakcji.
				</p>
			</section>
		{/if}
	</div>
</div>

<style>
	.content {
		display: grid;
		gap: 1rem;
		padding: var(--sui-pane-gutter, 1.5rem);
	}

	.back {
		padding: var(--sui-pane-gutter, 1.5rem) var(--sui-pane-gutter, 1.5rem) 0;
	}

	.copies {
		inline-size: 100%;
		border-collapse: collapse;
		font-size: 0.8125rem;
		font-variant-numeric: tabular-nums;
	}

	.copies th,
	.copies td {
		padding: 0.375rem 0.5rem;
		border-block-end: 1px solid var(--sui-border-subtle);
		text-align: start;
		vertical-align: middle;
	}

	.copies thead th {
		color: var(--sui-text-secondary);
		font-size: 0.75rem;
		font-weight: 700;
	}

	.copies .title {
		min-inline-size: 8rem;
		font-weight: 700;
		overflow-wrap: break-word;
	}

	.copies .num {
		text-align: end;
		white-space: nowrap;
	}

	.code {
		overflow-wrap: break-word;
	}

	.creators {
		min-inline-size: 6rem;
	}

	.cover-cell {
		--collection-cover-width: 2.5rem;

		inline-size: 2.5rem;
	}

	.visually-hidden {
		position: absolute;
		inline-size: 1px;
		block-size: 1px;
		overflow: hidden;
		clip-path: inset(50%);
		white-space: nowrap;
	}

	.totals h2 {
		margin: 0 0 0.5rem;
		font-size: 1rem;
	}

	.totals dl {
		display: grid;
		gap: 0.375rem;
		margin: 0;
	}

	.totals dl > div {
		display: flex;
		flex-wrap: wrap;
		gap: 0.25rem 1rem;
		justify-content: space-between;
	}

	.totals dt {
		font-weight: 700;
	}

	.totals dd {
		margin: 0;
		font-variant-numeric: tabular-nums;
		text-align: end;
	}

	.covers {
		display: block;
		color: var(--sui-text-muted);
		font-size: 0.75rem;
	}

	.note {
		margin: 0.75rem 0 0;
		color: var(--sui-text-muted);
		font-size: 0.75rem;
	}

	/* On a phone each copy is a card: its cover and title, then the other cells labelled. */
	@media screen and (max-width: 47.9375rem) {
		.copies thead {
			position: absolute;
			inline-size: 1px;
			block-size: 1px;
			overflow: hidden;
			clip-path: inset(50%);
		}

		.copies,
		.copies tbody {
			display: block;
		}

		.copies tr {
			display: grid;
			grid-template-columns: 2.5rem minmax(0, 1fr);
			gap: 0.125rem 0.75rem;
			padding: 0.625rem 0;
			border-block-end: 1px solid var(--sui-border-subtle);
		}

		.copies tbody th,
		.copies tbody td {
			display: block;
			padding: 0;
			border: 0;
		}

		.copies .cover-cell {
			grid-row: span 8;
		}

		.copies td[data-label] {
			display: flex;
			gap: 0.5rem;
			justify-content: space-between;
			text-align: end;
		}

		.copies td[data-label]::before {
			content: attr(data-label);
			color: var(--sui-text-muted);
			text-align: start;
		}
	}

	/* Printed: the report alone, on white, every row whole and the header on every page. */
	@media print {
		:global(:root:has(.report)) {
			background: #fff;
		}

		:global(:root:has(.report) body),
		:global(
			:root:has(.report)
				:is(.sui-shell, .sui-shell-grid, .sui-shell-pane, .ohara-page, .sui-page-header)
		) {
			background: #fff !important;
			color: #000;
		}

		:global(:root:has(.report) .sui-shell-bar),
		:global(:root:has(.report) .sui-shell-side),
		:global(:root:has(.report) .sui-shell-places),
		:global(:root:has(.report) .tab-bar),
		:global(:root:has(.report) .sui-service-worker-status),
		.back,
		.report :global(.sui-page-header-actions),
		.report :global(button),
		.report :global(.sui-button) {
			display: none !important;
		}

		:global(:root:has(.report) .sui-shell-grid) {
			display: block;
		}

		.content {
			display: block;
			padding: 0;
		}

		.totals {
			margin-block-start: 1.5rem;
			break-inside: avoid;
		}

		.copies {
			font-size: 9pt;
		}

		.code {
			white-space: nowrap;
		}

		.copies thead {
			display: table-header-group;
		}

		.copies tr {
			break-inside: avoid;
		}

		.copies th,
		.copies td {
			border-color: #999;
			color: #000;
		}

		.covers,
		.note {
			color: #333;
		}
	}

	@page {
		size: A4;
		margin: 12mm;
	}
</style>
