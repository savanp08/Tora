<script lang="ts">
	import { browser } from '$app/environment';
	import { onMount, onDestroy } from 'svelte';
	import { isDarkMode } from '$lib/store';
	import './layout.css';

	type ThemePreference = 'system' | 'dark' | 'light';
	type TooltipPlacement = 'top' | 'bottom';

	const THEME_PREFERENCE_KEY = 'converse_theme_preference';
	const TOOLTIP_DATA_ATTR = 'data-instant-tooltip-title';

	let systemThemeMediaQuery: MediaQueryList | null = null;
	let removeSystemThemeListener: (() => void) | null = null;
	let themePreference: ThemePreference = 'system';
	let tooltipVisible = false;
	let tooltipText = '';
	let tooltipX = 0;
	let tooltipY = 0;
	let tooltipPlacement: TooltipPlacement = 'top';
	let activeTooltipElement: HTMLElement | null = null;

	onMount(() => {
		if (!browser) {
			return;
		}
		initializeThemePreference();
		registerTooltipListeners();
		return () => {
			removeTooltipListeners();
			hideTooltip();
			if (removeSystemThemeListener) {
				removeSystemThemeListener();
				removeSystemThemeListener = null;
			}
		};
	});

	onDestroy(() => {
		removeTooltipListeners();
		hideTooltip();
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

	function registerTooltipListeners() {
		if (!browser) {
			return;
		}
		document.addEventListener('pointerover', handlePointerOver, true);
		document.addEventListener('pointerout', handlePointerOut, true);
		document.addEventListener('focusin', handleFocusIn, true);
		document.addEventListener('focusout', handleFocusOut, true);
		document.addEventListener('keydown', handleKeydown, true);
		window.addEventListener('scroll', handleViewportChange, true);
		window.addEventListener('resize', handleViewportChange);
	}

	function removeTooltipListeners() {
		if (!browser) {
			return;
		}
		document.removeEventListener('pointerover', handlePointerOver, true);
		document.removeEventListener('pointerout', handlePointerOut, true);
		document.removeEventListener('focusin', handleFocusIn, true);
		document.removeEventListener('focusout', handleFocusOut, true);
		document.removeEventListener('keydown', handleKeydown, true);
		window.removeEventListener('scroll', handleViewportChange, true);
		window.removeEventListener('resize', handleViewportChange);
	}

	function resolveTooltipText(element: HTMLElement): string {
		const preserved = element.getAttribute(TOOLTIP_DATA_ATTR);
		if (typeof preserved === 'string' && preserved.trim().length > 0) {
			return preserved.trim();
		}
		const nativeTitle = element.getAttribute('title');
		if (typeof nativeTitle === 'string' && nativeTitle.trim().length > 0) {
			return nativeTitle.trim();
		}
		return '';
	}

	function resolveTooltipTarget(target: EventTarget | null): HTMLElement | null {
		if (!(target instanceof Element)) {
			return null;
		}
		const element = target.closest<HTMLElement>(`[title], [${TOOLTIP_DATA_ATTR}]`);
		if (!element) {
			return null;
		}
		return resolveTooltipText(element).length > 0 ? element : null;
	}

	function stashNativeTitle(element: HTMLElement) {
		const title = element.getAttribute('title');
		if (title === null) {
			return;
		}
		if (!element.hasAttribute(TOOLTIP_DATA_ATTR)) {
			element.setAttribute(TOOLTIP_DATA_ATTR, title);
		}
		element.removeAttribute('title');
	}

	function restoreNativeTitle(element: HTMLElement) {
		const preserved = element.getAttribute(TOOLTIP_DATA_ATTR);
		if (preserved === null) {
			return;
		}
		element.setAttribute('title', preserved);
		element.removeAttribute(TOOLTIP_DATA_ATTR);
	}

	function clamp(value: number, min: number, max: number) {
		return Math.min(max, Math.max(min, value));
	}

	function updateTooltipPosition(element: HTMLElement) {
		if (!browser) {
			return;
		}
		const rect = element.getBoundingClientRect();
		const gap = 10;
		const minEdge = 20;
		tooltipX = clamp(rect.left + rect.width / 2, minEdge, window.innerWidth - minEdge);
		if (rect.top < 56) {
			tooltipPlacement = 'bottom';
			tooltipY = rect.bottom + gap;
			return;
		}
		tooltipPlacement = 'top';
		tooltipY = rect.top - gap;
	}

	function showTooltip(element: HTMLElement) {
		const text = resolveTooltipText(element);
		if (!text) {
			hideTooltip();
			return;
		}
		if (activeTooltipElement && activeTooltipElement !== element) {
			restoreNativeTitle(activeTooltipElement);
		}
		activeTooltipElement = element;
		tooltipText = text;
		stashNativeTitle(element);
		updateTooltipPosition(element);
		tooltipVisible = true;
	}

	function hideTooltip() {
		if (activeTooltipElement) {
			restoreNativeTitle(activeTooltipElement);
		}
		activeTooltipElement = null;
		tooltipVisible = false;
		tooltipText = '';
	}

	function handlePointerOver(event: PointerEvent) {
		const target = resolveTooltipTarget(event.target);
		if (!target) {
			return;
		}
		if (target === activeTooltipElement) {
			updateTooltipPosition(target);
			return;
		}
		showTooltip(target);
	}

	function handlePointerOut(event: PointerEvent) {
		if (!activeTooltipElement) {
			return;
		}
		const relatedTarget = event.relatedTarget;
		if (relatedTarget instanceof Node && activeTooltipElement.contains(relatedTarget)) {
			return;
		}
		const nextTarget = resolveTooltipTarget(relatedTarget);
		if (nextTarget) {
			showTooltip(nextTarget);
			return;
		}
		hideTooltip();
	}

	function handleFocusIn(event: FocusEvent) {
		const target = resolveTooltipTarget(event.target);
		if (!target) {
			return;
		}
		showTooltip(target);
	}

	function handleFocusOut(event: FocusEvent) {
		if (!activeTooltipElement) {
			return;
		}
		const nextTarget = resolveTooltipTarget(event.relatedTarget);
		if (nextTarget) {
			showTooltip(nextTarget);
			return;
		}
		hideTooltip();
	}

	function handleKeydown(event: KeyboardEvent) {
		if (event.key === 'Escape') {
			hideTooltip();
		}
	}

	function handleViewportChange() {
		if (!activeTooltipElement) {
			return;
		}
		if (!document.contains(activeTooltipElement)) {
			hideTooltip();
			return;
		}
		updateTooltipPosition(activeTooltipElement);
	}
</script>

<slot />
<div
	class="instant-tooltip"
	class:visible={tooltipVisible}
	class:placement-top={tooltipPlacement === 'top'}
	class:placement-bottom={tooltipPlacement === 'bottom'}
	role="tooltip"
	aria-hidden={!tooltipVisible}
	style={`left: ${tooltipX}px; top: ${tooltipY}px;`}
>
	{tooltipText}
</div>
