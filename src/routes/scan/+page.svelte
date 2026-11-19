<script lang="ts">
	import {
		Button,
		Callout,
		Field,
		Modal,
		ObjectList,
		ObjectRow,
		PageHeader,
		Pill,
		Section,
		TextInput
	} from '@smykla-skalski/sui';
	import { onMount } from 'svelte';
	import { MediaQuery } from 'svelte/reactivity';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { request } from '#lib/api.js';
	import BarcodeCamera from '#lib/components/BarcodeCamera.svelte';
	import { outcomeLabels, outcomeTones, recentScans } from '#lib/recent.svelte.js';
	import {
		RepeatGuard,
		barcodeInputError,
		cleanBarcode,
		looksLikeBarcode,
		type QueueEntry
	} from '#lib/scan.js';

	/** The tab bar's width, where the camera fills the screen and manual entry opens over it. */
	const phone = new MediaQuery('max-width: 47.9375rem');
	/** How long a camera read shows its green band before its page opens. */
	const readHold = 350;

	let code = $state('');
	let leaving = $state(false);
	let typing = $state(false);
	let notice = $state<string | null>(null);
	let codeError = $state<string>();
	let queued = $state<number | null>(null);
	let pending: ReturnType<typeof setTimeout> | undefined;
	const guard = new RepeatGuard(1500);

	async function countQueue() {
		try {
			queued = (await request<QueueEntry[]>(fetch, 'GET', '/api/scan-queue')).length;
		} catch {
			queued = null;
		}
	}

	/**
	 * Every scan opens its own add page, which says what the barcode turned out to be. Returns
	 * whether the code was taken; a camera read waits `hold` ms so its green band shows.
	 */
	function open(raw: string, manual = false, hold = 0): boolean {
		const barcode = cleanBarcode(raw);
		if (!barcode || leaving) return false;
		if (!looksLikeBarcode(barcode)) {
			notice = `„${raw.trim()}” nie wygląda na kod kreskowy.`;
			return false;
		}
		leaving = true;
		notice = null;
		code = '';
		const href = `/scan/${encodeURIComponent(barcode)}${manual ? '?manual=1' : ''}`;
		clearTimeout(pending);
		pending = setTimeout(() => void goto(href), hold);
		return true;
	}

	function fromCamera(barcode: string): boolean {
		if (!guard.accepts(barcode, performance.now())) return false;
		return open(barcode, false, readHold);
	}

	function submitCode() {
		if (leaving) return;
		codeError = barcodeInputError(cleanBarcode(code));
		if (codeError) {
			document.getElementById('scan-code')?.focus();
			return;
		}
		open(code);
	}

	onMount(() => {
		void countQueue();
		// Links from before the add page existed: /scan?barcode=…[&manual=1].
		const preset = page.url.searchParams.get('barcode');
		if (preset) open(preset, page.url.searchParams.get('manual') === '1');
		else document.getElementById('scan-code')?.focus();
		return () => clearTimeout(pending);
	});
</script>

<svelte:head>
	<title>Skanuj · Ohara</title>
</svelte:head>

{#snippet manualEntry()}
	<form
		novalidate
		onsubmit={(e) => {
			e.preventDefault();
			submitCode();
		}}
	>
		<Field
			id="scan-code"
			label="Kod kreskowy"
			hint="Wpisz kod albo użyj czytnika Bluetooth: kursor musi być w tym polu."
			error={codeError}
		>
			{#snippet control({ id, describedby, invalid })}
				<div class="manual">
					<TextInput
						{id}
						aria-describedby={describedby}
						{invalid}
						inputmode="numeric"
						enterkeyhint="search"
						autocomplete="off"
						disabled={leaving}
						bind:value={code}
						oninput={() => (codeError = undefined)}
					/>
					<Button type="submit" loading={leaving}>Szukaj</Button>
				</div>
			{/snippet}
		</Field>
	</form>
{/snippet}

{#snippet recent()}
	{#if recentScans.list.length > 0}
		<Section title="Ostatnie skany">
			<ObjectList>
				{#each recentScans.list as r (r.id)}
					<ObjectRow href={r.href}>
						{#snippet main()}
							<span class="code">{r.barcode}</span>
							{r.label}
						{/snippet}
						{#snippet side()}
							<Pill tone={outcomeTones[r.outcome]}>{outcomeLabels[r.outcome]}</Pill>
						{/snippet}
					</ObjectRow>
				{/each}
			</ObjectList>
		</Section>
	{/if}
{/snippet}

{#snippet message()}
	{#if notice}
		<Callout tone="warning" live>{notice}</Callout>
	{/if}
{/snippet}

{#if phone.current}
	<h1 class="title">Skanuj</h1>
{:else}
	<PageHeader
		title="Skanuj"
		description="Aparat albo czytnik kodów: każdy kod otwiera stronę, na której go dodasz."
	>
		{#snippet actions()}
			<Button href="/scan/queue">
				Do uzupełnienia{queued ? ` (${queued})` : ''}
			</Button>
		{/snippet}
	</PageHeader>
{/if}

<div class={['content', { phone: phone.current }]}>
	<BarcodeCamera
		full={phone.current}
		paused={leaving || (phone.current && typing)}
		ondetect={fromCamera}
		{queued}
		ontype={() => (typing = true)}
	>
		{@render message()}
	</BarcodeCamera>
	{#if !phone.current}
		{@render manualEntry()}
		{@render recent()}
	{/if}
</div>

{#if phone.current}
	<Modal bind:open={typing} title="Wpisz kod" closeLabel="Zamknij">
		<div class="sheet">
			{@render manualEntry()}
			{@render recent()}
		</div>
	</Modal>
{/if}

<style>
	.title {
		block-size: 1px;
		clip-path: inset(50%);
		inline-size: 1px;
		margin: 0;
		overflow: hidden;
		position: absolute;
		white-space: nowrap;
	}

	.content {
		display: grid;
		gap: 1rem;
		font-family: var(--sui-sans);
		color: var(--sui-text-primary);
		padding: var(--sui-pane-gutter, 1.5rem);
		max-width: 48rem;
	}

	.content.phone {
		padding: 0;
	}

	.sheet {
		display: grid;
		gap: 1rem;
	}

	.manual {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: 0.5rem;
		align-items: center;
	}

	.code {
		font-family: var(--sui-mono);
		margin-inline-end: 0.5rem;
	}
</style>
