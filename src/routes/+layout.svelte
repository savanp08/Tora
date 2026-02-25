<script lang="ts">
	import { browser } from '$app/environment';
	import { onMount, onDestroy } from 'svelte';
	import { isDarkMode } from '$lib/store';
	import './layout.css';

	type ThemePreference = 'system' | 'dark' | 'light';

	const THEME_PREFERENCE_KEY = 'converse_theme_preference';
	let systemThemeMediaQuery: MediaQueryList | null = null;
	let removeSystemThemeListener: (() => void) | null = null;
	let themePreference: ThemePreference = 'system';

	onMount(() => {
		if (!browser) {
			return;
		}
		initializeThemePreference();
		return () => {
			if (removeSystemThemeListener) {
				removeSystemThemeListener();
				removeSystemThemeListener = null;
			}
		};
	});

	onDestroy(() => {
		if (removeSystemThemeListener) {
			removeSystemThemeListener();
			removeSystemThemeListener = null;
		}
		systemThemeMediaQuery = null;
	});

	$: if (browser) {
		document.body.classList.toggle('theme-dark', $isDarkMode);
		document.body.dataset.theme = $isDarkMode ? 'dark' : 'light';
	}

	function initializeThemePreference() {
		if (!browser) {
			return;
		}
		systemThemeMediaQuery = window.matchMedia('(prefers-color-scheme: dark)');
		registerSystemThemeListener();
		const saved = window.localStorage.getItem(THEME_PREFERENCE_KEY);
		if (saved === 'dark' || saved === 'light' || saved === 'system') {
			applyThemePreference(saved as ThemePreference, false);
			return;
		}
		applyThemePreference('system', false);
	}

	function registerSystemThemeListener() {
		if (!systemThemeMediaQuery || removeSystemThemeListener) {
			return;
		}
		const onSystemThemeChange = () => {
			if (themePreference !== 'system') {
				return;
			}
			$isDarkMode = Boolean(systemThemeMediaQuery?.matches);
		};
		if (typeof systemThemeMediaQuery.addEventListener === 'function') {
			systemThemeMediaQuery.addEventListener('change', onSystemThemeChange);
			removeSystemThemeListener = () => {
				systemThemeMediaQuery?.removeEventListener('change', onSystemThemeChange);
			};
			return;
		}
		systemThemeMediaQuery.addListener(onSystemThemeChange);
		removeSystemThemeListener = () => {
			systemThemeMediaQuery?.removeListener(onSystemThemeChange);
		};
	}

	function resolveDarkMode(preference: ThemePreference) {
		if (preference === 'dark') {
			return true;
		}
		if (preference === 'light') {
			return false;
		}
		return Boolean(systemThemeMediaQuery?.matches);
	}

	function applyThemePreference(preference: ThemePreference, persist = true) {
		themePreference = preference;
		$isDarkMode = resolveDarkMode(preference);
		if (browser && persist) {
			window.localStorage.setItem(THEME_PREFERENCE_KEY, preference);
		}
	}
</script>

<slot />
