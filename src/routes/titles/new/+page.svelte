<script lang="ts">
	import { BackLink, PageHeader } from '@smykla-skalski/sui';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { ApiError, errorMessage, request } from '#lib/api.js';
	import { titlePayload, titleToForm, type FormIssue, type Title } from '#lib/collection.js';
	import TitleFormFields from '#lib/components/TitleFormFields.svelte';

	const requested = page.url.searchParams.get('kind');
	let form = $state(
		titleToForm(null, requested === 'book' || requested === 'music' ? requested : 'movie')
	);
	let issue = $state<FormIssue | null>(null);
	let saving = $state(false);

	async function save() {
		const parsed = titlePayload(form);
		if (!parsed.ok) {
			issue = parsed.issue;
			return;
		}
		saving = true;
		try {
			const created = await request<Title>(fetch, 'POST', '/api/titles', parsed.value);
			await goto(`/titles/${created.id}`);
		} catch (err) {
			issue = {
				field: err instanceof ApiError ? (err.field ?? '') : '',
				message: errorMessage(err)
			};
		} finally {
			saving = false;
		}
	}
</script>

<svelte:head>
	<title>Nowy tytuł · Ohara</title>
</svelte:head>

<PageHeader title="Nowy tytuł" description="Po zapisaniu tytułu możesz dodać wydanie i egzemplarz.">
	{#snippet kicker()}
		<BackLink href="/" label="Kolekcja" />
	{/snippet}
</PageHeader>

<div class="content">
	<TitleFormFields
		bind:form
		{issue}
		{saving}
		collapseDetails
		submitLabel="Zapisz tytuł"
		onsubmit={save}
		oncancel={() => goto('/')}
	/>
</div>

<style>
	.content {
		padding: var(--sui-pane-gutter, 1.5rem);
	}
</style>
