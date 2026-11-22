<script lang="ts">
	import {
		BackLink,
		Button,
		ErrorState,
		Field,
		PageHeader,
		Section,
		Skeleton
	} from '@smykla-skalski/sui';
	import { page } from '$app/state';
	import { errorMessage, issueFrom, request } from '#lib/api.js';
	import {
		displayName,
		kindLabels,
		seriesSummary,
		seriesTotalPayload,
		volumeStateLabels,
		type FormIssue,
		type MissingVolume,
		type SeriesDetail
	} from '#lib/collection.js';
	import CollectionCover from '#lib/components/CollectionCover.svelte';

	const id = $derived(Number(page.params.id));

	let detail = $state<SeriesDetail | null>(null);
	let failure = $state<string | null>(null);
	let busy = $state(false);
	let total = $state('');
	let totalIssue = $state<FormIssue | null>(null);
	let savingTotal = $state(false);

	/** The series the page last asked for, so a late answer for another one is dropped. */
	let requested: number | undefined;

	async function load(seriesID: number) {
		requested = seriesID;
		busy = true;
		try {
			const loaded = await request<SeriesDetail>(fetch, 'GET', `/api/series/${seriesID}`);
			if (requested !== seriesID) return;
			show(loaded);
			failure = null;
		} catch (err) {
			if (requested === seriesID) failure = errorMessage(err);
		} finally {
			if (requested === seriesID) busy = false;
		}
	}

	function show(loaded: SeriesDetail) {
		detail = loaded;
		total = loaded.total_volumes?.toString() ?? '';
		totalIssue = null;
	}

	$effect(() => {
		detail = null;
		failure = null;
		void load(id);
	});

	async function saveTotal() {
		const parsed = seriesTotalPayload(total);
		if (!parsed.ok) {
			totalIssue = parsed.issue;
			return;
		}
		const seriesID = id;
		savingTotal = true;
		try {
			const saved = await request<SeriesDetail>(
				fetch,
				'PUT',
				`/api/series/${seriesID}`,
				parsed.value
			);
			if (requested === seriesID) show(saved);
		} catch (err) {
			if (requested === seriesID) totalIssue = issueFrom(err);
		} finally {
			savingTotal = false;
		}
	}

	/** How a missing number reads: the title that has it, when one does, and what it lacks. */
	function missingState(m: MissingVolume): string {
		return m.title_id == null ? 'Brak w kolekcji' : volumeStateLabels[m.state];
	}

	function volumeName(d: SeriesDetail, titleID: number): string | null {
		const v = d.volumes.find((volume) => volume.title_id === titleID);
		return v ? displayName({ ...v, original_title: null }) : null;
	}
</script>

<svelte:head>
	<title>{detail ? detail.name : 'Seria'} · Ohara</title>
</svelte:head>

<PageHeader
	title={detail?.name ?? 'Seria'}
	description={detail ? `${kindLabels[detail.kind]} · ${seriesSummary(detail)}` : undefined}
>
	{#snippet kicker()}
		<BackLink href="/series" label="Serie" />
	{/snippet}
</PageHeader>

<div class="content">
	{#if failure}
		<ErrorState
			title="Nie udało się wczytać serii"
			description={failure}
			onretry={() => load(id)}
			{busy}
			retryLabel="Spróbuj ponownie"
			retryingLabel="Wczytywanie…"
		/>
	{:else if !detail}
		<Skeleton rows={4} label="Wczytywanie serii" />
	{:else}
		{@const d = detail}
		<Section title="Tomy">
			<ul class="volumes" aria-label="Tomy">
				{#each d.volumes as v (v.title_id)}
					<li>
						<CollectionCover kind={d.kind} url={v.cover_url ?? undefined} />
						<div class="volume">
							<span class="number">{v.number == null ? 'Bez numeru' : `Tom ${v.number}`}</span>
							<a href={`/titles/${v.title_id}`}>{displayName({ ...v, original_title: null })}</a>
							<span class="state">{volumeStateLabels[v.state]}</span>
						</div>
					</li>
				{/each}
			</ul>
		</Section>

		<Section title="Brakujące tomy">
			{#if d.missing.length === 0}
				<p class="muted">Wszystkie tomy są na półce.</p>
			{:else}
				<ul class="missing" aria-label="Brakujące tomy">
					{#each d.missing as m (m.number)}
						<li>
							<span class="number">Tom {m.number}</span>
							{#if m.title_id != null}
								<a href={`/titles/${m.title_id}`}>{volumeName(d, m.title_id)}</a>
							{/if}
							<span class="state">{missingState(m)}</span>
						</li>
					{/each}
				</ul>
			{/if}
		</Section>

		<form
			class="total"
			novalidate
			onsubmit={(e) => {
				e.preventDefault();
				void saveTotal();
			}}
		>
			<Field
				label="Liczba tomów"
				hint="Ile tomów ma cała seria. Puste, gdy nie wiesz."
				inputmode="numeric"
				bind:value={total}
				error={totalIssue?.message}
			/>
			<Button type="submit" loading={savingTotal}>Zapisz</Button>
		</form>
	{/if}
</div>

<style>
	.content {
		display: grid;
		gap: 1.5rem;
		padding: var(--sui-pane-gutter, 1.5rem);
		font-family: var(--sui-sans);
		color: var(--sui-text-primary);
		max-width: 48rem;
	}
	.volumes,
	.missing {
		display: grid;
		gap: 0.75rem;
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.volumes li {
		display: grid;
		grid-template-columns: 3rem minmax(0, 1fr);
		gap: 0.75rem;
		align-items: center;
	}
	.volume {
		display: grid;
		gap: 0.125rem;
		min-width: 0;
	}
	.missing li {
		display: flex;
		flex-wrap: wrap;
		gap: 0.25rem 0.75rem;
		align-items: baseline;
	}
	.number {
		font-weight: 600;
	}
	a {
		color: var(--sui-link);
		overflow-wrap: anywhere;
	}
	a:focus-visible {
		outline: 2px solid var(--sui-focus);
		outline-offset: 2px;
	}
	.state,
	.muted {
		margin: 0;
		color: var(--sui-text-secondary);
		font-size: 0.875rem;
	}
	.total {
		display: grid;
		grid-template-columns: minmax(0, 12rem) auto;
		gap: 0.75rem;
		align-items: end;
	}
</style>
