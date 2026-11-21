<script lang="ts">
	import { Button, Callout, Field, Skeleton, TextInput } from '@smykla-skalski/sui';
	import { tick, untrack } from 'svelte';
	import CollectionCover from '#lib/components/CollectionCover.svelte';
	import ScanConfirm from '#lib/components/ScanConfirm.svelte';
	import { sessionExpiredMessage } from '#lib/api.js';
	import { displayName, editionSummary, kindLabels, type Kind } from '#lib/collection.js';
	import { offlineSavedMessage } from '#lib/pending-scans.svelte.js';
	import type { Added, ScanFlow } from '#lib/scan-flow.svelte.js';
	import {
		barcodeGroups,
		candidateParts,
		providerLabel,
		queuedMessage,
		reasonLabels,
		searchHints,
		type Candidate
	} from '#lib/scan.js';
	import { editionArtwork } from '#lib/title-detail.js';

	let {
		flow,
		autofocus = false,
		onadded,
		onclose,
		onmanual
	}: {
		flow: ScanFlow;
		/** Moves focus into each answer, for a code that came from the keyboard. */
		autofocus?: boolean;
		/** A copy was saved. */
		onadded: (added: Added) => void;
		/** Pomiń or Skanuj dalej: the user moves on without adding. */
		onclose: () => void;
		/** Uzupełnij ręcznie for a code no source knows. */
		onmanual: () => void;
	} = $props();

	type Header = {
		overline: string;
		tone: 'ok' | 'owned' | 'warning' | 'quiet';
		name?: string;
		meta?: string;
		more?: string;
		cover?: { kind: Kind; url?: string };
	};

	/** Whether the title and kind of a found item show as fields. */
	let editing = $state(false);

	const item = $derived(flow.item);
	/** Empty for an item added without a barcode. */
	const code = $derived(item?.barcode ?? flow.code);
	const candidate = $derived(flow.candidate);
	const albums = $derived(item?.candidates.every((c) => c.type === 'music') ?? false);
	const sources = $derived(item?.errors.map((e) => e.provider).join(', ') ?? '');
	const header = $derived(headerFor());

	function line(parts: (string | null | undefined)[]): string {
		return parts.filter(Boolean).join(' · ');
	}

	function otherTitle(t: {
		name: string;
		original_title?: string | null;
		polish_title?: string | null;
	}): string | undefined {
		return [t.polish_title, t.original_title].find((n) => n && n !== t.name) ?? undefined;
	}

	function headerFor(): Header {
		const p = flow.phase;
		switch (p.kind) {
			case 'loading':
				return { overline: 'Sprawdzam kod…', tone: 'quiet' };
			case 'invalid':
				return { overline: 'Nieprawidłowy kod', tone: 'warning' };
			case 'failed':
				return { overline: 'Nie udało się sprawdzić kodu', tone: 'warning' };
			case 'queued':
				return { overline: reasonLabels[p.entry.reason], tone: 'warning' };
			case 'offline':
				return { overline: 'Brak połączenia', tone: 'warning' };
			case 'expired':
				return { overline: 'Sesja wygasła', tone: 'warning' };
		}
		const target = item?.target;
		if (target?.kind === 'owned') {
			const e = target.edition;
			const t = e.title;
			const copies = e.copies.filter((c) => c.status === 'owned').length;
			return {
				overline: 'Masz już to wydanie',
				tone: 'owned',
				name: displayName(t),
				meta: line([kindLabels[t.kind], t.authors.join(', '), t.year?.toString()]),
				more: line([editionSummary({ ...e, barcode: null }), `w kolekcji: ${copies}`]),
				cover: { kind: t.kind, url: editionArtwork(e, t) }
			};
		}
		if (p.status === 'missing') {
			return {
				overline: candidate ? `Znaleziono · ${providerLabel(candidate)}` : 'Dodaj ręcznie',
				tone: candidate ? 'ok' : 'quiet',
				name: flow.form.name.trim() || undefined,
				meta: kindLabels[flow.form.kind],
				cover: { kind: flow.form.kind, url: candidate?.cover_url }
			};
		}
		if (flow.choosing) {
			const n = item?.candidates.length ?? 0;
			return {
				overline: n === 1 ? 'Znaleziono propozycję' : `Znaleziono propozycje: ${n}`,
				tone: 'ok'
			};
		}
		const c = candidate;
		const same = c?.type === flow.form.kind ? c : null;
		return {
			overline: c ? `Znaleziono · ${providerLabel(c)}` : 'Znaleziono',
			tone: 'ok',
			name: flow.form.name.trim() || undefined,
			meta: line([
				kindLabels[flow.form.kind],
				same?.title.authors?.join(', '),
				(same?.title.year ?? same?.edition.year)?.toString(),
				same ? otherTitle(same.title) : undefined
			]),
			more: same?.type === 'music' ? line([same.edition.publisher, same.edition.label]) : undefined,
			cover: { kind: flow.form.kind, url: c?.cover_url }
		};
	}

	function candidateDetails(c: Candidate): string {
		return line([
			...candidateParts(c).details,
			providerLabel(c),
			`${Math.round(c.confidence * 100)}%`
		]);
	}

	async function focus(id: string) {
		await tick();
		document.getElementById(id)?.focus();
	}

	function foldTitleForNewItem() {
		editing = false;
	}

	function landOnNextStep(p: ScanFlow['phase'], choosing: boolean) {
		if (p.kind === 'ready' && choosing) void focus('scan-candidate-0');
		else if (p.kind === 'ready' && p.status === 'missing') void focus('scan-name');
		else if (p.kind === 'ready') void focus('scan-price');
		else if (p.kind !== 'loading') void focus('scan-way-on');
	}

	$effect(() => {
		void item?.key;
		untrack(foldTitleForNewItem);
	});

	$effect(() => {
		if (flow.issue?.field === 'name') editing = true;
	});

	// Any edit to the title, kind, medium or query while a search runs, even one undone at once,
	// leaves the form to the user: the search's answer is dropped.
	$effect(() => {
		const f = flow.form;
		void [f.kind, f.name, f.format, f.binding, ...f.packaging, flow.query];
		untrack(() => flow.edited());
	});

	$effect(() => {
		const p = flow.phase;
		const choosing = flow.choosing;
		void item?.key;
		if (autofocus) untrack(() => landOnNextStep(p, choosing));
	});

	async function save() {
		const added = await flow.save();
		if (added) onadded(added);
	}

	function close() {
		flow.dismiss();
		onclose();
	}
</script>

{#snippet titleSearch()}
	<form
		novalidate
		aria-label="Szukaj po tytule"
		onsubmit={(e) => {
			e.preventDefault();
			void flow.search();
		}}
	>
		<Field id="scan-album" label="Szukaj po tytule" hint={searchHints[flow.form.kind]}>
			{#snippet control({ id, describedby, invalid })}
				<div class="query">
					<TextInput
						{id}
						aria-describedby={describedby}
						{invalid}
						enterkeyhint="search"
						autocomplete="off"
						bind:value={flow.query}
					/>
					<Button type="submit" loading={flow.searching} disabled={!flow.query.trim()}>
						Szukaj
					</Button>
				</div>
			{/snippet}
		</Field>
	</form>
{/snippet}

<div class="item">
	<header class={['found', { covered: header.cover }]}>
		{#if header.cover}
			<span class="cover">
				<CollectionCover kind={header.cover.kind} url={header.cover.url} name={header.name} large />
			</span>
		{/if}
		<div class="text">
			<p class={['overline', header.tone]}>
				{#if header.tone === 'ok' || header.tone === 'owned'}
					<svg
						width="14"
						height="14"
						viewBox="0 0 24 24"
						fill="none"
						stroke="currentColor"
						stroke-width="3"
						stroke-linecap="round"
						stroke-linejoin="round"
						aria-hidden="true"
						focusable="false"
					>
						<path d="M5 12.5 10 17.5 19 7" />
					</svg>
				{/if}
				{header.overline}
			</p>
			{#if header.name}
				<h3 class="name">{header.name}</h3>
			{/if}
			{#if header.meta}
				<p class="meta">{header.meta}</p>
			{/if}
			{#if header.more}
				<p class="meta">{header.more}</p>
			{/if}
			{#if code}
				<p class="code">{barcodeGroups(code).join(' ')}</p>
			{/if}
			{#if flow.phase.kind === 'ready' && item?.target.kind === 'new' && !flow.choosing}
				<div class="tools">
					{#if flow.phase.status !== 'missing'}
						<button
							type="button"
							class="link"
							aria-expanded={editing}
							onclick={() => (editing = !editing)}
						>
							Popraw tytuł
						</button>
					{/if}
					{#if item.candidates.length > 0}
						<button type="button" class="link" onclick={() => flow.choose()}>
							{albums ? 'Inne wydanie' : 'Inna propozycja'}
						</button>
					{/if}
				</div>
			{/if}
		</div>
	</header>

	{#if flow.phase.kind === 'loading'}
		<Skeleton rows={3} />
	{:else if flow.phase.kind === 'invalid'}
		<Callout tone="warning" live>{flow.phase.message}</Callout>
		<div class="ways">
			<Button id="scan-way-on" tone="signal" onclick={close}>Skanuj dalej</Button>
		</div>
	{:else if flow.phase.kind === 'failed'}
		<Callout tone="warning" live>{flow.phase.message}</Callout>
		<div class="ways">
			<Button id="scan-way-on" tone="signal" onclick={() => void flow.load(flow.code)}>
				Spróbuj ponownie
			</Button>
			<Button onclick={close}>Skanuj dalej</Button>
		</div>
	{:else if flow.phase.kind === 'offline'}
		<Callout tone="warning" live>{offlineSavedMessage}</Callout>
		<div class="ways">
			<Button id="scan-way-on" tone="signal" onclick={close}>Skanuj dalej</Button>
		</div>
	{:else if flow.phase.kind === 'expired'}
		<Callout tone="warning" live>{sessionExpiredMessage}</Callout>
		<div class="ways">
			<Button id="scan-way-on" tone="signal" onclick={() => location.reload()}>Odśwież</Button>
			<Button onclick={close}>Skanuj dalej</Button>
		</div>
	{:else if flow.phase.kind === 'queued'}
		<Callout tone="warning" live>{queuedMessage(flow.phase.entry)}</Callout>
		<div class="ways">
			{#if flow.phase.entry.reason === 'lookup_failed'}
				<Button id="scan-way-on" tone="signal" onclick={() => void flow.load(flow.code)}>
					Spróbuj ponownie
				</Button>
				<Button onclick={onmanual}>Uzupełnij ręcznie</Button>
			{:else}
				<Button id="scan-way-on" tone="signal" onclick={onmanual}>Uzupełnij ręcznie</Button>
			{/if}
			<Button onclick={close}>Skanuj dalej</Button>
		</div>
	{:else if item}
		{#if flow.phase.status === 'missing'}
			{@render titleSearch()}
		{/if}

		{#if flow.notice}
			<Callout tone="warning" live>{flow.notice}</Callout>
		{/if}

		{#if flow.choosing}
			<div class="candidates" role="group" aria-labelledby="scan-candidates">
				<p class="caption" id="scan-candidates">
					{#if code}
						{albums ? 'Ten kod ma kilka wydań: wybierz swoje.' : 'Wybierz, co skanujesz.'}
					{:else}
						{albums ? 'Wybierz wydanie.' : 'Wybierz pozycję.'}
					{/if}
				</p>
				{#each item.candidates as c, i (i)}
					<button
						type="button"
						class="candidate"
						id={`scan-candidate-${i}`}
						onclick={() => flow.pick(i)}
					>
						<span class="thumb"><CollectionCover kind={c.type} url={c.cover_url} /></span>
						<span class="what">
							<strong>{candidateParts(c).name}</strong>
							<span>{candidateDetails(c)}</span>
						</span>
					</button>
				{/each}
			</div>
			{#if flow.phase.status === 'found'}
				{@render titleSearch()}
			{/if}
			<div class="ways">
				{#if item.searched}
					<Button onclick={() => flow.typeIn()}>Wpisz ręcznie</Button>
				{/if}
				<Button onclick={close}>Pomiń</Button>
			</div>
		{:else}
			<ScanConfirm
				target={item.target}
				bind:form={flow.form}
				issue={flow.issue}
				saving={flow.saving}
				shops={flow.sticky.shops}
				editTitle={editing || flow.phase.status === 'missing'}
				skipLabel={flow.phase.status === 'missing' ? 'Anuluj' : 'Pomiń'}
				onsave={save}
				onskip={close}
				onwish={(on) => flow.wish(on)}
			/>
		{/if}

		{#if sources}
			<Callout tone="warning">Nie odpowiedziały źródła: {sources}.</Callout>
		{/if}
	{/if}
</div>

<style>
	/* Buttons, fields and selects are 44px targets with a mouse too, as on a phone. */
	.item {
		--sui-control-height: max(44px, 2.75rem);
		--sui-control-height-compact: max(44px, 2.75rem);

		color: var(--sui-text-primary);
		display: grid;
		font-family: var(--sui-sans);
		gap: 1rem;
		min-inline-size: 0;
	}

	.found {
		align-items: flex-start;
		display: grid;
		gap: 0.875rem;
		grid-template-columns: minmax(0, 1fr);
	}

	.found.covered {
		grid-template-columns: auto minmax(0, 1fr);
	}

	.cover {
		--collection-cover-width: 5.125rem;

		display: block;
	}

	.text {
		display: grid;
		gap: 0.25rem;
		min-inline-size: 0;
	}

	.overline {
		align-items: center;
		display: flex;
		font-size: 0.75rem;
		font-weight: 700;
		gap: 0.3125rem;
		letter-spacing: 0.06em;
		margin: 0;
		text-transform: uppercase;
	}

	.overline.ok {
		color: var(--sui-success);
	}

	.overline.owned {
		color: var(--sui-brand-action-text);
	}

	.overline.warning {
		color: var(--sui-danger);
	}

	.overline.quiet {
		color: var(--sui-text-secondary);
	}

	.name {
		color: var(--ohara-heading, var(--sui-text-primary));
		font: 600 1.5rem / 1.1 var(--ohara-serif, var(--sui-sans));
		letter-spacing: -0.01em;
		margin: 0;
		overflow-wrap: anywhere;
	}

	.meta {
		color: var(--sui-text-secondary);
		font-size: 0.875rem;
		font-variant-numeric: tabular-nums;
		margin: 0;
	}

	.code {
		color: var(--sui-text-secondary);
		font-family: var(--sui-mono);
		font-size: 0.75rem;
		margin: 0.125rem 0 0;
	}

	.tools {
		display: flex;
		flex-wrap: wrap;
		gap: 0 1rem;
	}

	.link {
		background: none;
		border: 0;
		color: var(--sui-link);
		cursor: pointer;
		font: 700 0.875rem / 1 var(--sui-sans);
		min-block-size: 2.75rem;
		padding: 0;
		-webkit-tap-highlight-color: transparent;
	}

	.link:focus-visible,
	.candidate:focus-visible {
		outline: var(--sui-focus-ring-width, 2px) solid var(--sui-focus);
		outline-offset: 2px;
	}

	.caption {
		color: var(--sui-text-secondary);
		font-size: 0.8125rem;
		font-weight: 700;
		margin: 0;
	}

	.candidates {
		display: grid;
		gap: 0.5rem;
	}

	.candidate {
		--collection-cover-width: 2.75rem;

		align-items: center;
		background: var(--sui-surface-inset);
		border: 1px solid transparent;
		border-radius: 0.875rem;
		color: var(--sui-text-primary);
		cursor: pointer;
		display: grid;
		font: inherit;
		gap: 0.75rem;
		grid-template-columns: auto minmax(0, 1fr);
		min-block-size: 2.75rem;
		padding: 0.5rem 0.75rem;
		text-align: start;
		-webkit-tap-highlight-color: transparent;
	}

	@media (hover: hover) {
		.candidate:hover {
			border-color: var(--sui-border-subtle);
		}
	}

	.what {
		display: grid;
		gap: 0.125rem;
		min-inline-size: 0;
	}

	.what strong {
		font-size: 0.9375rem;
		overflow-wrap: anywhere;
	}

	.what span {
		color: var(--sui-text-secondary);
		font-size: 0.8125rem;
		font-variant-numeric: tabular-nums;
	}

	.query {
		align-items: center;
		display: grid;
		gap: 0.5rem;
		grid-template-columns: minmax(0, 1fr) auto;
	}

	.ways {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
	}
</style>
