<script lang="ts">
	import { browser } from '$app/environment';
	import { onDestroy, onMount } from 'svelte';
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

	onMount(() => {
		if (!browser) {
			return;
		}
		document.body.classList.add('protected-route-active');
	});

	onDestroy(() => {
		if (!browser) {
			return;
		}
		document.body.classList.remove('protected-route-active');
	});
</script>

<svelte:head>
	<meta name="robots" content="noindex, nofollow, noarchive" />
	<meta name="googlebot" content="noindex, nofollow, noarchive" />
</svelte:head>

<slot />

<style>
	:global(body.protected-route-active) {
		overflow: hidden;
	}
</style>
