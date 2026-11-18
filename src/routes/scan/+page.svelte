<script lang="ts">
	import {
		Button,
		Callout,
		Field,
		ObjectList,
		ObjectRow,
		PageHeader,
		Pill,
		Section,
		TextInput
	} from '@smykla-skalski/sui';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { request } from '$lib/api';
	import BarcodeCamera from '$lib/components/BarcodeCamera.svelte';
	import { outcomeLabels, outcomeTones, recentScans } from '$lib/recent.svelte';
	import { RepeatGuard, cleanBarcode, looksLikeBarcode, type QueueEntry } from '$lib/scan';

	let code = $state('');
	let leaving = $state(false);
	let notice = $state<string | null>(null);
	let codeError = $state<string>();
	let queued = $state<number | null>(null);
	const guard = new RepeatGuard(1500);

	async function countQueue() {
		try {
			queued = (await request<QueueEntry[]>(fetch, 'GET', '/api/scan-queue')).length;
		} catch {
			queued = null;
		}
	}

	/** Every scan opens its own add page, which says what the barcode turned out to be. */
	async function open(raw: string, manual = false) {
		const barcode = cleanBarcode(raw);
		if (!barcode || leaving) return;
		if (!looksLikeBarcode(barcode)) {
			notice = `„${raw.trim()}” nie wygląda na kod kreskowy.`;
			return;
		}
		leaving = true;
		notice = null;
		code = '';
		await goto(`/scan/${encodeURIComponent(barcode)}${manual ? '?manual=1' : ''}`);
	}

	function fromCamera(barcode: string) {
		if (!guard.accepts(barcode, performance.now())) return;
		void open(barcode);
	}

	function submitCode() {
		if (leaving) return;
		if (!looksLikeBarcode(cleanBarcode(code))) {
			codeError = 'Wpisz pełny kod kreskowy lub ISBN i spróbuj ponownie.';
			document.getElementById('scan-code')?.focus();
			return;
		}
		void open(code);
	}

	onMount(() => {
		void countQueue();
		// Links from before the add page existed: /scan?barcode=…[&manual=1].
		const preset = page.url.searchParams.get('barcode');
		if (preset) void open(preset, page.url.searchParams.get('manual') === '1');
		else document.getElementById('scan-code')?.focus();
	});
</script>

<svelte:head>
	<title>Skanuj · Ohara</title>
</svelte:head>

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

<div class="content">
	<BarcodeCamera paused={leaving} ondetect={fromCamera} />

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

	{#if notice}
		<Callout tone="warning" live>{notice}</Callout>
	{/if}

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
</div>

<style>
	.content {
		display: grid;
		gap: 1rem;
		font-family: var(--sui-sans);
		color: var(--sui-text-primary);
		padding: var(--sui-pane-gutter, 1.5rem);
		max-width: 48rem;
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
