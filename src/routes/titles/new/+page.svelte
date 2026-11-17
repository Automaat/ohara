<script lang="ts">
	import { BackLink, PageHeader } from '@smykla-skalski/sui';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { ApiError, errorMessage, request } from '$lib/api';
	import { titlePayload, titleToForm, type FormIssue, type Title } from '$lib/collection';
	import TitleFormFields from '$lib/components/TitleFormFields.svelte';

	let form = $state(
		titleToForm(null, page.url.searchParams.get('kind') === 'book' ? 'book' : 'movie')
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

<PageHeader title="Nowy tytuł" description="Najpierw dzieło, potem jego wydania i egzemplarze.">
	{#snippet kicker()}
		<BackLink href="/titles" label="Tytuły" />
	{/snippet}
</PageHeader>

<div class="content">
	<TitleFormFields
		bind:form
		{issue}
		{saving}
		submitLabel="Zapisz tytuł"
		onsubmit={save}
		oncancel={() => goto('/titles')}
	/>
</div>

<style>
	.content {
		padding: var(--sui-pane-gutter, 1.5rem);
	}
</style>
