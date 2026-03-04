<script lang="ts">
	import git from 'isomorphic-git';
	import http from 'isomorphic-git/http/web';
	import { initFileSystem as initLightningFS } from '$lib/utils/fs';
	import { onDestroy, onMount } from 'svelte';

	export let roomId: string;
	export let currentUser: { id: string; name: string; color: string };

	type ProjectFileEntry = {
		path: string;
		name: string;
		relativePath: string;
		isDir: boolean;
		depth: number;
	};

	let currentFileName = 'temp.txt';
	let repoUrl = '';
	let isCloning = false;
	let cloneError = '';
	let fileExplorerError = '';
	let fileTree: ProjectFileEntry[] = [];
	let vfs: any = null;

	let monacoApi: any = null;
	let editorContainer: HTMLDivElement;
	let editor: any = null;
	let ydoc: any = null;
	let provider: any = null;
	let binding: any = null;
	let awareness: any = null;
	let awarenessChangeHandler: (() => void) | null = null;
	let showReadOnlyWarning = false;
	const fsEventByRemoteClient = new Map<string, number>();

	// Automatically detect language from the file extension
	function getLanguageFromExtension(filename: string) {
		const ext = filename.split('.').pop()?.toLowerCase() || '';
		const map: Record<string, string> = {
			js: 'javascript',
			ts: 'typescript',
			py: 'python',
			cpp: 'cpp',
			cc: 'cpp',
			h: 'cpp',
			java: 'java',
			go: 'go',
			json: 'json',
			html: 'html',
			css: 'css'
		};
		return map[ext] || 'plaintext';
	}

	function normalizeProjectName(value: string) {
		return (value || '').trim().replace(/^\/+/, '');
	}

	function toRelativeProjectPath(path: string) {
		if (!path) {
			return '';
		}
		if (path.startsWith('/project/')) {
			return path.slice('/project/'.length);
		}
		if (path === '/project') {
			return '';
		}
		return path.replace(/^\//, '');
	}

	function yTextKeyForFile(fileName: string) {
		return `file:${normalizeProjectName(fileName)}`;
	}

	function getActiveFS() {
		if (!vfs) {
			throw new Error('Canvas filesystem is not initialized');
		}
		return vfs;
	}

	async function ensureProjectDirectory() {
		try {
			await getActiveFS().promises.stat('/project');
		} catch {
			await getActiveFS().promises.mkdir('/project');
		}
	}

	async function collectProjectFiles(dir = '/project', depth = 0): Promise<ProjectFileEntry[]> {
		const names = await getActiveFS().promises.readdir(dir);
		const sortedNames = [...names].sort((left, right) => left.localeCompare(right));
		const entries: ProjectFileEntry[] = [];
		for (const name of sortedNames) {
			const path = `${dir}/${name}`;
			const stat = await getActiveFS().promises.stat(path);
			const isDir = typeof stat.isDirectory === 'function' ? stat.isDirectory() : false;
			const relativePath = toRelativeProjectPath(path);
			entries.push({
				path,
				name,
				relativePath,
				isDir,
				depth
			});
			if (isDir) {
				const children = await collectProjectFiles(path, depth + 1);
				entries.push(...children);
			}
		}
		return entries;
	}

	async function refreshFileTree() {
		await ensureProjectDirectory();
		fileTree = await collectProjectFiles('/project', 0);
	}

	function firstFileEntry() {
		return fileTree.find((entry) => !entry.isDir) ?? null;
	}

	async function initFileSystem() {
		await ensureProjectDirectory();
		const rootEntries = await getActiveFS().promises.readdir('/project');
		if (rootEntries.length === 0) {
			await getActiveFS().promises.writeFile('/project/temp.txt', 'Type your code here...');
		}
		await refreshFileTree();
		const currentExists = fileTree.some(
			(entry) => !entry.isDir && entry.relativePath === currentFileName
		);
		if (!currentExists) {
			const fallback = firstFileEntry();
			if (fallback) {
				currentFileName = fallback.relativePath || fallback.name;
			}
		}
	}

	async function persistCurrentFileToFS() {
		if (!editor) {
			return;
		}
		const model = editor.getModel();
		if (!model) {
			return;
		}
		const normalized = normalizeProjectName(currentFileName);
		if (!normalized) {
			return;
		}
		await ensureProjectDirectory();
		await getActiveFS().promises.writeFile(`/project/${normalized}`, model.getValue());
	}

	async function recreateBindingForCurrentFile() {
		if (!editor || !ydoc || !awareness || !monacoApi) {
			return;
		}
		const model = editor.getModel();
		if (!model) {
			return;
		}
		const normalizedFileName = normalizeProjectName(currentFileName) || 'temp.txt';
		currentFileName = normalizedFileName;

		binding?.destroy();
		binding = null;

		await ensureProjectDirectory();
		const filePath = `/project/${normalizedFileName}`;
		let diskContent = '';
		try {
			diskContent = await getActiveFS().promises.readFile(filePath, { encoding: 'utf8' });
		} catch {
			const seed = normalizedFileName === 'temp.txt' ? 'Type your code here...' : '';
			diskContent = seed;
			await getActiveFS().promises.writeFile(filePath, seed);
		}

		const ytext = ydoc.getText(yTextKeyForFile(normalizedFileName));
		if (ytext.length === 0 && diskContent) {
			ytext.insert(0, diskContent);
		}

		monacoApi.editor.setModelLanguage(
			model,
			getLanguageFromExtension(normalizedFileName)
		);
		model.setValue('');
		binding = new (await import('y-monaco')).MonacoBinding(
			ytext,
			model,
			new Set([editor]),
			awareness
		);
	}

	function broadcastFsRefresh() {
		if (!awareness) {
			return;
		}
		awareness.setLocalStateField('fs_event', {
			type: 'refresh',
			timestamp: Date.now()
		});
	}

	async function switchToFile(fileName: string) {
		const normalized = normalizeProjectName(fileName);
		if (!normalized) {
			return;
		}
		if (normalized === currentFileName) {
			const model = editor?.getModel?.();
			if (model && monacoApi) {
				monacoApi.editor.setModelLanguage(model, getLanguageFromExtension(normalized));
			}
			return;
		}
		fileExplorerError = '';
		try {
			await persistCurrentFileToFS();
			currentFileName = normalized;
			await recreateBindingForCurrentFile();
			updateEditorAccessMode();
		} catch (error) {
			fileExplorerError = error instanceof Error ? error.message : 'Unable to open file';
		}
	}

	function openProjectFile(entry: ProjectFileEntry) {
		if (entry.isDir) {
			return;
		}
		void switchToFile(entry.relativePath || entry.name);
	}

	async function cloneRepo() {
		const normalizedRepoUrl = repoUrl.trim();
		if (!normalizedRepoUrl || isCloning) {
			return;
		}
		cloneError = '';
		fileExplorerError = '';
		isCloning = true;
		try {
			await ensureProjectDirectory();
			const rootEntries = await getActiveFS().promises.readdir('/project');
			if (
				rootEntries.length === 1 &&
				(rootEntries[0] === 'temp.txt' || rootEntries[0] === 'main.js')
			) {
				await getActiveFS().promises.unlink(`/project/${rootEntries[0]}`);
			}
			await git.clone({
				fs: getActiveFS(),
				http,
				dir: '/project',
				corsProxy: 'https://cors.isomorphic-git.org',
				url: normalizedRepoUrl,
				singleBranch: true,
				depth: 1
			});
			await refreshFileTree();
			const activeExists = fileTree.some(
				(entry) => !entry.isDir && entry.relativePath === currentFileName
			);
			if (!activeExists) {
				const next = firstFileEntry();
				if (next) {
					await switchToFile(next.relativePath || next.name);
				}
			}
			broadcastFsRefresh();
		} catch (error) {
			cloneError = error instanceof Error ? error.message : 'Failed to clone repository';
		} finally {
			isCloning = false;
		}
	}

	async function createNewFile() {
		const rawName = window.prompt('New file name', 'script.py') ?? '';
		const name = normalizeProjectName(rawName);
		if (!name) {
			return;
		}
		fileExplorerError = '';
		try {
			await getActiveFS().promises.writeFile(`/project/${name}`, '');
			await refreshFileTree();
			await switchToFile(name);
			broadcastFsRefresh();
		} catch (error) {
			fileExplorerError = error instanceof Error ? error.message : 'Failed to create file';
		}
	}

	async function createNewFolder() {
		const rawName = window.prompt('New folder name', 'src') ?? '';
		const name = normalizeProjectName(rawName);
		if (!name) {
			return;
		}
		fileExplorerError = '';
		try {
			await getActiveFS().promises.mkdir(`/project/${name}`);
			await refreshFileTree();
			broadcastFsRefresh();
		} catch (error) {
			fileExplorerError = error instanceof Error ? error.message : 'Failed to create folder';
		}
	}

	async function deleteEntry(entry: ProjectFileEntry) {
		fileExplorerError = '';
		try {
			if (entry.isDir) {
				await getActiveFS().promises.rmdir(entry.path);
			} else {
				await getActiveFS().promises.unlink(entry.path);
			}
			const deletedActive = !entry.isDir && entry.relativePath === currentFileName;
			await refreshFileTree();
			if (deletedActive) {
				const next = firstFileEntry();
				if (next) {
					await switchToFile(next.relativePath || next.name);
				} else {
					currentFileName = 'temp.txt';
					await getActiveFS().promises.writeFile('/project/temp.txt', 'Type your code here...');
					await refreshFileTree();
					await switchToFile('temp.txt');
				}
			}
			broadcastFsRefresh();
		} catch (error) {
			fileExplorerError = error instanceof Error ? error.message : 'Failed to delete item';
		}
	}

	function updateEditorAccessMode() {
		if (!awareness || !editor) {
			return;
		}
		let editorsOnCurrentFile = 0;
		const states = awareness.getStates();
		for (const state of states.values()) {
			if (state?.currentFile === currentFileName) {
				editorsOnCurrentFile += 1;
			}
		}
		const shouldBeReadOnly = editorsOnCurrentFile > 5;
		editor.updateOptions({ readOnly: shouldBeReadOnly });
		showReadOnlyWarning = shouldBeReadOnly;
	}

	async function handleAwarenessChange() {
		updateEditorAccessMode();
		if (!awareness) {
			return;
		}
		const states = awareness.getStates();
		let shouldRefresh = false;
		for (const [clientId, state] of states.entries()) {
			if (String(clientId) === String(awareness.clientID)) {
				continue;
			}
			const nextTimestamp = Number(state?.fs_event?.timestamp ?? 0);
			if (!Number.isFinite(nextTimestamp) || nextTimestamp <= 0) {
				continue;
			}
			const key = String(clientId);
			const previousTimestamp = fsEventByRemoteClient.get(key) ?? 0;
			if (nextTimestamp > previousTimestamp) {
				fsEventByRemoteClient.set(key, nextTimestamp);
				shouldRefresh = true;
			}
		}
		if (shouldRefresh) {
			await refreshFileTree();
		}
	}

	$: if (awareness) {
		awareness.setLocalStateField('currentFile', currentFileName);
		updateEditorAccessMode();
	}

	onMount(async () => {
		vfs = await initLightningFS();
		if (!vfs) {
			fileExplorerError = 'File system is unavailable in this environment';
			return;
		}

		const monaco = await import('monaco-editor');
		const Y = await import('yjs');
		const { WebsocketProvider } = await import('y-websocket');
		const { MonacoBinding } = await import('y-monaco');
		monacoApi = monaco;

		editor = monaco.editor.create(editorContainer, {
			theme: 'vs-dark',
			language: 'plaintext',
			automaticLayout: true,
			padding: { top: 16, bottom: 16 },
			fontFamily: "'Fira Code', 'JetBrains Mono', monospace",
			fontLigatures: true,
			minimap: { enabled: false },
			scrollbar: {
				verticalScrollbarSize: 8,
				horizontalScrollbarSize: 8
			},
			roundedSelection: true,
			renderLineHighlight: 'all'
		});

		const model = editor.getModel();
		if (!model) {
			return;
		}

		ydoc = new Y.Doc();
		provider = new WebsocketProvider(`ws://${window.location.host}/ws/canvas`, roomId, ydoc);
		awareness = provider.awareness;
		awareness.setLocalStateField('user', {
			name: currentUser.name,
			color: currentUser.color
		});
		awareness.setLocalStateField('currentFile', currentFileName);
		awarenessChangeHandler = () => {
			void handleAwarenessChange();
		};
		awareness.on('change', awarenessChangeHandler);

		// Keep type reference alive for dynamic import consistency.
		void MonacoBinding;

		await initFileSystem();
		await recreateBindingForCurrentFile();
		updateEditorAccessMode();
	});

	onDestroy(() => {
		void persistCurrentFileToFS();
		if (awareness && awarenessChangeHandler && typeof awareness.off === 'function') {
			awareness.off('change', awarenessChangeHandler);
		}
		awareness = null;
		awarenessChangeHandler = null;
		binding?.destroy();
		provider?.destroy();
		ydoc?.destroy();
		editor?.dispose();
	});
</script>

<div class="canvas-shell">
	{#if showReadOnlyWarning}
		<div class="canvas-readonly-warning" role="status" aria-live="polite">
			Max 5 editors reached. You are in read-only mode.
		</div>
	{/if}
	<aside class="canvas-sidebar">
		<div class="clone-controls">
			<input
				type="url"
				class="repo-url-input"
				placeholder="https://github.com/owner/repo.git"
				bind:value={repoUrl}
			/>
			<button type="button" class="clone-button" on:click={cloneRepo} disabled={isCloning}>
				{isCloning ? 'Cloning...' : 'Clone'}
			</button>
		</div>
		{#if cloneError}
			<div class="clone-error" role="status" aria-live="polite">{cloneError}</div>
		{/if}

		<div class="file-explorer-header">
			<span>Explorer</span>
			<div class="file-explorer-actions">
				<button
					type="button"
					class="file-action-btn"
					title="New File"
					aria-label="New File"
					on:click={() => void createNewFile()}
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d="M12 5v14M5 12h14" />
					</svg>
				</button>
				<button
					type="button"
					class="file-action-btn"
					title="New Folder"
					aria-label="New Folder"
					on:click={() => void createNewFolder()}
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d="M3.5 7.5h6l2 2h9v8.5a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2V7.5Z" />
					</svg>
				</button>
			</div>
		</div>
		{#if fileExplorerError}
			<div class="file-error" role="status" aria-live="polite">{fileExplorerError}</div>
		{/if}

		<div class="file-list">
			{#if fileTree.length === 0}
				<div class="file-list-empty">No files yet</div>
			{:else}
				{#each fileTree as entry (entry.path)}
					<div
						class="file-entry-row"
						class:active={!entry.isDir && entry.relativePath === currentFileName}
					>
						<button
							type="button"
							class="file-entry-main"
							class:is-dir={entry.isDir}
							style:padding-left={`${0.65 + entry.depth * 0.75}rem`}
							on:click={() => openProjectFile(entry)}
						>
							{entry.name}
						</button>
						<button
							type="button"
							class="file-entry-delete"
							title="Delete"
							aria-label="Delete"
							on:click|stopPropagation={() => void deleteEntry(entry)}
						>
							<svg viewBox="0 0 24 24" aria-hidden="true">
								<path d="M5 7h14M10 11v6M14 11v6M8 7l1-2h6l1 2M7 7l1 12h8l1-12" />
							</svg>
						</button>
					</div>
				{/each}
			{/if}
		</div>
	</aside>
	<div class="canvas-editor">
		<div class="code-canvas" bind:this={editorContainer}></div>
	</div>
</div>

<style>
	.canvas-shell {
		position: relative;
		width: 100%;
		height: 100%;
		min-height: 320px;
		display: grid;
		grid-template-columns: minmax(190px, 250px) minmax(0, 1fr);
		overflow: hidden;
	}

	.canvas-sidebar {
		min-width: 0;
		min-height: 0;
		display: flex;
		flex-direction: column;
		gap: 0.55rem;
		border-right: 1px solid rgba(120, 134, 160, 0.35);
		background: rgba(10, 14, 22, 0.72);
		padding: 0.55rem;
	}

	.clone-controls {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: 0.45rem;
	}

	.repo-url-input {
		width: 100%;
		min-width: 0;
		border: 1px solid rgba(109, 127, 160, 0.48);
		background: rgba(16, 22, 34, 0.86);
		color: #e7edf8;
		padding: 0.4rem 0.5rem;
		border-radius: 0.42rem;
		font-size: 0.75rem;
	}

	.clone-button {
		border: 1px solid rgba(95, 130, 180, 0.7);
		background: rgba(36, 71, 130, 0.92);
		color: #f7fbff;
		border-radius: 0.42rem;
		padding: 0.38rem 0.62rem;
		font-size: 0.75rem;
		font-weight: 600;
		cursor: pointer;
		white-space: nowrap;
	}

	.clone-button:disabled {
		opacity: 0.75;
		cursor: progress;
	}

	.clone-error,
	.file-error {
		font-size: 0.72rem;
		font-weight: 500;
		color: #fbcaca;
		background: rgba(137, 23, 23, 0.33);
		border: 1px solid rgba(226, 126, 126, 0.55);
		padding: 0.4rem 0.5rem;
		border-radius: 0.42rem;
	}

	.file-explorer-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		color: #dfe8f7;
		font-size: 0.72rem;
		font-weight: 700;
		letter-spacing: 0.03em;
		text-transform: uppercase;
	}

	.file-explorer-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.3rem;
	}

	.file-action-btn {
		border: 1px solid rgba(103, 125, 160, 0.52);
		background: rgba(24, 35, 52, 0.88);
		color: #dbe6f8;
		border-radius: 0.35rem;
		width: 1.45rem;
		height: 1.35rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		padding: 0;
	}

	.file-action-btn:hover {
		border-color: rgba(139, 168, 211, 0.68);
		background: rgba(41, 61, 92, 0.92);
	}

	.file-action-btn svg,
	.file-entry-delete svg {
		width: 0.85rem;
		height: 0.85rem;
		stroke: currentColor;
		stroke-width: 2;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.file-list {
		flex: 1;
		min-height: 0;
		overflow: auto;
		display: flex;
		flex-direction: column;
		gap: 0.22rem;
	}

	.file-list-empty {
		font-size: 0.74rem;
		color: rgba(221, 231, 246, 0.74);
		padding: 0.45rem 0.5rem;
	}

	.file-entry-row {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		align-items: center;
		gap: 0.28rem;
		border-radius: 0.36rem;
		border: 1px solid transparent;
		background: rgba(21, 28, 42, 0.68);
	}

	.file-entry-row:hover {
		border-color: rgba(127, 153, 194, 0.55);
		background: rgba(34, 45, 67, 0.86);
	}

	.file-entry-row.active {
		border-color: rgba(114, 159, 236, 0.72);
		background: rgba(39, 67, 117, 0.95);
	}

	.file-entry-main {
		border: none;
		background: transparent;
		color: #dbe6f8;
		padding: 0.32rem 0.48rem;
		text-align: left;
		font-size: 0.72rem;
		line-height: 1.3;
		cursor: pointer;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.file-entry-main.is-dir {
		color: #b8c8e2;
	}

	.file-entry-delete {
		opacity: 0;
		border: 1px solid rgba(108, 123, 149, 0.45);
		background: rgba(21, 29, 43, 0.9);
		color: #e0e8f8;
		border-radius: 0.32rem;
		width: 1.35rem;
		height: 1.22rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		padding: 0;
		margin-right: 0.22rem;
		transition: opacity 0.12s ease;
	}

	.file-entry-row:hover .file-entry-delete,
	.file-entry-row.active .file-entry-delete {
		opacity: 1;
	}

	.file-entry-delete:hover {
		border-color: rgba(231, 138, 138, 0.72);
		color: #ffd1d1;
		background: rgba(109, 26, 26, 0.86);
	}

	.canvas-editor {
		position: relative;
		min-width: 0;
		min-height: 0;
	}

	.code-canvas {
		width: 100%;
		height: 100%;
		min-height: 320px;
	}

	.canvas-readonly-warning {
		position: absolute;
		top: 0.65rem;
		right: 0.65rem;
		z-index: 3;
		background: rgba(153, 27, 27, 0.94);
		color: #fff;
		padding: 0.35rem 0.6rem;
		border-radius: 0.45rem;
		font-size: 0.78rem;
		font-weight: 600;
		line-height: 1.2;
		box-shadow: 0 6px 18px rgba(0, 0, 0, 0.24);
		max-width: min(90%, 340px);
	}

	@media (max-width: 900px) {
		.canvas-shell {
			grid-template-columns: 1fr;
			grid-template-rows: minmax(170px, 36%) minmax(0, 1fr);
		}

		.canvas-sidebar {
			border-right: none;
			border-bottom: 1px solid rgba(120, 134, 160, 0.35);
		}
	}
</style>
