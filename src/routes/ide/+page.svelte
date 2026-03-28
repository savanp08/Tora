<script lang="ts">
	import { browser } from '$app/environment';
	import { page } from '$app/stores';
	import { onMount } from 'svelte';
	import { isDarkMode } from '$lib/store';
	import IdeCodeCanvas from '$lib/components/ide/IdeCodeCanvas.svelte';
	import IdeDrawBoard from '$lib/components/ide/IdeDrawBoard.svelte';
	import { buildSoftwareApplicationSchema } from '$lib/utils/seo';

	type WorkspaceMode = 'ide' | 'draw';
	type IdeLanguageLink = { id: string; label: string; query: string };

	const IDE_SESSION_STORAGE_KEY = 'canvasIdeSessionId';
	const IDE_WORKSPACE_MODE_STORAGE_KEY = 'converse_ide_workspace_mode';
	const IDE_LANGUAGE_LINKS: IdeLanguageLink[] = [
		{ id: 'javascript', label: 'JavaScript', query: 'javascript' },
		{ id: 'python', label: 'Python', query: 'python' },
		{ id: 'cpp', label: 'C++', query: 'cpp' },
		{ id: 'c', label: 'C', query: 'c' },
		{ id: 'java', label: 'Java', query: 'java' },
		{ id: 'go', label: 'Go', query: 'go' },
		{ id: 'rust', label: 'Rust', query: 'rust' }
	];
	const DEFAULT_IDE_TITLE = 'AI IDE Online | Run C++ and Other Languages | Tora';
	const DEFAULT_IDE_DESCRIPTION =
		'AI-assisted online IDE to run C++, Python, JavaScript and more with code canvas, terminal, and free draw board.';

	function buildLanguageQueryAliases() {
		const map: Record<string, string> = {};
		for (const language of IDE_LANGUAGE_LINKS) {
			map[language.query] = language.id;
		}
		map.js = 'javascript';
		map.py = 'python';
		map['c++'] = 'cpp';
		map.cc = 'cpp';
		map.golang = 'go';
		map.rs = 'rust';
		return map;
	}

	const IDE_LANGUAGE_QUERY_ALIASES = buildLanguageQueryAliases();

	function resolveIdeSessionId() {
		if (!browser) {
			return 'ide-local-session';
		}
		const existing = (window.sessionStorage.getItem(IDE_SESSION_STORAGE_KEY) || '').trim();
		if (existing) {
			return `ide-local-${existing}`;
		}
		const generated =
			typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
				? crypto.randomUUID()
				: `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
		window.sessionStorage.setItem(IDE_SESSION_STORAGE_KEY, generated);
		return `ide-local-${generated}`;
	}

	function resolveInitialWorkspaceMode(): WorkspaceMode {
		if (!browser) {
			return 'ide';
		}
		const stored = (window.sessionStorage.getItem(IDE_WORKSPACE_MODE_STORAGE_KEY) || '')
			.trim()
			.toLowerCase();
		return stored === 'draw' ? 'draw' : 'ide';
	}

	const ideSessionId = resolveIdeSessionId();
	const ideDrawSessionId = `${ideSessionId}-draw`;
	const ideSchemaJson = buildSoftwareApplicationSchema({
		name: 'Tora AI IDE and Online Code Runner',
		description:
			'Browser IDE with AI-assisted coding, online code execution, and collaborative draw board.',
		url: '/ide'
	});
	const ideUser = {
		id: 'ide-guest',
		name: 'IDE Guest',
		color: '#3b82f6'
	};

	let workspaceMode: WorkspaceMode = 'ide';
	let workspaceModeReady = false;
	let requestedExecutionLanguage = '';
	let selectedExecutionLanguage: IdeLanguageLink | null = null;
	let lastRequestedExecutionLanguage = '';
	let ideSeoTitle = DEFAULT_IDE_TITLE;
	let ideSeoDescription = DEFAULT_IDE_DESCRIPTION;

	const THEME_PREFERENCE_KEY = 'converse_theme_preference';

	function normalizeRequestedExecutionLanguage(value: string | null) {
		const normalized = (value || '').trim().toLowerCase();
		return IDE_LANGUAGE_QUERY_ALIASES[normalized] || '';
	}

	function applyTheme(preference: 'light' | 'dark') {
		const nextDarkMode = preference === 'dark';
		isDarkMode.set(nextDarkMode);
		if (browser) {
			window.localStorage.setItem(THEME_PREFERENCE_KEY, preference);
		}
	}

	onMount(() => {
		if (!browser) {
			return;
		}
		workspaceMode = resolveInitialWorkspaceMode();
		workspaceModeReady = true;
		document.body.classList.add('ide-lab-mode');
		return () => {
			document.body.classList.remove('ide-lab-mode');
		};
	});

	$: if (browser && workspaceModeReady) {
		window.sessionStorage.setItem(IDE_WORKSPACE_MODE_STORAGE_KEY, workspaceMode);
	}

	$: requestedExecutionLanguage = normalizeRequestedExecutionLanguage(
		$page.url.searchParams.get('lang') || $page.url.searchParams.get('language')
	);

	$: selectedExecutionLanguage =
		IDE_LANGUAGE_LINKS.find((language) => language.id === requestedExecutionLanguage) || null;

	$: if (requestedExecutionLanguage !== lastRequestedExecutionLanguage) {
		lastRequestedExecutionLanguage = requestedExecutionLanguage;
		if (requestedExecutionLanguage && workspaceMode !== 'ide') {
			workspaceMode = 'ide';
		}
	}

	$: ideSeoTitle = selectedExecutionLanguage
		? `${selectedExecutionLanguage.label} Online IDE | Run ${selectedExecutionLanguage.label} in Browser | Tora`
		: DEFAULT_IDE_TITLE;

	$: ideSeoDescription = selectedExecutionLanguage
		? `AI-assisted online ${selectedExecutionLanguage.label} IDE with code canvas, terminal execution, and free draw board.`
		: DEFAULT_IDE_DESCRIPTION;
</script>

<svelte:head>
	<title>{ideSeoTitle}</title>
	<meta name="description" content={ideSeoDescription} />
	<meta property="og:title" content={ideSeoTitle} />
	<meta property="og:description" content={ideSeoDescription} />
	<meta name="twitter:card" content="summary_large_image" />
	<script type="application/ld+json">
		{@html ideSchemaJson}
	</script>
</svelte:head>

<section class="ide-lab" class:theme-light={!$isDarkMode}>
	<div class="ide-main">
		<header class="ide-toolbar">
			<div class="toolbar-left">
				<div class="mode-toggle">
					<button
						type="button"
						class="mode-btn"
						class:is-active={workspaceMode === 'ide'}
						on:click={() => (workspaceMode = 'ide')}
					>
						IDE
					</button>
					<button
						type="button"
						class="mode-btn"
						class:is-active={workspaceMode === 'draw'}
						on:click={() => (workspaceMode = 'draw')}
					>
						Free Draw
					</button>
				</div>
				<div class="theme-toggle" role="group" aria-label="Theme">
					<button
						type="button"
						class="theme-btn"
						class:is-active={!$isDarkMode}
						on:click={() => applyTheme('light')}
						aria-pressed={!$isDarkMode}
					>
						Light
					</button>
					<button
						type="button"
						class="theme-btn"
						class:is-active={$isDarkMode}
						on:click={() => applyTheme('dark')}
						aria-pressed={$isDarkMode}
					>
						Dark
					</button>
				</div>
			</div>
			<p class="mode-note">
				Local-only session. No backend save or sync.
			</p>
		</header>
		<nav class="ide-language-links" aria-label="Open IDE by language">
			
		</nav>

		<div class="ide-stage">
			{#if workspaceMode === 'ide'}
				<IdeCodeCanvas
					roomId={ideSessionId}
					currentUser={ideUser}
					isEphemeralRoom={true}
					requestScope="ide"
					remoteSyncEnabled={false}
					initialTerminalHeight={320}
					initialExecutionLanguage={requestedExecutionLanguage}
				/>
			{:else}
				<IdeDrawBoard
					roomId={ideDrawSessionId}
					isDarkMode={$isDarkMode}
					sessionOnly={true}
					canEdit={true}
					canModerateBoard={true}
					currentUserId={ideUser.id}
					currentUsername={ideUser.name}
					isEphemeralRoom={true}
					on:close={() => (workspaceMode = 'ide')}
				/>
			{/if}
		</div>
	</div>

	<aside class="ide-ad-rail" aria-label="Sponsored panel placeholder">
		<div class="ad-card">
			<h2>Ad Slot</h2>
			<p>Primary Area for sponsor modules or promo cards.</p>
		</div>
		<div class="ad-card muted">
			<h3>Second Slot</h3>
			<p>contextual ads or announcements.</p>
		</div>
	</aside>
</section>

<style>
	.ide-lab {
		height: 100vh;
		display: grid;
		grid-template-columns: minmax(0, 1fr) 300px;
		overflow: hidden;
		background:
			radial-gradient(circle at top left, rgba(34, 197, 94, 0.15), transparent 45%),
			radial-gradient(circle at 82% 14%, rgba(59, 130, 246, 0.18), transparent 42%),
			#0b1220;
	}

	.ide-main {
		min-width: 0;
		min-height: 0;
		display: grid;
		grid-template-rows: auto auto minmax(0, 1fr);
		gap: 0.65rem;
		padding: 0.7rem;
	}

	.ide-toolbar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.8rem;
		padding: 0.62rem 0.72rem;
		border: 1px solid rgba(148, 163, 184, 0.3);
		background: rgba(15, 23, 42, 0.8);
		border-radius: 0.85rem;
		color: #e2e8f0;
	}

	.mode-toggle {
		display: inline-flex;
		gap: 0.4rem;
	}

	.toolbar-left {
		display: inline-flex;
		align-items: center;
		gap: 0.52rem;
		flex-wrap: wrap;
	}

	.mode-btn {
		border: 1px solid rgba(148, 163, 184, 0.42);
		background: rgba(30, 41, 59, 0.86);
		color: #e2e8f0;
		padding: 0.42rem 0.66rem;
		border-radius: 0.52rem;
		font-size: 0.78rem;
		font-weight: 600;
		cursor: pointer;
	}

	.mode-btn:hover {
		border-color: rgba(191, 219, 254, 0.7);
		background: rgba(51, 65, 85, 0.92);
	}

	.mode-btn.is-active {
		border-color: rgba(34, 197, 94, 0.75);
		background: rgba(22, 163, 74, 0.24);
	}

	.theme-toggle {
		display: inline-flex;
		gap: 0.34rem;
		padding: 0.2rem;
		border: 1px solid rgba(148, 163, 184, 0.28);
		border-radius: 0.58rem;
		background: rgba(15, 23, 42, 0.46);
	}

	.theme-btn {
		border: 1px solid rgba(148, 163, 184, 0.36);
		background: rgba(30, 41, 59, 0.66);
		color: #dbe7f8;
		padding: 0.34rem 0.56rem;
		border-radius: 0.46rem;
		font-size: 0.72rem;
		font-weight: 600;
		cursor: pointer;
	}

	.theme-btn:hover {
		border-color: rgba(191, 219, 254, 0.72);
	}

	.theme-btn.is-active {
		border-color: rgba(96, 165, 250, 0.78);
		background: rgba(37, 99, 235, 0.28);
		color: #eff6ff;
	}

	.mode-note {
		margin: 0;
		font-size: 0.78rem;
		color: #cbd5e1;
	}

	.ide-language-links {
		display: flex;
		flex-wrap: wrap;
		gap: 0.4rem 0.48rem;
		align-items: center;
		padding: 0.48rem 0.6rem;
		border: 1px solid rgba(148, 163, 184, 0.28);
		border-radius: 0.72rem;
		background: rgba(15, 23, 42, 0.58);
	}

	.ide-language-links span {
		font-size: 0.73rem;
		font-weight: 600;
		color: #cbd5e1;
	}

	.ide-language-links a {
		font-size: 0.72rem;
		color: #dbeafe;
		text-decoration: none;
		padding: 0.2rem 0.42rem;
		border-radius: 999px;
		border: 1px solid rgba(147, 197, 253, 0.28);
		background: rgba(30, 64, 175, 0.18);
	}

	.ide-language-links a:hover {
		border-color: rgba(147, 197, 253, 0.58);
		background: rgba(30, 64, 175, 0.3);
	}

	.ide-stage {
		min-width: 0;
		min-height: 0;
		border: 1px solid rgba(148, 163, 184, 0.3);
		border-radius: 0.92rem;
		overflow: hidden;
		background: rgba(2, 6, 23, 0.76);
	}

	.ide-stage :global(.canvas-shell) {
		height: 100%;
	}

	.ide-ad-rail {
		min-height: 0;
		padding: 0.72rem 0.72rem 0.72rem 0;
		display: flex;
		flex-direction: column;
		gap: 0.7rem;
	}

	.ad-card {
		border-radius: 0.86rem;
		border: 1px dashed rgba(148, 163, 184, 0.46);
		background: rgba(15, 23, 42, 0.66);
		padding: 0.9rem;
		color: #e2e8f0;
	}

	.ad-card h2,
	.ad-card h3 {
		margin: 0 0 0.45rem;
		font-size: 0.95rem;
	}

	.ad-card p {
		margin: 0;
		font-size: 0.78rem;
		line-height: 1.45;
		color: #cbd5e1;
	}

	.ad-card.muted {
		margin-top: auto;
	}

	:global(body.ide-lab-mode) {
		overflow: hidden;
	}

	@media (max-width: 1180px) {
		.ide-lab {
			grid-template-columns: minmax(0, 1fr) 240px;
		}
	}

	.ide-lab.theme-light {
		background:
			radial-gradient(circle at top left, rgba(16, 185, 129, 0.12), transparent 44%),
			radial-gradient(circle at 82% 14%, rgba(59, 130, 246, 0.14), transparent 42%),
			#edf4ff;
	}

	.ide-lab.theme-light .ide-toolbar {
		background: rgba(255, 255, 255, 0.86);
		border-color: rgba(132, 157, 194, 0.32);
		color: #132845;
	}

	.ide-lab.theme-light .mode-btn {
		background: rgba(237, 244, 255, 0.92);
		border-color: rgba(138, 166, 208, 0.52);
		color: #18365f;
	}

	.ide-lab.theme-light .mode-btn:hover {
		border-color: rgba(86, 146, 235, 0.72);
		background: rgba(217, 230, 250, 0.95);
	}

	.ide-lab.theme-light .mode-btn.is-active {
		border-color: rgba(22, 163, 74, 0.72);
		background: rgba(22, 163, 74, 0.2);
	}

	.ide-lab.theme-light .theme-toggle {
		background: rgba(227, 238, 255, 0.8);
		border-color: rgba(151, 178, 219, 0.46);
	}

	.ide-lab.theme-light .theme-btn {
		background: rgba(247, 251, 255, 0.92);
		border-color: rgba(145, 174, 218, 0.5);
		color: #1a3d68;
	}

	.ide-lab.theme-light .theme-btn:hover {
		border-color: rgba(92, 151, 235, 0.76);
	}

	.ide-lab.theme-light .theme-btn.is-active {
		border-color: rgba(96, 165, 250, 0.72);
		background: rgba(37, 99, 235, 0.2);
		color: #143d7a;
	}

	.ide-lab.theme-light .mode-note {
		color: #365b89;
	}

	.ide-lab.theme-light .ide-language-links {
		border-color: rgba(133, 158, 197, 0.42);
		background: rgba(255, 255, 255, 0.82);
	}

	.ide-lab.theme-light .ide-language-links span {
		color: #375a87;
	}

	.ide-lab.theme-light .ide-language-links a {
		color: #1f4f88;
		border-color: rgba(118, 157, 212, 0.52);
		background: rgba(210, 226, 249, 0.72);
	}

	.ide-lab.theme-light .ide-language-links a:hover {
		border-color: rgba(78, 136, 224, 0.68);
		background: rgba(190, 214, 248, 0.84);
	}

	.ide-lab.theme-light .ide-stage {
		border-color: rgba(133, 158, 197, 0.36);
		background: rgba(255, 255, 255, 0.82);
	}

	.ide-lab.theme-light .ad-card {
		border-color: rgba(128, 158, 201, 0.44);
		background: rgba(255, 255, 255, 0.78);
		color: #18365b;
	}

	.ide-lab.theme-light .ad-card p {
		color: #355b8a;
	}

	@media (max-width: 900px) {
		.ide-lab {
			grid-template-columns: minmax(0, 1fr);
			grid-template-rows: minmax(0, 1fr) auto;
			height: 100svh;
		}

		.ide-ad-rail {
			padding: 0 0.7rem 0.7rem;
			flex-direction: row;
			overflow-x: auto;
		}

		.ad-card {
			min-width: 220px;
		}

		.ide-toolbar {
			flex-direction: column;
			align-items: flex-start;
		}

		.ide-language-links {
			padding: 0.5rem;
		}
	}
</style>
