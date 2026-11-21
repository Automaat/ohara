<script lang="ts">
	import { Link } from '@smykla-skalski/sui';
	import { discogsCredit, discogsReleaseUrl } from '#lib/discogs.js';

	let {
		release,
		class: className = ''
	}: {
		/** The Discogs release the data shown next to the credit comes from. */
		release: number | null | undefined;
		class?: string;
	} = $props();

	const href = $derived(discogsReleaseUrl(release));
</script>

<!--
The attribution the Discogs API terms ask for next to Discogs data: their words in English,
linked to the release's page, without nofollow. Nothing renders without a release.
-->
{#if href}
	<p class={['credit', className]}>
		<Link {href} target="_blank" externalLabel="(otwiera inną stronę)">
			<span lang="en">{discogsCredit}</span>
		</Link>
	</p>
{/if}

<style>
	.credit {
		color: var(--sui-text-secondary);
		font-family: var(--sui-sans);
		font-size: 0.75rem;
		margin: 0;
	}
</style>
