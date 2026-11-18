<script lang="ts">
	import {
		BackLink,
		Button,
		EmptyState,
		ErrorState,
		ObjectList,
		ObjectRow,
		PageHeader,
		Pill,
		RelativeTime,
		Skeleton
	} from '@smykla-skalski/sui';
	import { onMount } from 'svelte';
	import { errorMessage, request } from '$lib/api';
	import { reasonLabels, type QueueEntry } from '$lib/scan';

	let entries = $state<QueueEntry[] | null>(null);
	let failure = $state<string | null>(null);
	let removing = $state<number | null>(null);
	let removeError = $state<string | null>(null);

	async function load() {
		try {
			entries = await request<QueueEntry[]>(fetch, 'GET', '/api/scan-queue');
			failure = null;
		} catch (err) {
			failure = errorMessage(err);
		}
	}

	async function remove(entry: QueueEntry) {
		removing = entry.id;
		removeError = null;
		try {
			await request<void>(fetch, 'DELETE', `/api/scan-queue/${entry.id}`);
			entries = (entries ?? []).filter((e) => e.id !== entry.id);
		} catch (err) {
			removeError = `Nie udało się usunąć ${entry.barcode}: ${errorMessage(err)}`;
		} finally {
			removing = null;
		}
	}

	onMount(() => {
		void load();
	});
</script>

<svelte:head>
	<title>Do uzupełnienia · Ohara</title>
</svelte:head>

<PageHeader
	title="Do uzupełnienia"
	description="Kody, których nie znaleziono podczas skanowania. Kod znika stąd, gdy dodasz wydanie z tym kodem."
>
	{#snippet kicker()}
		<BackLink href="/scan" label="Skanuj" />
	{/snippet}
</PageHeader>

<div class="content">
	{#if failure}
		<ErrorState
			title="Nie udało się wczytać kolejki"
			description={failure}
			onretry={() => load()}
			retryLabel="Spróbuj ponownie"
		/>
	{:else if entries === null}
		<Skeleton rows={3} label="Wczytywanie kolejki" />
	{:else if entries.length === 0}
		<EmptyState title="Kolejka jest pusta" description="Każdy zeskanowany kod jest już w kolekcji.">
			{#snippet action()}
				<Button tone="signal" href="/scan">Skanuj</Button>
			{/snippet}
		</EmptyState>
	{:else}
		{#if removeError}
			<p class="error" role="alert">{removeError}</p>
		{/if}
		<ObjectList>
			{#each entries as entry (entry.id)}
				<ObjectRow>
					{#snippet main()}
						<span class="code">{entry.barcode}</span>
						<span class="meta">
							<Pill tone={entry.reason === 'lookup_failed' ? 'danger' : 'warning'}>
								{reasonLabels[entry.reason]}
							</Pill>
							<RelativeTime value={entry.last_scanned_at} />
							{#if entry.scans > 1}· zeskanowano {entry.scans}×{/if}
						</span>
						{#if entry.detail}
							<span class="detail">{entry.detail}</span>
						{/if}
					{/snippet}
					{#snippet side()}
						<Button href={`/scan/${entry.barcode}?manual=1`} tone="signal">Dodaj ręcznie</Button>
						<Button href={`/scan/${entry.barcode}`}>Szukaj ponownie</Button>
						<Button
							type="button"
							tone="stop-quiet"
							loading={removing === entry.id}
							onclick={() => void remove(entry)}
						>
							Usuń
						</Button>
					{/snippet}
				</ObjectRow>
			{/each}
		</ObjectList>
	{/if}
</div>

<style>
	.content {
		padding: var(--sui-pane-gutter, 1.5rem);
		font-family: var(--sui-sans);
		color: var(--sui-text-primary);
	}
	.code {
		font-family: var(--sui-mono);
		font-size: 1.0625rem;
	}
	.meta {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
		align-items: center;
	}
	.detail {
		display: block;
		color: var(--sui-text-muted, inherit);
		font-size: 0.875rem;
	}
	.error {
		color: var(--sui-text-danger, inherit);
	}
</style>
