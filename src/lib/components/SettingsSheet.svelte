<script lang="ts">
	import { IconButton, LinkList, Modal, ThemeSwitch } from '@smykla-skalski/sui';
	import type { Snippet } from 'svelte';
	import { morePages, sections } from '#lib/nav.js';

	/** Page actions the phone header has no room for, drawn under the theme switch. */
	let { children }: { children?: Snippet } = $props();

	let open = $state(false);
	const id = $props.id();

	const pages = morePages(sections).map((item) => ({ href: item.href, label: item.label }));
</script>

<span class="settings-open">
	<IconButton icon="gear" label="Ustawienia" aria-haspopup="dialog" onclick={() => (open = true)} />
</span>

<Modal bind:open title="Ustawienia" closeLabel="Zamknij">
	<div class="settings">
		<ThemeSwitch
			name={`ohara-settings-theme-${id}`}
			label="Motyw"
			hideLabel={false}
			lightLabel="Jasny"
			darkLabel="Ciemny"
			systemLabel="Systemowy"
		/>
		{@render children?.()}
		<nav aria-label="Pozostałe strony">
			<LinkList links={pages} />
		</nav>
	</div>
</Modal>

<style>
	.settings-open {
		display: none;
	}

	/* The side navigation holds the theme switch and these pages wherever the tab bar is not
	   drawn, so the gear shows only with the bar, in the phone layout. */
	:global(.ohara-phone) .settings-open {
		display: inline-flex;
	}

	.settings {
		display: grid;
		gap: var(--sui-space-4, 1rem);
	}
</style>
