<script lang="ts">
	import { ActionMenu } from '@smykla-skalski/sui';
	import { exportChoices, exportHref } from '#lib/export.js';

	let { toolbar = false }: { toolbar?: boolean } = $props();

	/** Downloads href: the server names the file and sends it as an attachment. */
	function download(href: string) {
		const link = document.createElement('a');
		link.href = href;
		link.download = '';
		document.body.append(link);
		link.click();
		link.remove();
	}

	const items = exportChoices.map((choice) => ({
		label: choice.label,
		description: choice.description,
		onselect: () => download(exportHref(choice.format))
	}));
</script>

<ActionMenu label="Eksportuj kolekcję" icon="file" {toolbar} align="end" {items} />
