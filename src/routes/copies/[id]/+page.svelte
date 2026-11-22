<script lang="ts">
	import {
		BackLink,
		Button,
		ConfirmDialog,
		ErrorState,
		Field,
		FormError,
		Modal,
		PageHeader,
		Skeleton
	} from '@smykla-skalski/sui';
	import { SvelteSet } from 'svelte/reactivity';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { ApiError, errorMessage, issueFrom, request } from '#lib/api.js';
	import {
		copyPayload,
		copyToForm,
		displayName,
		editionSummary,
		isoDate,
		loanPayload,
		loanSummary,
		loanToForm,
		returnDay,
		statusLabels,
		type Copy,
		type CopyDetail,
		type FormIssue,
		type Loan
	} from '#lib/collection.js';
	import CopyFormFields from '#lib/components/CopyFormFields.svelte';
	import { LatestLoad } from '#lib/scan.js';
	import { TitleMarkets } from '#lib/title-market.svelte.js';

	const id = $derived(Number(page.params.id));

	let detail = $state<CopyDetail | null>(null);
	let failure = $state<string | null>(null);
	let busy = $state(false);
	let form = $state(copyToForm(null));
	let issue = $state<FormIssue | null>(null);
	/**
	 * Copies whose save is still on its way, kept across a jump to another copy and back, so a
	 * second save of the same copy cannot start until the first one has landed.
	 */
	const pendingSaves = new SvelteSet<number>();
	const saving = $derived(detail !== null && pendingSaves.has(detail.id));
	let saved = $state(false);
	let confirmDelete = $state(false);
	let deleteError = $state<string | null>(null);

	/** The copy's loan still out, if any: it offers "Zwrócono" instead of "Wypożycz". */
	const openLoan = $derived(detail?.loans.find((l) => l.returned_on === null) ?? null);
	let lending = $state(false);
	let loanForm = $state(loanToForm(''));
	let loanIssue = $state<FormIssue | null>(null);
	let loanBusy = $state(false);
	let loanError = $state<string | null>(null);
	const loanFields = ['borrower', 'lent_on', 'due_on'];
	const loanErrorFor = (field: string) =>
		loanIssue?.field === field ? loanIssue.message : undefined;
	const loanGeneral = $derived(
		loanIssue && !loanFields.includes(loanIssue.field) ? loanIssue.message : null
	);

	/** The copy the page was last asked for; another one's answers and forms are dropped. */
	let requested: number | undefined;
	/** A slower answer for an earlier load never replaces a newer one. */
	const loads = new LatestLoad();

	/**
	 * Reads the copy. A refresh after a loan keeps the form, so an edit not yet saved stays; a
	 * load of another copy or a retry fills it from the answer.
	 */
	async function load(copyID: number, keepForm = false) {
		if (copyID !== requested) {
			requested = copyID;
			detail = null;
			failure = null;
			form = copyToForm(null);
			issue = null;
			saved = false;
			confirmDelete = false;
			deleteError = null;
			lending = false;
			loanIssue = null;
			loanError = null;
		}
		const isLatest = loads.start();
		busy = true;
		try {
			const next = await request<CopyDetail>(fetch, 'GET', `/api/copies/${copyID}`);
			if (!isLatest()) return;
			detail = next;
			if (!keepForm) form = copyToForm(next);
			failure = null;
		} catch (err) {
			if (!isLatest()) return;
			failure = errorMessage(err);
		} finally {
			if (isLatest()) busy = false;
		}
	}

	$effect(() => {
		void load(id);
	});

	/** The wish's market price, asked once the form shows a wish's limit. */
	const markets = new TitleMarkets(fetch);
	$effect(() => {
		if (detail && form.status === 'wishlist') void markets.load(detail.title.id);
	});

	/** Saves the form, or with `received` puts the ordered copy as stored on the shelf. */
	async function save(d: CopyDetail, received = false) {
		const parsed = received
			? copyPayload({ ...copyToForm(d), status: 'owned' }, d.edition_id)
			: copyPayload(form, d.edition_id);
		if (!parsed.ok) {
			issue = parsed.issue;
			return;
		}
		if (pendingSaves.has(d.id)) return;
		pendingSaves.add(d.id);
		saved = false;
		try {
			await request<Copy>(fetch, 'PUT', `/api/copies/${d.id}`, parsed.value);
			if (d.id !== requested) return;
			issue = null;
			saved = true;
			await load(d.id);
		} catch (err) {
			if (d.id !== requested) return;
			issue = issueFrom(err);
		} finally {
			pendingSaves.delete(d.id);
		}
	}

	function startLoan() {
		loanForm = loanToForm(isoDate(new Date()));
		loanIssue = null;
		loanError = null;
		lending = true;
	}

	async function lend(d: CopyDetail) {
		const parsed = loanPayload(loanForm);
		if (!parsed.ok) {
			loanIssue = parsed.issue;
			return;
		}
		if (loanBusy) return;
		loanBusy = true;
		try {
			await request<Loan>(fetch, 'POST', `/api/copies/${d.id}/loans`, parsed.value);
			if (d.id !== requested) return;
			lending = false;
			await load(d.id, true);
		} catch (err) {
			if (d.id !== requested) return;
			if (err instanceof ApiError && err.status === 409) {
				// Another tab lent it meanwhile, or it is no longer on the shelf.
				loanIssue = { field: '', message: 'Tego egzemplarza nie można teraz wypożyczyć.' };
				await load(d.id, true);
			} else {
				loanIssue = issueFrom(err);
			}
		} finally {
			loanBusy = false;
		}
	}

	async function giveBack(d: CopyDetail, loan: Loan) {
		if (loanBusy) return;
		loanBusy = true;
		loanError = null;
		try {
			await request<Loan>(fetch, 'PUT', `/api/loans/${loan.id}`, {
				returned_on: returnDay(loan.lent_on, isoDate(new Date()))
			});
			if (d.id === requested) await load(d.id, true);
		} catch (err) {
			if (d.id !== requested) return;
			if (err instanceof ApiError && err.status === 409) {
				// Another tab recorded the return first; its day stays.
				loanError = 'Ten egzemplarz już wrócił.';
				await load(d.id, true);
			} else {
				loanError = errorMessage(err);
			}
		} finally {
			loanBusy = false;
		}
	}

	async function remove(d: CopyDetail) {
		try {
			await request(fetch, 'DELETE', `/api/copies/${d.id}`);
			if (d.id === requested) await goto(`/editions/${d.edition_id}`);
		} catch (err) {
			if (d.id === requested) deleteError = errorMessage(err);
		}
	}
</script>

<svelte:head>
	<title>Egzemplarz · Ohara</title>
</svelte:head>

{#if failure}
	<div class="content">
		<ErrorState
			title="Nie udało się wczytać egzemplarza"
			description={failure}
			onretry={() => load(id)}
			{busy}
			retryLabel="Spróbuj ponownie"
			retryingLabel="Wczytywanie…"
		/>
	</div>
{:else if !detail}
	<div class="content"><Skeleton /></div>
{:else}
	{@const d = detail}
	<PageHeader
		title={`${statusLabels[d.status]}: ${displayName(d.title)}`}
		description={editionSummary(d.edition)}
	>
		{#snippet kicker()}
			<BackLink href={`/editions/${d.edition_id}`} label={editionSummary(d.edition)} />
		{/snippet}
		{#snippet actions()}
			{#if openLoan}
				{@const loan = openLoan}
				<Button tone="signal" loading={loanBusy} onclick={() => giveBack(d, loan)}>Zwrócono</Button>
			{:else if d.status === 'owned'}
				<Button onclick={startLoan}>Wypożycz</Button>
			{:else if d.status === 'ordered'}
				<!-- Receiving keeps the price, order date and shop; only the status changes. -->
				<Button tone="signal" loading={saving} onclick={() => save(d, true)}>Odebrano</Button>
			{/if}
			<Button tone="stop-quiet" onclick={() => ((deleteError = null), (confirmDelete = true))}>
				Usuń
			</Button>
		{/snippet}
	</PageHeader>

	<div class="content">
		<FormError message={deleteError} />
		<FormError message={loanError} />
		{#if d.loans.length}
			{@const today = isoDate(new Date())}
			<section class="loans" aria-labelledby="loans-heading">
				<h2 id="loans-heading">Wypożyczenia</h2>
				<ul>
					{#each d.loans as loan (loan.id)}
						<li class:out={loan.returned_on === null}>{loanSummary(loan, today)}</li>
					{/each}
				</ul>
			</section>
		{/if}
		{#if saved}
			<p role="status">Zapisano.</p>
		{/if}
		<CopyFormFields
			bind:form
			{issue}
			{saving}
			submitLabel="Zapisz zmiany"
			market={markets.for(d.title.id, d.edition.format)}
			onsubmit={() => save(d)}
		/>
	</div>

	<Modal bind:open={lending} title="Wypożycz egzemplarz" closeLabel="Zamknij">
		<form
			id="loan-form"
			class="loan-form"
			novalidate
			onsubmit={(e) => {
				e.preventDefault();
				void lend(d);
			}}
		>
			<FormError message={loanGeneral} />
			<Field
				label="Komu"
				required
				requiredLabel="Wymagane"
				bind:value={loanForm.borrower}
				error={loanErrorFor('borrower')}
			/>
			<Field
				label="Data wypożyczenia"
				placeholder="RRRR-MM-DD"
				bind:value={loanForm.lent_on}
				error={loanErrorFor('lent_on')}
			/>
			<Field
				label="Termin zwrotu"
				hint="Zostaw puste, jeśli bez terminu."
				placeholder="RRRR-MM-DD"
				bind:value={loanForm.due_on}
				error={loanErrorFor('due_on')}
			/>
		</form>
		{#snippet footer()}
			<Button tone="signal" type="submit" form="loan-form" loading={loanBusy}>Wypożycz</Button>
		{/snippet}
	</Modal>

	<ConfirmDialog
		bind:open={confirmDelete}
		title="Usunąć egzemplarz?"
		consequence="Egzemplarz i jego cena znikną z kolekcji. Tego nie można cofnąć."
		confirmLabel="Usuń egzemplarz"
		cancelLabel="Anuluj"
		closeLabel="Zamknij"
		tone="stop"
		onconfirm={() => remove(d)}
	/>
{/if}

<style>
	.content {
		display: grid;
		gap: 1.5rem;
		padding: var(--sui-pane-gutter, 1.5rem);
	}

	.loans h2 {
		margin: 0 0 0.5rem;
		font-size: 1rem;
	}

	/* A borrower is free text up to 500 bytes, often without a space to break at. */
	.loans ul {
		overflow-wrap: anywhere;
		display: grid;
		gap: 0.25rem;
		margin: 0;
		padding: 0;
		list-style: none;
		color: var(--sui-text-muted);
	}

	.loans .out {
		color: var(--sui-text-primary);
		font-weight: 600;
	}

	.loan-form {
		display: grid;
		gap: var(--sui-space-4);
	}
</style>
