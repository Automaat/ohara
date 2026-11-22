<script lang="ts">
	import { Button, Modal } from '@smykla-skalski/sui';
	import { errorMessage, request } from '#lib/api.js';
	import {
		browserCodec,
		coverDeadlineMs,
		PhotoError,
		shrinkPhoto,
		type CoverUndo
	} from '#lib/edition-cover.js';

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
		/** Shows the changed cover, after the sheet has closed. */
		onchange: () => Promise<void>;
	} = $props();

	type Action = 'camera' | 'library' | 'undo';

	let camera = $state<HTMLInputElement>();
	let library = $state<HTMLInputElement>();
	let pending = $state<Action | null>(null);
	let failure = $state<string>();

	/** Sends one change at a time: a second one started while the first is unanswered could land
	 * first and be overwritten by it. A photo is shrunk first; one the browser cannot read sends
	 * nothing. */
	async function save(action: Action, photo?: File) {
		if (pending) return;
		pending = action;
		failure = undefined;
		try {
			const body = photo ? await shrinkPhoto(photo, browserCodec) : undefined;
			await request(
				fetch,
				body ? 'PUT' : 'DELETE',
				`/api/editions/${editionID}/cover`,
				body,
				coverDeadlineMs
			);
		} catch (err) {
			failure = err instanceof PhotoError ? err.message : errorMessage(err);
			return;
		} finally {
			pending = null;
		}
		open = false;
		await onchange();
	}

	/** Uploads the chosen photo at once. The input is emptied first: an input keeps its file, and
	 * choosing the same file again after a failure would fire no change. */
	function picked(action: Action, input: HTMLInputElement) {
		const photo = input.files?.[0];
		input.value = '';
		if (photo && !pending) void save(action, photo);
	}

	function choose(input: HTMLInputElement | undefined) {
		if (!pending) input?.click();
	}
</script>

<Modal
	bind:open
	title="Zmień okładkę"
	description="JPEG lub PNG. Duże zdjęcia zostaną zmniejszone."
	closeLabel="Zamknij"
	busy={pending !== null}
	error={failure}
	onclose={() => (failure = undefined)}
>
	<div class="choices">
		<Button
			loading={pending === 'camera'}
			disabled={pending !== null && pending !== 'camera'}
			onclick={() => choose(camera)}
		>
			Zrób zdjęcie
		</Button>
		<Button
			loading={pending === 'library'}
			disabled={pending !== null && pending !== 'library'}
			onclick={() => choose(library)}
		>
			Wybierz z biblioteki
		</Button>
		{#if undo}
			<Button
				tone={undo === 'remove' ? 'stop-quiet' : 'ghost'}
				loading={pending === 'undo'}
				disabled={pending !== null && pending !== 'undo'}
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
