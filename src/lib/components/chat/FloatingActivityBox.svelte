<script lang="ts">
	import { createEventDispatcher, onMount } from 'svelte';
	import toraLogo from '$lib/assets/tora-logo.svg';
	import type { WorkspaceModule } from '$lib/types/dashboard';

	export let activeModules: WorkspaceModule[] = ['dashboard'];
	export let selectedModule: WorkspaceModule | null = null;
	export let addableModules: WorkspaceModule[] = [];

	const dispatch = createEventDispatcher<{
		selectModule: { module: WorkspaceModule };
		addModule: { module: WorkspaceModule };
		limitReached: { message: string };
	}>();

	const SMALL_BOX_SIZE = 56;
	const MEDIUM_BOX_SIZE = 62;
	const LARGE_BOX_SIZE = 68;
	const BASE_PANEL_SIZE = 300;
	const LARGE_PANEL_SIZE = 350;
	const LARGE_PANEL_BREAKPOINT = 600;
	const VIEWPORT_MARGIN = 12;
	const DESKTOP_DEFAULT_TOP_OFFSET_PX = 80;
	const DRAG_THRESHOLD_PX = 8;

	let shellEl: HTMLDivElement | null = null;
	let expanded = false;
	let showAddMenu = false;
	let boxSizePx = SMALL_BOX_SIZE;
	let panelSizePx = BASE_PANEL_SIZE;
	let positionX = 0;
	let positionY = DESKTOP_DEFAULT_TOP_OFFSET_PX;
	let dragging = false;
	let activePointerId: number | null = null;
	let pointerStartX = 0;
	let pointerStartY = 0;
	let dragOriginX = 0;
	let dragOriginY = 0;
	let dragMoved = false;
	let pressedMainButton = false;
	let suppressMainClick = false;

	const MODULE_META: Record<
		WorkspaceModule,
		{
			label: string;
			icon: string;
		}
	> = {
		dashboard: {
			label: 'Dashboard',
			icon: 'M4 4h7v7H4zm9 0h7v4h-7zm0 6h7v10h-7zM4 13h7v7H4z'
		},
		draw: {
			label: 'Draw',
			icon: 'M4.5 16.5 16 5a2.4 2.4 0 0 1 3.4 3.4L7.9 19.9l-3.9.6zm8.4-8.4 3.5 3.5'
		},
		code: {
			label: 'Code',
			icon: 'm8 9-4 3 4 3m8-6 4 3-4 3M13 7l-2 10'
		},
		tasks: {
			label: 'Tasks',
			icon: 'M8 7h12M8 12h12M8 17h12M4.5 7h.01M4.5 12h.01M4.5 17h.01'
		}
	};

	onMount(() => {
		boxSizePx = resolveBoxSize();
		panelSizePx = resolvePanelSize();
		resetToRightEdge();
		const onResize = () => {
			boxSizePx = resolveBoxSize();
			panelSizePx = resolvePanelSize();
			const maxY = maxPositionY();
			positionY = Math.max(VIEWPORT_MARGIN, Math.min(maxY, positionY));
			snapToNearestEdge();
		};
		window.addEventListener('resize', onResize);
		return () => {
			window.removeEventListener('resize', onResize);
		};
	});

	function resolveBoxSize() {
		if (typeof window === 'undefined') {
			return SMALL_BOX_SIZE;
		}
		if (window.innerWidth >= 1600) {
			return LARGE_BOX_SIZE;
		}
		if (window.innerWidth >= 1200) {
			return MEDIUM_BOX_SIZE;
		}
		return SMALL_BOX_SIZE;
	}

	function resolvePanelSize() {
		if (typeof window === 'undefined') {
			return BASE_PANEL_SIZE;
		}
		return window.innerWidth > LARGE_PANEL_BREAKPOINT ? LARGE_PANEL_SIZE : BASE_PANEL_SIZE;
	}

	function maxPositionX() {
		if (typeof window === 'undefined') {
			return VIEWPORT_MARGIN;
		}
		return Math.max(VIEWPORT_MARGIN, window.innerWidth - boxSizePx - VIEWPORT_MARGIN);
	}

	function maxPositionY() {
		if (typeof window === 'undefined') {
			return VIEWPORT_MARGIN;
		}
		// Keep drag bounds tied to the visible button size so the box can traverse
		// the full viewport even if shell height is affected by surrounding layout.
		return Math.max(VIEWPORT_MARGIN, window.innerHeight - boxSizePx - VIEWPORT_MARGIN);
	}

	function resetToRightEdge() {
		if (typeof window === 'undefined') {
			return;
		}
		positionX = maxPositionX();
		positionY = Math.max(
			VIEWPORT_MARGIN,
			Math.min(maxPositionY(), DESKTOP_DEFAULT_TOP_OFFSET_PX)
		);
	}

	function onShellPointerDown(event: PointerEvent) {
		if (event.button !== 0) {
			return;
		}
		dragging = false;
		activePointerId = event.pointerId;
		dragMoved = false;
		suppressMainClick = false;
		pointerStartX = event.clientX;
		pointerStartY = event.clientY;
		dragOriginX = positionX;
		dragOriginY = positionY;
		pressedMainButton =
			event.target instanceof Element ? Boolean(event.target.closest('.activity-box-main')) : false;
		shellEl?.setPointerCapture(event.pointerId);
	}

	function onShellPointerMove(event: PointerEvent) {
		if (!dragging || activePointerId !== event.pointerId) {
			if (activePointerId !== event.pointerId) {
				return;
			}
		}
		const deltaX = event.clientX - pointerStartX;
		const deltaY = event.clientY - pointerStartY;
		const movedDistance = Math.hypot(deltaX, deltaY);
		if (!dragging && movedDistance >= DRAG_THRESHOLD_PX) {
			dragging = true;
			dragMoved = true;
		}
		if (!dragging) {
			return;
		}
		positionX = Math.max(VIEWPORT_MARGIN, Math.min(maxPositionX(), dragOriginX + deltaX));
		positionY = Math.max(VIEWPORT_MARGIN, Math.min(maxPositionY(), dragOriginY + deltaY));
	}

	function onShellPointerUp(event: PointerEvent) {
		if (activePointerId !== event.pointerId) {
			return;
		}
		shellEl?.releasePointerCapture(event.pointerId);
		if (dragging || dragMoved) {
			snapToNearestEdge();
			suppressMainClick = true;
		} else if (pressedMainButton) {
			expanded = !expanded;
			showAddMenu = false;
			// Ignore any synthetic click that may follow pointerup.
			suppressMainClick = true;
		}
		dragging = false;
		dragMoved = false;
		pressedMainButton = false;
		activePointerId = null;
	}

	function onShellPointerCancel(event: PointerEvent) {
		if (activePointerId !== event.pointerId) {
			return;
		}
		dragging = false;
		dragMoved = false;
		pressedMainButton = false;
		shellEl?.releasePointerCapture(event.pointerId);
		snapToNearestEdge();
		activePointerId = null;
	}

	function snapToNearestEdge() {
		if (typeof window === 'undefined') {
			return;
		}
		const centerX = positionX + boxSizePx / 2;
		const targetX = centerX < window.innerWidth / 2 ? VIEWPORT_MARGIN : maxPositionX();
		positionX = targetX;
		positionY = Math.max(VIEWPORT_MARGIN, Math.min(maxPositionY(), positionY));
	}

	function onModuleSelect(module: WorkspaceModule) {
		expanded = false;
		showAddMenu = false;
		dispatch('selectModule', { module });
	}

	function onAddPressed() {
		if (addableModules.length === 0) {
			showAddMenu = true;
			dispatch('limitReached', {
				message: 'All boards are already active for this room.'
			});
			return;
		}
		showAddMenu = !showAddMenu;
	}

	function onAddModule(module: WorkspaceModule) {
		expanded = false;
		showAddMenu = false;
		dispatch('addModule', { module });
	}

	function toggleExpanded() {
		if (suppressMainClick) {
			suppressMainClick = false;
			return;
		}
		expanded = !expanded;
		showAddMenu = false;
	}

	function closeExpanded() {
		expanded = false;
		showAddMenu = false;
	}

	function onMainButtonKeydown(event: KeyboardEvent) {
		if (event.key !== 'Enter' && event.key !== ' ') {
			return;
		}
		event.preventDefault();
		toggleExpanded();
	}

	function onWindowKeydown(event: KeyboardEvent) {
		if (event.key !== 'Escape' || !expanded) {
			return;
		}
		closeExpanded();
	}

	function moduleMeta(module: WorkspaceModule) {
		return MODULE_META[module];
	}
</script>

<svelte:window on:keydown={onWindowKeydown} />

<div
	class="activity-box-shell"
	role="presentation"
	bind:this={shellEl}
	style={`--activity-box-size:${boxSizePx}px; transform: translate3d(${positionX}px, ${positionY}px, 0); transition: ${
		dragging ? 'none' : 'transform 220ms cubic-bezier(0.22, 1, 0.36, 1)'
	};`}
	on:pointerdown={onShellPointerDown}
	on:pointermove={onShellPointerMove}
	on:pointerup={onShellPointerUp}
	on:pointercancel={onShellPointerCancel}
>
	<button
		type="button"
		class="activity-box-main"
		aria-label={expanded ? 'Collapse module switcher' : 'Expand module switcher'}
		title={expanded ? 'Collapse module switcher' : 'Expand module switcher'}
		on:click|stopPropagation={toggleExpanded}
		on:keydown={onMainButtonKeydown}
	>
		<img class="main-logo" src={toraLogo} alt="" aria-hidden="true" />
	</button>

</div>

{#if expanded}
	<button
		type="button"
		class="activity-panel-backdrop"
		aria-label="Close module switcher"
		on:click={closeExpanded}
	></button>
	<div
		class="activity-panel"
		role="dialog"
		aria-label="Workspace boards"
		aria-modal="false"
		style={`--activity-panel-size:${panelSizePx}px;`}
	>
		<header class="activity-panel-head">
			<h4>Boards</h4>
			<button
				type="button"
				class="activity-panel-close"
				on:click={closeExpanded}
				aria-label="Close module switcher"
			>
				<svg viewBox="0 0 24 24" aria-hidden="true">
					<path d="m6 6 12 12M18 6 6 18"></path>
				</svg>
			</button>
		</header>
		<div class="activity-panel-body">
			<div class="activity-box-menu">
				{#each activeModules as module (module)}
					{@const meta = moduleMeta(module)}
					<button
						type="button"
						class="activity-action"
						class:is-active={selectedModule === module}
						title={meta.label}
						aria-label={meta.label}
						on:click|stopPropagation={() => onModuleSelect(module)}
					>
						<svg viewBox="0 0 24 24" aria-hidden="true">
							<path d={meta.icon}></path>
						</svg>
						<span>{meta.label}</span>
					</button>
				{/each}
				<button
					type="button"
					class="activity-action add-action"
					title="Activate board"
					aria-label="Activate board"
					on:click|stopPropagation={onAddPressed}
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d="M12 5v14M5 12h14"></path>
					</svg>
					<span>Add</span>
				</button>
			</div>
			{#if showAddMenu}
				<div class="module-add-menu">
					{#if addableModules.length > 0}
						{#each addableModules as module (module)}
							{@const meta = moduleMeta(module)}
							<button
								type="button"
								class="module-add-option"
								on:click|stopPropagation={() => onAddModule(module)}
							>
								<svg viewBox="0 0 24 24" aria-hidden="true">
									<path d={meta.icon}></path>
								</svg>
								<span>{meta.label}</span>
							</button>
						{/each}
					{:else}
						<p class="module-empty-state">All boards are already active for this room.</p>
					{/if}
				</div>
			{/if}
		</div>
	</div>
{/if}

<style>
	.activity-box-shell {
		--activity-box-size: 64px;
		--activity-icon-size: clamp(1.05rem, calc(var(--activity-box-size) * 0.34), 1.45rem);
		--activity-shell-text: #132742;
		--activity-shell-border: rgba(255, 255, 255, 0.56);
		--activity-shell-glass: rgba(255, 255, 255, 0.2);
		--activity-shell-shadow: rgba(43, 73, 116, 0.24);
		position: fixed;
		left: 0;
		top: 0;
		width: var(--activity-box-size);
		z-index: 350;
		display: grid;
		gap: 0.45rem;
		user-select: none;
		touch-action: none;
		cursor: grab;
	}

	:global(:root[data-theme='dark']) .activity-box-shell,
	:global(.theme-dark) .activity-box-shell {
		--activity-shell-text: #f3f8ff;
		--activity-shell-border: rgba(212, 228, 255, 0.34);
		--activity-shell-glass: rgba(29, 44, 68, 0.24);
		--activity-shell-shadow: rgba(5, 14, 32, 0.4);
	}

	.activity-box-shell:active {
		cursor: grabbing;
	}

		.activity-box-main {
			position: relative;
			overflow: hidden;
			width: var(--activity-box-size);
			height: var(--activity-box-size);
			border-radius: clamp(18px, calc(var(--activity-box-size) * 0.3), 24px);
			border: 1px solid var(--activity-shell-border);
		background:
			radial-gradient(circle at 22% 14%, rgba(255, 255, 255, 0.34), transparent 48%),
			linear-gradient(
				150deg,
				rgba(255, 255, 255, 0.2),
				rgba(198, 217, 245, 0.08) 58%,
				rgba(173, 202, 236, 0.06)
			),
			var(--activity-shell-glass);
		backdrop-filter: blur(22px) saturate(180%);
		-webkit-backdrop-filter: blur(22px) saturate(180%);
		color: var(--activity-shell-text);
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		box-shadow:
			0 18px 36px var(--activity-shell-shadow),
			inset 0 1px 0 rgba(255, 255, 255, 0.3),
			inset 0 -1px 0 rgba(120, 154, 202, 0.14);
		padding: 0;
		transform: translateY(0);
		transition:
			transform 180ms ease,
			box-shadow 220ms ease,
			border-color 220ms ease;
	}

	.activity-box-main::before {
		content: '';
		position: absolute;
		inset: 0;
		background: linear-gradient(
			145deg,
			rgba(255, 255, 255, 0.34) 0%,
			rgba(255, 255, 255, 0.08) 46%,
			transparent 76%
		);
		pointer-events: none;
	}

	.activity-box-main:hover {
		transform: translateY(-2px);
		box-shadow:
			0 24px 42px var(--activity-shell-shadow),
			inset 0 1px 0 rgba(255, 255, 255, 0.38),
			inset 0 -1px 0 rgba(120, 154, 202, 0.16);
	}

	.activity-box-main:focus-visible {
		outline: 2px solid rgba(106, 148, 218, 0.65);
		outline-offset: 2px;
	}

	.activity-box-main .main-logo {
		width: clamp(34px, calc(var(--activity-box-size) * 0.78), 54px);
		height: clamp(34px, calc(var(--activity-box-size) * 0.78), 54px);
		opacity: 0.64;
		filter: saturate(0.95) contrast(1.02);
		pointer-events: none;
		user-select: none;
	}

	:global(:root[data-theme='dark']) .activity-box-main .main-logo,
	:global(.theme-dark) .activity-box-main .main-logo {
		opacity: 0.72;
	}

	.activity-panel-backdrop {
		position: fixed;
		inset: 0;
		background:
			radial-gradient(circle at 20% 12%, rgba(210, 224, 246, 0.24), transparent 36%),
			rgba(146, 165, 194, 0.16);
		backdrop-filter: blur(7px) saturate(130%);
		-webkit-backdrop-filter: blur(7px) saturate(130%);
		border: 0;
		padding: 0;
		margin: 0;
		z-index: 330;
	}

	:global(:root[data-theme='dark']) .activity-panel-backdrop,
	:global(.theme-dark) .activity-panel-backdrop {
		background:
			radial-gradient(circle at 22% 15%, rgba(86, 126, 186, 0.22), transparent 40%),
			rgba(5, 11, 20, 0.26);
	}

	.activity-panel {
		--panel-text: #152740;
		--panel-muted: rgba(40, 62, 92, 0.76);
		--panel-border: rgba(255, 255, 255, 0.56);
		--panel-glass: rgba(255, 255, 255, 0.28);
		--panel-highlight: rgba(255, 255, 255, 0.42);
		--panel-shadow: rgba(37, 69, 115, 0.32);
		--panel-button-bg: rgba(255, 255, 255, 0.28);
		--panel-button-border: rgba(255, 255, 255, 0.5);
		--panel-button-hover: rgba(255, 255, 255, 0.38);
		--panel-active-bg: rgba(149, 200, 255, 0.26);
		--panel-active-border: rgba(106, 164, 236, 0.58);
		position: fixed;
		left: 50%;
		top: 50%;
		transform: translate(-50%, -50%);
		width: min(var(--activity-panel-size, 300px), calc(100vw - 1rem));
		height: min(var(--activity-panel-size, 300px), calc(100vh - 1rem));
		padding: 0.9rem;
		border-radius: 30px;
		border: 1px solid var(--panel-border);
		background:
			radial-gradient(circle at 14% 10%, rgba(255, 255, 255, 0.46), transparent 42%),
			linear-gradient(
				154deg,
				var(--panel-highlight) 0%,
				rgba(231, 240, 255, 0.16) 52%,
				rgba(197, 220, 250, 0.12) 100%
			),
			var(--panel-glass);
		backdrop-filter: blur(30px) saturate(190%);
		-webkit-backdrop-filter: blur(30px) saturate(190%);
		box-shadow:
			0 36px 78px var(--panel-shadow),
			inset 0 1px 0 rgba(255, 255, 255, 0.5),
			inset 0 -1px 0 rgba(124, 162, 211, 0.16);
		display: grid;
		grid-template-rows: auto minmax(0, 1fr);
		gap: 0.72rem;
		overflow: hidden;
		z-index: 340;
	}

	:global(:root[data-theme='dark']) .activity-panel,
	:global(.theme-dark) .activity-panel {
		--panel-text: #f2f7ff;
		--panel-muted: rgba(210, 225, 246, 0.84);
		--panel-border: rgba(208, 227, 255, 0.36);
		--panel-glass: rgba(18, 30, 48, 0.34);
		--panel-highlight: rgba(112, 155, 222, 0.14);
		--panel-shadow: rgba(3, 9, 22, 0.58);
		--panel-button-bg: rgba(30, 44, 65, 0.3);
		--panel-button-border: rgba(185, 211, 249, 0.3);
		--panel-button-hover: rgba(45, 65, 96, 0.36);
		--panel-active-bg: rgba(83, 141, 215, 0.3);
		--panel-active-border: rgba(156, 206, 255, 0.56);
	}

	.activity-panel-body {
		min-height: 0;
		overflow-y: auto;
		display: grid;
		gap: 0.65rem;
		padding-right: 0.08rem;
	}

	.activity-panel-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}

	.activity-panel-head h4 {
		margin: 0;
		font-size: 0.9rem;
		font-weight: 700;
		letter-spacing: 0.03em;
		color: var(--panel-text);
	}

	.activity-panel-close {
		width: 34px;
		height: 34px;
		border-radius: 11px;
		border: 1px solid var(--panel-button-border);
		background: var(--panel-button-bg);
		color: var(--panel-text);
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		padding: 0;
		transition:
			background 180ms ease,
			border-color 180ms ease;
	}

	.activity-panel-close:hover {
		background: var(--panel-button-hover);
		border-color: var(--panel-active-border);
	}

	.activity-panel-close svg,
	.activity-action svg,
	.module-add-option svg {
		width: 1.02rem;
		height: 1.02rem;
		stroke: currentColor;
		stroke-width: 1.9;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.activity-box-menu {
		display: grid;
		grid-template-columns: 1fr;
		gap: 0.52rem;
		align-content: start;
	}

	.activity-action {
		width: 100%;
		min-height: 52px;
		border-radius: 16px;
		border: 1px solid var(--panel-button-border);
		background: var(--panel-button-bg);
		display: inline-flex;
		align-items: center;
		gap: 0.58rem;
		padding: 0 0.85rem;
		justify-content: flex-start;
		font-size: 0.84rem;
		font-weight: 700;
		color: var(--panel-text);
		cursor: pointer;
		transition:
			transform 160ms ease,
			background 180ms ease,
			border-color 180ms ease,
			box-shadow 180ms ease;
	}

	.activity-action:hover {
		transform: translateY(-1px);
		background: var(--panel-button-hover);
		border-color: var(--panel-active-border);
		box-shadow: 0 12px 24px rgba(72, 107, 154, 0.18);
	}

	.activity-action.is-active {
		border-color: var(--panel-active-border);
		background: var(--panel-active-bg);
		box-shadow: 0 12px 24px rgba(82, 132, 203, 0.2);
	}

	.add-action {
		background: linear-gradient(
			145deg,
			rgba(184, 242, 230, 0.26),
			rgba(149, 229, 213, 0.16)
		);
		border-color: rgba(103, 194, 170, 0.48);
	}

	:global(:root[data-theme='dark']) .add-action,
	:global(.theme-dark) .add-action {
		background: linear-gradient(
			145deg,
			rgba(27, 101, 92, 0.38),
			rgba(20, 74, 74, 0.3)
		);
		border-color: rgba(130, 221, 195, 0.42);
	}

	.module-add-menu {
		display: grid;
		gap: 0.48rem;
		padding: 0.58rem;
		border-radius: 16px;
		border: 1px solid var(--panel-button-border);
		background: rgba(255, 255, 255, 0.2);
		backdrop-filter: blur(18px) saturate(165%);
		-webkit-backdrop-filter: blur(18px) saturate(165%);
		min-width: 0;
	}

	:global(:root[data-theme='dark']) .module-add-menu,
	:global(.theme-dark) .module-add-menu {
		background: rgba(25, 40, 61, 0.34);
	}

	.module-add-option {
		border: 1px solid var(--panel-button-border);
		border-radius: 13px;
		background: var(--panel-button-bg);
		color: var(--panel-text);
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		font-size: 0.79rem;
		font-weight: 700;
		padding: 0.58rem 0.68rem;
		cursor: pointer;
		transition:
			background 180ms ease,
			border-color 180ms ease,
			transform 150ms ease;
	}

	.module-add-option:hover {
		border-color: var(--panel-active-border);
		background: var(--panel-button-hover);
		transform: translateY(-1px);
	}

	.module-empty-state {
		margin: 0;
		font-size: 0.84rem;
		color: var(--panel-muted);
	}

	@media (max-width: 900px) {
		.activity-box-shell {
			z-index: 360;
		}
	}

		@media (max-width: 600px) {
			.activity-panel {
				padding: 0.75rem;
				border-radius: 24px;
			}
		}

		@media (min-width: 601px) {
			:global(:root[data-theme='dark']) .activity-box-main,
			:global(.theme-dark) .activity-box-main {
				background: var(--activity-shell-glass);
			}

			:global(:root[data-theme='dark']) .activity-box-main::before,
			:global(.theme-dark) .activity-box-main::before {
				background: none;
			}

			:global(:root[data-theme='dark']) .activity-panel,
			:global(.theme-dark) .activity-panel {
				background: var(--panel-glass);
			}

			:global(:root[data-theme='dark']) .add-action,
			:global(.theme-dark) .add-action {
				background: var(--panel-button-bg);
			}
		}
	</style>
