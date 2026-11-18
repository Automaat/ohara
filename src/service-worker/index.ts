import { version } from '$app/env';
import { assets, immutable, prerendered } from '$app/manifest';
import { self } from '$app/service-worker';
import { serve } from '@smykla-skalski/sui/service-worker';

serve(self, {
	version,
	immutable,
	assets,
	prerendered,
	never: [/^api\//, /^health$/, /^metrics$/]
});
