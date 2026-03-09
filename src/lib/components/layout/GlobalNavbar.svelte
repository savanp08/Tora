<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { onDestroy, onMount } from 'svelte';
	import { fade, scale } from 'svelte/transition';
	import toraLogo from '$lib/assets/tora-logo.svg';
	import { authState, logout } from '$lib/stores/auth';

	export let isHighContrast = false;
	export let scrollY = 0;

	type NavLink = { label: string; href: string };
	type QuickAction =
		| 'create-room'
		| 'open-room-list'
		| 'open-chat-pane'
		| 'toggle-search'
		| 'toggle-discussion-mode'
		| 'open-board-dashboard'
		| 'open-board-draw'
		| 'open-board-code'
		| 'open-board-tasks'
		| 'mark-active-read';
	type ChatQuickState = {
		isCompact: boolean;
		pane: 'list' | 'chat';
		totalUnread: number;
		activeUnread: number;
		discussionUnread: number;
		boardUnread: number;
	};
	type MobileMenuItem = {
		label: string;
		href?: string;
		quickAction?: QuickAction;
		badge?: number;
	};

	const APP_NAV_LINKS: NavLink[] = [
		{ label: 'DASHBOARD', href: '/dashboard' },
		{ label: 'ROOMS', href: '/rooms' },
		{ label: 'TASKS', href: '/tasks' }
	];
	const PUBLIC_NAV_LINKS: NavLink[] = [
		{ label: 'HOME', href: '/' },
		{ label: 'LOGIN', href: '/login' }
	];
	const MOBILE_BREAKPOINT = 600;
	const FAB_SIZE_PX = 56;
	const FAB_PADDING_PX = 15;

	let isHovered = false;
	let innerWidth = 0;
	let innerHeight = 0;
	let navLinks: NavLink[] = APP_NAV_LINKS;
	let mobileMenuTitle = 'SYSTEM_NAV';
	let mobileMenuItems: MobileMenuItem[] = [];
	let chatQuickState: ChatQuickState | null = null;

	let isProfileMenuOpen = false;
	let profileMenuRoot: HTMLDivElement | null = null;

	$: pathname = $page.url.pathname;
	$: isGhostMode = scrollY < 50 && !isHovered;
	$: navLinks = buildNavLinks(pathname, $authState.isAuthenticated);
	$: hideDesktopNavForRoute = pathname.startsWith('/chat/') || pathname === '/rooms' || pathname.startsWith('/rooms/');
	$: activeLabel =
		navLinks.find((link) => isPathActiveForNavLink(pathname, link.href))?.label ??
		navLinks[0]?.label ??
		'';
	$: desktopNavVisible = innerWidth >= MOBILE_BREAKPOINT && !hideDesktopNavForRoute;
	$: mobileFabVisible = innerWidth > 0 && innerWidth < MOBILE_BREAKPOINT;
	$: mobileMenuConfig = buildMobileMenu(pathname, $authState.isAuthenticated, chatQuickState);
	$: mobileMenuTitle = mobileMenuConfig.title;
	$: mobileMenuItems = mobileMenuConfig.items;
	$: if (!mobileFabVisible) {
		isMobileMenuOpen = false;
	}

	// --- MOBILE DRAGGABLE STATE ---
	let isMobileMenuOpen = false;
	let fabPosition = { x: 0, y: 0 };
	let isDragging = false;
	let isPressed = false;
	let dragStartPos = { x: 0, y: 0 };
	let dragOffset = { x: 0, y: 0 };
	let fabElement: HTMLButtonElement | null = null;
	let isSnapping = false;

	// --- SMART MENU POSITIONING ---
	$: menuPosition = (() => {
		const menuWidth = 240;
		const menuHeight =
			64 +
			mobileMenuItems.length * 48 +
			($authState.isAuthenticated ? 136 : 72);
		const gap = 12;

		let top = 0;
		let left = 0;

		if (fabPosition.y + FAB_SIZE_PX + gap + menuHeight > innerHeight) {
			top = fabPosition.y - menuHeight - gap;
		} else {
			top = fabPosition.y + FAB_SIZE_PX + gap;
		}

		if (fabPosition.x + FAB_SIZE_PX / 2 > innerWidth / 2) {
			left = fabPosition.x + FAB_SIZE_PX - menuWidth;
		} else {
			left = fabPosition.x;
		}

		left = Math.max(10, Math.min(innerWidth - menuWidth - 10, left));
		top = Math.max(10, Math.min(innerHeight - menuHeight - 10, top));

		return { top, left };
	})();

	$: userDisplayName = $authState.user?.name?.trim() || 'User';
	$: userEmail = $authState.user?.email?.trim() || '';
	$: userAvatarUrl = $authState.user?.avatarUrl?.trim() || '';
	$: userInitials = (() => {
		const parts = userDisplayName.split(/\s+/).filter(Boolean);
		if (parts.length === 0) {
			return 'U';
		}
		if (parts.length === 1) {
			return parts[0].slice(0, 1).toUpperCase();
		}
		return `${parts[0].slice(0, 1)}${parts[1].slice(0, 1)}`.toUpperCase();
	})();

	function isWorkspacePath(pathnameValue: string) {
		return (
			pathnameValue === '/dashboard' ||
			pathnameValue.startsWith('/dashboard/') ||
			pathnameValue === '/rooms' ||
			pathnameValue.startsWith('/rooms/') ||
			pathnameValue === '/tasks' ||
			pathnameValue.startsWith('/tasks/')
		);
	}

	function buildNavLinks(pathnameValue: string, isAuthenticated: boolean): NavLink[] {
		if (pathnameValue.startsWith('/chat/')) {
			if (isAuthenticated) {
				return [{ label: 'ROOM', href: pathnameValue }, ...APP_NAV_LINKS];
			}
			return [
				{ label: 'HOME', href: '/' },
				{ label: 'ROOM', href: pathnameValue },
				...PUBLIC_NAV_LINKS.slice(1)
			];
		}
		if (isWorkspacePath(pathnameValue)) {
			return APP_NAV_LINKS;
		}
		if (isAuthenticated) {
			return [{ label: 'HOME', href: '/' }, ...APP_NAV_LINKS];
		}
		return PUBLIC_NAV_LINKS;
	}

	function buildMobileMenu(
		pathnameValue: string,
		isAuthenticated: boolean,
		chatState: ChatQuickState | null
	) {
		if (pathnameValue.startsWith('/chat/')) {
			const normalizedState = chatState ?? {
				isCompact: true,
				pane: 'chat' as const,
				totalUnread: 0,
				activeUnread: 0,
				discussionUnread: 0,
				boardUnread: 0
			};
			if (normalizedState.pane === 'list') {
				const items: MobileMenuItem[] = [
					{ label: 'Create Room', quickAction: 'create-room' },
					{
						label: 'Unread Messages',
						quickAction: 'open-chat-pane',
						badge: normalizedState.totalUnread
					},
					{
						label: 'Board Changes',
						quickAction: 'open-board-dashboard',
						badge: normalizedState.boardUnread
					},
					{
						label: 'Discussion Updates',
						quickAction: 'toggle-discussion-mode',
						badge: normalizedState.discussionUnread
					},
					{ label: 'Home', href: '/' }
				];
				return {
					title: 'EPHEMERAL_LIST',
					items
				};
			}
			const chatItems: MobileMenuItem[] = [
				{ label: 'Dashboard Board', quickAction: 'open-board-dashboard' },
				{ label: 'Draw Board', quickAction: 'open-board-draw' },
				{ label: 'Task Board', quickAction: 'open-board-tasks' },
				{ label: 'Code Board', quickAction: 'open-board-code' },
				{ label: 'Search', quickAction: 'toggle-search' },
				{
					label: 'Discussions',
					quickAction: 'toggle-discussion-mode',
					badge: normalizedState.discussionUnread
				},
				{
					label: 'Rooms',
					quickAction: 'open-room-list',
					badge: normalizedState.totalUnread
				},
				{ label: 'Create Room', quickAction: 'create-room' }
			];
			if (normalizedState.activeUnread > 0) {
				chatItems.push({
					label: 'Mark Active Read',
					quickAction: 'mark-active-read',
					badge: normalizedState.activeUnread
				});
			}
			return {
				title: 'EPHEMERAL_CHAT',
				items: chatItems
			};
		}
		if (isWorkspacePath(pathnameValue)) {
			return {
				title: 'ACCOUNT_NAV',
				items: [
					{ label: 'Dashboard', href: '/dashboard' },
					{ label: 'Taskboard', href: '/tasks' },
					{ label: 'Messenger', href: '/rooms' }
				]
			};
		}
		if (!isAuthenticated || pathnameValue === '/' || pathnameValue === '/login') {
			return {
				title: 'PUBLIC_NAV',
				items: [
					{ label: 'Login', href: '/login' },
					{ label: 'Home', href: '/' },
					{ label: 'Ephemeral', href: '/rooms' }
				]
			};
		}
		return {
			title: 'SYSTEM_NAV',
			items: [
				{ label: 'Dashboard', href: '/dashboard' },
				{ label: 'Taskboard', href: '/tasks' },
				{ label: 'Messenger', href: '/rooms' }
			]
		};
	}

	function isPathActiveForNavLink(pathnameValue: string, href: string) {
		if (href === '/') {
			return pathnameValue === '/';
		}
		if (href.startsWith('/chat/')) {
			return pathnameValue.startsWith('/chat/');
		}
		return pathnameValue === href || pathnameValue.startsWith(`${href}/`);
	}

	function normalizePositiveCount(value: unknown) {
		if (!Number.isFinite(value)) {
			return 0;
		}
		return Math.max(0, Math.floor(Number(value)));
	}

	function parseChatQuickState(value: unknown): ChatQuickState | null {
		if (!value || typeof value !== 'object' || Array.isArray(value)) {
			return null;
		}
		const source = value as Record<string, unknown>;
		const paneValue = source.pane === 'list' ? 'list' : 'chat';
		return {
			isCompact: Boolean(source.isCompact),
			pane: paneValue,
			totalUnread: normalizePositiveCount(source.totalUnread),
			activeUnread: normalizePositiveCount(source.activeUnread),
			discussionUnread: normalizePositiveCount(source.discussionUnread),
			boardUnread: normalizePositiveCount(source.boardUnread)
		};
	}

	function handleDragStart(e: MouseEvent | TouchEvent) {
		const target = e.target as HTMLElement;
		if (!target.closest('.holo-fab')) {
			return;
		}

		isPressed = true;
		isDragging = false;
		isSnapping = false;

		const clientX = 'touches' in e ? e.touches[0].clientX : e.clientX;
		const clientY = 'touches' in e ? e.touches[0].clientY : e.clientY;

		dragStartPos = { x: clientX, y: clientY };
		const rect = fabElement?.getBoundingClientRect();
		if (!rect) {
			return;
		}
		dragOffset.x = clientX - rect.left;
		dragOffset.y = clientY - rect.top;
	}

	function handleDragMove(e: MouseEvent | TouchEvent) {
		if (!isPressed) {
			return;
		}

		const clientX = 'touches' in e ? e.touches[0].clientX : e.clientX;
		const clientY = 'touches' in e ? e.touches[0].clientY : e.clientY;

		const moveX = Math.abs(clientX - dragStartPos.x);
		const moveY = Math.abs(clientY - dragStartPos.y);

		if (!isDragging && (moveX > 5 || moveY > 5)) {
			isDragging = true;
		}

		if (isDragging) {
			if ('touches' in e && e.cancelable) {
				e.preventDefault();
			}
			let newX = clientX - dragOffset.x;
			let newY = clientY - dragOffset.y;

			newX = Math.max(FAB_PADDING_PX, Math.min(innerWidth - FAB_SIZE_PX - FAB_PADDING_PX, newX));
			newY = Math.max(FAB_PADDING_PX, Math.min(innerHeight - FAB_SIZE_PX - FAB_PADDING_PX, newY));

			fabPosition = { x: newX, y: newY };
		}
	}

	function handleDragEnd() {
		isPressed = false;

		if (isDragging) {
			isSnapping = true;

			const padding = 20;
			const centerX = fabPosition.x + FAB_SIZE_PX / 2;

			if (centerX < innerWidth / 2) {
				fabPosition.x = padding;
			} else {
				fabPosition.x = innerWidth - FAB_SIZE_PX - padding;
			}

			setTimeout(() => {
				isDragging = false;
			}, 50);
		} else {
			isDragging = false;
		}
	}

	function toggleMobileMenu() {
		if (!isDragging && mobileFabVisible) {
			isMobileMenuOpen = !isMobileMenuOpen;
		}
	}

	function closeAllMenus() {
		isMobileMenuOpen = false;
		isProfileMenuOpen = false;
	}

	function dispatchQuickAction(action: QuickAction) {
		if (typeof window === 'undefined') {
			return;
		}
		window.dispatchEvent(new CustomEvent('converse:quick-action', { detail: { action } }));
	}

	function onMobileQuickAction(action: QuickAction | undefined) {
		if (!action) {
			return;
		}
		dispatchQuickAction(action);
		isMobileMenuOpen = false;
	}

	function formatMenuBadge(value: number | undefined) {
		const normalized = normalizePositiveCount(value);
		if (normalized <= 0) {
			return '';
		}
		if (normalized > 99) {
			return '99+';
		}
		return String(normalized);
	}

	function handleGlobalPointerDown(event: PointerEvent) {
		const target = event.target;
		if (!(target instanceof Node)) {
			return;
		}
		if (profileMenuRoot && !profileMenuRoot.contains(target)) {
			isProfileMenuOpen = false;
		}
	}

	function handleChatNavState(event: Event) {
		const customEvent = event as CustomEvent<unknown>;
		chatQuickState = parseChatQuickState(customEvent.detail);
	}

	function handleDesktopLogin() {
		closeAllMenus();
		void goto('/login');
	}

	function handleSettings() {
		closeAllMenus();
		void goto('/dashboard');
	}

	function handleLogout() {
		logout();
		closeAllMenus();
		void goto('/login');
	}

	function handleMobileLinkClick() {
		isMobileMenuOpen = false;
	}

	onMount(() => {
		if (typeof window !== 'undefined') {
			fabPosition = {
				x: window.innerWidth - 76,
				y: 20
			};
			isSnapping = true;
			window.addEventListener('pointerdown', handleGlobalPointerDown);
			window.addEventListener('converse:chat-nav-state', handleChatNavState as EventListener);
		}
	});

	onDestroy(() => {
		if (typeof window !== 'undefined') {
			window.removeEventListener('pointerdown', handleGlobalPointerDown);
			window.removeEventListener('converse:chat-nav-state', handleChatNavState as EventListener);
		}
	});
</script>

<svelte:window
	bind:innerWidth={innerWidth}
	bind:innerHeight={innerHeight}
	on:mousemove={handleDragMove}
	on:mouseup={handleDragEnd}
	on:touchmove={handleDragMove}
	on:touchend={handleDragEnd}
/>

{#if desktopNavVisible}
	<nav
		class="desktop-nav"
		class:high-contrast={isHighContrast}
		class:ghost-mode={isGhostMode}
		on:mouseenter={() => (isHovered = true)}
		on:mouseleave={() => (isHovered = false)}
	>
		<div class="glass-pill">
			{#each navLinks as link}
				<a
					href={link.href}
					class="nav-item {activeLabel === link.label ? 'active' : ''}"
					data-sveltekit-preload-data="hover"
				>
					{link.label}
					{#if activeLabel === link.label}
						<div class="glow-dot"></div>
					{/if}
				</a>
			{/each}
		</div>
	</nav>

	{#if $authState.isAuthenticated}
		<div class="desktop-auth-corner" bind:this={profileMenuRoot}>
			<button
				type="button"
				class="desktop-avatar-btn"
				aria-label="Open user menu"
				on:click={() => (isProfileMenuOpen = !isProfileMenuOpen)}
			>
				{#if userAvatarUrl}
					<img src={userAvatarUrl} alt={userDisplayName} />
				{:else}
					<span>{userInitials}</span>
				{/if}
			</button>
			{#if isProfileMenuOpen}
				<div class="desktop-user-menu" transition:fade={{ duration: 140 }}>
					<div class="desktop-user-meta">
						<strong>{userDisplayName}</strong>
						{#if userEmail}
							<small>{userEmail}</small>
						{/if}
					</div>
					<button type="button" on:click={handleSettings}>Settings</button>
					<button type="button" class="danger" on:click={handleLogout}>Logout</button>
				</div>
				{/if}
		</div>
	{/if}
{:else if mobileFabVisible}
	{#if isMobileMenuOpen}
		<div
			class="mobile-overlay"
			transition:fade={{ duration: 200 }}
			on:click={closeAllMenus}
			role="button"
			tabindex="0"
			on:keydown={closeAllMenus}
		></div>

		<div
			class="mobile-menu-card"
			transition:scale={{ start: 0.9, duration: 200 }}
			style="top: {menuPosition.top}px; left: {menuPosition.left}px;"
			on:click|stopPropagation
			role="menu"
			tabindex="0"
			on:keydown={() => {}}
		>
			<div class="menu-header">{mobileMenuTitle}</div>
			{#each mobileMenuItems as item}
				{#if item.href}
					<a
						href={item.href}
						class="mobile-link {isPathActiveForNavLink(pathname, item.href) ? 'active' : ''}"
						on:click={handleMobileLinkClick}
					>
						<span>{item.label}</span>
						{#if formatMenuBadge(item.badge)}
							<span class="mobile-badge">{formatMenuBadge(item.badge)}</span>
						{:else if isPathActiveForNavLink(pathname, item.href)}
							<span class="active-dot">●</span>
						{/if}
					</a>
				{:else if item.quickAction}
					<button
						type="button"
						class="mobile-link mobile-link-button"
						on:click={() => onMobileQuickAction(item.quickAction)}
					>
						<span>{item.label}</span>
						{#if formatMenuBadge(item.badge)}
							<span class="mobile-badge">{formatMenuBadge(item.badge)}</span>
						{/if}
					</button>
				{/if}
			{/each}
			<div class="mobile-auth-section">
				{#if !$authState.isAuthenticated}
					<button type="button" class="mobile-auth-button" on:click={handleDesktopLogin}>Login</button>
				{:else}
					<div class="mobile-user-summary">
						<div class="mobile-avatar">
							{#if userAvatarUrl}
								<img src={userAvatarUrl} alt={userDisplayName} />
							{:else}
								<span>{userInitials}</span>
							{/if}
						</div>
						<div class="mobile-user-text">
							<strong>{userDisplayName}</strong>
							{#if userEmail}
								<small>{userEmail}</small>
							{/if}
						</div>
					</div>
					<button type="button" class="mobile-auth-button secondary" on:click={handleSettings}>
						Settings
					</button>
					<button type="button" class="mobile-auth-button danger" on:click={handleLogout}>
						Logout
					</button>
				{/if}
			</div>
		</div>
	{/if}

	<button
		class="holo-fab"
		class:snapping={isSnapping}
		bind:this={fabElement}
		style="transform: translate({fabPosition.x}px, {fabPosition.y}px);"
		on:mousedown={handleDragStart}
		on:touchstart={handleDragStart}
		on:click={toggleMobileMenu}
		class:open={isMobileMenuOpen}
		aria-label="Toggle Menu"
	>
		<div class="fab-inner">
			<img class="fab-logo" src={toraLogo} alt="" aria-hidden="true" />
		</div>
		<div class="fab-glow"></div>
	</button>
{/if}

	<style>
		:global(:root) {
		--navbar-pill-bg: rgba(8, 14, 24, 0.72);
		--navbar-pill-border: rgba(255, 255, 255, 0.16);
		--navbar-pill-shadow: 0 10px 34px rgba(0, 0, 0, 0.42);
		--navbar-item-text: rgba(236, 242, 255, 0.78);
		--navbar-item-hover-text: #ffffff;
		--navbar-item-hover-bg: rgba(255, 255, 255, 0.14);
		--navbar-active-text: #ffffff;
		--navbar-dot: #9ed3ff;
		--navbar-dot-glow: 0 0 10px rgba(158, 211, 255, 0.85);
		--navbar-auth-bg: rgba(10, 16, 28, 0.62);
		--navbar-auth-border: rgba(255, 255, 255, 0.22);
		--navbar-auth-text: #f7f8fd;
		--navbar-auth-hover-bg: rgba(33, 52, 81, 0.56);
		--navbar-auth-hover-border: rgba(163, 201, 255, 0.52);
		--navbar-menu-bg: rgba(14, 20, 32, 0.94);
		--navbar-menu-border: rgba(255, 255, 255, 0.22);
		--navbar-menu-text: #ecf1ff;
		--navbar-menu-muted: rgba(202, 206, 225, 0.82);
		--navbar-menu-hover: rgba(255, 255, 255, 0.08);
			--navbar-overlay: rgba(225, 236, 250, 0.46);
			--navbar-mobile-bg: rgba(255, 255, 255, 0.7);
			--navbar-mobile-border: rgba(223, 234, 249, 0.9);
			--navbar-mobile-text: #10233b;
			--navbar-mobile-muted: rgba(73, 94, 128, 0.8);
			--navbar-mobile-hover: rgba(208, 224, 248, 0.5);
			--navbar-mobile-active: rgba(164, 196, 239, 0.44);
			--navbar-mobile-shadow: 0 20px 56px rgba(118, 149, 196, 0.36);
			--navbar-fab-bg:
				radial-gradient(circle at 22% 14%, rgba(255, 255, 255, 0.66), transparent 48%),
				linear-gradient(
					150deg,
					rgba(255, 255, 255, 0.46),
					rgba(198, 217, 245, 0.16) 58%,
					rgba(173, 202, 236, 0.12)
				),
				rgba(255, 255, 255, 0.38);
			--navbar-fab-border: rgba(255, 255, 255, 0.82);
			--navbar-fab-shadow: 0 18px 38px rgba(104, 137, 186, 0.34);
			--navbar-fab-open-bg:
				radial-gradient(circle at 20% 14%, rgba(255, 255, 255, 0.72), transparent 46%),
				linear-gradient(145deg, rgba(255, 255, 255, 0.56), rgba(190, 215, 248, 0.24)),
				rgba(255, 255, 255, 0.5);
			--navbar-fab-open-border: rgba(163, 195, 240, 0.82);
			--navbar-fab-open-glow: 0 0 22px rgba(145, 184, 239, 0.55);
		}

		:global(:root[data-theme='dark']),
		:global(.theme-dark) {
		--navbar-pill-bg: rgba(248, 251, 255, 0.72);
		--navbar-pill-border: rgba(255, 255, 255, 0.86);
		--navbar-pill-shadow: 0 10px 34px rgba(189, 204, 229, 0.42);
		--navbar-item-text: rgba(17, 29, 47, 0.76);
		--navbar-item-hover-text: #0f172a;
		--navbar-item-hover-bg: rgba(255, 255, 255, 0.5);
		--navbar-active-text: #0f172a;
		--navbar-dot: #3c4f6a;
		--navbar-dot-glow: 0 0 10px rgba(87, 110, 142, 0.56);
		--navbar-auth-bg: rgba(255, 255, 255, 0.74);
		--navbar-auth-border: rgba(217, 229, 247, 0.95);
		--navbar-auth-text: #0f172a;
		--navbar-auth-hover-bg: rgba(255, 255, 255, 0.9);
		--navbar-auth-hover-border: rgba(180, 198, 225, 0.95);
		--navbar-menu-bg: rgba(255, 255, 255, 0.94);
		--navbar-menu-border: rgba(212, 224, 245, 0.92);
		--navbar-menu-text: #122238;
		--navbar-menu-muted: rgba(62, 84, 118, 0.78);
		--navbar-menu-hover: rgba(202, 220, 248, 0.4);
			--navbar-overlay: rgba(6, 12, 24, 0.62);
			--navbar-mobile-bg: rgba(20, 35, 60, 0.76);
			--navbar-mobile-border: rgba(129, 167, 227, 0.42);
			--navbar-mobile-text: #ecf4ff;
			--navbar-mobile-muted: rgba(177, 202, 241, 0.84);
			--navbar-mobile-hover: rgba(93, 137, 208, 0.28);
			--navbar-mobile-active: rgba(124, 172, 248, 0.34);
			--navbar-mobile-shadow: 0 24px 60px rgba(3, 12, 31, 0.68);
			--navbar-fab-bg:
				radial-gradient(circle at 20% 13%, rgba(132, 176, 245, 0.35), transparent 52%),
				linear-gradient(150deg, rgba(31, 55, 92, 0.72), rgba(20, 34, 58, 0.8)),
				rgba(18, 33, 57, 0.72);
			--navbar-fab-border: rgba(132, 174, 240, 0.5);
			--navbar-fab-shadow: 0 18px 40px rgba(3, 11, 28, 0.64);
			--navbar-fab-open-bg:
				radial-gradient(circle at 18% 12%, rgba(148, 190, 255, 0.42), transparent 50%),
				linear-gradient(145deg, rgba(44, 74, 120, 0.74), rgba(23, 39, 64, 0.82)),
				rgba(26, 46, 77, 0.76);
			--navbar-fab-open-border: rgba(167, 202, 250, 0.66);
			--navbar-fab-open-glow: 0 0 22px rgba(123, 170, 246, 0.5);
		}

	.desktop-nav {
		position: fixed;
		top: 25px;
		left: 50%;
		transform: translateX(-50%);
		z-index: 1000;
		width: 24vw;
		min-width: 300px;
		max-width: 500px;
		transition: all 0.5s cubic-bezier(0.4, 0, 0.2, 1);
	}

	.glass-pill {
		display: flex;
		justify-content: space-between;
		align-items: center;
		width: 100%;
		padding: 0.6vw 1vw;
		background: var(--navbar-pill-bg);
		backdrop-filter: blur(20px) saturate(180%);
		border: 1px solid var(--navbar-pill-border);
		border-radius: 999px;
		box-shadow: var(--navbar-pill-shadow);
		transition: all 0.5s ease;
	}

	.ghost-mode .glass-pill {
		background: transparent;
		border-color: transparent;
		backdrop-filter: none;
		box-shadow: none;
	}

	.high-contrast:not(.ghost-mode) .glass-pill {
		background: rgba(0, 0, 0, 0.9);
		border-color: #5227ff;
	}

	.nav-item {
		text-decoration: none;
		background: none;
		border: none;
		position: relative;
		font-size: clamp(0.5rem, 0.75vw, 0.9rem);
		font-family: 'JetBrains Mono', monospace;
		font-weight: 500;
		color: var(--navbar-item-text);
		letter-spacing: 0.05em;
		padding: 0.4vw 0.8vw;
		border-radius: 99px;
		transition: all 0.3s ease;
	}

	.nav-item:hover {
		color: var(--navbar-item-hover-text);
		background: var(--navbar-item-hover-bg);
	}

	.nav-item.active {
		color: var(--navbar-active-text);
	}

	.glow-dot {
		position: absolute;
		bottom: 2px;
		left: 50%;
		transform: translateX(-50%);
		width: 4px;
		height: 4px;
		background: var(--navbar-dot);
		border-radius: 50%;
		box-shadow: var(--navbar-dot-glow);
	}

	.desktop-auth-corner {
		position: fixed;
		top: 22px;
		right: 20px;
		z-index: 1100;
	}

	.desktop-avatar-btn {
		height: 42px;
		border-radius: 999px;
		border: 1px solid var(--navbar-auth-border);
		background: var(--navbar-auth-bg);
		color: var(--navbar-auth-text);
		font-family: 'Inter', sans-serif;
		font-weight: 650;
		cursor: pointer;
		backdrop-filter: blur(12px) saturate(145%);
		-webkit-backdrop-filter: blur(12px) saturate(145%);
	}

	.desktop-avatar-btn:hover {
		border-color: var(--navbar-auth-hover-border);
		background: var(--navbar-auth-hover-bg);
	}

	.desktop-avatar-btn {
		min-width: 42px;
		width: 42px;
		border-radius: 999px;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		padding: 0;
		overflow: hidden;
	}

	.desktop-avatar-btn img {
		width: 100%;
		height: 100%;
		object-fit: cover;
	}

	.desktop-avatar-btn span {
		font-size: 0.78rem;
		font-weight: 700;
	}

	.desktop-user-menu {
		margin-top: 0.5rem;
		padding: 0.48rem;
		width: 198px;
		border-radius: 14px;
		border: 1px solid var(--navbar-menu-border);
		background: var(--navbar-menu-bg);
		box-shadow: 0 16px 40px rgba(0, 0, 0, 0.62);
		display: grid;
		gap: 0.28rem;
	}

	.desktop-user-meta {
		padding: 0.25rem 0.4rem 0.5rem;
		border-bottom: 1px solid color-mix(in srgb, var(--navbar-menu-border) 70%, transparent);
	}

	.desktop-user-meta strong {
		display: block;
		color: var(--navbar-menu-text);
		font-size: 0.82rem;
		line-height: 1.2;
	}

	.desktop-user-meta small {
		display: block;
		color: var(--navbar-menu-muted);
		font-size: 0.69rem;
		font-family: 'JetBrains Mono', monospace;
		margin-top: 0.2rem;
	}

	.desktop-user-menu button {
		text-align: left;
		border: none;
		background: transparent;
		color: var(--navbar-menu-text);
		padding: 0.5rem 0.58rem;
		border-radius: 9px;
		cursor: pointer;
		font-size: 0.82rem;
		font-weight: 540;
	}

	.desktop-user-menu button:hover {
		background: var(--navbar-menu-hover);
	}

	.desktop-user-menu button.danger {
		color: #ffd3d8;
	}

	.desktop-user-menu button.danger:hover {
		background: rgba(255, 82, 112, 0.2);
	}

	.holo-fab {
		position: fixed;
		top: 30vh; left: 0px; /* JS handles translate */
		width: 56px;
		height: 56px;
		z-index: 2000;
		background: var(--navbar-fab-bg);
		backdrop-filter: blur(22px) saturate(180%);
		-webkit-backdrop-filter: blur(22px) saturate(180%);
		border: 1px solid var(--navbar-fab-border);
		border-radius: 18px;
		cursor: grab;
		touch-action: none;
		display: flex;
		align-items: center;
		justify-content: center;
		box-shadow:
			var(--navbar-fab-shadow),
			inset 0 1px 0 rgba(255, 255, 255, 0.54),
			inset 0 -1px 0 rgba(121, 157, 207, 0.2);
		transition: box-shadow 0.3s, border-color 0.3s, background 0.3s;
		overflow: hidden;
		padding: 0;
	}

	.holo-fab.snapping {
		transition:
			transform 0.4s cubic-bezier(0.2, 0.9, 0.2, 1.2),
			box-shadow 0.3s;
	}

	.holo-fab:active {
		cursor: grabbing;
		transform: scale(0.95);
	}

	.holo-fab.open {
		border-color: var(--navbar-fab-open-border);
		background: var(--navbar-fab-open-bg);
		box-shadow:
			var(--navbar-fab-open-glow),
			0 16px 34px color-mix(in srgb, var(--navbar-fab-open-border) 45%, transparent),
			inset 0 1px 0 rgba(255, 255, 255, 0.58);
	}

	.fab-inner {
		width: 100%;
		height: 100%;
		display: flex;
		align-items: center;
		justify-content: center;
		pointer-events: none;
	}

	.fab-logo {
		width: 82%;
		height: 82%;
		opacity: 0.72;
		filter: saturate(0.95) contrast(1.02);
		user-select: none;
		pointer-events: none;
	}

	.mobile-overlay {
		position: fixed;
		inset: 0;
		background: var(--navbar-overlay);
		backdrop-filter: blur(7px) saturate(130%);
		-webkit-backdrop-filter: blur(7px) saturate(130%);
		z-index: 1998;
	}

	.mobile-menu-card {
		position: fixed;
		width: 240px;
		background: var(--navbar-mobile-bg);
		border: 1px solid var(--navbar-mobile-border);
		border-radius: 20px;
		padding: 16px;
		display: flex;
		flex-direction: column;
		gap: 8px;
		backdrop-filter: blur(20px) saturate(165%);
		-webkit-backdrop-filter: blur(20px) saturate(165%);
		box-shadow: var(--navbar-mobile-shadow);
		z-index: 2200;
	}

	.menu-header {
		font-family: 'JetBrains Mono', monospace;
		font-size: 0.6rem;
		color: var(--navbar-mobile-muted);
		padding-bottom: 8px;
		border-bottom: 1px solid color-mix(in srgb, var(--navbar-mobile-border) 75%, transparent);
		margin-bottom: 4px;
		letter-spacing: 1px;
	}

	.mobile-link {
		display: flex;
		justify-content: space-between;
		align-items: center;
		text-decoration: none;
		color: var(--navbar-mobile-muted);
		font-family: 'Inter', sans-serif;
		font-weight: 600;
		font-size: 0.9rem;
		padding: 12px 16px;
		border-radius: 12px;
		transition: all 0.2s;
	}

	.mobile-link-button {
		width: 100%;
		border: 0;
		background: transparent;
		text-align: left;
		cursor: pointer;
	}

	.mobile-link:hover {
		background: var(--navbar-mobile-hover);
		color: var(--navbar-mobile-text);
	}

	.mobile-link.active {
		background: var(--navbar-mobile-active);
		color: var(--navbar-mobile-text);
	}

	.active-dot {
		color: var(--navbar-dot);
		font-size: 0.6rem;
	}

	.mobile-badge {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		min-width: 1.25rem;
		padding: 0.05rem 0.35rem;
		border-radius: 999px;
		background: color-mix(in srgb, var(--navbar-mobile-active) 80%, transparent);
		color: var(--navbar-mobile-text);
		font-size: 0.68rem;
		font-weight: 700;
		font-variant-numeric: tabular-nums;
	}

	.mobile-auth-section {
		margin-top: 0.4rem;
		padding-top: 0.65rem;
		border-top: 1px solid color-mix(in srgb, var(--navbar-mobile-border) 75%, transparent);
		display: grid;
		gap: 0.48rem;
	}

	.mobile-auth-button {
		border: 1px solid var(--navbar-mobile-border);
		background: var(--navbar-mobile-hover);
		color: var(--navbar-mobile-text);
		padding: 0.6rem 0.72rem;
		border-radius: 10px;
		font-size: 0.84rem;
		font-weight: 640;
		cursor: pointer;
		text-align: left;
	}

	.mobile-auth-button.secondary {
		border-color: var(--navbar-auth-hover-border);
		background: color-mix(in srgb, var(--navbar-auth-hover-bg) 65%, transparent);
	}

	.mobile-auth-button.danger {
		border-color: rgba(255, 114, 137, 0.32);
		color: #ffdce2;
	}

	.mobile-user-summary {
		display: flex;
		align-items: center;
		gap: 0.6rem;
		padding: 0.28rem 0.1rem;
	}

	.mobile-avatar {
		width: 34px;
		height: 34px;
		border-radius: 999px;
		background: color-mix(in srgb, var(--navbar-mobile-active) 82%, transparent);
		color: var(--navbar-mobile-text);
		display: inline-flex;
		align-items: center;
		justify-content: center;
		font-size: 0.72rem;
		font-weight: 700;
		overflow: hidden;
		flex: 0 0 auto;
	}

	.mobile-avatar img {
		width: 100%;
		height: 100%;
		object-fit: cover;
	}

	.mobile-user-text strong {
		display: block;
		font-size: 0.82rem;
		color: var(--navbar-mobile-text);
		line-height: 1.2;
	}

	.mobile-user-text small {
		display: block;
		margin-top: 0.12rem;
		color: var(--navbar-mobile-muted);
		font-size: 0.64rem;
		font-family: 'JetBrains Mono', monospace;
	}
</style>
