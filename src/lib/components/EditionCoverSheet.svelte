<script lang="ts">
	import { Button, Modal } from '@smykla-skalski/sui';
	import { errorMessage, request } from '#lib/api.js';
	import { photoProblem, type CoverUndo } from '#lib/edition-cover.js';

	let {
		open = $bindable(false),
		editionID,
		undo,
		onchange
	}: {
		open?: boolean;
		editionID: number;
		/** How the sheet drops a personal photo, or null when the edition has none. */
		undo: CoverUndo | null;
		/** Shows the changed cover; the sheet closes once it has. */
		onchange: () => Promise<void>;
	} = $props();

	type Action = 'camera' | 'library' | 'undo';

	let camera = $state<HTMLInputElement>();
	let library = $state<HTMLInputElement>();
	let pending = $state<Action | null>(null);
	let failure = $state<string>();

	async function save(action: Action, photo?: File) {
		pending = action;
		failure = undefined;
		try {
			await request(fetch, photo ? 'PUT' : 'DELETE', `/api/editions/${editionID}/cover`, photo);
			await onchange();
			open = false;
		} catch (err) {
			failure = errorMessage(err);
		} finally {
			pending = null;
		}
	}

	/** Uploads the chosen photo at once. The input is emptied first: an input keeps its file, and
	 * choosing the same file again after a failure would fire no change. */
	function picked(action: Action, input: HTMLInputElement) {
		const photo = input.files?.[0];
		input.value = '';
		if (!photo || pending) return;
		const problem = photoProblem(photo);
		if (problem) failure = problem;
		else void save(action, photo);
	}
</script>

<Modal
	bind:open
	title="Zmień okładkę"
	description="JPEG lub PNG, do 8 MiB i 16 megapikseli."
	closeLabel="Zamknij"
	busy={pending !== null}
	error={failure}
	onclose={() => (failure = undefined)}
>
	<div class="choices">
		<Button loading={pending === 'camera'} onclick={() => camera?.click()}>Zrób zdjęcie</Button>
		<Button loading={pending === 'library'} onclick={() => library?.click()}>
			Wybierz z biblioteki
		</Button>
		{#if undo}
			<Button
				tone={undo === 'remove' ? 'stop-quiet' : 'ghost'}
				loading={pending === 'undo'}
				onclick={() => save('undo')}
			>
				{undo === 'remove' ? 'Usuń zdjęcie' : 'Przywróć okładkę'}
			</Button>
		{/if}
	</div>
	<input
		bind:this={camera}
		type="file"
		accept="image/jpeg,image/png"
		capture="environment"
		hidden
		tabindex="-1"
		onchange={(event) => picked('camera', event.currentTarget)}
	/>
	<input
		bind:this={library}
		type="file"
		accept="image/jpeg,image/png"
		hidden
		tabindex="-1"
		onchange={(event) => picked('library', event.currentTarget)}
	/>
</Modal>

<style>
	.choices {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: 0.5rem;
	}
	.choices :global(.sui-button) {
		inline-size: 100%;
		max-inline-size: none;
	}
</style>
