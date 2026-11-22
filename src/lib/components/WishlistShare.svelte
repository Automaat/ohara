<script lang="ts">
	import {
		Button,
		CopyButton,
		Disclosure,
		ErrorState,
		Field,
		FormError
	} from '@smykla-skalski/sui';
	import { ApiError, errorMessage, request } from '#lib/api.js';
	import { shareLinkPath, shareURL, type ShareLink } from '#lib/deals.js';

	/**
	 * "Udostępnij listę życzeń" on Okazje: the one read-only link to the wishlist, which anyone who
	 * has it opens without signing in. The link is asked for only once the section is opened.
	 * "Nowy link" replaces it, so the old one stops working, and "Wyłącz link" turns it off.
	 * "Utwórz link" shows only once the server said no link is on: after a failed read only a retry
	 * is offered, since creating would replace a link already sent to someone.
	 */
	let open = $state(false);
	let link = $state<ShareLink | null>(null);
	let known = $state<'unasked' | 'loading' | 'failed' | 'ready'>('unasked');
	let loadFailure = $state('');
	let busy = $state(false);
	let failure = $state<string | null>(null);

	async function load() {
		known = 'loading';
		try {
			link = await request<ShareLink>(fetch, 'GET', shareLinkPath);
			known = 'ready';
		} catch (err) {
			if (err instanceof ApiError && err.status === 404) {
				link = null;
				known = 'ready';
			} else {
				loadFailure = errorMessage(err);
				known = 'failed';
			}
		}
	}

	$effect(() => {
		if (open && known === 'unasked') void load();
	});

	async function change(method: 'POST' | 'DELETE') {
		busy = true;
		try {
			const made = await request<ShareLink | undefined>(fetch, method, shareLinkPath);
			link = method === 'POST' ? (made ?? null) : null;
			failure = null;
		} catch (err) {
			failure = errorMessage(err);
		} finally {
			busy = false;
		}
	}

	const url = $derived(link ? shareURL(link, location.origin) : '');
</script>

<Disclosure summary="Udostępnij listę życzeń" bind:open>
	<div class="share">
		<FormError message={failure} />
		{#if known === 'failed'}
			<ErrorState
				title="Nie udało się wczytać linku"
				description={loadFailure}
				onretry={load}
				retryLabel="Spróbuj ponownie"
				retryingLabel="Ponawianie…"
			/>
		{:else if known !== 'ready'}
			<p role="status">Wczytywanie linku…</p>
		{:else if link}
			<Field
				label="Link do listy życzeń"
				value={url}
				readonly
				hint="Każdy, kto ma link, zobaczy pozycje z listy życzeń bez logowania, ale bez cen, limitów i notatek."
			/>
			<div class="actions">
				<CopyButton
					text={url}
					label="Kopiuj"
					copiedLabel="Skopiowano"
					failedLabel="Nie udało się skopiować"
					toolbar
				/>
				<Button loading={busy} onclick={() => change('POST')}>Nowy link</Button>
				<Button tone="stop-quiet" loading={busy} onclick={() => change('DELETE')}>
					Wyłącz link
				</Button>
			</div>
		{:else}
			<p class="about">
				Link pokaże rodzinie i znajomym tytuły z listy życzeń, bez cen, limitów i notatek. Każdy,
				kto go ma, otworzy listę bez logowania.
			</p>
			<div class="actions">
				<Button tone="signal" loading={busy} onclick={() => change('POST')}>Utwórz link</Button>
			</div>
		{/if}
	</div>
</Disclosure>

<style>
	.share {
		display: grid;
		gap: 0.75rem;
		max-width: 40rem;
	}
	.about {
		margin: 0;
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
		align-items: center;
	}
</style>
