<script lang="ts">
	import {
		BackLink,
		Button,
		EmptyState,
		ErrorState,
		PageHeader,
		Skeleton,
		Toaster,
		dismiss,
		toast
	} from '@smykla-skalski/sui';
	import { onDestroy, onMount, tick, untrack } from 'svelte';
	import QueueRow from '#lib/components/QueueRow.svelte';
	import { pendingScans } from '#lib/pending-scans.svelte.js';
	import { deleteQueueEntry } from '#lib/queue-deletes.js';
	import { QueueList, QueueRemovals } from '#lib/queue.svelte.js';
	import type { QueueEntry } from '#lib/scan.js';
	import { toastHeld } from '#lib/toast-hold.js';

	const list = new QueueList(fetch);
	let openRow = $state<number | null>(null);

	const removals = new QueueRemovals({
		toast,
		dismiss,
		holding: (id) => toastHeld(document, id),
		send: (entry) => deleteQueueEntry(entry.id),
		removed: (entry) => list.removed(entry.id)
	});

	const shown = $derived(list.entries?.filter((e) => !removals.hidden.has(e.id)) ?? null);

	$effect(() => {
		const waiting = pendingScans.count;
		untrack(() => list.waiting(waiting));
	});

	/**
	 * Hides the entry behind its undo toast. A removal from the menu leaves focus on a trigger
	 * that is gone, so focus moves to the next row's action, or to the empty state's link.
	 */
	async function remove(entry: QueueEntry, via: 'menu' | 'swipe') {
		const rows = shown ?? [];
		const at = rows.findIndex((e) => e.id === entry.id);
		const next = rows[at + 1] ?? rows[at - 1];
		openRow = null;
		removals.remove(entry);
		if (via !== 'menu') return;
		await tick();
		const target = next
			? document.querySelector<HTMLElement>(`[data-fix="${next.id}"]`)
			: document.querySelector<HTMLElement>('.queue-empty a');
		target?.focus();
	}

	/** A press anywhere but on the open row shuts it. */
	function shutOpenRow(event: PointerEvent) {
		if (openRow === null) return;
		const row = event.target instanceof Element ? event.target.closest('[data-queue-row]') : null;
		if (row?.getAttribute('data-queue-row') !== String(openRow)) openRow = null;
	}

	onMount(() => {
		void list.load();
	});

	onDestroy(() => {
		removals.destroy();
	});
</script>

<svelte:head>
	<title>Do uzupełnienia · Ohara</title>
</svelte:head>

<svelte:window onpagehide={() => removals.flush()} />
<svelte:document onpointerdown={shutOpenRow} />

<PageHeader
	title="Do uzupełnienia"
	description="Kody, których nie rozpoznano. Znikają po dodaniu wydania."
>
	{#snippet kicker()}
		<BackLink href="/scan" label="Skanuj" />
	{/snippet}
</PageHeader>

<div class="content">
	{#if list.failure}
		<ErrorState
			title="Nie udało się wczytać kolejki"
			description={list.failure}
			onretry={() => list.load()}
			retryLabel="Spróbuj ponownie"
		/>
	{:else if shown === null}
		<Skeleton rows={3} label="Wczytywanie kolejki" />
	{:else if shown.length === 0}
		<div class="queue-empty">
			<EmptyState
				title="Kolejka jest pusta"
				description="Każdy zeskanowany kod jest już w kolekcji."
			>
				{#snippet action()}
					<Button tone="signal" href="/scan">Skanuj</Button>
				{/snippet}
			</EmptyState>
		</div>
	{:else}
		<ul class="queue" role="list" aria-label="Kody do uzupełnienia">
			{#each shown as entry (entry.id)}
				<QueueRow
					{entry}
					open={openRow === entry.id}
					onswipe={(open) => (openRow = open ? entry.id : null)}
					onremove={(via) => void remove(entry, via)}
				/>
			{/each}
		</ul>
		<p class="hint">Przesuń w lewo, aby usunąć. Cofniesz to z komunikatu.</p>
	{/if}
</div>

<div class="toasts">
	<Toaster label="Powiadomienia" dismissLabel="Zamknij" />
</div>

<style>
	.content {
		padding: var(--sui-pane-gutter, 1.5rem);
		font-family: var(--sui-sans);
		color: var(--sui-text-primary);
		max-width: 48rem;
	}

	.queue {
		container: queue / inline-size;
		margin: 0;
		padding: 0;
		overflow: hidden;
		border: 1px solid var(--sui-border-subtle);
		border-radius: 1.375rem;
		background: var(--sui-surface-base);
		list-style: none;
	}

	.hint {
		display: none;
		margin: 0.75rem 0 0;
		color: var(--sui-text-secondary);
		font-size: 0.875rem;
		text-align: center;
	}

	@media (pointer: coarse) {
		.hint {
			display: block;
		}
	}

	/* On a phone the undo toast stands above the floating tab bar instead of covering it. */
	:global(.ohara-phone) .toasts :global(.sui-toaster) {
		inset-block-end: calc(4rem + max(env(safe-area-inset-bottom, 0px), 0.75rem));
	}
</style>
