<script lang="ts">
	import { BackLink, Button, EmptyState, ErrorState, PageHeader } from '@smykla-skalski/sui';
	import { onMount, tick } from 'svelte';
	import { errorMessage, request } from '#lib/api.js';
	import { isoDate } from '#lib/collection.js';
	import CollectionCover from '#lib/components/CollectionCover.svelte';
	import DiscogsCredit from '#lib/components/DiscogsCredit.svelte';
	import {
		boundCell,
		coverage,
		imagesSettled,
		midGrade,
		paidCell,
		reportDating,
		reportDay,
		reportRows,
		reportTitle,
		reportTotals,
		totalCell
	} from '#lib/insurance-report.js';
	import { discogsRelease, type ValuationView } from '#lib/valuation.js';

	let view = $state<ValuationView | null>(null);
	let failure = $state<string | null>(null);
	let loading = $state(true);
	let table = $state<HTMLTableElement>();
	let coversReady = $state(false);

	/** Reads the owned copies, their values and their details from one snapshot. */
	async function load() {
		loading = true;
		try {
			view = await request<ValuationView>(fetch, 'GET', '/api/valuation');
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

	const rows = $derived(view ? reportRows(view.copies) : []);
	const totals = $derived(reportTotals(view?.copies ?? []));
	const lastValuation = $derived(view?.last_valuation ?? null);
	const today = isoDate(new Date());
	const description = $derived(
		view && lastValuation ? reportDating(lastValuation, today, view.window_days) : undefined
	);
	/** The rows whose title or creators were cut, numbered for the appendix that lists them whole. */
	const appendix = $derived(rows.filter((r) => r.fullTitle !== null || r.fullCreators !== null));

	/**
	 * Marks the covers ready once every one in root has loaded or failed, so printing never misses
	 * a cover still on its way. Returns the cleanup that drops a check a newer one replaced.
	 */
	function watchCovers(root: HTMLTableElement | undefined): () => void {
		coversReady = false;
		let current = true;
		if (root) {
			void tick()
				.then(() => imagesSettled(root.querySelectorAll('img')))
				.then(() => {
					if (current) coversReady = true;
				});
		}
		return () => {
			current = false;
		};
	}

	$effect(() => {
		void rows;
		return watchCovers(table);
	});
</script>

<svelte:head>
	<title>{reportTitle} · Ohara</title>
</svelte:head>

{#snippet printButton()}
	<Button tone="signal" loading={!coversReady} onclick={() => window.print()}>
		{coversReady ? 'Drukuj' : 'Wczytywanie okładek…'}
	</Button>
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
		{#if !view}
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
			<table class="copies" aria-label="Egzemplarze w raporcie" bind:this={table}>
				<thead>
					<tr>
						<th scope="col" class="cover-col"><span class="visually-hidden">Okładka</span></th>
						<th scope="col" class="title-col">Tytuł</th>
						<th scope="col" class="creators-col">Twórcy</th>
						<th scope="col" class="edition-col">Wydanie</th>
						<th scope="col" class="code-col">Kod kreskowy</th>
						<th scope="col" class="condition-col">Stan</th>
						<th scope="col" class="num money-col">Zapłacono</th>
						<th scope="col" class="num money-col">Wartość (używane)</th>
						<th scope="col" class="num money-col">Wartość (nowe)</th>
					</tr>
				</thead>
				<tbody>
					{#each rows as row (row.copy.copy_id)}
						{@const note = appendix.indexOf(row) + 1}
						{@const grade = midGrade(row.copy)}
						<tr>
							<td class="cover-cell">
								<CollectionCover kind={row.copy.kind} url={row.copy.cover_url} large eager />
							</td>
							<th scope="row" class="title">
								{row.title}
								{#if note > 0}
									<span class="note-ref">(pełne dane: przypis {note})</span>
								{/if}
							</th>
							<td data-label="Twórcy" class="creators">{row.creators || '—'}</td>
							<td data-label="Wydanie">{row.edition}</td>
							<td data-label="Kod kreskowy" class="code">{row.copy.barcode ?? '—'}</td>
							<td data-label="Stan">{row.condition || '—'}</td>
							<td data-label="Zapłacono" class="num">{paidCell(row.copy.price_cents)}</td>
							<td data-label="Wartość (używane)" class="num">
								{boundCell(row.copy.mid)}
								{#if grade}
									<span class="grade">ocena {grade}</span>
								{/if}
							</td>
							<td data-label="Wartość (nowe)" class="num">
								{boundCell(row.copy.ceiling)}
								<DiscogsCredit class="credit" release={discogsRelease(row.copy)} />
							</td>
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
						<dt>Ostatnia wycena</dt>
						<dd>{reportDay(lastValuation)}</dd>
					</div>
					<div>
						<dt>Kwoty według cen na dzień</dt>
						<dd>{reportDay(today)}</dd>
					</div>
				</dl>
				<p class="note">
					Wartość używanych egzemplarzy to mediana cen ofertowych z OLX z ostatnich {view.window_days}
					dni, a dla płyt powiązanych z Discogs sugerowana cena Discogs dla oceny podanej przy kwocie:
					stanu egzemplarza, a gdy Discogs nie podaje ceny dla tego stanu lub egzemplarz nie ma stanu,
					VG+. Wartość nowych to sugerowana cena Discogs dla stanu Mint. Ceny ofertowe nie są cenami transakcji.
				</p>
			</section>

			{#if appendix.length > 0}
				<section class="appendix" aria-labelledby="report-appendix">
					<h2 id="report-appendix">Przypisy: pełne tytuły i listy twórców</h2>
					<ol>
						{#each appendix as row (row.copy.copy_id)}
							<li>
								<strong>{row.copy.title_name}</strong>
								{#if row.fullCreators !== null}
									<span class="full-creators">Twórcy: {row.fullCreators}</span>
								{/if}
							</li>
						{/each}
					</ol>
				</section>
			{/if}
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
		overflow-wrap: anywhere;
	}

	.full-creators {
		display: block;
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
		overflow-wrap: anywhere;
	}

	.note-ref,
	.grade {
		display: block;
		font-weight: 400;
		color: var(--sui-text-muted);
		font-size: 0.6875rem;
	}

	.copies :global(.credit) {
		margin: 0.125rem 0 0;
		font-size: 0.6875rem;
		white-space: normal;
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

	.totals h2,
	.appendix h2 {
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

	.appendix ol {
		display: grid;
		gap: 0.375rem;
		margin: 0;
		padding-inline-start: 1.5rem;
		font-size: 0.75rem;
		overflow-wrap: anywhere;
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
			flex-wrap: wrap;
			gap: 0 0.5rem;
			justify-content: space-between;
			text-align: end;
		}

		.copies td[data-label]::before {
			content: attr(data-label);
			color: var(--sui-text-muted);
			text-align: start;
		}

		.note-ref,
		.grade,
		.copies :global(.credit) {
			flex-basis: 100%;
		}
	}

	/* Printed: the report alone on its own A4 page type, on white, every row whole and the header
	   on every page. The columns share the page's width, so no text pushes the amounts off it. */
	@media print {
		.report {
			page: insurance-report;
		}

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

		.copies {
			table-layout: fixed;
			font-size: 8pt;
			line-height: 1.3;
		}

		.copies th,
		.copies td {
			padding: 1.5mm 1mm;
			border-color: #999;
			color: #000;
		}

		.copies thead th {
			font-size: 7pt;
		}

		.copies tbody th,
		.copies tbody td {
			overflow-wrap: anywhere;
		}

		.copies tbody .code {
			overflow-wrap: normal;
			white-space: nowrap;
		}

		.copies .title,
		.creators {
			min-inline-size: 0;
		}

		.copies .num {
			white-space: normal;
		}

		.cover-col {
			inline-size: 12mm;
		}

		.title-col {
			inline-size: 20%;
		}

		.creators-col {
			inline-size: 16%;
		}

		.edition-col {
			inline-size: 10%;
		}

		.code-col {
			inline-size: 15%;
		}

		.condition-col {
			inline-size: 8%;
		}

		.money-col {
			inline-size: 10%;
		}

		.copies thead {
			display: table-header-group;
		}

		.copies tr {
			break-inside: avoid;
		}

		.totals {
			margin-block-start: 1.5rem;
			break-inside: avoid;
		}

		.appendix {
			margin-block-start: 1.5rem;
		}

		.covers,
		.note,
		.note-ref,
		.grade {
			color: #333;
		}
	}

	@page insurance-report {
		size: A4;
		margin: 12mm;
	}
</style>
