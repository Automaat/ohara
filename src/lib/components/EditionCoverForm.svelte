<script lang="ts">
	import { Button, FormError, Section } from '@smykla-skalski/sui';
	import { errorMessage, request } from '#lib/api.js';

	let {
		editionID,
		hasOverride,
		onchange
	}: {
		editionID: number;
		hasOverride: boolean;
		onchange: () => Promise<void>;
	} = $props();

	let input: HTMLInputElement;
	let file = $state<File>();
	let busy = $state(false);
	let failure = $state<string | null>(null);

	async function save(photo?: File) {
		busy = true;
		failure = null;
		try {
			await request(fetch, photo ? 'PUT' : 'DELETE', `/api/editions/${editionID}/cover`, photo);
			input.value = '';
			file = undefined;
			await onchange();
		} catch (err) {
			failure = errorMessage(err);
		} finally {
			busy = false;
		}
	}
</script>

<Section title="Zdjęcie okładki">
	<form
		onsubmit={(event) => {
			event.preventDefault();
			if (file && !busy) void save(file);
		}}
	>
		<label for="edition-cover">Wybierz własne zdjęcie</label>
		<input
			id="edition-cover"
			type="file"
			accept="image/jpeg,image/png"
			aria-describedby="edition-cover-limits"
			bind:this={input}
			disabled={busy}
			onchange={() => {
				file = input.files?.[0];
				failure = null;
			}}
		/>
		<p id="edition-cover-limits">JPEG lub PNG, do 8 MiB i 16 megapikseli.</p>
		<div class="actions">
			<Button type="submit" disabled={!file || busy}>
				{hasOverride ? 'Zastąp zdjęcie' : 'Dodaj zdjęcie'}
			</Button>
			{#if hasOverride}
				<Button type="button" tone="stop-quiet" disabled={busy} onclick={() => save()}>
					Usuń własne zdjęcie
				</Button>
			{/if}
		</div>
		<FormError message={failure} />
	</form>
</Section>

<style>
	form {
		display: grid;
		gap: 0.5rem;
	}
	input {
		max-inline-size: 100%;
	}
	p {
		margin: 0;
		color: var(--sui-text-muted);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
	}
</style>
