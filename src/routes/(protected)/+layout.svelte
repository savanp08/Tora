<script lang="ts">
	import { browser } from '$app/environment';
	import { authState } from '$lib/stores/auth';
	import type { LayoutData } from './$types';

	export let data: LayoutData;

	$: if (browser && data?.user) {
		authState.set({
			isAuthenticated: true,
			user: data.user,
			token: null
		});
	}
</script>

<slot />
