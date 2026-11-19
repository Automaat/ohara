<!--
@component
One code in the scan queue: the barcode grouped as printed under its bars, why it is queued, how
often it was scanned and when last, and one "Uzupełnij" link to manual entry. "Szukaj ponownie"
and "Usuń" wait in the "…" menu. On a touch screen a swipe to the left slides the row open on
"Usuń"; the page keeps one row open at a time through `open` and `onswipe`.
-->
<script lang="ts">
	import { ActionMenu, Button, RelativeTime, VisuallyHidden } from '@smykla-skalski/sui';
	import type { Attachment } from 'svelte/attachments';
	import { goto } from '$app/navigation';
	import { scanCount } from '#lib/polish.js';
	import { swipeAxis, swipeOffset, swipeOpens, swipeWidth } from '#lib/queue.svelte.js';
	import { barcodeGroups, reasonLabels, type QueueEntry } from '#lib/scan.js';

	let {
		entry,
		open,
		onswipe,
		onremove
	}: {
		entry: QueueEntry;
		/** Whether the row is swiped open on "Usuń". */
		open: boolean;
		/** The reader swiped the row open or shut, or tapped an open row to shut it. */
		onswipe: (open: boolean) => void;
		/** The reader chose "Usuń", from the menu or from under a swiped row. */
		onremove: (via: 'menu' | 'swipe') => void;
	} = $props();

	/** How long after a swipe a click on the row is the end of that swipe, in milliseconds. */
	const swipeClickMs = 400;

	let drag = $state<number | null>(null);
	const offset = $derived(drag ?? (open ? -swipeWidth : 0));
	const lookup = $derived(`/scan/${encodeURIComponent(entry.barcode)}`);

	/**
	 * Follows one touch on the row's face. A touch that turns sideways drags the face; one that
	 * turns vertical is left to scroll the page. The click that ends a swipe, or lands on an open
	 * row, never reaches the link or the menu under the finger.
	 */
	const swipe: Attachment<HTMLElement> = (face) => {
		let touch: { id: number; x: number; y: number; base: number; axis: 'x' | 'y' | null } | null =
			null;
		let swipedAt = -Infinity;

		const down = (event: PointerEvent) => {
			if (event.pointerType !== 'touch' || !event.isPrimary) return;
			const base = open ? -swipeWidth : 0;
			touch = { id: event.pointerId, x: event.clientX, y: event.clientY, base, axis: null };
		};
		const move = (event: PointerEvent) => {
			if (!touch || event.pointerId !== touch.id) return;
			const dx = event.clientX - touch.x;
			touch.axis ??= swipeAxis(dx, event.clientY - touch.y);
			if (touch.axis === 'y') {
				touch = null;
			} else if (touch.axis === 'x') {
				if (!face.hasPointerCapture(event.pointerId)) face.setPointerCapture(event.pointerId);
				drag = swipeOffset(touch.base, dx);
			}
		};
		const up = (event: PointerEvent) => {
			if (!touch || event.pointerId !== touch.id) return;
			const swiped = touch.axis === 'x';
			touch = null;
			if (!swiped) return;
			swipedAt = performance.now();
			const opens = swipeOpens(drag ?? 0);
			drag = null;
			if (opens !== open) onswipe(opens);
		};
		const cancel = (event: PointerEvent) => {
			if (touch?.id !== event.pointerId) return;
			touch = null;
			drag = null;
		};
		const click = (event: MouseEvent) => {
			const endsSwipe = performance.now() - swipedAt < swipeClickMs;
			if (!endsSwipe && !open) return;
			event.preventDefault();
			event.stopPropagation();
			if (!endsSwipe) onswipe(false);
		};

		face.addEventListener('pointerdown', down);
		face.addEventListener('pointermove', move);
		face.addEventListener('pointerup', up);
		face.addEventListener('pointercancel', cancel);
		face.addEventListener('click', click, true);
		return () => {
			face.removeEventListener('pointerdown', down);
			face.removeEventListener('pointermove', move);
			face.removeEventListener('pointerup', up);
			face.removeEventListener('pointercancel', cancel);
			face.removeEventListener('click', click, true);
		};
	};
</script>

<li class="qrow" data-queue-row={entry.id}>
	{#if offset < 0}
		<button
			type="button"
			class="del"
			style:inline-size={`${swipeWidth}px`}
			onclick={() => onremove('swipe')}
		>
			Usuń
		</button>
	{/if}
	<div
		class={['face', drag !== null && 'dragging']}
		style:transform={offset < 0 ? `translateX(${offset}px)` : undefined}
		{@attach swipe}
	>
		<span class="icon" aria-hidden="true">
			<svg viewBox="0 0 20 20" width="20" height="20" fill="none" stroke="currentColor">
				<path d="M3 4v12M6 4v12M8.5 4v12M11 4v12M14 4v12M17 4v12" stroke-width="1.6" />
				<path d="M6 4v12M14 4v12" stroke-width="2.4" />
			</svg>
		</span>
		<div class="text">
			<span class="code">
				{#each barcodeGroups(entry.barcode) as group, index (index)}<span>{group}</span>{/each}
			</span>
			<span class="meta">
				<span class={['reason', entry.reason === 'lookup_failed' && 'failed']}>
					{reasonLabels[entry.reason]}
				</span>
				· {scanCount(entry.scans)} ·
				<RelativeTime value={entry.last_scanned_at} />
			</span>
			{#if entry.detail}
				<span class="detail" title={entry.detail}>{entry.detail}</span>
			{/if}
		</div>
		<span class="fix">
			<Button href={`${lookup}?manual=1`} tone="signal" data-fix={entry.id}>
				Uzupełnij<VisuallyHidden>&nbsp;{entry.barcode}</VisuallyHidden>
			</Button>
		</span>
		<ActionMenu
			label={`Więcej działań dla ${entry.barcode}`}
			icon="more"
			align="end"
			items={[
				{ label: 'Szukaj ponownie', icon: 'search', onselect: () => void goto(lookup) },
				{ label: 'Usuń', icon: 'trash', tone: 'stop', onselect: () => onremove('menu') }
			]}
		/>
	</div>
</li>

<style>
	.qrow {
		position: relative;
		border-block-start: 1px solid var(--sui-border-subtle);
	}

	.qrow:first-child {
		border-block-start: 0;
	}

	.face {
		position: relative;
		z-index: 1;
		display: flex;
		align-items: center;
		gap: 0.625rem;
		padding: 0.75rem;
		background: var(--sui-surface-base);
		touch-action: pan-y;
		transition: transform var(--sui-duration-normal) var(--sui-ease-out);
	}

	.face.dragging {
		transition: none;
	}

	.icon {
		display: grid;
		flex: none;
		place-items: center;
		inline-size: 2.5rem;
		block-size: 2.5rem;
		border-radius: 0.75rem;
		background: var(--sui-surface-inset);
		color: var(--sui-text-secondary);
	}

	.text {
		display: flex;
		flex: 1;
		flex-direction: column;
		gap: 0.125rem;
		min-inline-size: 0;
	}

	/* The groups wrap as whole groups when a long code meets a narrow row, never under the
	   button. */
	.code {
		display: flex;
		flex-wrap: wrap;
		column-gap: 1ch;
		color: var(--sui-text-primary);
		font-family: var(--sui-mono);
		font-size: 0.9375rem;
		font-weight: 500;
		letter-spacing: 0.02em;
	}

	.meta,
	.detail {
		color: var(--sui-text-secondary);
		font-size: 0.75rem;
		line-height: 1.35;
	}

	.failed {
		color: var(--sui-danger);
		font-weight: 700;
	}

	.detail {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	/* The one action of a row is the accent's soft ground with accent ink, as the mockup draws
	   it, and stays 34px tall on a phone while its hit area grows to 44px. Hover and press
	   lighten the ground toward the surface, so the ink keeps its contrast in both themes. */
	.fix {
		flex: none;
		--sui-brand-action: var(--sui-brand-action-tint);
		--sui-brand-action-hover: color-mix(
			in srgb,
			var(--sui-surface-base) 45%,
			var(--sui-brand-action-tint)
		);
		--sui-brand-action-pressed: var(--sui-brand-action-hover);
		--sui-on-brand-action: var(--sui-brand-action-text);
		--sui-local-button-seat: var(--sui-control-height-drawn);
	}

	.fix :global(.sui-button) {
		border-radius: var(--sui-radius-pill);
		font-weight: 800;
	}

	.del {
		position: absolute;
		inset-block: 0;
		inset-inline-end: 0;
		border: 0;
		background: var(--sui-danger);
		color: var(--sui-on-danger);
		font: inherit;
		font-size: 0.8125rem;
		font-weight: 800;
		cursor: pointer;
	}

	.del:focus-visible {
		outline: var(--sui-focus-ring-width) solid var(--sui-focus);
		outline-offset: calc(-1 * var(--sui-focus-ring-width));
	}

	/* The barcode, its action and the menu need the room on a phone's narrower list, so the
	   decorative tile goes first. */
	@container queue (max-width: 22.5rem) {
		.icon {
			display: none;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.face {
			transition: none;
		}
	}
</style>
