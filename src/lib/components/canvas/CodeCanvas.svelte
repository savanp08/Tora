<script lang="ts">
	import { zipSync, unzipSync } from 'fflate';
	import { initFileSystem as initLightningFS } from '$lib/utils/fs';
	import { applyUpdate, encodeStateAsUpdate } from 'yjs';
	import { onDestroy, onMount, tick } from 'svelte';
	import 'xterm/css/xterm.css';

	export let roomId: string;
	export let currentUser: { id: string; name: string; color: string };

	type ProjectFileEntry = {
		path: string;
		name: string;
		relativePath: string;
		isDir: boolean;
		depth: number;
	};

	type Disposable = {
		dispose: () => void;
	};

	type WebkitFileEntry = {
		isFile: true;
		isDirectory: false;
		name: string;
		file: (
			successCallback: (file: File) => void,
			errorCallback?: (error: DOMException | Error) => void
		) => void;
	};

	type WebkitDirectoryReader = {
		readEntries: (
			successCallback: (entries: WebkitEntry[]) => void,
			errorCallback?: (error: DOMException | Error) => void
		) => void;
	};

	type WebkitDirectoryEntry = {
		isFile: false;
		isDirectory: true;
		name: string;
		createReader: () => WebkitDirectoryReader;
	};

	type WebkitEntry = WebkitFileEntry | WebkitDirectoryEntry;

	type DataTransferItemWithWebkitEntry = DataTransferItem & {
		webkitGetAsEntry?: () => WebkitEntry | null;
	};

	type SharedFileTreeEntry = {
		isDir: boolean;
	};

	type PromptType = '' | 'rename' | 'new-file' | 'new-folder';

	type PromptState = {
		isOpen: boolean;
		type: PromptType;
		initialValue: string;
		resolve: ((value: string) => void) | null;
		reject: ((reason?: unknown) => void) | null;
	};

	type MobileCanvasPane = 'explorer' | 'editor';
	type CanvasSocketPayload = string | ArrayBufferLike | Blob | ArrayBufferView;
	type CanvasDebugWebSocket = WebSocket & {
		__canvasDebugOriginalSend?: (data: CanvasSocketPayload) => void;
		__canvasDebugSendWrapped?: boolean;
	};

	const DEFAULT_PROJECT_FILE_NAME = 'main.js';
	const DEFAULT_PROJECT_FILE_CONTENT = "console.log('Hello from Converse canvas');\n";
	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://localhost:8080';
	const textEncoder = new TextEncoder();
	const textDecoder = new TextDecoder();
	const QUERY_AWARENESS_MESSAGE_TYPE = 3;
	const FILE_TREE_SYNC_ORIGIN = 'canvas-file-tree-sync';
	const MODEL_SYNC_ORIGIN = 'canvas-model-sync';
	const PROVIDER_SYNC_TIMEOUT_MS = 8000;
	const SNAPSHOT_LOAD_TIMEOUT_MS = 5000;
	const PROMPT_CANCELLED_ERROR = 'canvas-prompt-cancelled';
	const CANVAS_CLIENT_LOG_PREFIX = '[canvas-client]';
	let currentFile = '';
	let openTabs: string[] = [];
	let fileExplorerError = '';
	let githubRepoURL = '';
	let isImportingRepo = false;
	let fileTree: ProjectFileEntry[] = [];
	let visibleFileTree: ProjectFileEntry[] = [];
	let vfs: any = null;
	let expandedDirectories: Record<string, boolean> = {};

	let monacoApi: any = null;
	let canvasEditorBodyElement: HTMLDivElement | null = null;
	let editorContainer: HTMLDivElement;
	let editor: any = null;
	let terminalContainer: HTMLDivElement | null = null;
	let terminal: any = null;
	let terminalFitAddon: any = null;
	let terminalResizeObserver: ResizeObserver | null = null;
	let terminalHeight = 200;
	let terminalResizeStartY = 0;
	let terminalResizeStartHeight = 200;
	let yjsApi: any = null;
	let ydoc: any = null;
	let yFileTree: any = null;
	let yFileTreeObserver: ((event: any) => void) | null = null;
	let ydocUpdateHandler: (() => void) | null = null;
	let provider: any = null;
	let providerSnapshotSocket: WebSocket | null = null;
	let providerSnapshotMessageHandler: ((event: MessageEvent) => void) | null = null;
	let providerTransportDebugSocket: CanvasDebugWebSocket | null = null;
	let providerTransportDebugCleanup: (() => void) | null = null;
	let binding: any = null;
	let awareness: any = null;
	let awarenessChangeHandler: (() => void) | null = null;
	let cursorSelectionDisposable: Disposable | null = null;
	let editorContentChangeDisposable: Disposable | null = null;
	let currentYText: any = null;
	let remoteSelectionDecorations: string[] = [];
	let showReadOnlyWarning = false;
	let explorerClipboard: { path: string; isDir: boolean } | null = null;
	let contextMenuOpen = false;
	let contextMenuX = 0;
	let contextMenuY = 0;
	let contextMenuTarget: ProjectFileEntry | null = null;
	let contextMenuElement: HTMLDivElement | null = null;
	let importZipInput: HTMLInputElement | null = null;
	let sidebarElement: HTMLElement | null = null;
	let isSidebarDragOver = false;
	let promptInputElement: HTMLInputElement | null = null;
	let promptInputValue = '';
	let promptState: PromptState = {
		isOpen: false,
		type: '',
		initialValue: '',
		resolve: null,
		reject: null
	};
	let deleteConfirmTarget: ProjectFileEntry | null = null;
	let isCompactCanvasLayout = false;
	let mobileCanvasPane: MobileCanvasPane = 'explorer';
	let remotePresenceStyleElement: HTMLStyleElement | null = null;
	let removeGlobalContextHandlers: (() => void) | null = null;
	let removeCanvasViewportListener: (() => void) | null = null;
	let removeTerminalResizeListeners: (() => void) | null = null;
	let removeBeforeUnloadListener: (() => void) | null = null;
	let saveTimeout: number | null = null;
	let filePersistTimeout: number | null = null;
	let periodicSnapshotInterval: number | null = null;
	let snapshotDirty = false;
	const presenceSessionId = createPresenceSessionId();

	function canvasClientLog(event: string, payload?: unknown) {
		const timestamp = new Date().toISOString();
		if (payload === undefined) {
			console.log(`${CANVAS_CLIENT_LOG_PREFIX} ${timestamp} ${event}`);
			return;
		}
		console.log(`${CANVAS_CLIENT_LOG_PREFIX} ${timestamp} ${event}`, payload);
	}

	function canvasClientNarrative(message: string, payload?: unknown) {
		const timestamp = new Date().toISOString();
		if (payload === undefined) {
			console.log(`${CANVAS_CLIENT_LOG_PREFIX} ${timestamp} ${message}`);
			return;
		}
		console.log(`${CANVAS_CLIENT_LOG_PREFIX} ${timestamp} ${message}`, payload);
	}

	function describeSocketPayload(payload: unknown) {
		if (typeof payload === 'string') {
			return { kind: 'text', size: payload.length };
		}
		if (payload instanceof ArrayBuffer) {
			return { kind: 'arraybuffer', size: payload.byteLength };
		}
		if (payload instanceof Uint8Array) {
			return { kind: 'uint8array', size: payload.byteLength };
		}
		if (typeof Blob !== 'undefined' && payload instanceof Blob) {
			return { kind: 'blob', size: payload.size };
		}
		if (ArrayBuffer.isView(payload)) {
			return { kind: 'arraybuffer-view', size: payload.byteLength };
		}
		return { kind: typeof payload, size: 0 };
	}

	function syncCurrentModelIntoYText() {
		if (!ydoc || !editor || !currentYText) {
			return;
		}
		const model = editor.getModel?.();
		if (!model) {
			return;
		}
		const modelValue = model.getValue();
		if (currentYText.toString() === modelValue) {
			return;
		}
		ydoc.transact(() => {
			syncYTextValue(currentYText, modelValue);
		}, MODEL_SYNC_ORIGIN);
	}

	function createCanvasSnapshotBytes() {
		if (!ydoc) {
			return null;
		}
		syncCurrentModelIntoYText();
		const snapshot = encodeStateAsUpdate(ydoc);
		const snapshotBytes = new Uint8Array(snapshot.length);
		snapshotBytes.set(snapshot);
		return snapshotBytes;
	}

	function canvasSnapshotURL() {
		return `${API_BASE}/api/canvas/${encodeURIComponent(roomId)}/snapshot`;
	}

	async function saveCanvasSnapshotNow(options?: { useBeacon?: boolean }) {
		if (!roomId) {
			return false;
		}
		const snapshotBytes = createCanvasSnapshotBytes();
		if (!snapshotBytes) {
			return false;
		}
		const url = canvasSnapshotURL();
		if (
			options?.useBeacon &&
			typeof navigator !== 'undefined' &&
			typeof navigator.sendBeacon === 'function'
		) {
			canvasClientNarrative(`Room ${roomId} sending snapshot with beacon.`, {
				url,
				bytes: snapshotBytes.byteLength
			});
			canvasClientLog('snapshot-save-beacon-request', {
				roomId,
				url,
				bytes: snapshotBytes.byteLength
			});
			const beaconQueued = navigator.sendBeacon(
				url,
				new Blob([snapshotBytes], { type: 'application/octet-stream' })
			);
			canvasClientLog('snapshot-save-beacon-response', {
				roomId,
				queued: beaconQueued
			});
			canvasClientNarrative(`Room ${roomId} beacon snapshot queue result.`, {
				queued: beaconQueued
			});
			if (beaconQueued) {
				snapshotDirty = false;
			}
			return beaconQueued;
		}
		try {
			const requestStartedAt = Date.now();
			canvasClientNarrative(`Room ${roomId} sending snapshot with HTTP POST.`, {
				url,
				bytes: snapshotBytes.byteLength
			});
			canvasClientLog('snapshot-save-http-request', {
				roomId,
				url,
				bytes: snapshotBytes.byteLength
			});
			const response = await fetch(url, {
				method: 'POST',
				headers: {
					'Content-Type': 'application/octet-stream'
				},
				body: snapshotBytes
			});
			canvasClientLog('snapshot-save-http-response', {
				roomId,
				status: response.status,
				ok: response.ok
			});
			canvasClientNarrative(`Room ${roomId} snapshot POST completed.`, {
				status: response.status,
				ok: response.ok,
				elapsedMs: Date.now() - requestStartedAt
			});
			if (response.ok) {
				snapshotDirty = false;
			}
			return response.ok;
		} catch (error) {
			canvasClientLog('snapshot-save-http-error', {
				roomId,
				error: error instanceof Error ? error.message : String(error)
			});
			canvasClientNarrative(`Room ${roomId} snapshot POST failed.`, {
				error: error instanceof Error ? error.message : String(error)
			});
			return false;
		}
	}
	function scheduleCanvasSnapshotSave() {
		if (!ydoc || !roomId) {
			return;
		}
		snapshotDirty = true;
		if (saveTimeout) {
			window.clearTimeout(saveTimeout);
			saveTimeout = null;
		}
		saveTimeout = window.setTimeout(async () => {
			await saveCanvasSnapshotNow();
			saveTimeout = null;
		}, 1500);
	}

	function scheduleCurrentFilePersistToFS() {
		if (filePersistTimeout) {
			window.clearTimeout(filePersistTimeout);
			filePersistTimeout = null;
		}
		filePersistTimeout = window.setTimeout(() => {
			void persistCurrentFileToFS();
			filePersistTimeout = null;
		}, 800);
	}

	function canvasWebSocketURL() {
		try {
			const baseURL = new URL(API_BASE, window.location.origin);
			const wsProtocol = baseURL.protocol === 'https:' ? 'wss:' : 'ws:';
			return `${wsProtocol}//${baseURL.host}/ws/canvas`;
		} catch {
			const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
			return `${wsProtocol}//${window.location.host}/ws/canvas`;
		}
	}

	function registerBeforeUnloadPersistence() {
		const persistWithBeacon = () => {
			void persistCurrentFileToFS();
			void saveCanvasSnapshotNow({ useBeacon: true });
		};
		const handleBeforeUnload = () => {
			persistWithBeacon();
		};
		const handlePageHide = () => {
			persistWithBeacon();
		};
		const handleVisibilityChange = () => {
			if (document.visibilityState === 'hidden') {
				persistWithBeacon();
			}
		};
		window.addEventListener('beforeunload', handleBeforeUnload);
		window.addEventListener('pagehide', handlePageHide);
		document.addEventListener('visibilitychange', handleVisibilityChange);
		return () => {
			window.removeEventListener('beforeunload', handleBeforeUnload);
			window.removeEventListener('pagehide', handlePageHide);
			document.removeEventListener('visibilitychange', handleVisibilityChange);
		};
	}

	async function loadPersistedCanvasSnapshotFromServer() {
		if (!roomId || !ydoc) {
			return;
		}
		let timeoutId: number | null = null;
		try {
			const url = `${API_BASE}/api/canvas/${encodeURIComponent(roomId)}/snapshot`;
			const requestStartedAt = Date.now();
			const controller = new AbortController();
			timeoutId = window.setTimeout(() => {
				controller.abort();
			}, SNAPSHOT_LOAD_TIMEOUT_MS);
			canvasClientNarrative(`Room ${roomId} requested full canvas snapshot from server.`, {
				url,
				timeoutMs: SNAPSHOT_LOAD_TIMEOUT_MS
			});
			canvasClientLog('snapshot-load-http-request', { roomId, url });
			const response = await fetch(url, {
				method: 'GET',
				cache: 'no-store',
				signal: controller.signal
			});
			canvasClientLog('snapshot-load-http-response', {
				roomId,
				status: response.status,
				ok: response.ok
			});
			canvasClientNarrative(`Room ${roomId} snapshot GET completed.`, {
				status: response.status,
				ok: response.ok,
				elapsedMs: Date.now() - requestStartedAt
			});
			if (response.status === 204 || response.status === 404) {
				canvasClientLog('snapshot-load-empty', { roomId, status: response.status });
				canvasClientNarrative(`Room ${roomId} has no snapshot on server.`, {
					status: response.status
				});
				return;
			}
			if (!response.ok) {
				canvasClientLog('snapshot-load-non-ok', { roomId, status: response.status });
				canvasClientNarrative(`Room ${roomId} snapshot GET failed with non-OK status.`, {
					status: response.status
				});
				return;
			}
			const snapshot = new Uint8Array(await response.arrayBuffer());
			if (snapshot.length === 0) {
				canvasClientLog('snapshot-load-zero-bytes', { roomId });
				canvasClientNarrative(`Room ${roomId} snapshot response returned zero bytes.`);
				return;
			}
			applyUpdate(ydoc, snapshot);
			canvasClientLog('snapshot-load-applied', { roomId, bytes: snapshot.byteLength });
			canvasClientNarrative(`Room ${roomId} snapshot applied to Yjs document.`, {
				bytes: snapshot.byteLength
			});
		} catch (error) {
			const isAbortError =
				(error instanceof DOMException && error.name === 'AbortError') ||
				(error instanceof Error && error.name === 'AbortError');
			canvasClientLog('snapshot-load-http-error', {
				roomId,
				error: error instanceof Error ? error.message : String(error)
			});
			if (isAbortError) {
				canvasClientNarrative(`Room ${roomId} snapshot GET timed out.`, {
					timeoutMs: SNAPSHOT_LOAD_TIMEOUT_MS
				});
			} else {
				canvasClientNarrative(`Room ${roomId} snapshot GET failed.`, {
					error: error instanceof Error ? error.message : String(error)
				});
			}
			// Ignore transient snapshot load failures.
		} finally {
			if (timeoutId !== null) {
				window.clearTimeout(timeoutId);
			}
		}
	}

	async function configureMonacoWorkerEnvironment() {
		if (typeof window === 'undefined') {
			return;
		}
		const globalWindow = window as Window & {
			MonacoEnvironment?: {
				getWorker?: (moduleId: string, label: string) => Worker;
			};
		};
		if (typeof globalWindow.MonacoEnvironment?.getWorker === 'function') {
			return;
		}
		const [
			{ default: EditorWorker },
			{ default: JsonWorker },
			{ default: CssWorker },
			{ default: HtmlWorker },
			{ default: TsWorker }
		] = await Promise.all([
			import('monaco-editor/esm/vs/editor/editor.worker?worker'),
			import('monaco-editor/esm/vs/language/json/json.worker?worker'),
			import('monaco-editor/esm/vs/language/css/css.worker?worker'),
			import('monaco-editor/esm/vs/language/html/html.worker?worker'),
			import('monaco-editor/esm/vs/language/typescript/ts.worker?worker')
		]);
		globalWindow.MonacoEnvironment = {
			...(globalWindow.MonacoEnvironment || {}),
			getWorker: (_moduleId: string, label: string) => {
				switch (label) {
					case 'json':
						return new JsonWorker();
					case 'css':
					case 'scss':
					case 'less':
						return new CssWorker();
					case 'html':
					case 'handlebars':
					case 'razor':
						return new HtmlWorker();
					case 'typescript':
					case 'javascript':
						return new TsWorker();
					default:
						return new EditorWorker();
				}
			}
		};
	}
	function detachProviderSnapshotListener() {
		if (providerSnapshotSocket && providerSnapshotMessageHandler) {
			providerSnapshotSocket.removeEventListener('message', providerSnapshotMessageHandler);
		}
		providerSnapshotSocket = null;
		providerSnapshotMessageHandler = null;
	}

	function detachProviderTransportDebugListener() {
		if (providerTransportDebugCleanup) {
			providerTransportDebugCleanup();
			providerTransportDebugCleanup = null;
		}
		providerTransportDebugSocket = null;
	}

	function attachProviderTransportDebugListener() {
		const socket = provider?.ws as CanvasDebugWebSocket | null;
		if (!socket || providerTransportDebugSocket === socket) {
			return;
		}
		detachProviderTransportDebugListener();
		const onOpen = () => {
			canvasClientLog('ws-open', { roomId });
		};
		const onClose = (event: CloseEvent) => {
			canvasClientLog('ws-close', {
				roomId,
				code: event.code,
				reason: event.reason,
				wasClean: event.wasClean
			});
		};
		const onError = () => {
			canvasClientLog('ws-error', { roomId });
		};
		const onMessage = (event: MessageEvent) => {
			canvasClientLog('ws-recv', { roomId, ...describeSocketPayload(event.data) });
		};
		socket.addEventListener('open', onOpen);
		socket.addEventListener('close', onClose);
		socket.addEventListener('error', onError);
		socket.addEventListener('message', onMessage);
		if (!socket.__canvasDebugSendWrapped) {
			const originalSend = socket.send.bind(socket) as (data: CanvasSocketPayload) => void;
			socket.__canvasDebugOriginalSend = originalSend;
			socket.send = ((data: CanvasSocketPayload) => {
				canvasClientLog('ws-send', { roomId, ...describeSocketPayload(data) });
				originalSend(data);
			}) as typeof socket.send;
			socket.__canvasDebugSendWrapped = true;
		}
		providerTransportDebugSocket = socket;
		providerTransportDebugCleanup = () => {
			socket.removeEventListener('open', onOpen);
			socket.removeEventListener('close', onClose);
			socket.removeEventListener('error', onError);
			socket.removeEventListener('message', onMessage);
			if (socket.__canvasDebugSendWrapped && socket.__canvasDebugOriginalSend) {
				socket.send = socket.__canvasDebugOriginalSend as typeof socket.send;
				delete socket.__canvasDebugOriginalSend;
				delete socket.__canvasDebugSendWrapped;
			}
		};
		canvasClientLog('ws-debug-attached', { roomId });
	}

	function attachProviderSnapshotListener() {
		const socket = provider?.ws as WebSocket | null;
		if (!socket || providerSnapshotSocket === socket) {
			return;
		}
		detachProviderSnapshotListener();
		canvasClientLog('ws-snapshot-listener-attached', { roomId });
		let shouldCaptureInitialBinaryMessage = true;
		const handleMessage = (event: MessageEvent) => {
			if (!shouldCaptureInitialBinaryMessage || !ydoc) {
				return;
			}
			const applyInitialSnapshot = (payload: Uint8Array) => {
				if (!shouldCaptureInitialBinaryMessage || !ydoc) {
					return;
				}
				shouldCaptureInitialBinaryMessage = false;
				try {
					applyUpdate(ydoc, payload);
					canvasClientLog('ws-initial-snapshot-applied', { roomId, bytes: payload.byteLength });
				} catch {
					canvasClientLog('ws-initial-snapshot-ignored', { roomId, bytes: payload.byteLength });
					// Ignore non-snapshot binary protocol packets.
				}
			};
			if (event.data instanceof ArrayBuffer) {
				applyInitialSnapshot(new Uint8Array(event.data));
				return;
			}
			if (event.data instanceof Blob) {
				void event.data
					.arrayBuffer()
					.then((arrayBuffer) => {
						applyInitialSnapshot(new Uint8Array(arrayBuffer));
					})
					.catch(() => {
						shouldCaptureInitialBinaryMessage = false;
						canvasClientLog('ws-initial-snapshot-blob-read-failed', { roomId });
					});
				return;
			}
			if (event.data instanceof Uint8Array) {
				applyInitialSnapshot(event.data);
				return;
			}
			shouldCaptureInitialBinaryMessage = false;
		};
		socket.addEventListener('message', handleMessage);
		providerSnapshotSocket = socket;
		providerSnapshotMessageHandler = handleMessage;
	}

	// Automatically detect language from the file extension
	function getLanguageFromExtension(filename: string) {
		const ext = filename.split('.').pop()?.toLowerCase() || '';
		const map: Record<string, string> = {
			js: 'javascript',
			mjs: 'javascript',
			cjs: 'javascript',
			ts: 'typescript',
			tsx: 'typescript',
			py: 'python',
			cpp: 'cpp',
			cc: 'cpp',
			h: 'cpp',
			hpp: 'cpp',
			c: 'c',
			java: 'java',
			go: 'go',
			json: 'json',
			html: 'html',
			css: 'css',
			md: 'markdown',
			rs: 'rust',
			sh: 'shell',
			yaml: 'yaml',
			yml: 'yaml'
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

	function splitPath(path: string) {
		const normalized = (path || '').replace(/\/+$/, '');
		const index = normalized.lastIndexOf('/');
		if (index <= 0) {
			return { dir: '/project', name: normalized.replace(/^\//, '') };
		}
		return { dir: normalized.slice(0, index), name: normalized.slice(index + 1) };
	}

	function buildPath(dir: string, name: string) {
		const parent = dir.endsWith('/') ? dir.slice(0, -1) : dir;
		return `${parent}/${normalizeProjectName(name)}`;
	}

	function toProjectPath(relativePath: string) {
		const normalized = normalizeProjectName(relativePath);
		return normalized ? `/project/${normalized}` : '/project';
	}

	function normalizeSharedTreeEntry(value: unknown): SharedFileTreeEntry | null {
		if (!value || typeof value !== 'object') {
			return null;
		}
		return { isDir: Boolean((value as SharedFileTreeEntry).isDir) };
	}

	function getEntriesWithinRelativePath(relativePath: string) {
		const normalized = normalizeProjectName(relativePath);
		if (!normalized) {
			return [];
		}
		return fileTree.filter(
			(entry) =>
				entry.relativePath === normalized || entry.relativePath.startsWith(`${normalized}/`)
		);
	}

	function getFileEntriesWithinRelativePath(relativePath: string) {
		return getEntriesWithinRelativePath(relativePath).filter((entry) => !entry.isDir);
	}

	function syncYTextValue(target: any, content: string) {
		const nextContent = content ?? '';
		if (target.toString() === nextContent) {
			return;
		}
		if (target.length > 0) {
			target.delete(0, target.length);
		}
		if (nextContent) {
			target.insert(0, nextContent);
		}
	}

	function clearYTextForRelativePath(relativePath: string) {
		if (!ydoc) {
			return;
		}
		const normalized = normalizeProjectName(relativePath);
		if (!normalized) {
			return;
		}
		const ytext = ydoc.getText(yTextKeyForFile(normalized));
		if (ytext.length > 0) {
			ytext.delete(0, ytext.length);
		}
	}

	async function readProjectFileContent(relativePath: string) {
		const filePath = toProjectPath(relativePath);
		const fileBytes = await getActiveFS().promises.readFile(filePath);
		if (typeof fileBytes === 'string') {
			return fileBytes;
		}
		return textDecoder.decode(fileBytes);
	}

	async function collectSharedFilePayloads(
		entries: Array<{ relativePath: string; isDir: boolean; content?: string }>
	) {
		const normalizedEntries = entries
			.map((entry) => ({
				relativePath: normalizeProjectName(entry.relativePath),
				isDir: entry.isDir,
				content: entry.content
			}))
			.filter((entry) => entry.relativePath !== '');
		const payloads = await Promise.all(
			normalizedEntries.map(async (entry) => {
				if (entry.isDir) {
					return { ...entry, content: '' };
				}
				if (typeof entry.content === 'string') {
					return entry;
				}
				return {
					...entry,
					content: await readProjectFileContent(entry.relativePath)
				};
			})
		);
		return payloads;
	}

	async function upsertSharedEntries(
		entries: Array<{ relativePath: string; isDir: boolean; content?: string }>
	) {
		if (!ydoc || !yFileTree || entries.length === 0) {
			return;
		}
		const payloads = await collectSharedFilePayloads(entries);
		ydoc.transact(() => {
			for (const entry of payloads) {
				yFileTree.set(entry.relativePath, { isDir: entry.isDir });
				if (!entry.isDir) {
					const ytext = ydoc.getText(yTextKeyForFile(entry.relativePath));
					syncYTextValue(ytext, entry.content ?? '');
				}
			}
		}, FILE_TREE_SYNC_ORIGIN);
	}

	function removeSharedEntries(relativePaths: string[], options?: { clearYText?: boolean }) {
		if (!ydoc || !yFileTree || relativePaths.length === 0) {
			return;
		}
		const normalizedPaths = Array.from(
			new Set(relativePaths.map((path) => normalizeProjectName(path)).filter(Boolean))
		);
		ydoc.transact(() => {
			for (const relativePath of normalizedPaths) {
				if (options?.clearYText) {
					clearYTextForRelativePath(relativePath);
				}
				yFileTree.delete(relativePath);
			}
		}, FILE_TREE_SYNC_ORIGIN);
	}

	async function applySharedTreeEntry(
		relativePath: string,
		entry: SharedFileTreeEntry | null,
		action: 'add' | 'update' | 'delete'
	) {
		const normalized = normalizeProjectName(relativePath);
		if (!normalized) {
			return;
		}
		const targetPath = toProjectPath(normalized);
		if (action === 'delete') {
			if (!(await pathExists(targetPath))) {
				return;
			}
			const stat = await getActiveFS().promises.stat(targetPath);
			const isDir = typeof stat.isDirectory === 'function' ? stat.isDirectory() : false;
			if (isDir) {
				await removeDirectoryRecursive(targetPath);
			} else {
				await getActiveFS().promises.unlink(targetPath);
			}
			return;
		}
		if (!entry) {
			return;
		}
		const parentDir = splitPath(targetPath).dir;
		await ensureDirectoryPathExists(parentDir);
		if (entry.isDir) {
			await mkdirIfMissing(targetPath);
			return;
		}
		const ytext = ydoc?.getText?.(yTextKeyForFile(normalized));
		const content = ytext ? ytext.toString() : '';
		if (await pathExists(targetPath)) {
			const stat = await getActiveFS().promises.stat(targetPath);
			const isDir = typeof stat.isDirectory === 'function' ? stat.isDirectory() : false;
			if (isDir) {
				await removeDirectoryRecursive(targetPath);
			}
		}
		await getActiveFS().promises.writeFile(targetPath, content);
	}

	async function reconcileLocalFileSystemWithSharedTree() {
		if (!yFileTree) {
			return;
		}
		const localEntries = await collectProjectFiles('/project', 0);
		const sharedEntries: Array<{ relativePath: string; entry: SharedFileTreeEntry }> = [];
		for (const [relativePath, value] of Array.from(yFileTree.entries()) as Array<
			[string, unknown]
		>) {
			const normalizedPath = normalizeProjectName(String(relativePath));
			const normalizedEntry = normalizeSharedTreeEntry(value);
			if (!normalizedPath || !normalizedEntry) {
				continue;
			}
			sharedEntries.push({
				relativePath: normalizedPath,
				entry: normalizedEntry
			});
		}
		const sharedKeys = new Set(sharedEntries.map((entry) => entry.relativePath));
		const staleEntries = [...localEntries]
			.filter((entry) => !sharedKeys.has(entry.relativePath))
			.sort((left, right) => right.depth - left.depth);
		for (const entry of staleEntries) {
			if (entry.isDir) {
				await removeDirectoryRecursive(entry.path);
			} else {
				await getActiveFS().promises.unlink(entry.path);
			}
		}
		const orderedSharedEntries = sharedEntries.sort((left, right) => {
			const leftDepth = left.relativePath.split('/').length;
			const rightDepth = right.relativePath.split('/').length;
			if (left.entry.isDir !== right.entry.isDir) {
				return left.entry.isDir ? -1 : 1;
			}
			return leftDepth - rightDepth;
		});
		for (const sharedEntry of orderedSharedEntries) {
			await applySharedTreeEntry(sharedEntry.relativePath, sharedEntry.entry, 'add');
		}
	}

	async function copySharedEntries(sourcePrefix: string, targetPrefix: string) {
		if (!ydoc || !yFileTree) {
			return;
		}
		const entriesToCopy = getEntriesWithinRelativePath(sourcePrefix);
		const payloads = await collectSharedFilePayloads(
			entriesToCopy.map((entry) => ({
				relativePath: renameRelativeProjectPath(entry.relativePath, sourcePrefix, targetPrefix),
				isDir: entry.isDir,
				content: entry.isDir ? '' : undefined
			}))
		);
		ydoc.transact(() => {
			for (const payload of payloads) {
				yFileTree.set(payload.relativePath, { isDir: payload.isDir });
				if (!payload.isDir) {
					const ytext = ydoc.getText(yTextKeyForFile(payload.relativePath));
					syncYTextValue(ytext, payload.content ?? '');
				}
			}
		}, FILE_TREE_SYNC_ORIGIN);
	}

	async function moveSharedEntries(sourcePrefix: string, targetPrefix: string) {
		if (!ydoc || !yFileTree) {
			return;
		}
		const entriesToMove = getEntriesWithinRelativePath(sourcePrefix);
		const payloads = entriesToMove.map((entry) => ({
			relativePath: entry.relativePath,
			isDir: entry.isDir,
			content: entry.isDir ? '' : ydoc.getText(yTextKeyForFile(entry.relativePath)).toString()
		}));
		ydoc.transact(() => {
			for (const payload of payloads) {
				const nextRelativePath = renameRelativeProjectPath(
					payload.relativePath,
					sourcePrefix,
					targetPrefix
				);
				yFileTree.set(nextRelativePath, { isDir: payload.isDir });
				if (!payload.isDir) {
					const nextText = ydoc.getText(yTextKeyForFile(nextRelativePath));
					syncYTextValue(nextText, payload.content ?? '');
				}
			}
			for (const payload of payloads) {
				if (!payload.isDir) {
					clearYTextForRelativePath(payload.relativePath);
				}
				yFileTree.delete(payload.relativePath);
			}
		}, FILE_TREE_SYNC_ORIGIN);
	}

	async function syncOpenTabsWithFileTree() {
		const availableFiles = new Set(
			fileTree.filter((entry) => !entry.isDir).map((entry) => entry.relativePath)
		);
		openTabs = openTabs.filter((tab) => availableFiles.has(tab));
		if (currentFile && availableFiles.has(currentFile)) {
			return;
		}
		if (openTabs.length > 0) {
			await switchToFile(openTabs[openTabs.length - 1]);
			return;
		}
		await clearActiveEditor();
	}

	function waitForInitialProviderSync() {
		return new Promise<void>((resolve) => {
			if (!provider || typeof provider.on !== 'function') {
				canvasClientLog('provider-sync-skip-no-provider', { roomId });
				resolve();
				return;
			}
			canvasClientLog('provider-sync-wait-start', {
				roomId,
				timeoutMs: PROVIDER_SYNC_TIMEOUT_MS
			});
			let settled = false;
			const finish = (reason: 'synced' | 'timeout') => {
				if (settled) {
					return;
				}
				settled = true;
				if (typeof provider.off === 'function') {
					provider.off('sync', handleSync);
				}
				window.clearTimeout(timeoutId);
				canvasClientLog('provider-sync-wait-finish', { roomId, reason });
				resolve();
			};
			const handleSync = (isSynced: boolean) => {
				canvasClientLog('provider-sync-event', { roomId, isSynced });
				if (isSynced) {
					finish('synced');
				}
			};
			const timeoutId = window.setTimeout(() => {
				finish('timeout');
			}, PROVIDER_SYNC_TIMEOUT_MS);
			provider.on('sync', handleSync);
		});
	}

	function resolveTargetDirectory(target: ProjectFileEntry | null) {
		if (!target) {
			return '/project';
		}
		if (target.isDir) {
			return target.path;
		}
		return splitPath(target.path).dir;
	}

	function currentFileEntry() {
		return fileTree.find((entry) => !entry.isDir && entry.relativePath === currentFile) ?? null;
	}

	function getParentRelativeProjectPath(relativePath: string) {
		const normalized = normalizeProjectName(relativePath).replace(/\/+$/, '');
		if (!normalized) {
			return '';
		}
		const index = normalized.lastIndexOf('/');
		if (index < 0) {
			return '';
		}
		return normalized.slice(0, index);
	}

	function ensureExpandedDirectoriesForPath(
		relativePath: string,
		baseState: Record<string, boolean> = expandedDirectories
	) {
		const nextState = { ...baseState };
		let parentPath = getParentRelativeProjectPath(relativePath);
		while (parentPath) {
			nextState[parentPath] = true;
			parentPath = getParentRelativeProjectPath(parentPath);
		}
		return nextState;
	}

	function syncExpandedDirectoriesWithFileTree() {
		const nextState: Record<string, boolean> = {};
		for (const entry of fileTree) {
			if (!entry.isDir) {
				continue;
			}
			const key = entry.relativePath || entry.name;
			nextState[key] = key in expandedDirectories ? expandedDirectories[key] : false;
		}
		expandedDirectories = currentFile
			? ensureExpandedDirectoriesForPath(currentFile, nextState)
			: nextState;
	}

	function isFolderExpanded(entry: ProjectFileEntry) {
		const key = entry.relativePath || entry.name;
		return expandedDirectories[key] !== false;
	}

	function isExplorerEntryVisible(entry: ProjectFileEntry, state: Record<string, boolean>) {
		let parentPath = getParentRelativeProjectPath(entry.relativePath || entry.name);
		while (parentPath) {
			if (state[parentPath] === false) {
				return false;
			}
			parentPath = getParentRelativeProjectPath(parentPath);
		}
		return true;
	}

	function folderContainsCurrentFile(entry: ProjectFileEntry) {
		if (!entry.isDir) {
			return false;
		}
		const relativePath = entry.relativePath || entry.name;
		return currentFile.startsWith(`${relativePath}/`);
	}

	function toggleFolder(entry: ProjectFileEntry) {
		if (!entry.isDir) {
			return;
		}
		const key = entry.relativePath || entry.name;
		expandedDirectories = {
			...expandedDirectories,
			[key]: !isFolderExpanded(entry)
		};
	}

	function getTabLabel(fileName: string) {
		const normalized = normalizeProjectName(fileName);
		if (!normalized) {
			return 'Untitled';
		}
		const parts = normalized.split('/');
		return parts[parts.length - 1] || normalized;
	}

	function isPromptCancelled(error: unknown) {
		return error instanceof Error && error.message === PROMPT_CANCELLED_ERROR;
	}

	function getPromptTitle(type: PromptType) {
		switch (type) {
			case 'rename':
				return 'Rename Item';
			case 'new-folder':
				return 'New Folder';
			case 'new-file':
			default:
				return 'New File';
		}
	}

	function getPromptSubmitLabel(type: PromptType) {
		switch (type) {
			case 'rename':
				return 'Rename';
			case 'new-folder':
				return 'Create Folder';
			case 'new-file':
			default:
				return 'Create File';
		}
	}

	function getPromptPlaceholder(type: PromptType) {
		switch (type) {
			case 'rename':
				return 'Enter a new name';
			case 'new-folder':
				return 'src';
			case 'new-file':
			default:
				return 'script.py';
		}
	}

	function resetPromptState() {
		promptState = {
			isOpen: false,
			type: '',
			initialValue: '',
			resolve: null,
			reject: null
		};
		promptInputValue = '';
		promptInputElement = null;
	}

	async function requestPrompt(type: PromptType, initialValue = '') {
		if (promptState.isOpen && promptState.reject) {
			promptState.reject(new Error(PROMPT_CANCELLED_ERROR));
		}
		promptInputValue = initialValue;
		const result = new Promise<string>((resolve, reject) => {
			promptState = {
				isOpen: true,
				type,
				initialValue,
				resolve,
				reject
			};
		});
		await tick();
		promptInputElement?.focus();
		promptInputElement?.select();
		return result;
	}

	function cancelPrompt() {
		if (promptState.reject) {
			promptState.reject(new Error(PROMPT_CANCELLED_ERROR));
		}
		resetPromptState();
	}

	function submitPrompt() {
		if (promptState.resolve) {
			promptState.resolve(promptInputValue);
		}
		resetPromptState();
	}

	function handlePromptInputKeydown(event: KeyboardEvent) {
		if (event.key === 'Escape') {
			event.preventDefault();
			cancelPrompt();
		}
	}

	function syncCanvasViewportState(matches: boolean) {
		isCompactCanvasLayout = matches;
		if (!matches) {
			return;
		}
		mobileCanvasPane = currentFile ? 'editor' : 'explorer';
	}

	function showExplorerPane() {
		mobileCanvasPane = 'explorer';
	}

	function showEditorPane() {
		mobileCanvasPane = 'editor';
	}

	function openDeleteConfirmation(entry: ProjectFileEntry) {
		deleteConfirmTarget = entry;
	}

	function closeDeleteConfirmation() {
		deleteConfirmTarget = null;
	}

	function getDeleteConfirmationTitle(entry: ProjectFileEntry | null) {
		if (!entry) {
			return 'Delete item?';
		}
		return entry.isDir ? 'Delete folder?' : 'Delete file?';
	}

	function getDeleteConfirmationMessage(entry: ProjectFileEntry | null) {
		if (!entry) {
			return '';
		}
		if (entry.isDir) {
			return `Delete "${entry.name}" and everything inside it? This cannot be undone.`;
		}
		return `Delete "${entry.name}"? This cannot be undone.`;
	}

	async function confirmDeleteTarget() {
		const target = deleteConfirmTarget;
		if (!target) {
			return;
		}
		closeDeleteConfirmation();
		await deleteEntry(target);
	}

	function scheduleTerminalFit() {
		requestAnimationFrame(() => {
			terminalFitAddon?.fit();
		});
	}

	function writeTerminalLine(message: string) {
		terminal?.writeln(message);
	}

	function clearTerminal() {
		terminal?.clear();
	}

	function formatTerminalArg(value: unknown) {
		if (typeof value === 'string') {
			return value;
		}
		if (
			typeof value === 'number' ||
			typeof value === 'boolean' ||
			value == null ||
			typeof value === 'bigint'
		) {
			return String(value);
		}
		if (value instanceof Error) {
			return value.stack || value.message;
		}
		try {
			return JSON.stringify(value);
		} catch {
			return String(value);
		}
	}

	function getTerminalResizeBounds() {
		const editorBodyHeight = canvasEditorBodyElement?.clientHeight ?? 0;
		return {
			min: 120,
			max: Math.max(160, editorBodyHeight - 180)
		};
	}

	function handleTerminalResizeMove(event: PointerEvent) {
		const deltaY = terminalResizeStartY - event.clientY;
		const { min, max } = getTerminalResizeBounds();
		terminalHeight = Math.max(min, Math.min(max, terminalResizeStartHeight + deltaY));
		scheduleTerminalFit();
	}

	function stopTerminalResize() {
		document.body.style.removeProperty('cursor');
		document.body.style.removeProperty('user-select');
		if (removeTerminalResizeListeners) {
			removeTerminalResizeListeners();
			removeTerminalResizeListeners = null;
		}
	}

	function startTerminalResize(event: PointerEvent) {
		terminalResizeStartY = event.clientY;
		terminalResizeStartHeight = terminalHeight;
		document.body.style.cursor = 'row-resize';
		document.body.style.userSelect = 'none';
		const onPointerMove = (pointerEvent: PointerEvent) => {
			handleTerminalResizeMove(pointerEvent);
		};
		const onPointerUp = () => {
			stopTerminalResize();
		};
		window.addEventListener('pointermove', onPointerMove);
		window.addEventListener('pointerup', onPointerUp);
		removeTerminalResizeListeners = () => {
			window.removeEventListener('pointermove', onPointerMove);
			window.removeEventListener('pointerup', onPointerUp);
		};
		event.preventDefault();
	}

	async function initializeTerminal() {
		if (!terminalContainer || terminal || typeof window === 'undefined') {
			return;
		}
		const [{ Terminal }, { FitAddon }] = await Promise.all([
			import('xterm'),
			import('@xterm/addon-fit')
		]);
		terminal = new Terminal({
			theme: {
				background: '#1e1e1e',
				foreground: '#d8e1f2',
				cursor: '#7dd3fc',
				selectionBackground: 'rgba(125, 211, 252, 0.22)'
			},
			convertEol: true,
			fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
			fontSize: 12,
			lineHeight: 1.25
		});
		terminalFitAddon = new FitAddon();
		terminal.loadAddon(terminalFitAddon);
		terminal.open(terminalContainer);
		scheduleTerminalFit();
		writeTerminalLine('\x1b[32mWelcome to Converse Terminal...\x1b[0m');
		if (typeof ResizeObserver !== 'undefined') {
			terminalResizeObserver = new ResizeObserver(() => {
				scheduleTerminalFit();
			});
			terminalResizeObserver.observe(terminalContainer);
		}
	}

	function escapeCSSContent(value: string) {
		return value.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\n/g, ' ');
	}

	function resolvePresenceColor(value: unknown) {
		if (typeof value !== 'string') {
			return '#58a6ff';
		}
		const color = value.trim();
		return color || '#58a6ff';
	}

	function createPresenceSessionId() {
		if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
			return crypto.randomUUID();
		}
		return `canvas-${Date.now()}-${Math.random().toString(16).slice(2)}`;
	}

	function getLocalPresenceUser() {
		return {
			id: currentUser?.id || 'guest',
			name: currentUser?.name || 'Guest',
			color: currentUser?.color || '#58a6ff',
			sessionId: presenceSessionId
		};
	}

	function isSelfPresenceState(clientId: number | string, state: any) {
		if (!awareness) {
			return false;
		}
		if (String(clientId) === String(awareness.clientID)) {
			return true;
		}
		const localUserId = String(currentUser?.id || '');
		const stateUserId = String(state?.user?.id || '');
		if (localUserId && stateUserId && localUserId === stateUserId) {
			return true;
		}
		const stateSessionId = String(state?.user?.sessionId || '');
		if (stateSessionId && stateSessionId === presenceSessionId) {
			return true;
		}
		return false;
	}

	function syncLocalPresenceMetadata() {
		if (!awareness) {
			return;
		}
		const localState = awareness.getLocalState?.() ?? {};
		const nextUser = getLocalPresenceUser();
		const currentPresenceUser = localState?.user ?? {};
		if (
			currentPresenceUser.id !== nextUser.id ||
			currentPresenceUser.name !== nextUser.name ||
			currentPresenceUser.color !== nextUser.color ||
			currentPresenceUser.sessionId !== nextUser.sessionId
		) {
			awareness.setLocalStateField('user', nextUser);
		}
		if ((localState?.currentFile ?? '') !== currentFile) {
			awareness.setLocalStateField('currentFile', currentFile);
		}
	}

	function ensureRemotePresenceStyleElement() {
		if (typeof document === 'undefined') {
			return null;
		}
		if (!remotePresenceStyleElement) {
			remotePresenceStyleElement = document.createElement('style');
			remotePresenceStyleElement.id = `canvas-remote-presence-${roomId}`;
			document.head.appendChild(remotePresenceStyleElement);
		}
		return remotePresenceStyleElement;
	}

	function renderRemotePresenceStyles() {
		if (!awareness) {
			return;
		}
		const styleElement = ensureRemotePresenceStyleElement();
		if (!styleElement) {
			return;
		}
		const lines: string[] = [];
		for (const [clientId, state] of awareness.getStates().entries()) {
			if (isSelfPresenceState(clientId, state)) {
				continue;
			}
			const color = resolvePresenceColor(state?.user?.color);
			const name = escapeCSSContent(String(state?.user?.name || `User ${clientId}`));
			lines.push(`.yRemoteSelection-${clientId}{background-color:${color};opacity:0.28;}`);
			lines.push(`.yRemoteSelectionHead-${clientId}{border-left-color:${color};}`);
			lines.push(
				`.yRemoteSelectionHead-${clientId}::after{content:"${name}";background-color:${color};border-color:${color};}`
			);
		}
		styleElement.textContent = lines.join('\n');
	}

	function clearRemoteSelectionDecorations() {
		if (!editor || remoteSelectionDecorations.length === 0) {
			remoteSelectionDecorations = [];
			return;
		}
		remoteSelectionDecorations = editor.deltaDecorations(remoteSelectionDecorations, []);
	}

	function clearLocalSelectionState() {
		if (!awareness) {
			return;
		}
		const localState = awareness.getLocalState?.();
		if (localState?.selection != null) {
			awareness.setLocalStateField('selection', null);
		}
	}

	function syncLocalSelectionState() {
		if (!awareness || !editor || !monacoApi || !yjsApi || !currentYText || !currentFile) {
			clearLocalSelectionState();
			return;
		}
		const model = editor.getModel();
		const selection = editor.getSelection();
		if (!model || !selection) {
			clearLocalSelectionState();
			return;
		}
		let anchor = model.getOffsetAt(selection.getStartPosition());
		let head = model.getOffsetAt(selection.getEndPosition());
		if (selection.getDirection() === monacoApi.SelectionDirection.RTL) {
			const previousAnchor = anchor;
			anchor = head;
			head = previousAnchor;
		}
		awareness.setLocalStateField('selection', {
			anchor: yjsApi.createRelativePositionFromTypeIndex(currentYText, anchor),
			head: yjsApi.createRelativePositionFromTypeIndex(currentYText, head)
		});
	}

	function renderRemoteSelections() {
		if (!awareness || !editor || !monacoApi || !yjsApi || !currentYText || !currentFile) {
			clearRemoteSelectionDecorations();
			return;
		}
		const model = editor.getModel();
		if (!model) {
			clearRemoteSelectionDecorations();
			return;
		}
		const nextDecorations: Array<{
			range: any;
			options: {
				className: string;
				afterContentClassName: string | null;
				beforeContentClassName: string | null;
			};
		}> = [];
		for (const [clientId, state] of awareness.getStates().entries()) {
			if (isSelfPresenceState(clientId, state)) {
				continue;
			}
			if (state?.currentFile !== currentFile) {
				continue;
			}
			if (state?.selection?.anchor == null || state?.selection?.head == null) {
				continue;
			}
			const anchorAbs = yjsApi.createAbsolutePositionFromRelativePosition(
				state.selection.anchor,
				ydoc
			);
			const headAbs = yjsApi.createAbsolutePositionFromRelativePosition(state.selection.head, ydoc);
			if (
				anchorAbs == null ||
				headAbs == null ||
				anchorAbs.type !== currentYText ||
				headAbs.type !== currentYText
			) {
				continue;
			}
			let start = model.getPositionAt(anchorAbs.index);
			let end = model.getPositionAt(headAbs.index);
			let afterContentClassName: string | null =
				`yRemoteSelectionHead yRemoteSelectionHead-${clientId}`;
			let beforeContentClassName: string | null = null;
			if (anchorAbs.index > headAbs.index) {
				start = model.getPositionAt(headAbs.index);
				end = model.getPositionAt(anchorAbs.index);
				afterContentClassName = null;
				beforeContentClassName = `yRemoteSelectionHead yRemoteSelectionHead-${clientId}`;
			}
			nextDecorations.push({
				range: new monacoApi.Range(start.lineNumber, start.column, end.lineNumber, end.column),
				options: {
					className: `yRemoteSelection yRemoteSelection-${clientId}`,
					afterContentClassName,
					beforeContentClassName
				}
			});
		}
		remoteSelectionDecorations = editor.deltaDecorations(
			remoteSelectionDecorations,
			nextDecorations
		);
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
		const collectedEntries = await Promise.all(
			names.map(async (name: string) => {
				const path = `${dir}/${name}`;
				const stat = await getActiveFS().promises.stat(path);
				const isDir = typeof stat.isDirectory === 'function' ? stat.isDirectory() : false;
				return {
					path,
					name,
					relativePath: toRelativeProjectPath(path),
					isDir,
					depth
				} satisfies ProjectFileEntry;
			})
		);
		const sortedEntries = collectedEntries.sort((left, right) => {
			if (left.isDir !== right.isDir) {
				return left.isDir ? -1 : 1;
			}
			return left.name.localeCompare(right.name);
		});
		const entries: ProjectFileEntry[] = [];
		for (const entry of sortedEntries) {
			entries.push(entry);
			if (entry.isDir) {
				const children = await collectProjectFiles(entry.path, depth + 1);
				entries.push(...children);
			}
		}
		return entries;
	}

	async function refreshFileTree() {
		await ensureProjectDirectory();
		fileTree = await collectProjectFiles('/project', 0);
		syncExpandedDirectoriesWithFileTree();
	}

	function firstFileEntry() {
		return fileTree.find((entry) => !entry.isDir) ?? null;
	}

	async function ensureWorkspaceHasAtLeastOneFile() {
		const existingFile = firstFileEntry();
		if (existingFile) {
			return false;
		}
		const bootstrapPath = `/project/${DEFAULT_PROJECT_FILE_NAME}`;
		await ensureProjectDirectory();
		await getActiveFS().promises.writeFile(bootstrapPath, DEFAULT_PROJECT_FILE_CONTENT);
		await upsertSharedEntries([
			{
				relativePath: DEFAULT_PROJECT_FILE_NAME,
				isDir: false,
				content: DEFAULT_PROJECT_FILE_CONTENT
			}
		]);
		await refreshFileTree();
		return true;
	}

	function selectInitialFileFromTree() {
		const firstEntry = firstFileEntry();
		if (!firstEntry) {
			return false;
		}
		const firstRelativePath = normalizeProjectName(firstEntry.relativePath || firstEntry.name);
		if (!firstRelativePath) {
			return false;
		}
		currentFile = firstRelativePath;
		openTabs = [firstRelativePath];
		expandedDirectories = ensureExpandedDirectoriesForPath(firstRelativePath);
		return true;
	}

	async function initFileSystem(options?: { createDefaultIfEmpty?: boolean }) {
		await ensureProjectDirectory();
		const rootEntries = await getActiveFS().promises.readdir('/project');
		if (rootEntries.length === 0 && options?.createDefaultIfEmpty !== false) {
			await getActiveFS().promises.writeFile(
				`/project/${DEFAULT_PROJECT_FILE_NAME}`,
				DEFAULT_PROJECT_FILE_CONTENT
			);
		}
		await refreshFileTree();
		const currentExists = fileTree.some(
			(entry) => !entry.isDir && entry.relativePath === currentFile
		);
		if (!currentExists) {
			currentFile = '';
		}
		openTabs = currentFile ? [currentFile] : [];
	}

	async function pathExists(path: string) {
		try {
			await getActiveFS().promises.stat(path);
			return true;
		} catch {
			return false;
		}
	}

	async function resolveCopyDestinationPath(targetDir: string, sourceName: string) {
		let candidate = `${targetDir}/${sourceName}`;
		if (!(await pathExists(candidate))) {
			return candidate;
		}
		const extIndex = sourceName.lastIndexOf('.');
		const hasExtension = extIndex > 0;
		const baseName = hasExtension ? sourceName.slice(0, extIndex) : sourceName;
		const extension = hasExtension ? sourceName.slice(extIndex) : '';
		for (let i = 1; i < 1000; i += 1) {
			const suffix = i === 1 ? ' copy' : ` copy ${i}`;
			candidate = `${targetDir}/${baseName}${suffix}${extension}`;
			if (!(await pathExists(candidate))) {
				return candidate;
			}
		}
		throw new Error('Unable to find an available destination name');
	}

	async function copyPathRecursive(sourcePath: string, destinationPath: string) {
		const stat = await getActiveFS().promises.stat(sourcePath);
		const isDirectory = typeof stat.isDirectory === 'function' ? stat.isDirectory() : false;
		if (!isDirectory) {
			const fileBytes = await getActiveFS().promises.readFile(sourcePath);
			await getActiveFS().promises.writeFile(destinationPath, fileBytes);
			return;
		}
		await getActiveFS().promises.mkdir(destinationPath);
		const children = await getActiveFS().promises.readdir(sourcePath);
		for (const child of children) {
			await copyPathRecursive(`${sourcePath}/${child}`, `${destinationPath}/${child}`);
		}
	}

	async function removeDirectoryRecursive(path: string) {
		const children = await getActiveFS().promises.readdir(path);
		for (const child of children) {
			const childPath = `${path}/${child}`;
			const childStat = await getActiveFS().promises.stat(childPath);
			const childIsDir =
				typeof childStat.isDirectory === 'function' ? childStat.isDirectory() : false;
			if (childIsDir) {
				await removeDirectoryRecursive(childPath);
			} else {
				await getActiveFS().promises.unlink(childPath);
			}
		}
		await getActiveFS().promises.rmdir(path);
	}

	function closeContextMenu() {
		contextMenuOpen = false;
		contextMenuTarget = null;
	}

	function joinDropPath(basePath: string, entryName: string) {
		const normalizedName = normalizeProjectName(entryName);
		if (!normalizedName) {
			return '';
		}
		const normalizedBase = basePath.endsWith('/') ? basePath.slice(0, -1) : basePath;
		return `${normalizedBase}/${normalizedName}`;
	}

	async function mkdirIfMissing(path: string) {
		try {
			const stat = await getActiveFS().promises.stat(path);
			const isDir = typeof stat.isDirectory === 'function' ? stat.isDirectory() : false;
			if (isDir) {
				return;
			}
			await getActiveFS().promises.unlink(path);
		} catch (error) {
			const message = error instanceof Error ? error.message.toLowerCase() : '';
			if (
				message &&
				!message.includes('enoent') &&
				!message.includes('no such') &&
				!message.includes('not found')
			) {
				throw error;
			}
		}
		try {
			await getActiveFS().promises.mkdir(path);
		} catch (error) {
			const message = error instanceof Error ? error.message.toLowerCase() : '';
			if (message.includes('exist')) {
				return;
			}
			throw error;
		}
	}

	function readFileFromEntry(entry: WebkitFileEntry) {
		return new Promise<File>((resolve, reject) => {
			entry.file(
				(file) => resolve(file),
				(error) => reject(error)
			);
		});
	}

	function readAllDirectoryEntries(reader: WebkitDirectoryReader) {
		return new Promise<WebkitEntry[]>((resolve, reject) => {
			const allEntries: WebkitEntry[] = [];
			const readBatch = () => {
				reader.readEntries(
					(entries) => {
						if (!entries.length) {
							resolve(allEntries);
							return;
						}
						allEntries.push(...entries);
						readBatch();
					},
					(error) => reject(error)
				);
			};
			readBatch();
		});
	}

	async function processEntry(entry: WebkitEntry, currentPath: string) {
		const targetPath = joinDropPath(currentPath, entry.name);
		if (!targetPath) {
			return;
		}
		if (entry.isFile) {
			const file = await readFileFromEntry(entry);
			const bytes = new Uint8Array(await file.arrayBuffer());
			await getActiveFS().promises.writeFile(targetPath, bytes);
			return;
		}
		await mkdirIfMissing(targetPath);
		const reader = entry.createReader();
		const childEntries = await readAllDirectoryEntries(reader);
		for (const childEntry of childEntries) {
			await processEntry(childEntry, targetPath);
		}
	}

	async function collectZipFilesRecursively(
		directoryPath: string,
		relativePrefix = ''
	): Promise<Record<string, Uint8Array>> {
		const zipEntries: Record<string, Uint8Array> = {};
		const names = await getActiveFS().promises.readdir(directoryPath);
		const sortedNames = [...names].sort((left, right) => left.localeCompare(right));
		for (const name of sortedNames) {
			const fullPath = `${directoryPath}/${name}`;
			const stat = await getActiveFS().promises.stat(fullPath);
			const isDirectory = typeof stat.isDirectory === 'function' ? stat.isDirectory() : false;
			if (isDirectory) {
				const nested = await collectZipFilesRecursively(fullPath, `${relativePrefix}${name}/`);
				for (const [entryPath, value] of Object.entries(nested)) {
					zipEntries[entryPath] = value;
				}
				continue;
			}
			const rawContent = await getActiveFS().promises.readFile(fullPath);
			if (typeof rawContent === 'string') {
				zipEntries[`${relativePrefix}${name}`] = textEncoder.encode(rawContent);
				continue;
			}
			const fileBytes =
				rawContent instanceof Uint8Array ? rawContent : new Uint8Array(rawContent);
			zipEntries[`${relativePrefix}${name}`] = new Uint8Array(fileBytes);
		}
		return zipEntries;
	}

	function triggerImportZip() {
		if (!importZipInput) {
			return;
		}
		importZipInput.value = '';
		importZipInput.click();
	}

	function normalizeZipEntryPath(path: string) {
		const trimmed = (path || '').trim().replace(/^\/+/, '').replace(/\/+$/, '');
		if (!trimmed || trimmed.startsWith('__MACOSX/')) {
			return '';
		}
		return trimmed
			.split('/')
			.map((segment) => normalizeProjectName(segment))
			.join('/');
	}

	function resolveZipRootFolder(paths: string[]) {
		const normalizedPaths = paths.filter((path) => path !== '');
		if (normalizedPaths.length === 0) {
			return '';
		}
		const firstSegment = normalizedPaths[0].split('/')[0];
		if (!firstSegment) {
			return '';
		}
		if (!normalizedPaths.every((path) => path.split('/')[0] === firstSegment)) {
			return '';
		}
		return firstSegment;
	}

	async function ensureDirectoryPathExists(path: string) {
		const normalized = (path || '').replace(/\/+$/, '');
		if (!normalized) {
			return;
		}
		const segments = normalized.split('/').filter(Boolean);
		let currentPath = '';
		for (const segment of segments) {
			currentPath += `/${segment}`;
			await mkdirIfMissing(currentPath);
		}
	}

	async function ensureZipDirectoryTarget(path: string) {
		try {
			const stat = await getActiveFS().promises.stat(path);
			const isDir = typeof stat.isDirectory === 'function' ? stat.isDirectory() : false;
			if (!isDir) {
				await getActiveFS().promises.unlink(path);
				await mkdirIfMissing(path);
			}
		} catch {
			await mkdirIfMissing(path);
		}
	}

	async function writeUnzippedEntriesToProject(
		unzipped: Record<string, Uint8Array>,
		options?: { stripRootFolder?: boolean }
	) {
		const rawEntries = Object.entries(unzipped);
		const entries = rawEntries
			.map(([entryPath, entryBytes]) => {
				const normalizedPath = normalizeZipEntryPath(entryPath);
				const directoryPrefix = normalizedPath ? `${normalizedPath}/` : '';
				const isDir =
					/\/$/.test(entryPath) ||
					(directoryPrefix !== '' &&
						rawEntries.some(([candidatePath]) =>
							normalizeZipEntryPath(candidatePath).startsWith(directoryPrefix)
						));
				return {
					path: normalizedPath,
					bytes: entryBytes,
					isDir
				};
			})
			.filter((entry) => entry.path !== '');
		const rootFolder = options?.stripRootFolder
			? resolveZipRootFolder(entries.map((entry) => entry.path))
			: '';
		for (const entry of entries) {
			let relativePath = entry.path;
			if (rootFolder) {
				if (relativePath === rootFolder) {
					continue;
				}
				if (relativePath.startsWith(`${rootFolder}/`)) {
					relativePath = relativePath.slice(rootFolder.length + 1);
				}
			}
			if (!relativePath) {
				continue;
			}
			const targetPath = `/project/${relativePath}`;
			if (entry.isDir) {
				await ensureDirectoryPathExists(splitPath(targetPath).dir);
				await ensureZipDirectoryTarget(targetPath);
				continue;
			}
			const parentDir = splitPath(targetPath).dir;
			await ensureDirectoryPathExists(parentDir);
			await getActiveFS().promises.writeFile(targetPath, entry.bytes);
		}
	}

	function parseGitHubRepositoryURL(rawURL: string) {
		const input = (rawURL || '').trim();
		if (!input) {
			return null;
		}
		const withProtocol = /^https?:\/\//i.test(input) ? input : `https://${input}`;
		let parsed: URL;
		try {
			parsed = new URL(withProtocol);
		} catch {
			return null;
		}
		const hostname = parsed.hostname.toLowerCase();
		if (hostname !== 'github.com' && hostname !== 'www.github.com') {
			return null;
		}
		const segments = parsed.pathname.split('/').filter(Boolean);
		if (segments.length < 2) {
			return null;
		}
		const owner = normalizeProjectName(segments[0]);
		const repo = normalizeProjectName(segments[1].replace(/\.git$/i, ''));
		if (!owner || !repo) {
			return null;
		}
		let ref = '';
		if (segments[2] === 'tree' && segments.length >= 4) {
			ref = segments.slice(3).join('/').trim();
		}
		return { owner, repo, ref };
	}

	async function exportWorkspaceZip() {
		fileExplorerError = '';
		try {
			await persistCurrentFileToFS();
			await ensureProjectDirectory();
			const zipFiles = await collectZipFilesRecursively('/project');
			const zipBytes = zipSync(zipFiles);
			const zipBlobBytes = new Uint8Array(zipBytes.length);
			zipBlobBytes.set(zipBytes);
			const blob = new Blob([zipBlobBytes], { type: 'application/zip' });
			const downloadURL = URL.createObjectURL(blob);
			const anchor = document.createElement('a');
			anchor.href = downloadURL;
			anchor.download = 'workspace.zip';
			anchor.style.display = 'none';
			document.body.appendChild(anchor);
			anchor.click();
			document.body.removeChild(anchor);
			URL.revokeObjectURL(downloadURL);
		} catch (error) {
			fileExplorerError = error instanceof Error ? error.message : 'Failed to export zip';
		}
	}

	async function importFromGitHub() {
		const parsed = parseGitHubRepositoryURL(githubRepoURL);
		if (!parsed) {
			fileExplorerError = 'Enter a valid GitHub URL like https://github.com/user/repo';
			return;
		}
		isImportingRepo = true;
		fileExplorerError = '';
		try {
			await persistCurrentFileToFS();
			const { owner, repo, ref } = parsed;
			const searchParams = new URLSearchParams({
				owner,
				repo
			});
			if (ref) {
				searchParams.set('ref', ref);
			}
			const githubArchiveURL = `${API_BASE}/api/canvas/github-archive?${searchParams}`;
			canvasClientLog('github-archive-request', {
				roomId,
				owner,
				repo,
				ref: ref || '',
				url: githubArchiveURL
			});
			const response = await fetch(githubArchiveURL);
			canvasClientLog('github-archive-response', {
				roomId,
				status: response.status,
				ok: response.ok
			});
			if (!response.ok) {
				let errorMessage = `GitHub import failed (${response.status})`;
				try {
					const data = await response.json();
					if (typeof data?.error === 'string' && data.error.trim()) {
						errorMessage = data.error.trim();
					}
				} catch {
					// Ignore malformed error responses and fall back to HTTP status.
				}
				throw new Error(errorMessage);
			}
			const zippedBytes = new Uint8Array(await response.arrayBuffer());
			canvasClientLog('github-archive-bytes', { roomId, bytes: zippedBytes.byteLength });
			const unzipped = unzipSync(zippedBytes);
			await ensureProjectDirectory();
			await writeUnzippedEntriesToProject(unzipped, { stripRootFolder: true });
			await refreshFileTree();
			await upsertSharedEntries(
				fileTree.map((entry) => ({
					relativePath: entry.relativePath,
					isDir: entry.isDir
				}))
			);
			const hasCurrentFile =
				currentFile && fileTree.some((entry) => !entry.isDir && entry.relativePath === currentFile);
			if (hasCurrentFile) {
				ensureTabOpen(currentFile);
				await switchToFile(currentFile);
			} else {
				openTabs = [];
				await clearActiveEditor();
			}
		} catch (error) {
			canvasClientLog('github-archive-error', {
				roomId,
				error: error instanceof Error ? error.message : String(error)
			});
			fileExplorerError = error instanceof Error ? error.message : 'Failed to import repository';
		} finally {
			isImportingRepo = false;
		}
	}

	async function handleZipImportChange(event: Event) {
		const input = event.currentTarget as HTMLInputElement | null;
		const selectedFile = input?.files?.[0];
		if (!selectedFile) {
			return;
		}
		fileExplorerError = '';
		try {
			const arrayBuffer = await selectedFile.arrayBuffer();
			const zippedBytes = new Uint8Array(arrayBuffer);
			const unzipped = unzipSync(zippedBytes);
			await ensureProjectDirectory();
			await writeUnzippedEntriesToProject(unzipped);
			await refreshFileTree();
			await upsertSharedEntries(
				fileTree.map((entry) => ({
					relativePath: entry.relativePath,
					isDir: entry.isDir
				}))
			);
			const hasCurrentFile =
				currentFile && fileTree.some((entry) => !entry.isDir && entry.relativePath === currentFile);
			if (hasCurrentFile) {
				ensureTabOpen(currentFile);
				await switchToFile(currentFile);
			} else {
				openTabs = [];
				await clearActiveEditor();
			}
		} catch (error) {
			fileExplorerError = error instanceof Error ? error.message : 'Failed to import zip';
		} finally {
			if (input) {
				input.value = '';
			}
		}
	}

	function handleSidebarDragEnter(event: DragEvent) {
		event.preventDefault();
		event.stopPropagation();
		isSidebarDragOver = true;
	}

	function handleSidebarDragOver(event: DragEvent) {
		event.preventDefault();
		event.stopPropagation();
		if (event.dataTransfer) {
			event.dataTransfer.dropEffect = 'copy';
		}
		isSidebarDragOver = true;
	}

	function handleSidebarDragLeave(event: DragEvent) {
		event.preventDefault();
		event.stopPropagation();
		const relatedTarget = event.relatedTarget as Node | null;
		if (relatedTarget && sidebarElement?.contains(relatedTarget)) {
			return;
		}
		isSidebarDragOver = false;
	}

	async function handleSidebarDrop(event: DragEvent) {
		event.preventDefault();
		event.stopPropagation();
		isSidebarDragOver = false;
		const items = Array.from(event.dataTransfer?.items ?? []);
		if (items.length === 0) {
			return;
		}
		const droppedEntries = items
			.map((item) => (item as DataTransferItemWithWebkitEntry).webkitGetAsEntry?.() ?? null)
			.filter((entry) => Boolean(entry)) as unknown as WebkitEntry[];
		if (droppedEntries.length === 0) {
			return;
		}
		fileExplorerError = '';
		try {
			await ensureProjectDirectory();
			for (const entry of droppedEntries) {
				await processEntry(entry, '/project');
			}
			await refreshFileTree();
			await upsertSharedEntries(
				fileTree.map((entry) => ({
					relativePath: entry.relativePath,
					isDir: entry.isDir
				}))
			);
		} catch (error) {
			fileExplorerError =
				error instanceof Error ? error.message : 'Failed to import dropped files/folders';
		}
	}

	async function openContextMenu(event: MouseEvent, target: ProjectFileEntry | null) {
		event.preventDefault();
		event.stopPropagation();
		contextMenuTarget = target;
		contextMenuOpen = true;
		contextMenuX = event.clientX;
		contextMenuY = event.clientY;
		await tick();
		if (!contextMenuElement) {
			return;
		}
		const bounds = contextMenuElement.getBoundingClientRect();
		contextMenuX = Math.min(Math.max(8, contextMenuX), window.innerWidth - bounds.width - 8);
		contextMenuY = Math.min(Math.max(8, contextMenuY), window.innerHeight - bounds.height - 8);
	}

	async function persistCurrentFileToFS() {
		if (!editor) {
			return;
		}
		const model = editor.getModel();
		if (!model) {
			return;
		}
		const normalized = normalizeProjectName(currentFile);
		if (!normalized) {
			return;
		}
		await ensureProjectDirectory();
		await getActiveFS().promises.writeFile(`/project/${normalized}`, model.getValue());
	}

	async function recreateBindingForCurrentFile() {
		if (!editor || !ydoc || !monacoApi || !yjsApi) {
			return;
		}
		const model = editor.getModel();
		if (!model) {
			return;
		}
		const normalizedFileName = normalizeProjectName(currentFile) || DEFAULT_PROJECT_FILE_NAME;
		currentFile = normalizedFileName;

		binding?.destroy();
		binding = null;
		currentYText = null;
		clearRemoteSelectionDecorations();
		clearLocalSelectionState();

		await ensureProjectDirectory();
		const filePath = `/project/${normalizedFileName}`;
		let diskContent = '';
		try {
			diskContent = await getActiveFS().promises.readFile(filePath, { encoding: 'utf8' });
		} catch {
			const seed =
				normalizedFileName === DEFAULT_PROJECT_FILE_NAME ? DEFAULT_PROJECT_FILE_CONTENT : '';
			diskContent = seed;
			await getActiveFS().promises.writeFile(filePath, seed);
		}

		const ytext = ydoc.getText(yTextKeyForFile(normalizedFileName));
		if (ytext.length === 0 && diskContent) {
			ytext.insert(0, diskContent);
		}

		monacoApi.editor.setModelLanguage(model, getLanguageFromExtension(normalizedFileName));
		model.setValue('');
		currentYText = ytext;
		binding = new (await import('y-monaco')).MonacoBinding(ytext, model, new Set([editor]));
		syncLocalSelectionState();
		renderRemoteSelections();
	}

	function ensureTabOpen(fileName: string) {
		const normalized = normalizeProjectName(fileName);
		if (!normalized || openTabs.includes(normalized)) {
			return;
		}
		openTabs = [...openTabs, normalized];
	}

	async function clearActiveEditor() {
		binding?.destroy();
		binding = null;
		currentYText = null;
		clearRemoteSelectionDecorations();
		currentFile = '';
		showReadOnlyWarning = false;
		const model = editor?.getModel?.();
		if (model && monacoApi) {
			monacoApi.editor.setModelLanguage(model, 'plaintext');
			model.setValue('');
		}
		if (editor) {
			editor.updateOptions({ readOnly: true });
		}
		if (awareness) {
			awareness.setLocalStateField('currentFile', '');
			awareness.setLocalStateField('selection', null);
		}
		if (isCompactCanvasLayout) {
			showExplorerPane();
		}
	}

	async function closeTab(fileName: string) {
		const normalized = normalizeProjectName(fileName);
		if (!normalized) {
			return;
		}
		const tabIndex = openTabs.indexOf(normalized);
		if (tabIndex < 0) {
			return;
		}
		const wasCurrent = normalized === currentFile;
		if (wasCurrent) {
			await persistCurrentFileToFS();
		}
		const nextTabs = openTabs.filter((tab) => tab !== normalized);
		openTabs = nextTabs;
		if (!wasCurrent) {
			return;
		}
		if (nextTabs.length === 0) {
			await clearActiveEditor();
			return;
		}
		const fallbackTab = nextTabs[Math.max(0, tabIndex - 1)] ?? nextTabs[nextTabs.length - 1];
		await switchToFile(fallbackTab);
	}

	async function switchToFile(fileName: string) {
		const normalized = normalizeProjectName(fileName);
		if (!normalized) {
			return;
		}
		if (isCompactCanvasLayout) {
			showEditorPane();
		}
		ensureTabOpen(normalized);
		expandedDirectories = ensureExpandedDirectoriesForPath(normalized);
		if (normalized === currentFile) {
			const model = editor?.getModel?.();
			if (model && monacoApi) {
				monacoApi.editor.setModelLanguage(model, getLanguageFromExtension(normalized));
			}
			return;
		}
		fileExplorerError = '';
		try {
			await persistCurrentFileToFS();
			currentFile = normalized;
			const model = editor?.getModel?.();
			if (model && monacoApi) {
				monacoApi.editor.setModelLanguage(model, getLanguageFromExtension(normalized));
			}
			await recreateBindingForCurrentFile();
			updateEditorAccessMode();
		} catch (error) {
			fileExplorerError = error instanceof Error ? error.message : 'Unable to open file';
		}
	}

	function handleExplorerEntryClick(entry: ProjectFileEntry) {
		if (entry.isDir) {
			toggleFolder(entry);
			return;
		}
		void switchToFile(entry.relativePath || entry.name);
	}

	function handleExplorerEntryKeydown(event: KeyboardEvent, entry: ProjectFileEntry) {
		if (!entry.isDir) {
			return;
		}
		if (event.key === 'ArrowRight') {
			event.preventDefault();
			if (!isFolderExpanded(entry)) {
				toggleFolder(entry);
			}
			return;
		}
		if (event.key === 'ArrowLeft') {
			event.preventDefault();
			if (isFolderExpanded(entry)) {
				toggleFolder(entry);
			}
		}
	}

	function renameRelativeProjectPath(path: string, currentPrefix: string, nextPrefix: string) {
		if (!path) {
			return path;
		}
		if (path === currentPrefix) {
			return nextPrefix;
		}
		if (path.startsWith(`${currentPrefix}/`)) {
			return `${nextPrefix}${path.slice(currentPrefix.length)}`;
		}
		return path;
	}

	async function renameEntry(entry: ProjectFileEntry) {
		let rawName = '';
		try {
			rawName = await requestPrompt('rename', entry.name);
		} catch (error) {
			if (isPromptCancelled(error)) {
				return;
			}
			throw error;
		}
		const nextName = normalizeProjectName(rawName);
		if (!nextName || nextName === entry.name) {
			return;
		}
		if (nextName.includes('/')) {
			fileExplorerError = 'Rename only supports a single file or folder name';
			return;
		}
		fileExplorerError = '';
		try {
			const currentRelativePath = entry.relativePath || entry.name;
			const parentDirectory = splitPath(entry.path).dir;
			const nextPath = buildPath(parentDirectory, nextName);
			if (nextPath === entry.path) {
				return;
			}
			if (await pathExists(nextPath)) {
				throw new Error('An item with that name already exists');
			}
			const nextRelativePath = toRelativeProjectPath(nextPath);
			const previousCurrentFile = currentFile;
			const activePathAfterRename = renameRelativeProjectPath(
				previousCurrentFile,
				currentRelativePath,
				nextRelativePath
			);
			const affectsActiveFile =
				activePathAfterRename !== previousCurrentFile ||
				currentRelativePath === previousCurrentFile;
			if (affectsActiveFile) {
				await persistCurrentFileToFS();
			}
			await getActiveFS().promises.rename(entry.path, nextPath);
			openTabs = Array.from(
				new Set(
					openTabs.map((tab) =>
						renameRelativeProjectPath(tab, currentRelativePath, nextRelativePath)
					)
				)
			);
			currentFile = activePathAfterRename;
			await moveSharedEntries(currentRelativePath, nextRelativePath);
			await refreshFileTree();
			if (currentFile) {
				ensureTabOpen(currentFile);
			}
			if (affectsActiveFile && currentFile) {
				await recreateBindingForCurrentFile();
				updateEditorAccessMode();
			}
		} catch (error) {
			fileExplorerError = error instanceof Error ? error.message : 'Failed to rename item';
		}
	}

	async function createNewFile(baseDir = '/project') {
		let rawName = '';
		try {
			rawName = await requestPrompt('new-file', 'script.py');
		} catch (error) {
			if (isPromptCancelled(error)) {
				return;
			}
			throw error;
		}
		const name = normalizeProjectName(rawName);
		if (!name) {
			return;
		}
		fileExplorerError = '';
		try {
			const filePath = buildPath(baseDir, name);
			await getActiveFS().promises.writeFile(filePath, '');
			await upsertSharedEntries([
				{
					relativePath: toRelativeProjectPath(filePath),
					isDir: false,
					content: ''
				}
			]);
			await refreshFileTree();
			await switchToFile(toRelativeProjectPath(filePath));
		} catch (error) {
			fileExplorerError = error instanceof Error ? error.message : 'Failed to create file';
		}
	}

	async function createNewFolder(baseDir = '/project') {
		let rawName = '';
		try {
			rawName = await requestPrompt('new-folder', 'src');
		} catch (error) {
			if (isPromptCancelled(error)) {
				return;
			}
			throw error;
		}
		const name = normalizeProjectName(rawName);
		if (!name) {
			return;
		}
		fileExplorerError = '';
		try {
			const folderPath = buildPath(baseDir, name);
			await getActiveFS().promises.mkdir(folderPath);
			await upsertSharedEntries([
				{
					relativePath: toRelativeProjectPath(folderPath),
					isDir: true
				}
			]);
			await refreshFileTree();
		} catch (error) {
			fileExplorerError = error instanceof Error ? error.message : 'Failed to create folder';
		}
	}

	async function deleteEntry(entry: ProjectFileEntry) {
		fileExplorerError = '';
		try {
			const deletedRelativePath = entry.relativePath || entry.name;
			const deletedEntries = entry.isDir
				? getEntriesWithinRelativePath(deletedRelativePath)
				: [entry];
			if (entry.isDir) {
				openTabs = openTabs.filter(
					(tab) => tab !== deletedRelativePath && !tab.startsWith(`${deletedRelativePath}/`)
				);
			} else {
				openTabs = openTabs.filter((tab) => tab !== deletedRelativePath);
			}
			if (entry.isDir) {
				await removeDirectoryRecursive(entry.path);
			} else {
				await getActiveFS().promises.unlink(entry.path);
			}
			removeSharedEntries(
				deletedEntries.map((candidate) => candidate.relativePath),
				{ clearYText: true }
			);
			const deletedActive =
				(!entry.isDir && entry.relativePath === currentFile) ||
				(entry.isDir && currentFile.startsWith(`${entry.relativePath}/`));
			await refreshFileTree();
			if (deletedActive) {
				const fallbackTab = openTabs[openTabs.length - 1] || '';
				if (fallbackTab) {
					await switchToFile(fallbackTab);
				} else {
					await clearActiveEditor();
				}
			}
		} catch (error) {
			fileExplorerError = error instanceof Error ? error.message : 'Failed to delete item';
		}
	}

	async function runFile(entry: ProjectFileEntry | null) {
		const target = entry && !entry.isDir ? entry : currentFileEntry();
		if (!target || target.isDir) {
			fileExplorerError = 'Select a file to run';
			writeTerminalLine('\x1b[31mSelect a file to run.\x1b[0m');
			return;
		}
		const extension = target.name.split('.').pop()?.toLowerCase() || '';
		if (!['js', 'mjs', 'cjs'].includes(extension)) {
			fileExplorerError = 'Run File currently supports JavaScript files (.js, .mjs, .cjs)';
			writeTerminalLine(
				'\x1b[33mRun File currently supports JavaScript files (.js, .mjs, .cjs).\x1b[0m'
			);
			return;
		}
		fileExplorerError = '';
		try {
			clearTerminal();
			writeTerminalLine(`\x1b[36m> Executing ${target.name}...\x1b[0m`);
			let source = '';
			if (target.relativePath === currentFile && editor?.getModel?.()) {
				source = String(editor.getModel().getValue() || '');
			} else {
				source = String(await getActiveFS().promises.readFile(target.path, { encoding: 'utf8' }));
			}
			const customConsole = {
				log: (...args: unknown[]) =>
					writeTerminalLine(args.map((arg) => formatTerminalArg(arg)).join(' ')),
				info: (...args: unknown[]) =>
					writeTerminalLine(args.map((arg) => formatTerminalArg(arg)).join(' ')),
				warn: (...args: unknown[]) =>
					writeTerminalLine(
						`\x1b[33m${args.map((arg) => formatTerminalArg(arg)).join(' ')}\x1b[0m`
					),
				error: (...args: unknown[]) =>
					writeTerminalLine(
						`\x1b[31m${args.map((arg) => formatTerminalArg(arg)).join(' ')}\x1b[0m`
					),
				debug: (...args: unknown[]) =>
					writeTerminalLine(args.map((arg) => formatTerminalArg(arg)).join(' ')),
				clear: () => clearTerminal()
			};
			const customPrint = (...args: unknown[]) =>
				writeTerminalLine(args.map((arg) => formatTerminalArg(arg)).join(' '));
			const execute = new Function(
				'console',
				'print',
				`"use strict";\nreturn (async () => {\n${source}\n})();`
			) as (consoleObject: typeof customConsole, printFunction: typeof customPrint) => unknown;
			await Promise.resolve(execute(customConsole, customPrint));
			writeTerminalLine('\x1b[32m> Script finished.\x1b[0m');
		} catch (error) {
			const errorMessage = error instanceof Error ? error.message : 'Run failed';
			fileExplorerError = error instanceof Error ? `Run failed: ${error.message}` : 'Run failed';
			writeTerminalLine(`\x1b[31m${errorMessage}\x1b[0m`);
			writeTerminalLine('\x1b[31m> Script failed.\x1b[0m');
		}
	}

	async function showFileHistory(entry: ProjectFileEntry | null) {
		const target = entry && !entry.isDir ? entry : currentFileEntry();
		if (!target || target.isDir) {
			fileExplorerError = 'Select a file to view history';
			return;
		}
		fileExplorerError = 'File history is unavailable after removing isomorphic-git.';
	}

	async function copyEntryPathToClipboard(entry: ProjectFileEntry | null) {
		const target = entry ?? currentFileEntry();
		if (!target) {
			fileExplorerError = 'No path available to copy';
			return;
		}
		try {
			if (navigator?.clipboard?.writeText) {
				await navigator.clipboard.writeText(target.path);
			} else {
				const textarea = document.createElement('textarea');
				textarea.value = target.path;
				textarea.setAttribute('readonly', 'true');
				textarea.style.position = 'absolute';
				textarea.style.left = '-9999px';
				document.body.appendChild(textarea);
				textarea.select();
				document.execCommand('copy');
				document.body.removeChild(textarea);
			}
			fileExplorerError = '';
		} catch (error) {
			fileExplorerError = error instanceof Error ? error.message : 'Failed to copy file path';
		}
	}

	function contextCopy() {
		if (!contextMenuTarget) {
			return;
		}
		explorerClipboard = {
			path: contextMenuTarget.path,
			isDir: contextMenuTarget.isDir
		};
		closeContextMenu();
	}

	async function contextPaste() {
		const targetDirectory = resolveTargetDirectory(contextMenuTarget);
		closeContextMenu();
		if (!explorerClipboard) {
			return;
		}
		fileExplorerError = '';
		try {
			await persistCurrentFileToFS();
			const sourceRelativePath = toRelativeProjectPath(explorerClipboard.path);
			const sourceName = splitPath(explorerClipboard.path).name;
			const destinationPath = await resolveCopyDestinationPath(targetDirectory, sourceName);
			await copyPathRecursive(explorerClipboard.path, destinationPath);
			await copySharedEntries(sourceRelativePath, toRelativeProjectPath(destinationPath));
			await refreshFileTree();
		} catch (error) {
			fileExplorerError = error instanceof Error ? error.message : 'Paste failed';
		}
	}

	async function contextEdit() {
		const target = contextMenuTarget;
		closeContextMenu();
		if (!target || target.isDir) {
			return;
		}
		await switchToFile(target.relativePath || target.name);
	}

	async function contextNewFile() {
		const targetDirectory = resolveTargetDirectory(contextMenuTarget);
		closeContextMenu();
		await createNewFile(targetDirectory);
	}

	async function contextNewFolder() {
		const targetDirectory = resolveTargetDirectory(contextMenuTarget);
		closeContextMenu();
		await createNewFolder(targetDirectory);
	}

	async function contextRunFile() {
		const target = contextMenuTarget;
		closeContextMenu();
		await runFile(target);
	}

	async function contextRename() {
		const target = contextMenuTarget;
		closeContextMenu();
		if (!target) {
			return;
		}
		await renameEntry(target);
	}

	async function contextDelete() {
		const target = contextMenuTarget;
		closeContextMenu();
		if (!target) {
			return;
		}
		openDeleteConfirmation(target);
	}

	async function contextHistory() {
		const target = contextMenuTarget;
		closeContextMenu();
		await showFileHistory(target);
	}

	async function contextCopyPath() {
		const target = contextMenuTarget;
		closeContextMenu();
		await copyEntryPathToClipboard(target);
	}

	function updateEditorAccessMode() {
		if (!awareness || !editor) {
			return;
		}
		if (!currentFile) {
			editor.updateOptions({ readOnly: true });
			showReadOnlyWarning = false;
			return;
		}
		let editorsOnCurrentFile = 0;
		const states = awareness.getStates();
		for (const state of states.values()) {
			if (state?.currentFile === currentFile) {
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
			clearRemoteSelectionDecorations();
			return;
		}
		renderRemotePresenceStyles();
		renderRemoteSelections();
	}

	$: if (awareness) {
		syncLocalPresenceMetadata();
		if (!currentFile) {
			clearLocalSelectionState();
		}
		updateEditorAccessMode();
		renderRemotePresenceStyles();
		renderRemoteSelections();
	}

	$: visibleFileTree = fileTree.filter((entry) =>
		isExplorerEntryVisible(entry, expandedDirectories)
	);

	$: if (canvasEditorBodyElement) {
		const { min, max } = getTerminalResizeBounds();
		const clampedHeight = Math.max(min, Math.min(max, terminalHeight));
		if (clampedHeight !== terminalHeight) {
			terminalHeight = clampedHeight;
		}
	}

	function registerGlobalContextHandlers() {
		const onPointerDown = (event: PointerEvent) => {
			if (!contextMenuOpen) {
				return;
			}
			const target = event.target as Node | null;
			if (target && contextMenuElement && contextMenuElement.contains(target)) {
				return;
			}
			closeContextMenu();
		};
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key === 'Escape' && deleteConfirmTarget) {
				closeDeleteConfirmation();
				return;
			}
			if (event.key === 'Escape') {
				closeContextMenu();
			}
		};
		const onWindowBlur = () => {
			closeContextMenu();
		};
		window.addEventListener('pointerdown', onPointerDown, true);
		window.addEventListener('keydown', onKeyDown, true);
		window.addEventListener('blur', onWindowBlur);
		return () => {
			window.removeEventListener('pointerdown', onPointerDown, true);
			window.removeEventListener('keydown', onKeyDown, true);
			window.removeEventListener('blur', onWindowBlur);
		};
	}

	onMount(async () => {
		try {
			canvasClientLog('init-start', { roomId });
			removeGlobalContextHandlers = registerGlobalContextHandlers();
			removeBeforeUnloadListener = registerBeforeUnloadPersistence();
			await initializeTerminal();
			const compactCanvasMediaQuery = window.matchMedia('(max-width: 900px)');
			const handleCompactCanvasChange = (event: MediaQueryListEvent) => {
				syncCanvasViewportState(event.matches);
			};
			syncCanvasViewportState(compactCanvasMediaQuery.matches);
			if (typeof compactCanvasMediaQuery.addEventListener === 'function') {
				compactCanvasMediaQuery.addEventListener('change', handleCompactCanvasChange);
				removeCanvasViewportListener = () =>
					compactCanvasMediaQuery.removeEventListener('change', handleCompactCanvasChange);
			} else {
				compactCanvasMediaQuery.addListener(handleCompactCanvasChange);
				removeCanvasViewportListener = () =>
					compactCanvasMediaQuery.removeListener(handleCompactCanvasChange);
			}
			vfs = await initLightningFS(roomId);
			if (!vfs) {
				fileExplorerError = 'File system is unavailable in this environment';
				canvasClientLog('init-fs-unavailable', { roomId });
				return;
			}
			canvasClientLog('init-fs-ready', { roomId });

			await configureMonacoWorkerEnvironment();
			const monaco = await import('monaco-editor');
			const Y = await import('yjs');
			const { WebsocketProvider } = await import('y-websocket');
			const { MonacoBinding } = await import('y-monaco');
			monacoApi = monaco;
			yjsApi = Y;

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
			cursorSelectionDisposable = editor.onDidChangeCursorSelection(() => {
				syncLocalSelectionState();
				renderRemoteSelections();
			});
			editorContentChangeDisposable = model.onDidChangeContent(() => {
				renderRemoteSelections();
				scheduleCurrentFilePersistToFS();
			});

			ydoc = new Y.Doc();
			ydocUpdateHandler = () => {
				scheduleCanvasSnapshotSave();
			};
			ydoc.on('update', ydocUpdateHandler);
			if (periodicSnapshotInterval) {
				window.clearInterval(periodicSnapshotInterval);
				periodicSnapshotInterval = null;
			}
			periodicSnapshotInterval = window.setInterval(() => {
				if (!snapshotDirty) {
					return;
				}
				void saveCanvasSnapshotNow();
			}, 15000);
			yFileTree = ydoc.getMap('fileTree');
			yFileTreeObserver = (event: any) => {
				if (event.transaction.local) {
					return;
				}
				void (async () => {
					const deletions: string[] = [];
					const upserts: Array<{ relativePath: string; entry: SharedFileTreeEntry | null }> = [];
					for (const [key, change] of event.changes.keys.entries()) {
						const relativePath = normalizeProjectName(String(key));
						if (!relativePath) {
							continue;
						}
						if (change.action === 'delete') {
							deletions.push(relativePath);
							continue;
						}
						upserts.push({
							relativePath,
							entry: normalizeSharedTreeEntry(yFileTree.get(relativePath))
						});
					}
					deletions.sort((left, right) => right.split('/').length - left.split('/').length);
					for (const relativePath of deletions) {
						await applySharedTreeEntry(relativePath, null, 'delete');
					}
					upserts.sort((left, right) => {
						const leftDepth = left.relativePath.split('/').length;
						const rightDepth = right.relativePath.split('/').length;
						if (left.entry?.isDir !== right.entry?.isDir) {
							return left.entry?.isDir ? -1 : 1;
						}
						return leftDepth - rightDepth;
					});
					for (const update of upserts) {
						await applySharedTreeEntry(update.relativePath, update.entry, 'add');
					}
					await refreshFileTree();
					await syncOpenTabsWithFileTree();
				})();
			};
			yFileTree.observe(yFileTreeObserver);
			const wsURL = canvasWebSocketURL();
			canvasClientLog('provider-create', { roomId, wsURL });
			provider = new WebsocketProvider(wsURL, roomId, ydoc);
			awareness = provider.awareness;
			syncLocalPresenceMetadata();
			provider.on('status', (event: { status: string }) => {
				canvasClientLog('provider-status', { roomId, status: event.status });
				if (event.status === 'connected') {
					attachProviderTransportDebugListener();
					attachProviderSnapshotListener();
					syncLocalPresenceMetadata();
					syncLocalSelectionState();
				}
			});
			provider.on('connection-error', () => {
				canvasClientLog('provider-connection-error', { roomId });
			});
			provider.on('connection-close', (event: CloseEvent | null) => {
				canvasClientLog('provider-connection-close', {
					roomId,
					code: event?.code ?? 0,
					reason: event?.reason ?? '',
					wasClean: event?.wasClean ?? false
				});
			});
			const defaultQueryAwarenessHandler = provider.messageHandlers[QUERY_AWARENESS_MESSAGE_TYPE];
			provider.messageHandlers[QUERY_AWARENESS_MESSAGE_TYPE] = (
				encoder: unknown,
				decoder: unknown,
				wsProvider: unknown,
				emitSynced: boolean,
				messageType: number
			) => {
				canvasClientLog('provider-query-awareness', { roomId });
				if (typeof defaultQueryAwarenessHandler === 'function') {
					defaultQueryAwarenessHandler(encoder, decoder, wsProvider, emitSynced, messageType);
				}
			};
			awarenessChangeHandler = () => {
				void handleAwarenessChange();
			};
			awareness.on('change', awarenessChangeHandler);
			attachProviderTransportDebugListener();
			attachProviderSnapshotListener();

			// Keep type reference alive for dynamic import consistency.
			void MonacoBinding;

			await waitForInitialProviderSync();
			if (yFileTree.size === 0) {
				canvasClientNarrative(
					`Room ${roomId} provider sync returned empty file tree; trying HTTP snapshot fallback.`
				);
				await loadPersistedCanvasSnapshotFromServer();
			}
			await initFileSystem({ createDefaultIfEmpty: yFileTree.size === 0 });
			if (yFileTree.size > 0) {
				await reconcileLocalFileSystemWithSharedTree();
				await refreshFileTree();
				await syncOpenTabsWithFileTree();
			} else {
				await upsertSharedEntries(
					fileTree.map((entry) => ({
						relativePath: entry.relativePath,
						isDir: entry.isDir
					}))
				);
			}
			await ensureWorkspaceHasAtLeastOneFile();
			if (!currentFile) {
				selectInitialFileFromTree();
			}
			if (currentFile) {
				await recreateBindingForCurrentFile();
				updateEditorAccessMode();
			} else {
				await clearActiveEditor();
			}
			canvasClientLog('init-ready', {
				roomId,
				fileCount: fileTree.length,
				currentFile: currentFile || ''
			});
			renderRemotePresenceStyles();
		} catch (error) {
			canvasClientLog('init-error', {
				roomId,
				error: error instanceof Error ? error.message : String(error)
			});
			fileExplorerError =
				error instanceof Error ? error.message : 'Canvas failed to initialize';
		}
	});

	onDestroy(() => {
		void persistCurrentFileToFS();
		cursorSelectionDisposable?.dispose();
		cursorSelectionDisposable = null;
		editorContentChangeDisposable?.dispose();
		editorContentChangeDisposable = null;
		if (removeGlobalContextHandlers) {
			removeGlobalContextHandlers();
			removeGlobalContextHandlers = null;
		}
		if (removeCanvasViewportListener) {
			removeCanvasViewportListener();
			removeCanvasViewportListener = null;
		}
		if (removeBeforeUnloadListener) {
			removeBeforeUnloadListener();
			removeBeforeUnloadListener = null;
		}
		if (terminalResizeObserver) {
			terminalResizeObserver.disconnect();
			terminalResizeObserver = null;
		}
		stopTerminalResize();
		closeContextMenu();
		closeDeleteConfirmation();
		if (promptState.reject) {
			promptState.reject(new Error(PROMPT_CANCELLED_ERROR));
		}
		resetPromptState();
		if (awareness && awarenessChangeHandler && typeof awareness.off === 'function') {
			awareness.off('change', awarenessChangeHandler);
		}
		if (yFileTree && yFileTreeObserver) {
			yFileTree.unobserve(yFileTreeObserver);
		}
		if (ydoc && ydocUpdateHandler) {
			ydoc.off('update', ydocUpdateHandler);
		}
		ydocUpdateHandler = null;
		detachProviderTransportDebugListener();
		detachProviderSnapshotListener();
		if (saveTimeout) {
			window.clearTimeout(saveTimeout);
			saveTimeout = null;
		}
		if (periodicSnapshotInterval) {
			window.clearInterval(periodicSnapshotInterval);
			periodicSnapshotInterval = null;
		}
		if (filePersistTimeout) {
			window.clearTimeout(filePersistTimeout);
			filePersistTimeout = null;
		}
		void saveCanvasSnapshotNow({ useBeacon: true });
		if (remotePresenceStyleElement?.parentNode) {
			remotePresenceStyleElement.parentNode.removeChild(remotePresenceStyleElement);
		}
		remotePresenceStyleElement = null;
		clearRemoteSelectionDecorations();
		currentYText = null;
		yjsApi = null;
		yFileTree = null;
		yFileTreeObserver = null;
		awareness = null;
		awarenessChangeHandler = null;
		binding?.destroy();
		provider?.destroy();
		ydoc?.destroy();
		editor?.dispose();
		terminal?.dispose();
		terminal = null;
		terminalFitAddon = null;
	});
</script>

<div
	class="canvas-shell"
	class:is-compact-layout={isCompactCanvasLayout}
	class:show-mobile-explorer={isCompactCanvasLayout && mobileCanvasPane === 'explorer'}
	class:show-mobile-editor={isCompactCanvasLayout && mobileCanvasPane === 'editor'}
>
	{#if showReadOnlyWarning}
		<div class="canvas-readonly-warning" role="status" aria-live="polite">
			Max 5 editors reached. You are in read-only mode.
		</div>
	{/if}
	<aside
		class="canvas-sidebar"
		class:drag-over={isSidebarDragOver}
		bind:this={sidebarElement}
		on:dragenter={handleSidebarDragEnter}
		on:dragover={handleSidebarDragOver}
		on:dragleave={handleSidebarDragLeave}
		on:drop={handleSidebarDrop}
	>
		<div class="file-explorer-header">
			<span>Explorer</span>
			<div class="file-explorer-actions">
				<button
					type="button"
					class="file-action-label-btn"
					title="Export Workspace Zip"
					aria-label="Export Workspace Zip"
					on:click={() => void exportWorkspaceZip()}
				>
					Export
				</button>
				<button
					type="button"
					class="file-action-label-btn"
					title="Import Workspace Zip"
					aria-label="Import Workspace Zip"
					on:click={triggerImportZip}
				>
					Import
				</button>
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
		<div class="github-import-row">
			<input
				type="url"
				class="github-import-input"
				placeholder="https://github.com/user/repo"
				bind:value={githubRepoURL}
				on:keydown={(event) => {
					if (event.key === 'Enter') {
						event.preventDefault();
						void importFromGitHub();
					}
				}}
			/>
			<button
				type="button"
				class="github-import-btn"
				on:click={() => void importFromGitHub()}
				disabled={isImportingRepo}
			>
				{isImportingRepo ? 'Importing...' : 'Import Repo'}
			</button>
		</div>
		<input
			type="file"
			accept=".zip"
			class="zip-import-input"
			bind:this={importZipInput}
			on:change={handleZipImportChange}
		/>
		{#if fileExplorerError}
			<div class="file-error" role="status" aria-live="polite">{fileExplorerError}</div>
		{/if}

		<div
			class="file-list"
			role="presentation"
			on:contextmenu={(event) => void openContextMenu(event, null)}
		>
			{#if fileTree.length === 0}
				<div class="file-list-empty">No files yet</div>
			{:else}
				{#each visibleFileTree as entry (entry.path)}
					<div
						class="file-entry-row"
						class:is-dir={entry.isDir}
						class:active={!entry.isDir && entry.relativePath === currentFile}
						class:contains-active={folderContainsCurrentFile(entry)}
						role="presentation"
						on:contextmenu={(event) => void openContextMenu(event, entry)}
					>
						<div
							class="file-entry-main"
							class:is-dir={entry.isDir}
							style:padding-left={`${0.48 + entry.depth * 0.82}rem`}
						>
							{#if entry.isDir}
								<button
									type="button"
									class="file-entry-chevron-button"
									aria-label={isFolderExpanded(entry)
										? `Collapse ${entry.name}`
										: `Expand ${entry.name}`}
									aria-expanded={isFolderExpanded(entry)}
									on:click|stopPropagation={() => toggleFolder(entry)}
									on:keydown={(event) => handleExplorerEntryKeydown(event, entry)}
								>
									<span class="file-entry-chevron" aria-hidden="true">
										<svg viewBox="0 0 24 24" class:expanded={isFolderExpanded(entry)}>
											<path d="M9 6l6 6-6 6" />
										</svg>
									</span>
								</button>
							{:else}
								<span class="file-entry-chevron-spacer" aria-hidden="true"></span>
							{/if}
							<button
								type="button"
								class="file-entry-trigger"
								class:is-dir={entry.isDir}
								aria-expanded={entry.isDir ? isFolderExpanded(entry) : undefined}
								on:click={() => handleExplorerEntryClick(entry)}
								on:keydown={(event) => handleExplorerEntryKeydown(event, entry)}
							>
								<span class="file-entry-icon" class:is-dir={entry.isDir} aria-hidden="true">
									{#if entry.isDir}
										{#if isFolderExpanded(entry)}
											<svg viewBox="0 0 24 24">
												<path
													d="M3.5 9h6l2 2h9l-2 7.2a2 2 0 0 1-1.92 1.46H5.4a2 2 0 0 1-1.95-1.57L2 11.3A2 2 0 0 1 3.5 9Z"
												/>
											</svg>
										{:else}
											<svg viewBox="0 0 24 24">
												<path d="M3.5 7.5h6l2 2h9v8.5a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2V7.5Z" />
											</svg>
										{/if}
									{:else}
										<svg viewBox="0 0 24 24">
											<path
												d="M7.5 3.5h6l4 4v12.8a1.7 1.7 0 0 1-1.7 1.7H8.2a1.7 1.7 0 0 1-1.7-1.7V5.2a1.7 1.7 0 0 1 1-1.7Z"
											/>
											<path d="M13.5 3.5v4h4" />
										</svg>
									{/if}
								</span>
								<span class="file-entry-label">{entry.name}</span>
							</button>
						</div>
						<button
							type="button"
							class="file-entry-more"
							title="More Options"
							aria-label="More Options"
							on:click|stopPropagation={(event) => void openContextMenu(event, entry)}
						>
							<svg viewBox="0 0 24 24" aria-hidden="true">
								<path
									d="M12 5.5a1.5 1.5 0 1 0 0 .01M12 12a1.5 1.5 0 1 0 0 .01M12 18.5a1.5 1.5 0 1 0 0 .01"
								/>
							</svg>
						</button>
						<button
							type="button"
							class="file-entry-delete"
							title={`Delete ${entry.name}`}
							aria-label={`Delete ${entry.name}`}
							on:click|stopPropagation={() => openDeleteConfirmation(entry)}
						>
							<svg viewBox="0 0 24 24" aria-hidden="true">
								<path d="M4.5 7.5h15" />
								<path d="M9.5 7.5v-2a1 1 0 0 1 1-1h3a1 1 0 0 1 1 1v2" />
								<path d="M7.5 7.5l.8 11a1.5 1.5 0 0 0 1.5 1.4h4.4a1.5 1.5 0 0 0 1.5-1.4l.8-11" />
								<path d="M10 11v5.5M14 11v5.5" />
							</svg>
						</button>
					</div>
				{/each}
			{/if}
		</div>
	</aside>
	<div class="canvas-editor">
		<div class="editor-tabs-bar">
			{#if isCompactCanvasLayout}
				<button
					type="button"
					class="editor-mobile-back"
					on:click={showExplorerPane}
					aria-label="Back to Explorer"
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d="M15 6l-6 6 6 6" />
					</svg>
					<span>Explorer</span>
				</button>
			{/if}
			<div class="editor-tabs" role="tablist" aria-label="Open files">
				{#if openTabs.length === 0}
					<div class="editor-tabs-empty">No open files</div>
				{:else}
					{#each openTabs as tab (tab)}
						<div class="editor-tab" class:active={tab === currentFile}>
							<button
								type="button"
								class="editor-tab-trigger"
								role="tab"
								aria-selected={tab === currentFile}
								title={tab}
								on:click={() => void switchToFile(tab)}
							>
								{getTabLabel(tab)}
							</button>
							<button
								type="button"
								class="editor-tab-close"
								aria-label={`Close ${getTabLabel(tab)} tab`}
								on:click|stopPropagation={() => void closeTab(tab)}
							>
								<svg viewBox="0 0 24 24" aria-hidden="true">
									<path d="M6 6l12 12M18 6 6 18" />
								</svg>
							</button>
						</div>
					{/each}
				{/if}
			</div>
		</div>
		<div class="canvas-editor-body" bind:this={canvasEditorBodyElement}>
			<div class="canvas-editor-pane" class:is-empty={openTabs.length === 0}>
				<div class="code-canvas" bind:this={editorContainer}></div>
				{#if openTabs.length === 0}
					<div class="canvas-blank-state" role="status" aria-live="polite">
						Open a file from Explorer to start editing.
					</div>
				{/if}
			</div>
			<div class="terminal-panel" style:height={`${terminalHeight}px`}>
				<button
					type="button"
					class="terminal-resize-handle"
					on:pointerdown={startTerminalResize}
					aria-label="Resize terminal"
				>
					<span class="terminal-resize-grip" aria-hidden="true"></span>
				</button>
				<div class="terminal-header">
					<span class="terminal-title">Terminal</span>
					<button type="button" class="terminal-action-button" on:click={clearTerminal}>
						Clear
					</button>
				</div>
				<div class="terminal-container" bind:this={terminalContainer}></div>
			</div>
		</div>
	</div>
	{#if deleteConfirmTarget}
		<div class="canvas-delete-overlay" role="presentation" on:click|self={closeDeleteConfirmation}>
			<div
				class="canvas-delete-dialog"
				role="alertdialog"
				aria-modal="true"
				aria-labelledby="canvas-delete-title"
				aria-describedby="canvas-delete-description"
			>
				<form on:submit|preventDefault={() => void confirmDeleteTarget()}>
					<div class="canvas-delete-title" id="canvas-delete-title">
						{getDeleteConfirmationTitle(deleteConfirmTarget)}
					</div>
					<p class="canvas-delete-description" id="canvas-delete-description">
						{getDeleteConfirmationMessage(deleteConfirmTarget)}
					</p>
					<div class="canvas-delete-actions">
						<button
							type="button"
							class="canvas-prompt-button secondary"
							on:click={closeDeleteConfirmation}
						>
							Cancel
						</button>
						<button type="submit" class="canvas-prompt-button danger">Delete</button>
					</div>
				</form>
			</div>
		</div>
	{/if}
</div>

{#if contextMenuOpen}
	<div
		class="explorer-context-menu"
		role="menu"
		aria-label="File explorer menu"
		tabindex="-1"
		bind:this={contextMenuElement}
		style:left={`${contextMenuX}px`}
		style:top={`${contextMenuY}px`}
	>
		<button
			type="button"
			class="explorer-context-action"
			role="menuitem"
			on:click={() => void contextEdit()}
			disabled={!contextMenuTarget || contextMenuTarget.isDir}
		>
			Edit
		</button>
		<button
			type="button"
			class="explorer-context-action"
			role="menuitem"
			on:click={contextCopy}
			disabled={!contextMenuTarget}
		>
			Copy
		</button>
		<button
			type="button"
			class="explorer-context-action"
			role="menuitem"
			on:click={() => void contextRename()}
			disabled={!contextMenuTarget}
		>
			Rename
		</button>
		<button
			type="button"
			class="explorer-context-action"
			role="menuitem"
			on:click={() => void contextPaste()}
			disabled={!explorerClipboard}
		>
			Paste
		</button>
		<div class="explorer-context-divider"></div>
		<button
			type="button"
			class="explorer-context-action"
			role="menuitem"
			on:click={() => void contextNewFolder()}
		>
			New Folder
		</button>
		<button
			type="button"
			class="explorer-context-action"
			role="menuitem"
			on:click={() => void contextNewFile()}
		>
			New File
		</button>
		<div class="explorer-context-divider"></div>
		<button
			type="button"
			class="explorer-context-action"
			role="menuitem"
			on:click={() => void contextRunFile()}
		>
			Run File
		</button>
		<button
			type="button"
			class="explorer-context-action"
			role="menuitem"
			on:click={() => void contextDelete()}
			disabled={!contextMenuTarget}
		>
			Delete Item
		</button>
		<button
			type="button"
			class="explorer-context-action"
			role="menuitem"
			on:click={() => void contextHistory()}
		>
			See File History
		</button>
		<button
			type="button"
			class="explorer-context-action"
			role="menuitem"
			on:click={() => void contextCopyPath()}
		>
			Copy File Path
		</button>
	</div>
{/if}

{#if promptState.isOpen}
	<div class="canvas-prompt-overlay" role="presentation" on:click|self={cancelPrompt}>
		<div
			class="canvas-prompt-dialog"
			role="dialog"
			aria-modal="true"
			aria-labelledby="canvas-prompt-title"
		>
			<form on:submit|preventDefault={submitPrompt}>
				<div class="canvas-prompt-title" id="canvas-prompt-title">
					{getPromptTitle(promptState.type)}
				</div>
				<input
					bind:this={promptInputElement}
					bind:value={promptInputValue}
					class="canvas-prompt-input"
					type="text"
					placeholder={getPromptPlaceholder(promptState.type)}
					autocomplete="off"
					on:keydown={handlePromptInputKeydown}
				/>
				<div class="canvas-prompt-actions">
					<button type="button" class="canvas-prompt-button secondary" on:click={cancelPrompt}>
						Cancel
					</button>
					<button type="submit" class="canvas-prompt-button primary">
						{getPromptSubmitLabel(promptState.type)}
					</button>
				</div>
			</form>
		</div>
	</div>
{/if}

<style>
	.canvas-shell {
		position: relative;
		width: 100%;
		height: 100%;
		min-height: 320px;
		display: flex;
		overflow: hidden;
	}

	.canvas-sidebar {
		width: 250px;
		flex: 0 0 250px;
		min-width: 0;
		min-height: 0;
		display: flex;
		flex-direction: column;
		gap: 0.55rem;
		border-right: 1px solid rgba(120, 134, 160, 0.35);
		background: rgba(10, 14, 22, 0.72);
		padding: 0.55rem;
		transition:
			border-color 0.14s ease,
			box-shadow 0.14s ease,
			background 0.14s ease;
	}

	.canvas-sidebar.drag-over {
		border-right-color: rgba(106, 166, 255, 0.9);
		background: rgba(16, 27, 44, 0.88);
		box-shadow: inset 0 0 0 1px rgba(106, 166, 255, 0.45);
	}

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

	.github-import-row {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: 0.3rem;
	}

	.github-import-input {
		width: 100%;
		min-width: 0;
		border: 1px solid rgba(103, 125, 160, 0.52);
		background: rgba(18, 27, 42, 0.86);
		color: #dbe6f8;
		border-radius: 0.35rem;
		padding: 0.32rem 0.46rem;
		font-size: 0.69rem;
		line-height: 1.2;
	}

	.github-import-input:focus {
		outline: none;
		border-color: rgba(117, 166, 248, 0.78);
		box-shadow: 0 0 0 2px rgba(117, 166, 248, 0.25);
	}

	.github-import-btn {
		border: 1px solid rgba(103, 125, 160, 0.52);
		background: rgba(24, 35, 52, 0.88);
		color: #dbe6f8;
		border-radius: 0.35rem;
		padding: 0 0.5rem;
		font-size: 0.66rem;
		font-weight: 600;
		letter-spacing: 0.02em;
		cursor: pointer;
		white-space: nowrap;
	}

	.github-import-btn:hover:not(:disabled) {
		border-color: rgba(139, 168, 211, 0.68);
		background: rgba(41, 61, 92, 0.92);
	}

	.github-import-btn:disabled {
		opacity: 0.72;
		cursor: wait;
	}

	.file-action-label-btn {
		border: 1px solid rgba(103, 125, 160, 0.52);
		background: rgba(24, 35, 52, 0.88);
		color: #dbe6f8;
		border-radius: 0.35rem;
		height: 1.35rem;
		padding: 0 0.42rem;
		font-size: 0.66rem;
		font-weight: 600;
		letter-spacing: 0.02em;
		cursor: pointer;
	}

	.file-action-label-btn:hover {
		border-color: rgba(139, 168, 211, 0.68);
		background: rgba(41, 61, 92, 0.92);
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

	.zip-import-input {
		display: none;
	}

	.file-action-btn svg,
	.file-entry-more svg,
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
		grid-template-columns: minmax(0, 1fr) auto auto;
		align-items: center;
		gap: 0.28rem;
		border-radius: 0.36rem;
		border: 1px solid transparent;
		background: rgba(21, 28, 42, 0.68);
	}

	.file-entry-row.is-dir {
		background: rgba(19, 26, 39, 0.72);
	}

	.file-entry-row:hover {
		border-color: rgba(127, 153, 194, 0.55);
		background: rgba(34, 45, 67, 0.86);
	}

	.file-entry-row.contains-active {
		border-color: rgba(95, 129, 189, 0.46);
		background: rgba(30, 44, 71, 0.82);
	}

	.file-entry-row.active {
		border-color: rgba(114, 159, 236, 0.72);
		background: rgba(39, 67, 117, 0.95);
	}

	.file-entry-main {
		padding: 0.32rem 0.44rem;
		min-width: 0;
		display: grid;
		grid-template-columns: auto minmax(0, 1fr);
		align-items: center;
		column-gap: 0.18rem;
	}

	.file-entry-main.is-dir {
		column-gap: 0.12rem;
	}

	.file-entry-trigger {
		border: none;
		background: transparent;
		color: #dbe6f8;
		padding: 0;
		text-align: left;
		font-size: 0.72rem;
		line-height: 1.3;
		cursor: pointer;
		min-width: 0;
		display: grid;
		grid-template-columns: auto minmax(0, 1fr);
		align-items: center;
		column-gap: 0.34rem;
	}

	.file-entry-trigger.is-dir {
		color: #c7d8f0;
		font-weight: 600;
	}

	.file-entry-trigger:focus-visible,
	.file-entry-chevron-button:focus-visible {
		outline: none;
		border-radius: 0.3rem;
		box-shadow: inset 0 0 0 1px rgba(117, 166, 248, 0.56);
	}

	.file-entry-chevron-button {
		border: none;
		background: transparent;
		padding: 0;
		width: 0.95rem;
		height: 0.95rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		border-radius: 0.25rem;
		color: rgba(181, 198, 224, 0.84);
	}

	.file-entry-chevron {
		width: 0.9rem;
		height: 0.9rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		color: rgba(181, 198, 224, 0.84);
		flex: 0 0 auto;
	}

	.file-entry-chevron svg,
	.file-entry-icon svg {
		width: 0.9rem;
		height: 0.9rem;
		stroke: currentColor;
		stroke-width: 1.9;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.file-entry-chevron svg {
		transition: transform 0.12s ease;
	}

	.file-entry-chevron svg.expanded {
		transform: rotate(90deg);
	}

	.file-entry-chevron-spacer {
		display: inline-block;
		width: 0.9rem;
		height: 0.9rem;
		flex: 0 0 auto;
	}

	.file-entry-icon {
		width: 0.95rem;
		height: 0.95rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		color: #9ab7ea;
		flex: 0 0 auto;
	}

	.file-entry-icon.is-dir {
		color: #e8bf63;
	}

	.file-entry-label {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.file-entry-more,
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

	.file-entry-row:hover .file-entry-more,
	.file-entry-row.active .file-entry-more,
	.file-entry-row:hover .file-entry-delete,
	.file-entry-row.active .file-entry-delete {
		opacity: 1;
	}

	.file-entry-more:hover {
		border-color: rgba(139, 168, 211, 0.72);
		color: #f1f6ff;
		background: rgba(39, 61, 95, 0.92);
	}

	.file-entry-delete:hover {
		border-color: rgba(231, 138, 138, 0.72);
		color: #ffd1d1;
		background: rgba(109, 26, 26, 0.86);
	}

	.editor-mobile-back {
		border: 1px solid rgba(103, 125, 160, 0.52);
		background: rgba(24, 35, 52, 0.88);
		color: #dbe6f8;
		border-radius: 0.4rem;
		min-height: 1.7rem;
		padding: 0.36rem 0.5rem;
		display: inline-flex;
		align-items: center;
		gap: 0.28rem;
		cursor: pointer;
		flex: 0 0 auto;
		font-size: 0.72rem;
		font-weight: 600;
		white-space: nowrap;
	}

	.editor-mobile-back:hover {
		border-color: rgba(139, 168, 211, 0.68);
		background: rgba(41, 61, 92, 0.92);
	}

	.editor-mobile-back svg {
		width: 0.9rem;
		height: 0.9rem;
		stroke: currentColor;
		stroke-width: 2;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.canvas-editor {
		display: flex;
		flex: 1;
		flex-direction: column;
		min-width: 0;
		min-height: 0;
	}

	.editor-tabs-bar {
		display: flex;
		align-items: center;
		gap: 0.22rem;
		min-height: 2.35rem;
		padding: 0.34rem 0.4rem;
		border-bottom: 1px solid rgba(120, 134, 160, 0.35);
		background: rgba(16, 23, 36, 0.84);
		min-width: 0;
	}

	.editor-tabs {
		display: flex;
		align-items: center;
		gap: 0.22rem;
		min-width: 0;
		flex: 1;
		overflow-x: auto;
		overflow-y: hidden;
	}

	.editor-tabs-empty {
		font-size: 0.74rem;
		color: rgba(216, 228, 246, 0.76);
		padding: 0 0.3rem;
		white-space: nowrap;
		flex: 0 0 auto;
	}

	.editor-tab {
		display: inline-flex;
		align-items: center;
		gap: 0.16rem;
		border: 1px solid rgba(109, 131, 168, 0.35);
		border-radius: 0.4rem;
		background: rgba(30, 43, 64, 0.72);
		max-width: min(18rem, 56vw);
	}

	.editor-tab.active {
		border-color: rgba(122, 168, 244, 0.68);
		background: rgba(43, 70, 118, 0.94);
	}

	.editor-tab-trigger {
		border: none;
		background: transparent;
		color: #dbe6f8;
		font-size: 0.74rem;
		line-height: 1.25;
		padding: 0.36rem 0.2rem 0.36rem 0.48rem;
		cursor: pointer;
		max-width: min(15rem, 46vw);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.editor-tab-close {
		border: none;
		background: transparent;
		color: rgba(219, 230, 248, 0.86);
		width: 1.35rem;
		height: 1.35rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		border-radius: 0.3rem;
		padding: 0;
		margin-right: 0.15rem;
	}

	.editor-tab-close:hover {
		background: rgba(131, 41, 41, 0.62);
		color: #ffe0e0;
	}

	.editor-tab-close svg {
		width: 0.72rem;
		height: 0.72rem;
		stroke: currentColor;
		stroke-width: 2;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.canvas-editor-body {
		display: flex;
		flex-direction: column;
		flex: 1;
		min-width: 0;
		min-height: 0;
	}

	.canvas-editor-pane {
		position: relative;
		flex: 1;
		min-width: 0;
		min-height: 0;
	}

	.code-canvas {
		width: 100%;
		height: 100%;
		min-height: 220px;
	}

	.canvas-editor-pane.is-empty .code-canvas {
		visibility: hidden;
		pointer-events: none;
	}

	.canvas-blank-state {
		position: absolute;
		inset: 0;
		display: flex;
		align-items: center;
		justify-content: center;
		text-align: center;
		padding: 1rem;
		font-size: 0.86rem;
		color: rgba(214, 227, 247, 0.82);
		background: radial-gradient(circle at 28% 24%, rgba(67, 97, 148, 0.3), rgba(8, 12, 19, 0.88));
	}

	.terminal-panel {
		position: relative;
		flex: 0 0 auto;
		min-height: 120px;
		border-top: 1px solid rgba(103, 125, 160, 0.42);
		background: linear-gradient(180deg, rgba(17, 22, 31, 0.98), rgba(12, 16, 24, 0.98)), #1e1e1e;
		display: flex;
		flex-direction: column;
		min-width: 0;
		overflow: hidden;
	}

	.terminal-resize-handle {
		position: absolute;
		top: 0;
		left: 0;
		right: 0;
		height: 0.8rem;
		border: none;
		background: transparent;
		cursor: row-resize;
		padding: 0;
		z-index: 2;
	}

	.terminal-resize-grip {
		position: absolute;
		top: 0.18rem;
		left: 50%;
		transform: translateX(-50%);
		width: 3rem;
		height: 0.18rem;
		border-radius: 999px;
		background: rgba(148, 163, 184, 0.46);
	}

	.terminal-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		padding: 0.72rem 0.9rem 0.48rem;
		color: #dbe6f8;
		font-size: 0.72rem;
		font-weight: 700;
		letter-spacing: 0.03em;
		text-transform: uppercase;
		border-bottom: 1px solid rgba(58, 73, 98, 0.68);
		background: rgba(10, 14, 22, 0.72);
	}

	.terminal-title {
		white-space: nowrap;
	}

	.terminal-action-button {
		border: 1px solid rgba(103, 125, 160, 0.52);
		background: rgba(24, 35, 52, 0.88);
		color: #dbe6f8;
		border-radius: 0.35rem;
		padding: 0.22rem 0.48rem;
		font-size: 0.66rem;
		font-weight: 600;
		cursor: pointer;
	}

	.terminal-action-button:hover {
		border-color: rgba(139, 168, 211, 0.68);
		background: rgba(41, 61, 92, 0.92);
	}

	.terminal-container {
		flex: 1;
		min-height: 0;
		padding: 0.65rem 0.72rem 0.72rem;
		background: #1e1e1e;
	}

	.terminal-container :global(.xterm) {
		height: 100%;
	}

	.terminal-container :global(.xterm-viewport) {
		overflow-y: auto;
		background: transparent;
	}

	.terminal-container :global(.xterm-screen),
	.terminal-container :global(.xterm-helpers) {
		width: 100% !important;
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

	.explorer-context-menu {
		position: fixed;
		z-index: 10050;
		min-width: 13rem;
		padding: 0.32rem;
		border-radius: 0.52rem;
		border: 1px solid rgba(118, 139, 177, 0.42);
		background: rgba(14, 21, 34, 0.98);
		box-shadow: 0 16px 34px rgba(0, 0, 0, 0.4);
		display: flex;
		flex-direction: column;
		gap: 0.12rem;
	}

	.explorer-context-action {
		border: 1px solid transparent;
		background: transparent;
		color: #dce7fa;
		border-radius: 0.36rem;
		padding: 0.38rem 0.52rem;
		font-size: 0.74rem;
		font-weight: 500;
		text-align: left;
		cursor: pointer;
	}

	.explorer-context-action:hover:not(:disabled) {
		border-color: rgba(114, 156, 225, 0.48);
		background: rgba(36, 60, 96, 0.9);
	}

	.explorer-context-action:disabled {
		opacity: 0.45;
		cursor: not-allowed;
	}

	.explorer-context-divider {
		height: 1px;
		margin: 0.18rem 0.25rem;
		background: rgba(123, 141, 172, 0.34);
	}

	.canvas-delete-overlay {
		position: absolute;
		inset: 0;
		z-index: 6;
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 1rem;
		background: rgba(6, 11, 18, 0.66);
		backdrop-filter: blur(4px);
	}

	.canvas-delete-dialog {
		width: min(25rem, 100%);
		padding: 0.95rem;
		border-radius: 0.6rem;
		border: 1px solid rgba(118, 139, 177, 0.42);
		background: rgba(14, 21, 34, 0.98);
		box-shadow: 0 18px 40px rgba(0, 0, 0, 0.45);
	}

	.canvas-delete-dialog form {
		display: flex;
		flex-direction: column;
		gap: 0.72rem;
	}

	.canvas-delete-title {
		color: #f1f5ff;
		font-size: 0.88rem;
		font-weight: 700;
		letter-spacing: 0.02em;
	}

	.canvas-delete-description {
		margin: 0;
		color: rgba(219, 230, 248, 0.84);
		font-size: 0.76rem;
		line-height: 1.45;
	}

	.canvas-delete-actions {
		display: flex;
		justify-content: flex-end;
		gap: 0.45rem;
	}

	.canvas-prompt-overlay {
		position: fixed;
		inset: 0;
		z-index: 10070;
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 1rem;
		background: rgba(6, 11, 18, 0.76);
		backdrop-filter: blur(6px);
	}

	.canvas-prompt-dialog {
		width: min(24rem, 100%);
		padding: 0.9rem;
		border-radius: 0.6rem;
		border: 1px solid rgba(118, 139, 177, 0.42);
		background: rgba(14, 21, 34, 0.98);
		box-shadow: 0 18px 40px rgba(0, 0, 0, 0.45);
	}

	.canvas-prompt-dialog form {
		display: flex;
		flex-direction: column;
		gap: 0.7rem;
	}

	.canvas-prompt-title {
		color: #e4ecfb;
		font-size: 0.86rem;
		font-weight: 700;
		letter-spacing: 0.02em;
	}

	.canvas-prompt-input {
		min-width: 0;
		border: 1px solid rgba(103, 125, 160, 0.52);
		background: rgba(18, 27, 42, 0.86);
		color: #dbe6f8;
		border-radius: 0.4rem;
		padding: 0.55rem 0.65rem;
		font-size: 0.78rem;
		line-height: 1.25;
	}

	.canvas-prompt-input:focus {
		outline: none;
		border-color: rgba(117, 166, 248, 0.78);
		box-shadow: 0 0 0 2px rgba(117, 166, 248, 0.25);
	}

	.canvas-prompt-actions {
		display: flex;
		justify-content: flex-end;
		gap: 0.45rem;
	}

	.canvas-prompt-button {
		border: 1px solid rgba(103, 125, 160, 0.52);
		border-radius: 0.4rem;
		padding: 0.46rem 0.72rem;
		font-size: 0.74rem;
		font-weight: 600;
		cursor: pointer;
		transition:
			background 0.14s ease,
			border-color 0.14s ease;
	}

	.canvas-prompt-button.secondary {
		background: rgba(24, 35, 52, 0.88);
		color: #dbe6f8;
	}

	.canvas-prompt-button.secondary:hover {
		border-color: rgba(139, 168, 211, 0.68);
		background: rgba(41, 61, 92, 0.92);
	}

	.canvas-prompt-button.primary {
		border-color: rgba(95, 130, 180, 0.7);
		background: rgba(36, 71, 130, 0.92);
		color: #f7fbff;
	}

	.canvas-prompt-button.primary:hover {
		border-color: rgba(122, 168, 244, 0.82);
		background: rgba(49, 88, 156, 0.96);
	}

	.canvas-prompt-button.danger {
		border-color: rgba(183, 82, 82, 0.76);
		background: rgba(131, 35, 35, 0.94);
		color: #fff3f3;
	}

	.canvas-prompt-button.danger:hover {
		border-color: rgba(231, 138, 138, 0.82);
		background: rgba(154, 42, 42, 0.98);
	}

	@media (max-width: 900px) {
		.canvas-shell {
			flex-direction: column;
		}

		.canvas-sidebar {
			width: 100%;
			flex: 1 1 auto;
			max-height: none;
			border-right: none;
			border-bottom: none;
		}

		.canvas-shell.show-mobile-explorer .canvas-editor {
			display: none;
		}

		.canvas-shell.show-mobile-editor .canvas-sidebar {
			display: none;
		}

		.canvas-shell.show-mobile-explorer .canvas-sidebar,
		.canvas-shell.show-mobile-editor .canvas-editor {
			flex: 1 1 auto;
			min-height: 0;
		}

		.editor-tab {
			max-width: 70vw;
		}
	}

	@media (hover: none) and (pointer: coarse) {
		.file-entry-more {
			opacity: 1;
		}
	}
</style>
