<script lang="ts">
	import { IconButton, LinkList, Modal, ThemeSwitch } from '@smykla-skalski/sui';
	import { morePages, sections } from '#lib/nav.js';

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
	   drawn, so the gear shows only with the bar. */
	@container sui-shell (max-width: 47.9375rem) {
		.settings-open {
			display: inline-flex;
		}
	}

	.settings {
		display: grid;
		gap: var(--sui-space-4, 1rem);
	}
</style>
