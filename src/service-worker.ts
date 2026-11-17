/// <reference types="@sveltejs/kit" />
/// <reference no-default-lib="true"/>
/// <reference lib="esnext" />
/// <reference lib="webworker" />
import { build, files, prerendered, version } from '$service-worker';
import { serve } from '@smykla-skalski/sui/service-worker';

const toFiles = (paths: string[]) => paths.map((path) => ({ path }));

serve(self as unknown as ServiceWorkerGlobalScope, {
	version,
	immutable: toFiles(build),
	assets: toFiles(files),
	prerendered: toFiles(prerendered),
	never: [/^api\//, /^health$/, /^metrics$/]
});
