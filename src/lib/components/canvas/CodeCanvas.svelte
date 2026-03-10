<script lang="ts">
	import { zipSync, unzipSync } from 'fflate';
	import { initFileSystem as initLightningFS } from '$lib/utils/fs';
	import {
		ExecutionManager,
		type ExecutionOutputLine,
		type ExecutionRunHandle,
		type ExecutionWorkspaceFile
	} from '$lib/utils/executionManager';
	import { applyUpdate, encodeStateAsUpdate } from 'yjs';
	import { createEventDispatcher, onDestroy, onMount, tick } from 'svelte';
	import { APP_LIMITS } from '$lib/config/limits';
	import 'xterm/css/xterm.css';

	export let roomId: string;
	export let currentUser: { id: string; name: string; color: string };
	export let isEphemeralRoom = true;
	export let aiEnabled = true;

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

	type CanvasSidebarView = 'explorer' | 'search' | 'canvas_ai';
	type MobileCanvasPane = 'explorer' | 'editor';
	type CanvasSocketPayload = string | ArrayBufferLike | Blob | ArrayBufferView;
	type CanvasDebugWebSocket = WebSocket & {
		__canvasDebugOriginalSend?: (data: CanvasSocketPayload) => void;
		__canvasDebugSendWrapped?: boolean;
	};

	type CanvasSnippetPayload = {
		snippet: string;
		message: string;
		fileName: string;
	};

	type CanvasAIChatRole = 'user' | 'assistant';

	type CanvasAIChangeAction = 'replace' | 'create' | 'delete';

	type CanvasAIChangeDraft = {
		filePath: string;
		action: CanvasAIChangeAction;
		summary: string;
		locationHint: string;
		updatedCode: string;
	};

	type CanvasAIProposedChange = CanvasAIChangeDraft & {
		id: string;
		previousCode: string;
		diffText: string;
		applyState: 'pending' | 'applied' | 'failed';
		applyError: string;
	};

	type CanvasAIChatMessage = {
		id: string;
		role: CanvasAIChatRole;
		text: string;
		changes?: CanvasAIProposedChange[];
		timestamp: number;
	};

	type CanvasAIParsedResponse = {
		assistantReply: string;
		changes: CanvasAIChangeDraft[];
	};

	type TerminalPanelTab = 'out' | 'in';

	type SidebarSearchResultKind = 'file' | 'folder' | 'text';

	type SidebarSearchResult = {
		key: string;
		kind: SidebarSearchResultKind;
		path: string;
		preview: string;
		lineNumber?: number;
		startColumn?: number;
		endColumn?: number;
		range?: any;
	};

	type SidebarSearchHighlightSegment = {
		value: string;
		isMatch: boolean;
	};

	type FileIconKind =
		| 'generic'
		| 'javascript'
		| 'typescript'
		| 'python'
		| 'c'
		| 'cpp'
		| 'java'
		| 'go'
		| 'rust'
		| 'json'
		| 'html'
		| 'css'
		| 'markdown'
		| 'shell';

	const DEFAULT_PROJECT_FILE_NAME = 'ToraEditorInput.txt';
	const DEFAULT_PROJECT_FILE_CONTENT = '';
	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';
	const textEncoder = new TextEncoder();
	const textDecoder = new TextDecoder();
	const QUERY_AWARENESS_MESSAGE_TYPE = 3;
	const FILE_TREE_SYNC_ORIGIN = 'canvas-file-tree-sync';
	const MODEL_SYNC_ORIGIN = 'canvas-model-sync';
	const SNAPSHOT_LOAD_TIMEOUT_MS = 15000;
	const PROMPT_CANCELLED_ERROR = 'canvas-prompt-cancelled';
	const CANVAS_CLIENT_LOG_PREFIX = '[canvas-client]';
	const CANVAS_AI_DEVICE_ID_STORAGE_KEY = 'canvasAiDeviceId';
	const CANVAS_AI_SYSTEM_PROMPT = `You are an in-editor coding assistant for a collaborative canvas IDE.
Return ONLY valid JSON with this exact shape:
{
  "assistant_reply": "short conversational response for the user",
  "changes": [
    {
      "file_path": "relative/path/from/project/root.ext",
      "action": "replace | create | delete",
      "summary": "what changed in one sentence",
      "location_hint": "function/class/section affected",
      "updated_code": "full updated file content for replace/create; empty string for delete"
    }
  ]
}
Rules:
- assistant_reply: concise, plain text, no markdown.
- changes: include every file modification needed.
- file_path must match workspace relative paths exactly.
- action:
  - replace: file exists and updated_code must be full final file content
  - create: file does not exist and updated_code must be full file content
  - delete: remove file and keep updated_code empty
- location_hint is required and should identify where the change applies.
- Never omit assistant_reply or changes.
- Return raw JSON only, no markdown fences, no extra text.`;
	const CANVAS_AI_CHAT_HISTORY_LIMIT = 20;
	const CANVAS_AI_CONTEXT_MESSAGES = 8;
	const CANVAS_AI_TEXT_PREVIEW_LIMIT = 420;
	const CANVAS_AI_MAX_INPUT_TOKENS = 10000;
	const CANVAS_AI_CHARS_PER_TOKEN = 4;
	const CANVAS_AI_MAX_PROMPT_CHARS = CANVAS_AI_MAX_INPUT_TOKENS * CANVAS_AI_CHARS_PER_TOKEN;
	const CANVAS_AI_PROMPT_RESERVED_CHARS = 9000;
	const CANVAS_AI_CONTEXT_MAX_CHARS = Math.max(4000, CANVAS_AI_MAX_PROMPT_CHARS - CANVAS_AI_PROMPT_RESERVED_CHARS);
	const CANVAS_AI_MAX_CONVERSATION_CONTEXT_CHARS = 4000;
	const CANVAS_AI_MAX_INSTRUCTION_CHARS = 3200;
	const CANVAS_AI_MIN_SECTION_CHARS = 160;
	const CANVAS_AI_MAX_CONTEXT_FILES = 40;
	const CANVAS_AI_MAX_CHARS_PER_FILE = 12000;
	const CANVAS_AI_DIFF_CONTEXT_LINES = 3;
	const CANVAS_AI_DIFF_MAX_LINES = 320;
	const EXPLORER_LONG_PRESS_DELAY_MS = 520;
	const EXPLORER_LONG_PRESS_MOVE_TOLERANCE_PX = 12;
	const EXPLORER_LONG_PRESS_CLICK_SUPPRESSION_MS = 700;
	const EXPLORER_NATIVE_CONTEXT_SUPPRESSION_MS = 1400;
	const MAX_FILE_EDITORS = APP_LIMITS.codeCanvas.maxFileEditors;
	const CODE_CANVAS_MEMORY_LIMIT_BYTES = APP_LIMITS.codeCanvas.memoryLimitBytes;
	const CODE_CANVAS_MEMORY_LIMIT_MESSAGE = `Code Canvas memory limit (${Math.max(
		1,
		Math.round(CODE_CANVAS_MEMORY_LIMIT_BYTES / (1024 * 1024))
	)}MB) reached.`;
	const YDOC_LIMIT_REVERT_ORIGIN = 'canvas-memory-limit-revert';
	const YDOC_LIMIT_ALERT_COOLDOWN_MS = APP_LIMITS.codeCanvas.yDocLimitAlertCooldownMs;
	const FILE_ICON_SVG: Record<FileIconKind, string> = {
		generic:
			'<svg viewBox="0 0 24 24" aria-hidden="true"><path fill="#9FB2CC" d="M7 2.75h7.6L20.5 8.7V20a1.25 1.25 0 0 1-1.25 1.25h-12.5A1.25 1.25 0 0 1 5.5 20V4A1.25 1.25 0 0 1 6.75 2.75Zm7.1 1.6V8.4h4.07z"/><path fill="#6E84A3" d="M8 13h8v1.4H8zm0 3.2h8v1.4H8z"/></svg>',
		javascript:
			'<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3.2" y="3.2" width="17.6" height="17.6" rx="2.2" fill="#F7DF1E"/><path fill="#1F2328" d="M9.2 16.8c.35.58.82 1 1.73 1 1 0 1.64-.5 1.64-1.2 0-.84-.66-1.14-1.78-1.63l-.38-.16c-1.1-.47-1.82-1.06-1.82-2.3 0-1.14.87-2.02 2.23-2.02.97 0 1.66.34 2.16 1.22l-1.18.76c-.26-.47-.55-.66-.98-.66-.45 0-.73.29-.73.66 0 .46.29.64.95.93l.38.16c1.29.55 2.01 1.12 2.01 2.4 0 1.38-1.08 2.13-2.54 2.13-1.43 0-2.36-.68-2.82-1.57Zm5.48-.13c.31.55.6 1.01 1.29 1.01.66 0 1.08-.26 1.08-1.27V10.6h1.58v5.82c0 1.76-1.03 2.56-2.52 2.56-1.35 0-2.13-.7-2.52-1.54Z"/></svg>',
		typescript:
			'<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3.2" y="3.2" width="17.6" height="17.6" rx="2.2" fill="#3178C6"/><path fill="#FFFFFF" d="M9.23 10.7H6.9V9.35h6.22v1.35h-2.33v7.12H9.23Zm5.1 5.57c.42.7 1 1.2 2 1.2.85 0 1.4-.42 1.4-1.01 0-.7-.55-.95-1.47-1.35l-.5-.21c-1.43-.6-2.37-1.34-2.37-2.9 0-1.45 1.1-2.55 2.83-2.55 1.22 0 2.1.42 2.74 1.53l-1.34.86c-.3-.53-.61-.73-1.1-.73-.5 0-.82.32-.82.73 0 .52.32.73 1.06 1.05l.5.21c1.68.72 2.63 1.46 2.63 3.12 0 1.78-1.4 2.76-3.29 2.76-1.85 0-3.05-.88-3.64-2.03Z"/></svg>',
		python:
			'<svg viewBox="0 0 24 24" aria-hidden="true"><path fill="#3776AB" d="M12.1 3.2c-4.3 0-4.03 1.86-4.03 1.86v1.93h4.1v.58H6.43S3.7 7.25 3.7 12.13c0 4.9 2.4 4.73 2.4 4.73h1.43v-2.02s-.08-2.4 2.35-2.4h4.07s2.3.04 2.3-2.22V6.3s.35-3.1-4.15-3.1Zm-2.27 1.78a.78.78 0 1 1 0 1.56.78.78 0 0 1 0-1.56Z"/><path fill="#FFD43B" d="M11.9 20.8c4.3 0 4.03-1.86 4.03-1.86V17h-4.1v-.58h5.74s2.73.32 2.73-4.56c0-4.9-2.4-4.73-2.4-4.73h-1.43v2.02s.08 2.4-2.35 2.4h-4.07s-2.3-.04-2.3 2.22v3.92s-.35 3.1 4.15 3.1Zm2.27-1.78a.78.78 0 1 1 0-1.56.78.78 0 0 1 0 1.56Z"/></svg>',
		c:
			'<svg viewBox="0 0 24 24" aria-hidden="true"><path fill="#4A5FB5" d="m12 2.2 8.49 4.9v9.8L12 21.8 3.5 16.9V7.1Z"/><path fill="#FFFFFF" d="M14.8 15.7a4.2 4.2 0 1 1 0-7.4l-.86 1.17a2.73 2.73 0 1 0 0 5.06z"/></svg>',
		cpp:
			'<svg viewBox="0 0 24 24" aria-hidden="true"><path fill="#659AD2" d="m12 2.2 8.49 4.9v9.8L12 21.8 3.5 16.9V7.1Z"/><path fill="#FFFFFF" d="M11.38 15.68a4.2 4.2 0 1 1 0-7.36l-.86 1.17a2.73 2.73 0 1 0 0 5.02Zm3.1-4.08h1.03v-1.02h1.04v1.02h1.03v1.04h-1.03v1.03H15.5v-1.03h-1.03Zm3.34 0h1.03v-1.02h1.03v1.02h1.04v1.04h-1.04v1.03h-1.03v-1.03h-1.03Z"/></svg>',
		java:
			'<svg viewBox="0 0 24 24" aria-hidden="true"><path fill="#E76F00" d="M12.8 3.9c1.4 1-.86 2.03-.86 3.2 0 .62.57 1.1.92 1.7.58 1.02-.34 1.65-1.06 2.26 1.77-.48 2.9-1.45 2.9-2.76 0-1.06-.72-1.74-1.9-4.4Z"/><path fill="#4A89C7" d="M7.1 14.7h9.8c.76 0 1.36.6 1.36 1.36v.12c0 1.76-1.42 3.18-3.18 3.18h-6.16a3.18 3.18 0 0 1-3.18-3.18v-.12c0-.76.6-1.36 1.36-1.36Zm1.55-2.7c2.05 1.17 5 1.16 7.12-.02l.42.86c-2.33 1.36-5.64 1.37-8 .02Z"/></svg>',
		go:
			'<svg viewBox="0 0 24 24" aria-hidden="true"><path fill="#00ADD8" d="M4.2 12.9c0-2.86 2.31-5.18 5.17-5.18h5.15c2.86 0 5.18 2.32 5.18 5.18s-2.32 5.18-5.18 5.18H9.37A5.18 5.18 0 0 1 4.2 12.9Z"/><circle cx="10.05" cy="12.9" r="1.05" fill="#FFFFFF"/><circle cx="14.28" cy="12.9" r="1.05" fill="#FFFFFF"/><path fill="#FFFFFF" d="M7.4 15.3h9.2v1.12H7.4z"/></svg>',
		rust:
			'<svg viewBox="0 0 24 24" aria-hidden="true"><path fill="#D9D9D9" d="m12 3.2 2.04.56 1.96-.78 1.16 1.78 2.09.2.2 2.08 1.78 1.17-.78 1.95L21.01 12l-.56 2.05.78 1.95-1.78 1.17-.2 2.08-2.09.2-1.16 1.78-1.96-.78-2.04.56-2.05-.56-1.96.78-1.16-1.78-2.09-.2-.2-2.08-1.78-1.17.78-1.95L2.99 12l.56-2.04-.78-1.96 1.78-1.17.2-2.08 2.09-.2 1.16-1.78 1.96.78Z"/><circle cx="12" cy="12" r="3.15" fill="#111827"/><path fill="#111827" d="M11.12 10.24h1.56c1.15 0 1.88.6 1.88 1.58 0 .73-.42 1.27-1.08 1.5l1.2 1.95h-1.24l-1.06-1.77h-.18v1.77h-1.08Zm1.42 2.43c.55 0 .86-.28.86-.75s-.31-.73-.86-.73h-.34v1.48Z"/></svg>',
		json:
			'<svg viewBox="0 0 24 24" aria-hidden="true"><path fill="#F7C948" d="M5.8 4.2h12.4a1.4 1.4 0 0 1 1.4 1.4v12.8a1.4 1.4 0 0 1-1.4 1.4H5.8a1.4 1.4 0 0 1-1.4-1.4V5.6a1.4 1.4 0 0 1 1.4-1.4Z"/><path fill="#1F2937" d="M8.35 8.2h1.18v1.62c0 .34-.08.62-.23.84-.16.22-.37.38-.63.48.26.1.47.26.63.47.15.22.23.5.23.84v1.63H8.35v-1.63c0-.34-.08-.58-.25-.72-.16-.14-.43-.2-.8-.2v-1.06c.37 0 .64-.07.8-.2.17-.14.25-.38.25-.72Zm7.3 0h-1.18v1.62c0 .34.08.62.23.84.16.22.37.38.63.48-.26.1-.47.26-.63.47-.15.22-.23.5-.23.84v1.63h1.18v-1.63c0-.34.08-.58.25-.72.16-.14.43-.2.8-.2v-1.06c-.37 0-.64-.07-.8-.2-.17-.14-.25-.38-.25-.72Z"/></svg>',
		html:
			'<svg viewBox="0 0 24 24" aria-hidden="true"><path fill="#E34F26" d="m4.1 3.6 1.6 16.8L12 22.2l6.3-1.8 1.6-16.8Z"/><path fill="#F16529" d="m12 20.73 5.07-1.45 1.37-14.3H12Z"/><path fill="#EBEBEB" d="M12 11.2H8.9l-.21-2.3H12V6.65H6.22l.06.63.58 6.17H12Zm0 5.87-.01.01-2.14-.6-.14-1.56H7.78l.27 2.97 3.94 1.1Z"/><path fill="#FFFFFF" d="M11.99 11.2v2.25h2.86l-.27 3.02-2.59.71v2.34l3.94-1.1.03-.32.54-5.65.06-.63Zm0-4.55V8.9h4.2l.03-.34.06-.65.14-1.57.06-.63Z"/></svg>',
		css:
			'<svg viewBox="0 0 24 24" aria-hidden="true"><path fill="#1572B6" d="m4.1 3.6 1.6 16.8L12 22.2l6.3-1.8 1.6-16.8Z"/><path fill="#33A9DC" d="m12 20.73 5.07-1.45 1.37-14.3H12Z"/><path fill="#EBEBEB" d="M12 11.1H8.93l-.2-2.2H12V6.65H6.2l.05.62.56 6.08H12Zm0 5.83-.01.01-2.12-.59-.14-1.53H7.8l.27 2.93 3.92 1.09Z"/><path fill="#FFFFFF" d="M12 11.1v2.2h2.72l-.26 2.86-2.46.67v2.29l3.9-1.09.03-.31.53-5.5.05-.62Zm0-4.45V8.9h4.07l.03-.33.06-.65.13-1.54.05-.62Z"/></svg>',
		markdown:
			'<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3.4" y="4.5" width="17.2" height="15" rx="2" fill="#6B7280"/><path fill="#FFFFFF" d="M6.9 8h2.05l1.8 2.23L12.55 8h2.05v7.96h-2.05v-4.8l-1.8 2.2-1.8-2.2v4.8H6.9Zm8.8 4.25h1.6V9.92h1.56v2.33h1.61L18 15.96Z"/></svg>',
		shell:
			'<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3.2" y="4.2" width="17.6" height="15.6" rx="2.2" fill="#111827"/><path fill="#9CA3AF" d="m7.1 9.03 3.02 2.7-3.02 2.71-1.02-1.14 1.75-1.57-1.75-1.56Zm4.15 4.75h5.65v1.45h-5.65z"/></svg>'
	};
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
	let terminalExpandedHeight = 200;
	let terminalPanelCollapsed = false;
	let activeTerminalPanelTab: TerminalPanelTab = 'out';
	let terminalInputDraft = '';
	let yjsApi: any = null;
	let ydoc: any = null;
	let yFileTree: any = null;
	let yFileTreeObserver: ((event: any) => void) | null = null;
	let ydocUpdateHandler:
		| ((update: Uint8Array, origin: unknown, doc: unknown, transaction: { local?: boolean }) => void)
		| null = null;
	let ydocBeforeTransactionHandler: ((transaction: { local?: boolean }) => void) | null = null;
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
	let editorScrollDisposable: Disposable | null = null;
	let currentYText: any = null;
	let remoteSelectionDecorations: string[] = [];
	let showReadOnlyWarning = false;
	let explorerClipboard: { path: string; isDir: boolean } | null = null;
	let activeSidebarView: CanvasSidebarView = 'explorer';
	let sidebarSearchQuery = '';
	let sidebarReplaceQuery = '';
	let sidebarSearchMatchCase = false;
	let sidebarSearchUseRegex = false;
	let sidebarSearchResults: SidebarSearchResult[] = [];
	let sidebarActiveSearchIndex = -1;
	let sidebarFileResultCount = 0;
	let sidebarFolderResultCount = 0;
	let sidebarTextResultCount = 0;
	let searchInputElement: HTMLInputElement | null = null;
	let dirtyFiles: string[] = [];
	let contextMenuOpen = false;
	let contextMenuX = 0;
	let contextMenuY = 0;
	let contextMenuTarget: ProjectFileEntry | null = null;
	let contextMenuElement: HTMLDivElement | null = null;
	let explorerLongPressTimer: ReturnType<typeof setTimeout> | null = null;
	let explorerLongPressTouchIdentifier = -1;
	let explorerLongPressTarget: ProjectFileEntry | null = null;
	let explorerLongPressStartX = 0;
	let explorerLongPressStartY = 0;
	let explorerLongPressLastX = 0;
	let explorerLongPressLastY = 0;
	let suppressExplorerClickUntil = 0;
	let suppressNativeExplorerContextMenuUntil = 0;
	let importZipInput: HTMLInputElement | null = null;
	let sidebarElement: HTMLElement | null = null;
	let isSidebarDragOver = false;
	let promptInputElement: HTMLInputElement | null = null;
	let snippetMessageInputElement: HTMLTextAreaElement | null = null;
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
	let saveTimeout: ReturnType<typeof setTimeout> | null = null;
	let filePersistTimeout: number | null = null;
	let periodicSnapshotInterval: number | null = null;
	let snapshotDirty = false;
	let executionManager: ExecutionManager | null = null;
	let activeExecutionHandle: ExecutionRunHandle | null = null;
	let removeExecutionOutputSubscription: (() => void) | null = null;
	let isRunInProgress = false;
	let runningFilePath = '';
	let isDraggingCode = false;
	let snippetDraft = '';
	let snippetMessage = '';
	let showSnippetComposer = false;
	let showCanvasAIPrompt = false;
	let canvasAIPrompt = '';
	let canvasAIError = '';
	let isCanvasAIGenerating = false;
	let canvasAIPromptElement: HTMLTextAreaElement | null = null;
	let canvasAISidebarPromptElement: HTMLTextAreaElement | null = null;
	let canvasAIAbortController: AbortController | null = null;
	let canvasAIThreadElement: HTMLDivElement | null = null;
	let canvasAISidebarThreadElement: HTMLDivElement | null = null;
	let canvasAIChatMessages: CanvasAIChatMessage[] = [];
	let canvasAILastSuggestedMessageId = '';
	let canSendSnippetFromSelection = false;
	let showSelectionSnippetAction = false;
	let selectionSnippetActionTop = 0;
	let selectionSnippetActionLeft = 0;
	let selectedSnippetText = '';
	let ydocSnapshotBeforeLocalTransaction: Uint8Array | null = null;
	let isRevertingOversizedYDocState = false;
	let lastYDocLimitAlertAt = 0;
	const presenceSessionId = createPresenceSessionId();
	const dispatch = createEventDispatcher<{
		sendSnippet: CanvasSnippetPayload;
	}>();

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

	function isLocalYDocTransaction(origin: unknown, transaction: { local?: boolean } | undefined) {
		if (typeof transaction?.local === 'boolean') {
			return transaction.local;
		}
		if (typeof origin === 'string') {
			return origin === MODEL_SYNC_ORIGIN || origin === FILE_TREE_SYNC_ORIGIN;
		}
		return false;
	}

	function notifyCodeCanvasMemoryLimitReached() {
		const now = Date.now();
		if (now - lastYDocLimitAlertAt < YDOC_LIMIT_ALERT_COOLDOWN_MS) {
			return;
		}
		lastYDocLimitAlertAt = now;
		fileExplorerError = CODE_CANVAS_MEMORY_LIMIT_MESSAGE;
		writeTerminalLine(`\x1b[31m${CODE_CANVAS_MEMORY_LIMIT_MESSAGE}\x1b[0m`);
		if (typeof window !== 'undefined' && typeof window.alert === 'function') {
			window.alert(CODE_CANVAS_MEMORY_LIMIT_MESSAGE);
		}
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
			clearTimeout(saveTimeout);
			saveTimeout = null;
		}
		saveTimeout = setTimeout(async () => {
			if (!ydoc || !roomId) {
				saveTimeout = null;
				return;
			}
			const snapshot = encodeStateAsUpdate(ydoc);
			const snapshotBytes = new Uint8Array(snapshot);
			try {
				const response = await fetch(canvasSnapshotURL(), {
					method: 'POST',
					headers: {
						'Content-Type': 'application/octet-stream'
					},
					body: snapshotBytes
				});
				if (response.ok) {
					snapshotDirty = false;
				}
			} catch (error) {
				canvasClientLog('snapshot-save-http-error', {
					roomId,
					error: error instanceof Error ? error.message : String(error)
				});
			}
			saveTimeout = null;
		}, 5000);
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
		const handleBeforeUnload = () => {
			if (saveTimeout) {
				clearTimeout(saveTimeout);
				saveTimeout = null;
			}
			void persistCurrentFileToFS();
			if (
				!ydoc ||
				!roomId ||
				typeof navigator === 'undefined' ||
				typeof navigator.sendBeacon !== 'function'
			) {
				return;
			}
			const snapshot = encodeStateAsUpdate(ydoc);
			const snapshotBytes = new Uint8Array(snapshot);
			navigator.sendBeacon(canvasSnapshotURL(), snapshotBytes);
		};
		window.addEventListener('beforeunload', handleBeforeUnload);
		return () => {
			window.removeEventListener('beforeunload', handleBeforeUnload);
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
				throw new Error('Failed to load snapshot from server: ' + response.status);
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
			throw error;
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
		const ext = getFileExtension(filename);
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

	function getFileExtension(filename: string) {
		return normalizeProjectName(filename).split('.').pop()?.toLowerCase() || '';
	}

	function getFileIconKind(filename: string): FileIconKind {
		const ext = getFileExtension(filename);
		if (ext === 'js' || ext === 'mjs' || ext === 'cjs') return 'javascript';
		if (ext === 'ts' || ext === 'tsx') return 'typescript';
		if (ext === 'py') return 'python';
		if (ext === 'c') return 'c';
		if (ext === 'cc' || ext === 'cpp' || ext === 'h' || ext === 'hpp') return 'cpp';
		if (ext === 'java') return 'java';
		if (ext === 'go') return 'go';
		if (ext === 'rs') return 'rust';
		if (ext === 'json') return 'json';
		if (ext === 'html') return 'html';
		if (ext === 'css' || ext === 'scss') return 'css';
		if (ext === 'md' || ext === 'markdown') return 'markdown';
		if (ext === 'sh' || ext === 'zsh' || ext === 'bash') return 'shell';
		return 'generic';
	}

	function getFileIconSVG(filename: string) {
		const iconKind = getFileIconKind(filename);
		return FILE_ICON_SVG[iconKind] || FILE_ICON_SVG.generic;
	}

	function markFileDirty(fileName: string) {
		const normalized = normalizeProjectName(fileName);
		if (!normalized || dirtyFiles.includes(normalized)) {
			return;
		}
		dirtyFiles = [...dirtyFiles, normalized];
	}

	function clearFileDirty(fileName: string) {
		const normalized = normalizeProjectName(fileName);
		if (!normalized) {
			return;
		}
		dirtyFiles = dirtyFiles.filter((path) => path !== normalized);
	}

	function isFileDirty(fileName: string) {
		const normalized = normalizeProjectName(fileName);
		return Boolean(normalized && dirtyFiles.includes(normalized));
	}

	function closeEditorFindWidget() {
		if (!editor) {
			return;
		}
		editor.trigger('canvas-search', 'closeFindWidget', null);
	}

	function setActiveSidebarView(view: CanvasSidebarView) {
		if (!aiEnabled && view === 'canvas_ai') {
			activeSidebarView = 'explorer';
			return;
		}
		activeSidebarView = view;
		if (view === 'search') {
			if (sidebarSearchQuery.trim()) {
				updateSidebarSearchResults();
			}
			void tick().then(() => {
				searchInputElement?.focus();
			});
			return;
		}
		if (view === 'canvas_ai') {
			void tick().then(() => {
				resizeCanvasAIPromptInput(canvasAISidebarPromptElement);
				canvasAISidebarPromptElement?.focus();
				scrollCanvasAIThreadToBottom();
			});
		}
	}

	function buildSidebarSearchPattern(rawQuery: string) {
		const trimmed = rawQuery.trim();
		if (!trimmed) {
			return '';
		}
		return trimmed;
	}

	function escapeRegExp(value: string) {
		return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
	}

	function buildSidebarQueryRegExp(pattern: string, global = false) {
		const source = sidebarSearchUseRegex ? pattern : escapeRegExp(pattern);
		const flags = `${sidebarSearchMatchCase ? '' : 'i'}${global ? 'g' : ''}`;
		try {
			return new RegExp(source, flags);
		} catch {
			return null;
		}
	}

	function collectSidebarSearchHighlights(value: string): SidebarSearchHighlightSegment[] {
		if (!value) {
			return [{ value, isMatch: false }];
		}
		const pattern = buildSidebarSearchPattern(sidebarSearchQuery);
		if (!pattern) {
			return [{ value, isMatch: false }];
		}
		const regex = buildSidebarQueryRegExp(pattern, true);
		if (!regex) {
			return [{ value, isMatch: false }];
		}
		const segments: SidebarSearchHighlightSegment[] = [];
		let cursor = 0;
		let guard = 0;
		let match: RegExpExecArray | null = null;
		while ((match = regex.exec(value)) !== null && guard < 500) {
			guard += 1;
			const start = match.index ?? 0;
			const matchedText = match[0] ?? '';
			if (!matchedText) {
				regex.lastIndex = start + 1;
				continue;
			}
			if (start > cursor) {
				segments.push({ value: value.slice(cursor, start), isMatch: false });
			}
			segments.push({ value: matchedText, isMatch: true });
			cursor = start + matchedText.length;
		}
		if (cursor < value.length) {
			segments.push({ value: value.slice(cursor), isMatch: false });
		}
		return segments.length > 0 ? segments : [{ value, isMatch: false }];
	}

	function getSidebarTextResultIndexes() {
		const indexes: number[] = [];
		for (let index = 0; index < sidebarSearchResults.length; index += 1) {
			const candidate = sidebarSearchResults[index];
			if (candidate.kind === 'text' && candidate.range) {
				indexes.push(index);
			}
		}
		return indexes;
	}

	function updateSidebarSearchResults() {
		const pattern = buildSidebarSearchPattern(sidebarSearchQuery);
		if (!pattern) {
			sidebarSearchResults = [];
			sidebarActiveSearchIndex = -1;
			sidebarFileResultCount = 0;
			sidebarFolderResultCount = 0;
			sidebarTextResultCount = 0;
			return;
		}
		const queryRegExp = buildSidebarQueryRegExp(pattern);
		if (!queryRegExp) {
			fileExplorerError = 'Invalid search pattern';
			sidebarSearchResults = [];
			sidebarActiveSearchIndex = -1;
			sidebarFileResultCount = 0;
			sidebarFolderResultCount = 0;
			sidebarTextResultCount = 0;
			return;
		}
		fileExplorerError = '';

		const fileAndFolderResults: SidebarSearchResult[] = fileTree
			.filter((entry) => {
				const relativePath = entry.relativePath || entry.name;
				return queryRegExp.test(entry.name) || queryRegExp.test(relativePath);
			})
			.map((entry) => {
				const relativePath = entry.relativePath || entry.name;
				return {
					key: `${entry.isDir ? 'folder' : 'file'}:${relativePath}`,
					kind: entry.isDir ? 'folder' : 'file',
					path: relativePath,
					preview: relativePath
				} satisfies SidebarSearchResult;
			})
			.sort((left, right) => {
				if (left.kind !== right.kind) {
					return left.kind === 'folder' ? -1 : 1;
				}
				return left.path.localeCompare(right.path);
			});

		let textResults: SidebarSearchResult[] = [];
		if (!editor || !monacoApi) {
			sidebarSearchResults = fileAndFolderResults;
			sidebarFileResultCount = fileAndFolderResults.filter((result) => result.kind === 'file').length;
			sidebarFolderResultCount = fileAndFolderResults.filter((result) => result.kind === 'folder').length;
			sidebarTextResultCount = 0;
			sidebarActiveSearchIndex = sidebarSearchResults.length > 0 ? 0 : -1;
			return;
		}

		const model = editor.getModel?.();
		if (!model) {
			sidebarSearchResults = fileAndFolderResults;
			sidebarFileResultCount = fileAndFolderResults.filter((result) => result.kind === 'file').length;
			sidebarFolderResultCount = fileAndFolderResults.filter((result) => result.kind === 'folder').length;
			sidebarTextResultCount = 0;
			sidebarActiveSearchIndex = sidebarSearchResults.length > 0 ? 0 : -1;
			return;
		}

		let matches: any[] = [];
		try {
			matches = model.findMatches(
				pattern,
				true,
				sidebarSearchUseRegex,
				sidebarSearchMatchCase,
				null,
				false,
				400
			);
		} catch (error) {
			fileExplorerError = error instanceof Error ? error.message : 'Invalid search pattern';
			sidebarSearchResults = [];
			sidebarActiveSearchIndex = -1;
			sidebarFileResultCount = 0;
			sidebarFolderResultCount = 0;
			sidebarTextResultCount = 0;
			return;
		}
		textResults = matches.map((match, index) => ({
			key: `text:${currentFile || 'active'}:${match.range.startLineNumber}:${match.range.startColumn}:${index}`,
			kind: 'text',
			path: currentFile || 'active-file',
			lineNumber: match.range.startLineNumber,
			startColumn: match.range.startColumn,
			endColumn: match.range.endColumn,
			preview: String(model.getLineContent(match.range.startLineNumber) || '').trim(),
			range: match.range
		}));

		sidebarSearchResults = [...fileAndFolderResults, ...textResults];
		sidebarFileResultCount = fileAndFolderResults.filter((result) => result.kind === 'file').length;
		sidebarFolderResultCount = fileAndFolderResults.filter((result) => result.kind === 'folder').length;
		sidebarTextResultCount = textResults.length;
		if (sidebarSearchResults.length === 0) {
			sidebarActiveSearchIndex = -1;
			return;
		}
		if (
			sidebarActiveSearchIndex < 0 ||
			sidebarActiveSearchIndex >= sidebarSearchResults.length
		) {
			sidebarActiveSearchIndex = 0;
		}
	}

	async function focusSidebarSearchResult(nextIndex: number) {
		if (sidebarSearchResults.length === 0) {
			return;
		}
		const wrappedIndex =
			((nextIndex % sidebarSearchResults.length) + sidebarSearchResults.length) %
			sidebarSearchResults.length;
		sidebarActiveSearchIndex = wrappedIndex;
		const target = sidebarSearchResults[wrappedIndex];
		if (!target) {
			return;
		}
		if (target.kind === 'folder') {
			expandedDirectories = {
				...ensureExpandedDirectoriesForPath(target.path, expandedDirectories),
				[target.path]: true
			};
			setActiveSidebarView('explorer');
			if (isCompactCanvasLayout) {
				showExplorerPane();
			}
			return;
		}
		if (target.kind === 'file') {
			await switchToFile(target.path);
			return;
		}
		if (!editor || !target.range) {
			return;
		}
		editor.setSelection(target.range);
		editor.revealRangeInCenter(target.range);
		editor.focus();
	}

	async function searchNextResult() {
		if (!sidebarSearchResults.length) {
			updateSidebarSearchResults();
		}
		const textResultIndexes = getSidebarTextResultIndexes();
		if (!textResultIndexes.length) {
			return;
		}
		const currentTextIndex = textResultIndexes.findIndex((index) => index === sidebarActiveSearchIndex);
		const nextIndex =
			currentTextIndex >= 0
				? textResultIndexes[(currentTextIndex + 1) % textResultIndexes.length]
				: textResultIndexes[0];
		await focusSidebarSearchResult(nextIndex);
	}

	async function searchPreviousResult() {
		if (!sidebarSearchResults.length) {
			updateSidebarSearchResults();
		}
		const textResultIndexes = getSidebarTextResultIndexes();
		if (!textResultIndexes.length) {
			return;
		}
		const currentTextIndex = textResultIndexes.findIndex((index) => index === sidebarActiveSearchIndex);
		const previousIndex =
			currentTextIndex >= 0
				? textResultIndexes[
						(currentTextIndex - 1 + textResultIndexes.length) % textResultIndexes.length
					]
				: textResultIndexes[textResultIndexes.length - 1];
		await focusSidebarSearchResult(previousIndex);
	}

	function replaceCurrentResult() {
		if (!editor || !sidebarSearchResults.length) {
			return;
		}
		const textResultIndexes = getSidebarTextResultIndexes();
		if (textResultIndexes.length === 0) {
			return;
		}
		const activeResultIndex = textResultIndexes.includes(sidebarActiveSearchIndex)
			? sidebarActiveSearchIndex
			: textResultIndexes[0];
		const activeResult = sidebarSearchResults[activeResultIndex];
		if (activeResult.kind !== 'text' || !activeResult.range) {
			return;
		}
		editor.executeEdits('canvas-sidebar-replace', [
			{
				range: activeResult.range,
				text: sidebarReplaceQuery,
				forceMoveMarkers: true
			}
		]);
		markFileDirty(currentFile);
		updateSidebarSearchResults();
		const refreshedTextIndexes = getSidebarTextResultIndexes();
		if (refreshedTextIndexes.length > 0) {
			void focusSidebarSearchResult(refreshedTextIndexes[0]);
		}
	}

	function replaceAllResults() {
		if (!editor || sidebarSearchResults.length === 0) {
			return;
		}
		const textResults = sidebarSearchResults.filter(
			(result): result is SidebarSearchResult =>
				result.kind === 'text' && Boolean(result.range) && typeof result.lineNumber === 'number'
		);
		if (textResults.length === 0) {
			return;
		}
		const sortedEdits = [...textResults]
			.sort((left, right) => {
				const leftLine = left.lineNumber ?? 0;
				const rightLine = right.lineNumber ?? 0;
				if (leftLine !== rightLine) {
					return rightLine - leftLine;
				}
				return (right.startColumn ?? 0) - (left.startColumn ?? 0);
			})
			.map((result) => ({
				range: result.range,
				text: sidebarReplaceQuery,
				forceMoveMarkers: true
			}));
		editor.pushUndoStop();
		editor.executeEdits('canvas-sidebar-replace-all', sortedEdits);
		editor.pushUndoStop();
		markFileDirty(currentFile);
		updateSidebarSearchResults();
	}

	async function saveAllDirtyFiles() {
		const dirtySnapshot = [...dirtyFiles];
		if (dirtySnapshot.length === 0) {
			return;
		}
		try {
			await ensureProjectDirectory();
			for (const relativePath of dirtySnapshot) {
				if (!relativePath) {
					continue;
				}
				if (relativePath === currentFile) {
					await persistCurrentFileToFS({ clearDirty: true });
					continue;
				}
				const yText = ydoc?.getText?.(yTextKeyForFile(relativePath));
				const content = typeof yText?.toString === 'function' ? yText.toString() : '';
				await getActiveFS().promises.writeFile(`/project/${relativePath}`, content);
				clearFileDirty(relativePath);
			}
			scheduleCanvasSnapshotSave();
			writeTerminalLine('\x1b[36m> Saved all pending files.\x1b[0m');
		} catch (error) {
			fileExplorerError = error instanceof Error ? error.message : 'Unable to save changed files';
		}
	}

	function resolveExecutionLanguageForEntry(entry: ProjectFileEntry) {
		const modelLanguageId =
			entry.relativePath === currentFile ? editor?.getModel?.()?.getLanguageId?.() || '' : '';
		const normalizedModelLanguage = (modelLanguageId || '').toLowerCase();
		if (normalizedModelLanguage) {
			return normalizedModelLanguage;
		}
		return getLanguageFromExtension(entry.name);
	}

	function writeExecutionLineToTerminal(output: ExecutionOutputLine) {
		if (!terminal) {
			return;
		}
		const content = output.line === '\x1bc' ? '\x1bc' : `${output.line}\r\n`;
		if (output.stream === 'stderr' && content !== '\x1bc') {
			terminal.write(`\x1b[31m${content}\x1b[0m`);
			return;
		}
		terminal.write(content);
	}

	function resetExecutionState() {
		if (removeExecutionOutputSubscription) {
			removeExecutionOutputSubscription();
			removeExecutionOutputSubscription = null;
		}
		activeExecutionHandle = null;
		isRunInProgress = false;
		runningFilePath = '';
	}

	function stopRunningCode() {
		if (!executionManager || !activeExecutionHandle) {
			return;
		}
		executionManager.stop(activeExecutionHandle.id);
	}

	function resolveTerminalInputFallbackFromYDoc() {
		const yText = ydoc?.getText?.(yTextKeyForFile(DEFAULT_PROJECT_FILE_NAME));
		if (!yText || typeof yText.toString !== 'function') {
			return '';
		}
		return String(yText.toString() || '');
	}

	async function resolveExecutionStdin() {
		if (terminalInputDraft.length > 0) {
			return terminalInputDraft;
		}
		const fromYDoc = resolveTerminalInputFallbackFromYDoc();
		if (fromYDoc.length > 0) {
			return fromYDoc;
		}
		return '';
	}

	async function buildExecutionWorkspaceFiles(
		activeRelativePath: string,
		activeSource: string
	): Promise<ExecutionWorkspaceFile[]> {
		const normalizedActivePath = normalizeProjectName(activeRelativePath);
		if (!normalizedActivePath) {
			return [];
		}
		const workspaceFiles = await Promise.all(
			fileTree
				.filter((entry) => !entry.isDir)
				.map(async (entry) => {
					const normalizedPath = normalizeProjectName(entry.relativePath);
					if (!normalizedPath) {
						return null;
					}
					if (normalizedPath === normalizedActivePath) {
						return {
							name: normalizedPath,
							content: activeSource
						};
					}
					return {
						name: normalizedPath,
						content: await resolveCanvasAIFileContent(normalizedPath)
					};
				})
		);

		const normalizedWorkspaceFiles = workspaceFiles.filter(
			(file): file is ExecutionWorkspaceFile => Boolean(file && file.name)
		);
		if (!normalizedWorkspaceFiles.some((file) => file.name === normalizedActivePath)) {
			normalizedWorkspaceFiles.unshift({
				name: normalizedActivePath,
				content: activeSource
			});
		}
		return normalizedWorkspaceFiles;
	}

	async function applyExecutionArtifacts(artifacts: ExecutionWorkspaceFile[]) {
		if (!artifacts.length) {
			return;
		}
		await ensureProjectDirectory();
		const knownFiles = new Set(
			fileTree
				.filter((entry) => !entry.isDir)
				.map((entry) => normalizeProjectName(entry.relativePath))
				.filter(Boolean)
		);
		const upserts: Array<{ relativePath: string; isDir: boolean; content: string }> = [];
		const createdFiles: string[] = [];
		for (const artifact of artifacts) {
			const normalizedPath = normalizeProjectName(artifact?.name || '');
			if (!normalizedPath) {
				continue;
			}
			const targetPath = toProjectPath(normalizedPath);
			await ensureDirectoryPathExists(splitPath(targetPath).dir);
			const nextContent = String(artifact?.content ?? '');
			await getActiveFS().promises.writeFile(targetPath, nextContent);
			upserts.push({
				relativePath: normalizedPath,
				isDir: false,
				content: nextContent
			});
			if (!knownFiles.has(normalizedPath)) {
				knownFiles.add(normalizedPath);
				createdFiles.push(normalizedPath);
			}
			clearFileDirty(normalizedPath);
		}
		if (upserts.length === 0) {
			return;
		}
		await upsertSharedEntries(upserts);
		await refreshFileTree();
		for (const relativePath of createdFiles) {
			ensureTabOpen(relativePath);
		}
		scheduleCanvasSnapshotSave();
	}

	async function executeCode(
		language: string,
		source: string,
		target: ProjectFileEntry,
		stdin: string,
		workspaceFiles: ExecutionWorkspaceFile[]
	) {
		if (!executionManager) {
			throw new Error('Execution manager is not ready');
		}
		if (isRunInProgress) {
			throw new Error('Another execution is already running');
		}
		isRunInProgress = true;
		runningFilePath = normalizeProjectName(target.relativePath || target.name);
		activeExecutionHandle = await executionManager.run(language, source, 30000, stdin, {
			activeFile: normalizeProjectName(target.relativePath || target.name),
			workspaceFiles,
			onArtifacts: (artifacts) => {
				void applyExecutionArtifacts(artifacts).catch((error) => {
					fileExplorerError =
						error instanceof Error ? error.message : 'Failed to apply Python artifacts';
				});
			}
		});
		removeExecutionOutputSubscription = activeExecutionHandle.subscribe((output) => {
			writeExecutionLineToTerminal(output);
		});
		try {
			await activeExecutionHandle.result;
		} finally {
			resetExecutionState();
		}
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
		activeSidebarView = 'explorer';
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

	function switchTerminalPanelTab(tab: TerminalPanelTab) {
		if (activeTerminalPanelTab === tab) {
			return;
		}
		activeTerminalPanelTab = tab;
		if (tab === 'out' && !terminalPanelCollapsed) {
			scheduleTerminalFit();
		}
	}

	function writeTerminalLine(message: string) {
		terminal?.writeln(message);
	}

	function clearTerminal() {
		terminal?.clear();
	}

	function getTerminalResizeBounds() {
		const editorBodyHeight = canvasEditorBodyElement?.clientHeight ?? 0;
		return {
			min: 120,
			max: Math.max(160, editorBodyHeight - 180)
		};
	}

	function toggleTerminalPanelCollapse() {
		if (terminalPanelCollapsed) {
			terminalPanelCollapsed = false;
			const { min, max } = getTerminalResizeBounds();
			const restoredHeight = Math.max(min, Math.min(max, terminalExpandedHeight));
			terminalHeight = restoredHeight;
			scheduleTerminalFit();
			return;
		}
		terminalExpandedHeight = terminalHeight;
		terminalPanelCollapsed = true;
	}

	function handleTerminalResizeMove(event: PointerEvent) {
		if (terminalPanelCollapsed) {
			return;
		}
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
		if (terminalPanelCollapsed) {
			return;
		}
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

	function countRemoteEditorsForFile(fileName: string) {
		const normalizedFile = normalizeProjectName(fileName);
		if (!awareness || !normalizedFile) {
			return 0;
		}
		let totalEditors = 0;
		for (const [clientId, state] of awareness.getStates().entries()) {
			if (isSelfPresenceState(clientId, state)) {
				continue;
			}
			const remoteCurrentFile = normalizeProjectName(String(state?.currentFile || ''));
			if (remoteCurrentFile === normalizedFile) {
				totalEditors += 1;
			}
		}
		return totalEditors;
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
		clearExplorerLongPressState();
		contextMenuOpen = false;
		contextMenuTarget = null;
	}

	function clearExplorerLongPressState() {
		if (explorerLongPressTimer) {
			clearTimeout(explorerLongPressTimer);
			explorerLongPressTimer = null;
		}
		explorerLongPressTouchIdentifier = -1;
		explorerLongPressTarget = null;
		explorerLongPressStartX = 0;
		explorerLongPressStartY = 0;
		explorerLongPressLastX = 0;
		explorerLongPressLastY = 0;
	}

	function findTouchByIdentifier(touches: TouchList, identifier: number) {
		for (const touch of Array.from(touches)) {
			if (touch.identifier === identifier) {
				return touch;
			}
		}
		return null;
	}

	function consumeSuppressedExplorerClick(event?: Event) {
		if (Date.now() >= suppressExplorerClickUntil) {
			suppressExplorerClickUntil = 0;
			return false;
		}
		suppressExplorerClickUntil = 0;
		if (event) {
			event.preventDefault();
			event.stopPropagation();
		}
		return true;
	}

	async function openContextMenuAtPosition(
		clientX: number,
		clientY: number,
		target: ProjectFileEntry | null
	) {
		contextMenuTarget = target;
		contextMenuOpen = true;
		contextMenuX = clientX;
		contextMenuY = clientY;
		await tick();
		if (!contextMenuElement) {
			return;
		}
		const bounds = contextMenuElement.getBoundingClientRect();
		contextMenuX = Math.min(Math.max(8, contextMenuX), window.innerWidth - bounds.width - 8);
		contextMenuY = Math.min(Math.max(8, contextMenuY), window.innerHeight - bounds.height - 8);
	}

	function onExplorerEntryTouchStart(event: TouchEvent, target: ProjectFileEntry) {
		if (event.touches.length !== 1) {
			clearExplorerLongPressState();
			return;
		}
		const source = event.target instanceof Element ? event.target : null;
		if (source?.closest('.file-entry-more, .file-entry-delete')) {
			clearExplorerLongPressState();
			return;
		}
		const touch = event.touches[0];
		clearExplorerLongPressState();
		explorerLongPressTouchIdentifier = touch.identifier;
		explorerLongPressTarget = target;
		explorerLongPressStartX = touch.clientX;
		explorerLongPressStartY = touch.clientY;
		explorerLongPressLastX = touch.clientX;
		explorerLongPressLastY = touch.clientY;
		suppressNativeExplorerContextMenuUntil =
			Date.now() + EXPLORER_LONG_PRESS_DELAY_MS + EXPLORER_NATIVE_CONTEXT_SUPPRESSION_MS;
		explorerLongPressTimer = setTimeout(() => {
			const contextTarget = explorerLongPressTarget;
			const clientX = explorerLongPressLastX;
			const clientY = explorerLongPressLastY;
			clearExplorerLongPressState();
			suppressExplorerClickUntil = Date.now() + EXPLORER_LONG_PRESS_CLICK_SUPPRESSION_MS;
			suppressNativeExplorerContextMenuUntil =
				Date.now() + EXPLORER_NATIVE_CONTEXT_SUPPRESSION_MS;
			void openContextMenuAtPosition(clientX, clientY, contextTarget);
		}, EXPLORER_LONG_PRESS_DELAY_MS);
	}

	function onExplorerEntryTouchMove(event: TouchEvent) {
		if (explorerLongPressTouchIdentifier < 0) {
			return;
		}
		const touch = findTouchByIdentifier(event.touches, explorerLongPressTouchIdentifier);
		if (!touch) {
			clearExplorerLongPressState();
			return;
		}
		explorerLongPressLastX = touch.clientX;
		explorerLongPressLastY = touch.clientY;
		const dx = touch.clientX - explorerLongPressStartX;
		const dy = touch.clientY - explorerLongPressStartY;
		const movedDistance = Math.hypot(dx, dy);
		if (movedDistance > EXPLORER_LONG_PRESS_MOVE_TOLERANCE_PX) {
			clearExplorerLongPressState();
		}
	}

	function onExplorerEntryTouchEnd(event: TouchEvent) {
		if (Date.now() < suppressExplorerClickUntil) {
			event.preventDefault();
			event.stopPropagation();
		}
		clearExplorerLongPressState();
	}

	function onExplorerEntryTouchCancel() {
		clearExplorerLongPressState();
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

	function dragEventHasPlainText(event: DragEvent) {
		const types = Array.from(event.dataTransfer?.types ?? []);
		return types.includes('text/plain');
	}

	function getCurrentSelectionText() {
		const model = editor?.getModel?.();
		const selection = editor?.getSelection?.();
		if (!model || !selection || selection.isEmpty?.()) {
			return '';
		}
		try {
			return String(model.getValueInRange(selection) || '');
		} catch {
			return '';
		}
	}

	function hideSelectionSnippetAction() {
		showSelectionSnippetAction = false;
	}

	function updateSelectionSnippetAction() {
		const selectionText = getCurrentSelectionText();
		selectedSnippetText = selectionText;
		canSendSnippetFromSelection = selectionText.trim().length > 0;
		if (!canSendSnippetFromSelection || showSnippetComposer || !editor) {
			hideSelectionSnippetAction();
			return;
		}
		const selection = editor.getSelection?.();
		const selectionStart = selection?.getStartPosition?.();
		const selectionEnd = selection?.getEndPosition?.();
		if (!selectionStart || !selectionEnd) {
			hideSelectionSnippetAction();
			return;
		}
		const startVisiblePosition = editor.getScrolledVisiblePosition(selectionStart);
		const endVisiblePosition = editor.getScrolledVisiblePosition(selectionEnd);
		const editorNode = editor.getDomNode?.();
		if (!startVisiblePosition || !endVisiblePosition || !editorNode) {
			hideSelectionSnippetAction();
			return;
		}
		const buttonWidth = 34;
		const buttonHeight = 30;
		const edgePadding = 8;
		const selectionGap = 8;
		const minLeft = edgePadding;
		const maxLeft = Math.max(edgePadding, editorNode.clientWidth - buttonWidth - edgePadding);
		const startLeft = startVisiblePosition.left;
		const endLeft = endVisiblePosition.left;
		const selectionCenterX = (Math.min(startLeft, endLeft) + Math.max(startLeft, endLeft)) / 2;
		selectionSnippetActionLeft = Math.min(
			maxLeft,
			Math.max(minLeft, selectionCenterX - buttonWidth / 2)
		);
		const selectionTop = Math.min(startVisiblePosition.top, endVisiblePosition.top);
		const selectionBottom = Math.max(
			startVisiblePosition.top + startVisiblePosition.height,
			endVisiblePosition.top + endVisiblePosition.height
		);
		const minTop = edgePadding;
		const maxTop = Math.max(edgePadding, editorNode.clientHeight - buttonHeight - edgePadding);
		const aboveTop = selectionTop - buttonHeight - selectionGap;
		const belowTop = selectionBottom + selectionGap;
		const canPlaceAbove = aboveTop >= minTop;
		const canPlaceBelow = belowTop <= maxTop;
		const availableTop = selectionTop - edgePadding;
		const availableBottom = editorNode.clientHeight - selectionBottom - edgePadding;
		const preferBelow =
			isCompactCanvasLayout ||
			(typeof window !== 'undefined' && window.matchMedia('(pointer: coarse)').matches);
		let targetTop = aboveTop;
		if (preferBelow) {
			if (canPlaceBelow) {
				targetTop = belowTop;
			} else if (canPlaceAbove) {
				targetTop = aboveTop;
			} else {
				targetTop = availableBottom >= availableTop ? belowTop : aboveTop;
			}
		} else if (canPlaceAbove) {
			targetTop = aboveTop;
		} else if (canPlaceBelow) {
			targetTop = belowTop;
		} else {
			targetTop = availableTop >= availableBottom ? aboveTop : belowTop;
		}
		selectionSnippetActionTop = Math.min(maxTop, Math.max(minTop, targetTop));
		showSelectionSnippetAction = true;
	}

	function openSnippetComposerForSelection() {
		const text = selectedSnippetText || getCurrentSelectionText();
		if (!text.trim()) {
			return;
		}
		openSnippetComposerFromDrop(text);
	}

	function handleEditorCodeDragStart(event: DragEvent) {
		const selectedText = getCurrentSelectionText();
		if (!selectedText.trim()) {
			isDraggingCode = false;
			hideSelectionSnippetAction();
			return;
		}
		if (event.dataTransfer) {
			event.dataTransfer.setData('text/plain', selectedText);
			event.dataTransfer.effectAllowed = 'copy';
			event.dataTransfer.dropEffect = 'copy';
		}
		isDraggingCode = true;
		hideSelectionSnippetAction();
	}

	function handleEditorCodeDragEnd() {
		isDraggingCode = false;
	}

	function handleEditorCodeDragEnter(event: DragEvent) {
		if (!dragEventHasPlainText(event)) {
			return;
		}
		event.preventDefault();
		event.stopPropagation();
		if (event.dataTransfer) {
			event.dataTransfer.dropEffect = 'copy';
		}
		isDraggingCode = true;
	}

	function handleEditorCodeDragOver(event: DragEvent) {
		if (!dragEventHasPlainText(event)) {
			return;
		}
		event.preventDefault();
		event.stopPropagation();
		if (event.dataTransfer) {
			event.dataTransfer.dropEffect = 'copy';
		}
		isDraggingCode = true;
	}

	function handleEditorCodeDragLeave(event: DragEvent) {
		const currentTarget = event.currentTarget as HTMLElement | null;
		const relatedTarget = event.relatedTarget as Node | null;
		if (relatedTarget && currentTarget?.contains(relatedTarget)) {
			return;
		}
		isDraggingCode = false;
	}

	function closeSnippetComposer() {
		showSnippetComposer = false;
		snippetDraft = '';
		snippetMessage = '';
		void tick().then(() => {
			updateSelectionSnippetAction();
		});
	}

	function openSnippetComposerFromDrop(text: string) {
		snippetDraft = text;
		snippetMessage = '';
		showSnippetComposer = true;
		hideSelectionSnippetAction();
		void tick().then(() => {
			snippetMessageInputElement?.focus();
		});
	}

	function sendSnippetMessage() {
		if (!snippetDraft.trim()) {
			closeSnippetComposer();
			return;
		}
		dispatch('sendSnippet', {
			snippet: snippetDraft,
			message: snippetMessage,
			fileName: getTabLabel(currentFile)
		});
		closeSnippetComposer();
	}

	function resolveCanvasAIDeviceID() {
		if (typeof window === 'undefined') {
			return `canvas-device-${createPresenceSessionId()}`;
		}
		const existing = (window.localStorage.getItem(CANVAS_AI_DEVICE_ID_STORAGE_KEY) || '').trim();
		if (existing) {
			return existing;
		}
		const generated =
			typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
				? crypto.randomUUID()
				: `canvas-device-${createPresenceSessionId()}`;
		window.localStorage.setItem(CANVAS_AI_DEVICE_ID_STORAGE_KEY, generated);
		return generated;
	}

	function stripCodeFences(rawResponse: string) {
		let normalized = String(rawResponse || '').replace(/\r\n/g, '\n').trim();
		if (!normalized) {
			return '';
		}
		if (normalized.startsWith('```')) {
			normalized = normalized.replace(/^```[a-zA-Z0-9_+-]*\n?/, '');
			normalized = normalized.replace(/\n?```$/, '');
		}
		return normalized.trim();
	}

	function toCanvasAIString(value: unknown) {
		return typeof value === 'string' ? value.trim() : '';
	}

	function toCanvasAICodeString(value: unknown) {
		return typeof value === 'string' ? value.replace(/\r\n/g, '\n') : '';
	}

	function toCanvasAIRecord(value: unknown): Record<string, unknown> | null {
		if (!value || typeof value !== 'object' || Array.isArray(value)) {
			return null;
		}
		return value as Record<string, unknown>;
	}

	function toCanvasAIArray(value: unknown) {
		return Array.isArray(value) ? value : [];
	}

	function normalizeCanvasAIFilePath(rawPath: string) {
		const normalizedInput = String(rawPath || '').trim().replace(/\\/g, '/');
		if (!normalizedInput) {
			return '';
		}
		let relative = normalizedInput.replace(/^\/+/, '').replace(/^\.\/+/, '');
		if (relative.toLowerCase().startsWith('project/')) {
			relative = relative.slice('project/'.length);
		}
		const segments = relative.split('/').filter(Boolean);
		const resolvedSegments: string[] = [];
		for (const segment of segments) {
			if (segment === '.') {
				continue;
			}
			if (segment === '..') {
				resolvedSegments.pop();
				continue;
			}
			resolvedSegments.push(segment);
		}
		return normalizeProjectName(resolvedSegments.join('/'));
	}

	function parseCanvasAIChangeAction(value: unknown, fallback: CanvasAIChangeAction = 'replace') {
		const normalized = toCanvasAIString(value).toLowerCase();
		if (normalized === 'replace' || normalized === 'create' || normalized === 'delete') {
			return normalized;
		}
		return fallback;
	}

	function approximateCanvasAITokenCount(value: string) {
		if (!value) {
			return 0;
		}
		return Math.max(1, Math.ceil(value.length / CANVAS_AI_CHARS_PER_TOKEN));
	}

	function createCanvasAIMessageID() {
		if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
			return crypto.randomUUID();
		}
		return `canvas-ai-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
	}

	function createCanvasAIChangeID(filePath: string) {
		const slug = normalizeProjectName(filePath).replace(/[^a-zA-Z0-9/_-]/g, '_') || 'file';
		return `${createCanvasAIMessageID()}-${slug}`;
	}

	function appendCanvasAIMessage(
		role: CanvasAIChatRole,
		text: string,
		changes?: CanvasAIProposedChange[]
	) {
		const normalizedText = String(text || '').trim();
		const normalizedChanges =
			Array.isArray(changes) && changes.length > 0
				? changes.map((change) => ({
						...change
					}))
				: undefined;
		const nextMessage: CanvasAIChatMessage = {
			id: createCanvasAIMessageID(),
			role,
			text: normalizedText,
			changes: normalizedChanges,
			timestamp: Date.now()
		};
		canvasAIChatMessages = [...canvasAIChatMessages, nextMessage].slice(-CANVAS_AI_CHAT_HISTORY_LIMIT);
		scrollCanvasAIThreadToBottom();
		return nextMessage.id;
	}

	function updateCanvasAIMessageById(
		messageId: string,
		updater: (message: CanvasAIChatMessage) => CanvasAIChatMessage
	) {
		let updated = false;
		canvasAIChatMessages = canvasAIChatMessages.map((message) => {
			if (message.id !== messageId) {
				return message;
			}
			updated = true;
			return updater(message);
		});
		return updated;
	}

	function getCanvasAIPendingChangeCount(message: CanvasAIChatMessage | null) {
		if (!message?.changes || message.changes.length === 0) {
			return 0;
		}
		return message.changes.filter((change) => change.applyState === 'pending').length;
	}

	function resolveCanvasAILastSuggestedMessage() {
		if (!canvasAILastSuggestedMessageId) {
			return null;
		}
		return canvasAIChatMessages.find((message) => message.id === canvasAILastSuggestedMessageId) ?? null;
	}

	function getCanvasAILastPendingChangeCount() {
		return getCanvasAIPendingChangeCount(resolveCanvasAILastSuggestedMessage());
	}

	function scrollCanvasAIThreadToBottom() {
		void tick().then(() => {
			const targetThread =
				(showCanvasAIPrompt ? canvasAIThreadElement : null) ||
				(activeSidebarView === 'canvas_ai' ? canvasAISidebarThreadElement : null) ||
				canvasAIThreadElement ||
				canvasAISidebarThreadElement;
			if (!targetThread) {
				return;
			}
			targetThread.scrollTop = targetThread.scrollHeight;
		});
	}

	function truncateCanvasAIText(value: string, maxLength: number) {
		if (value.length <= maxLength) {
			return value;
		}
		return `${value.slice(0, Math.max(0, maxLength - 3)).trimEnd()}...`;
	}

	function buildCanvasAIConversationContext() {
		if (canvasAIChatMessages.length === 0) {
			return 'No prior conversation context.';
		}
		const recentMessages = canvasAIChatMessages.slice(-CANVAS_AI_CONTEXT_MESSAGES);
		return recentMessages
			.map((message, index) => {
				const roleLabel = message.role === 'assistant' ? 'Assistant' : 'User';
				const text = truncateCanvasAIText(message.text || '(no message)', CANVAS_AI_TEXT_PREVIEW_LIMIT);
				if (!message.changes || message.changes.length === 0) {
					return `${index + 1}. ${roleLabel}: ${text}`;
				}
				const previewItems = message.changes.slice(0, 4).map((change) => {
					const summary = truncateCanvasAIText(change.summary || 'Updated file', 70);
					const location = truncateCanvasAIText(change.locationHint || 'file-level', 48);
					return `- ${change.action.toUpperCase()} ${change.filePath} @ ${location}: ${summary}`;
				});
				const overflowCount = Math.max(0, message.changes.length - previewItems.length);
				const overflowLabel = overflowCount > 0 ? `\n- ...and ${overflowCount} more file change(s)` : '';
				return `${index + 1}. ${roleLabel}: ${text}
Proposed changes:
${previewItems.join('\n')}${overflowLabel}`;
			})
			.join('\n\n');
	}

	async function resolveCanvasAIFileContent(relativePath: string) {
		const normalizedPath = normalizeCanvasAIFilePath(relativePath);
		if (!normalizedPath) {
			return '';
		}
		if (normalizedPath === normalizeProjectName(currentFile) && editor?.getModel?.()) {
			return String(editor.getModel().getValue() || '');
		}
		const yText = ydoc?.getText?.(yTextKeyForFile(normalizedPath));
		if (yText && (yFileTree?.has?.(normalizedPath) || fileTree.some((entry) => entry.relativePath === normalizedPath))) {
			return String(yText.toString() || '');
		}
		try {
			return String(await readProjectFileContent(normalizedPath));
		} catch {
			return '';
		}
	}

	async function resolveCanvasAIExistingContent(relativePath: string) {
		const normalizedPath = normalizeCanvasAIFilePath(relativePath);
		if (!normalizedPath) {
			return { exists: false, content: '' };
		}
		const targetPath = toProjectPath(normalizedPath);
		const exists = await pathExists(targetPath);
		if (!exists) {
			return { exists: false, content: '' };
		}
		const content = await resolveCanvasAIFileContent(normalizedPath);
		return { exists: true, content };
	}

	async function buildCanvasAIWorkspaceContext(targetFilePath: string) {
		const allFilePaths = fileTree
			.filter((entry) => !entry.isDir)
			.map((entry) => normalizeCanvasAIFilePath(entry.relativePath || entry.name))
			.filter(Boolean);
		const prioritized = Array.from(
			new Set([
				normalizeCanvasAIFilePath(targetFilePath),
				normalizeCanvasAIFilePath(currentFile),
				...openTabs.map((path) => normalizeCanvasAIFilePath(path)),
				...dirtyFiles.map((path) => normalizeCanvasAIFilePath(path)),
				...allFilePaths
			].filter(Boolean))
		);
		const contextBlocks: string[] = [];
		let remainingChars = CANVAS_AI_CONTEXT_MAX_CHARS;
		let truncatedFiles = 0;
		let includedFiles = 0;
		for (const filePath of prioritized.slice(0, CANVAS_AI_MAX_CONTEXT_FILES)) {
			if (remainingChars <= 280) {
				break;
			}
			const language = getLanguageFromExtension(filePath) || 'plaintext';
			const source = await resolveCanvasAIFileContent(filePath);
			const maxContentChars = Math.max(0, Math.min(CANVAS_AI_MAX_CHARS_PER_FILE, remainingChars - 220));
			if (maxContentChars <= 0) {
				break;
			}
			let nextContent = source;
			let wasTruncated = false;
			if (nextContent.length > maxContentChars) {
				nextContent = nextContent.slice(0, maxContentChars);
				wasTruncated = true;
			}
			if (wasTruncated) {
				truncatedFiles += 1;
			}
			const fileBlock = [
				`FILE: ${filePath}`,
				`LANGUAGE: ${language}`,
				wasTruncated ? 'NOTE: content truncated to fit model context window.' : '',
				'<<<FILE_CONTENT',
				nextContent,
				'FILE_CONTENT'
			]
				.filter(Boolean)
				.join('\n');
			if (fileBlock.length > remainingChars && includedFiles > 0) {
				break;
			}
			contextBlocks.push(fileBlock);
			remainingChars -= fileBlock.length + 2;
			includedFiles += 1;
		}
		return {
			contextText:
				contextBlocks.length > 0 ? contextBlocks.join('\n\n') : 'No workspace files are currently available.',
			includedFiles,
			totalFiles: allFilePaths.length,
			truncatedFiles,
			omittedFiles: Math.max(0, allFilePaths.length - includedFiles)
		};
	}

	function trimCanvasAIResponseCodeFence(rawText: string) {
		let normalized = String(rawText || '').replace(/\r\n/g, '\n').trim();
		if (!normalized) {
			return '';
		}
		if (!normalized.startsWith('```')) {
			return normalized;
		}
		normalized = normalized.replace(/^```[a-zA-Z0-9_+-]*\n?/, '');
		normalized = normalized.replace(/\n?```$/, '');
		return normalized.trim();
	}

	function extractCanvasAIJSONCandidates(rawText: string) {
		const normalized = trimCanvasAIResponseCodeFence(rawText);
		if (!normalized) {
			return [] as string[];
		}
		if (normalized.startsWith('{') && normalized.endsWith('}')) {
			return [normalized];
		}

		const candidates: string[] = [];
		let depth = 0;
		let startIndex = -1;
		let inString = false;
		let escaped = false;

		for (let index = 0; index < normalized.length; index += 1) {
			const char = normalized[index];
			if (inString) {
				if (escaped) {
					escaped = false;
					continue;
				}
				if (char === '\\') {
					escaped = true;
					continue;
				}
				if (char === '"') {
					inString = false;
				}
				continue;
			}
			if (char === '"') {
				inString = true;
				continue;
			}
			if (char === '{') {
				if (depth === 0) {
					startIndex = index;
				}
				depth += 1;
				continue;
			}
			if (char === '}' && depth > 0) {
				depth -= 1;
				if (depth === 0 && startIndex >= 0) {
					candidates.push(normalized.slice(startIndex, index + 1).trim());
					startIndex = -1;
				}
			}
		}

		return candidates.length > 0 ? candidates : [normalized];
	}

	function parseCanvasAIChangeDraft(
		source: Record<string, unknown>,
		fallbackFilePath: string
	): CanvasAIChangeDraft | null {
		const filePath = normalizeCanvasAIFilePath(
			toCanvasAIString(
				source.file_path ??
					source.filePath ??
					source.path ??
					source.target_file ??
					source.targetFile ??
					source.file ??
					fallbackFilePath
			)
		);
		if (!filePath) {
			return null;
		}
		const action = parseCanvasAIChangeAction(source.action, 'replace');
		const summary =
			toCanvasAIString(source.summary ?? source.reason ?? source.description) || 'Updated file content';
		const locationHint =
			toCanvasAIString(source.location_hint ?? source.locationHint ?? source.location ?? source.scope) ||
			'file-level update';
		const updatedCode = stripCodeFences(
			toCanvasAICodeString(
				source.updated_code ??
					source.updatedCode ??
					source.content ??
					source.code ??
					source.new_content ??
					source.newContent ??
					source.replacement
			)
		);
		if (action !== 'delete' && !updatedCode.trim()) {
			return null;
		}
		return {
			filePath,
			action,
			summary,
			locationHint,
			updatedCode: action === 'delete' ? '' : updatedCode
		};
	}

	function parseCanvasAIResponseObject(
		payload: Record<string, unknown>,
		fallbackFilePath: string
	): CanvasAIParsedResponse | null {
		const nested =
			toCanvasAIRecord(payload.timeline) ||
			toCanvasAIRecord(payload.result) ||
			toCanvasAIRecord(payload.payload) ||
			toCanvasAIRecord(payload.data) ||
			payload;
		const assistantReply = toCanvasAIString(
			nested.assistant_reply ??
				nested.assistantReply ??
				nested.reply ??
				nested.explanation ??
				nested.message
		);
		const rawChanges = toCanvasAIArray(
			nested.changes ??
				nested.edits ??
				nested.patches ??
				nested.file_changes ??
				nested.fileChanges
		);
		const changes: CanvasAIChangeDraft[] = [];
		for (const candidate of rawChanges) {
			const changeRecord = toCanvasAIRecord(candidate);
			if (!changeRecord) {
				continue;
			}
			const parsedChange = parseCanvasAIChangeDraft(changeRecord, fallbackFilePath);
			if (!parsedChange) {
				continue;
			}
			changes.push(parsedChange);
		}
		const code = stripCodeFences(
			toCanvasAICodeString(
				nested.code ??
					nested.file_content ??
					nested.fileContent ??
					nested.updated_code ??
					nested.updatedCode ??
					nested.content
			)
		);
		if (changes.length === 0 && code.trim()) {
			const normalizedFallbackPath = normalizeCanvasAIFilePath(fallbackFilePath);
			if (!normalizedFallbackPath) {
				return null;
			}
			changes.push({
				filePath: normalizedFallbackPath,
				action: 'replace',
				summary: 'Updated file content',
				locationHint: 'file-level update',
				updatedCode: code
			});
		}
		if (changes.length === 0) {
			return null;
		}
		return {
			assistantReply: assistantReply || 'I prepared a set of file updates for your workspace.',
			changes
		};
	}

	function parseCanvasAIResponseFromText(
		rawText: string,
		fallbackFilePath: string
	): CanvasAIParsedResponse {
		const normalized = String(rawText || '').trim();
		if (!normalized) {
			throw new Error('AI returned an empty response.');
		}

		const jsonCandidates = extractCanvasAIJSONCandidates(normalized);
		for (const candidate of jsonCandidates) {
			try {
				const parsed = JSON.parse(candidate);
				const parsedRecord = toCanvasAIRecord(parsed);
				if (!parsedRecord) {
					continue;
				}
				const structured = parseCanvasAIResponseObject(parsedRecord, fallbackFilePath);
				if (structured && structured.changes.length > 0) {
					return structured;
				}
			} catch {
				// Ignore malformed candidate; continue to other fallbacks.
			}
		}

		const codeFenceMatch = normalized.match(/```[a-zA-Z0-9_+-]*\n?[\s\S]*?```/);
		if (codeFenceMatch) {
			const fencedCode = stripCodeFences(codeFenceMatch[0] || '');
			if (fencedCode) {
				const conversationalText = normalized.replace(codeFenceMatch[0], '').trim();
				const normalizedFallbackPath = normalizeCanvasAIFilePath(fallbackFilePath);
				if (!normalizedFallbackPath) {
					throw new Error('AI response did not include file paths for changes.');
				}
				return {
					assistantReply: conversationalText || 'I prepared an updated version of your active file.',
					changes: [
						{
							filePath: normalizedFallbackPath,
							action: 'replace',
							summary: 'Updated file content',
							locationHint: 'file-level update',
							updatedCode: fencedCode
						}
					]
				};
			}
		}

		const fallbackCode = stripCodeFences(normalized);
		if (!fallbackCode) {
			throw new Error('AI response could not be parsed into code.');
		}
		const normalizedFallbackPath = normalizeCanvasAIFilePath(fallbackFilePath);
		if (!normalizedFallbackPath) {
			throw new Error('AI response did not include file paths for changes.');
		}
		return {
			assistantReply: 'I prepared an updated version of your active file.',
			changes: [
				{
					filePath: normalizedFallbackPath,
					action: 'replace',
					summary: 'Updated file content',
					locationHint: 'file-level update',
					updatedCode: fallbackCode
				}
			]
		};
	}

	function openCanvasAIPromptPanel() {
		if (!aiEnabled) {
			fileExplorerError = 'AI assistant is disabled for this room.';
			return;
		}
		if (!currentFileEntry()) {
			fileExplorerError = 'Open a file before using AI.';
			return;
		}
		showCanvasAIPrompt = true;
		canvasAIError = '';
		void tick().then(() => {
			resizeCanvasAIPromptInput();
			canvasAIPromptElement?.focus();
			scrollCanvasAIThreadToBottom();
		});
	}

	function closeCanvasAIPromptPanel() {
		if (isCanvasAIGenerating) {
			canvasAIAbortController?.abort();
		}
		showCanvasAIPrompt = false;
		canvasAIError = '';
		isCanvasAIGenerating = false;
		canvasAIPrompt = '';
		canvasAIAbortController = null;
	}

	async function buildCanvasAICodePrompt(
		instruction: string,
		targetFilePath: string,
		language: string
	) {
		const normalizedInstruction = truncateCanvasAIText(
			String(instruction || '').trim(),
			CANVAS_AI_MAX_INSTRUCTION_CHARS
		);
		const workspaceContext = await buildCanvasAIWorkspaceContext(targetFilePath);
		const baseContextSummary = `Included ${workspaceContext.includedFiles}/${workspaceContext.totalFiles} files from workspace context.` +
			` Omitted: ${workspaceContext.omittedFiles}. Truncated: ${workspaceContext.truncatedFiles}.`;
		let contextSummary = baseContextSummary;
		let workspaceContextText = workspaceContext.contextText;
		let conversationContext = truncateCanvasAIText(
			buildCanvasAIConversationContext(),
			CANVAS_AI_MAX_CONVERSATION_CONTEXT_CHARS
		);
		const localNow = new Date();
		const buildPrompt = () => `${CANVAS_AI_SYSTEM_PROMPT}

Target file path: ${targetFilePath}
Target language: ${language}
Client local time (ISO): ${localNow.toISOString()}
Context budget: ~${CANVAS_AI_MAX_INPUT_TOKENS} tokens maximum input.
${contextSummary}

Workspace files context:
${workspaceContextText}

Recent conversation context:
${conversationContext}

Latest user instruction:
${normalizedInstruction}

Return only JSON with keys "assistant_reply" and "changes".`;

		let prompt = buildPrompt();
		if (prompt.length > CANVAS_AI_MAX_PROMPT_CHARS) {
			const overflow = prompt.length - CANVAS_AI_MAX_PROMPT_CHARS;
			const nextMaxWorkspaceChars = Math.max(
				CANVAS_AI_MIN_SECTION_CHARS,
				workspaceContextText.length - overflow - 48
			);
			if (nextMaxWorkspaceChars < workspaceContextText.length) {
				workspaceContextText = truncateCanvasAIText(workspaceContextText, nextMaxWorkspaceChars);
				contextSummary = `${baseContextSummary} Workspace context trimmed to respect model budget.`;
				prompt = buildPrompt();
			}
		}
		if (prompt.length > CANVAS_AI_MAX_PROMPT_CHARS) {
			const overflow = prompt.length - CANVAS_AI_MAX_PROMPT_CHARS;
			const nextMaxConversationChars = Math.max(
				CANVAS_AI_MIN_SECTION_CHARS,
				conversationContext.length - overflow - 48
			);
			if (nextMaxConversationChars < conversationContext.length) {
				conversationContext = truncateCanvasAIText(conversationContext, nextMaxConversationChars);
				prompt = buildPrompt();
			}
		}
		if (prompt.length > CANVAS_AI_MAX_PROMPT_CHARS) {
			prompt = prompt.slice(0, CANVAS_AI_MAX_PROMPT_CHARS).trimEnd();
		}

		return {
			prompt,
			estimatedTokens: approximateCanvasAITokenCount(prompt)
		};
	}

	async function requestCanvasAIResponse(
		instruction: string,
		target: ProjectFileEntry,
		language: string,
		signal: AbortSignal
	): Promise<CanvasAIParsedResponse> {
		const targetFilePath = normalizeCanvasAIFilePath(target.relativePath || target.name);
		const { prompt, estimatedTokens } = await buildCanvasAICodePrompt(
			instruction,
			targetFilePath,
			language
		);
		if (estimatedTokens > CANVAS_AI_MAX_INPUT_TOKENS) {
			canvasClientLog('canvas-ai-context-over-budget', {
				estimatedTokens,
				maxTokens: CANVAS_AI_MAX_INPUT_TOKENS
			});
		}
		const headers: Record<string, string> = {
			'Content-Type': 'application/json',
			'X-User-Id': currentUser?.id || '',
			'X-Username': currentUser?.name || ''
		};
		const body = JSON.stringify({
			prompt,
			deviceId: resolveCanvasAIDeviceID(),
			roomId
		});

		let response = await fetch(`${API_BASE}/api/ai/chat`, {
			method: 'POST',
			headers,
			body,
			signal
		});
		if (response.status === 404) {
			response = await fetch(`${API_BASE}/api/ai/private-chat`, {
				method: 'POST',
				headers,
				body,
				signal
			});
		}

		const payload = (await response.json().catch(() => ({}))) as Record<string, unknown>;
		if (!response.ok) {
			const details = typeof payload.error === 'string' ? payload.error.trim() : '';
			throw new Error(details || `AI request failed (${response.status})`);
		}

		const structuredFromPayload = parseCanvasAIResponseObject(payload, targetFilePath);
		if (structuredFromPayload?.changes.length) {
			return structuredFromPayload;
		}

		const aiText =
			typeof payload.response === 'string'
				? payload.response
				: typeof payload.message === 'string'
					? payload.message
					: '';
		return parseCanvasAIResponseFromText(aiText, targetFilePath);
	}

	function splitCanvasAIContentIntoLines(content: string) {
		return String(content ?? '').replace(/\r\n/g, '\n').split('\n');
	}

	function buildCanvasAIUnifiedDiff(filePath: string, previousCode: string, updatedCode: string) {
		const oldLines = splitCanvasAIContentIntoLines(previousCode);
		const newLines = splitCanvasAIContentIntoLines(updatedCode);
		if (previousCode === updatedCode) {
			return `--- a/${filePath}\n+++ b/${filePath}\n@@ no textual change @@`;
		}
		let prefix = 0;
		while (
			prefix < oldLines.length &&
			prefix < newLines.length &&
			oldLines[prefix] === newLines[prefix]
		) {
			prefix += 1;
		}
		let oldSuffix = oldLines.length - 1;
		let newSuffix = newLines.length - 1;
		while (oldSuffix >= prefix && newSuffix >= prefix && oldLines[oldSuffix] === newLines[newSuffix]) {
			oldSuffix -= 1;
			newSuffix -= 1;
		}
		const oldStart = Math.max(0, prefix - CANVAS_AI_DIFF_CONTEXT_LINES);
		const newStart = Math.max(0, prefix - CANVAS_AI_DIFF_CONTEXT_LINES);
		const oldEnd = Math.min(oldLines.length, oldSuffix + 1 + CANVAS_AI_DIFF_CONTEXT_LINES);
		const newEnd = Math.min(newLines.length, newSuffix + 1 + CANVAS_AI_DIFF_CONTEXT_LINES);
		const oldCount = Math.max(0, oldEnd - oldStart);
		const newCount = Math.max(0, newEnd - newStart);
		const diffLines = [
			`--- a/${filePath}`,
			`+++ b/${filePath}`,
			`@@ -${oldStart + 1},${oldCount} +${newStart + 1},${newCount} @@`
		];
		for (let index = oldStart; index < prefix; index += 1) {
			diffLines.push(` ${oldLines[index] ?? ''}`);
		}
		for (let index = prefix; index <= oldSuffix; index += 1) {
			diffLines.push(`-${oldLines[index] ?? ''}`);
		}
		for (let index = prefix; index <= newSuffix; index += 1) {
			diffLines.push(`+${newLines[index] ?? ''}`);
		}
		for (let index = oldSuffix + 1; index < oldEnd; index += 1) {
			diffLines.push(` ${oldLines[index] ?? ''}`);
		}
		if (diffLines.length > CANVAS_AI_DIFF_MAX_LINES) {
			const headLines = Math.max(0, CANVAS_AI_DIFF_MAX_LINES - 2);
			return `${diffLines.slice(0, headLines).join('\n')}\n... diff truncated ...\n`;
		}
		return diffLines.join('\n');
	}

	async function buildCanvasAIProposedChanges(changes: CanvasAIChangeDraft[]) {
		const latestByPath = new Map<string, CanvasAIChangeDraft>();
		for (const change of changes) {
			const normalizedPath = normalizeCanvasAIFilePath(change.filePath);
			if (!normalizedPath) {
				continue;
			}
			if (latestByPath.has(normalizedPath)) {
				latestByPath.delete(normalizedPath);
			}
			latestByPath.set(normalizedPath, {
				...change,
				filePath: normalizedPath
			});
		}
		const proposedChanges: CanvasAIProposedChange[] = [];
		for (const draft of latestByPath.values()) {
			const existing = await resolveCanvasAIExistingContent(draft.filePath);
			let action = draft.action;
			if (action === 'create' && existing.exists) {
				action = 'replace';
			}
			if (action === 'replace' && !existing.exists) {
				action = 'create';
			}
			const updatedCode = action === 'delete' ? '' : draft.updatedCode;
			if (action !== 'delete' && !updatedCode.trim()) {
				continue;
			}
			const diffText = buildCanvasAIUnifiedDiff(
				draft.filePath,
				existing.content,
				action === 'delete' ? '' : updatedCode
			);
			proposedChanges.push({
				id: createCanvasAIChangeID(draft.filePath),
				filePath: draft.filePath,
				action,
				summary: draft.summary,
				locationHint: draft.locationHint,
				updatedCode,
				previousCode: existing.content,
				diffText,
				applyState: 'pending',
				applyError: ''
			});
		}
		return proposedChanges;
	}

	async function applyCanvasAIChangeToWorkspace(change: CanvasAIProposedChange) {
		const normalizedPath = normalizeCanvasAIFilePath(change.filePath);
		if (!normalizedPath) {
			throw new Error('Change is missing a valid file path.');
		}
		await ensureProjectDirectory();
		const filePath = toProjectPath(normalizedPath);
		if (change.action === 'delete') {
			if (await pathExists(filePath)) {
				const stat = await getActiveFS().promises.stat(filePath);
				const isDirectory = typeof stat.isDirectory === 'function' ? stat.isDirectory() : false;
				if (isDirectory) {
					throw new Error(`Refusing to delete directory via AI change: ${normalizedPath}`);
				}
				await getActiveFS().promises.unlink(filePath);
			}
			removeSharedEntries([normalizedPath], { clearYText: true });
			openTabs = openTabs.filter((tab) => tab !== normalizedPath);
			return;
		}
		const parentDir = splitPath(filePath).dir;
		await ensureDirectoryPathExists(parentDir);
		await getActiveFS().promises.writeFile(filePath, change.updatedCode);
		await upsertSharedEntries([
			{
				relativePath: normalizedPath,
				isDir: false,
				content: change.updatedCode
			}
		]);
	}

	async function finalizeCanvasAIWorkspaceChange() {
		await refreshFileTree();
		await syncOpenTabsWithFileTree();
		scheduleCanvasSnapshotSave();
	}

	async function applyCanvasAIChange(messageId: string, changeId: string) {
		const message = canvasAIChatMessages.find((entry) => entry.id === messageId);
		const change = message?.changes?.find((entry) => entry.id === changeId);
		if (!message || !change) {
			canvasAIError = 'Unable to locate this AI change.';
			return;
		}
		if (change.applyState === 'applied') {
			return;
		}
		if (
			showReadOnlyWarning &&
			normalizeCanvasAIFilePath(change.filePath) === normalizeCanvasAIFilePath(currentFile)
		) {
			canvasAIError = 'Current file is read-only. Wait for editor slots to free up before applying.';
			return;
		}
		canvasAIError = '';
		try {
			await applyCanvasAIChangeToWorkspace(change);
			await finalizeCanvasAIWorkspaceChange();
			updateCanvasAIMessageById(messageId, (entry) => ({
				...entry,
				changes: (entry.changes ?? []).map((candidate) =>
					candidate.id === changeId
						? { ...candidate, applyState: 'applied', applyError: '' }
						: candidate
				)
			}));
			writeTerminalLine(`\x1b[35m> Applied AI change for ${change.filePath}.\x1b[0m`);
		} catch (error) {
			const messageText = error instanceof Error ? error.message : 'Failed to apply AI change.';
			updateCanvasAIMessageById(messageId, (entry) => ({
				...entry,
				changes: (entry.changes ?? []).map((candidate) =>
					candidate.id === changeId
						? { ...candidate, applyState: 'failed', applyError: messageText }
						: candidate
				)
			}));
			canvasAIError = messageText;
		}
	}

	async function applyAllCanvasAIChanges(messageId: string) {
		const message = canvasAIChatMessages.find((entry) => entry.id === messageId);
		const pendingChanges = (message?.changes ?? []).filter((entry) => entry.applyState === 'pending');
		if (!message || pendingChanges.length === 0) {
			canvasAIError = 'No pending AI changes to apply.';
			return;
		}
		canvasAIError = '';
		const succeededIds = new Set<string>();
		const failedById = new Map<string, string>();
		for (const change of pendingChanges) {
			if (
				showReadOnlyWarning &&
				normalizeCanvasAIFilePath(change.filePath) === normalizeCanvasAIFilePath(currentFile)
			) {
				failedById.set(change.id, 'Current file is read-only.');
				continue;
			}
			try {
				await applyCanvasAIChangeToWorkspace(change);
				succeededIds.add(change.id);
			} catch (error) {
				failedById.set(
					change.id,
					error instanceof Error ? error.message : 'Failed to apply this change.'
				);
			}
		}
		if (succeededIds.size > 0) {
			await finalizeCanvasAIWorkspaceChange();
		}
		updateCanvasAIMessageById(messageId, (entry) => ({
			...entry,
			changes: (entry.changes ?? []).map((candidate) => {
				if (succeededIds.has(candidate.id)) {
					return { ...candidate, applyState: 'applied', applyError: '' };
				}
				const failure = failedById.get(candidate.id);
				if (failure) {
					return { ...candidate, applyState: 'failed', applyError: failure };
				}
				return candidate;
			})
		}));
		if (failedById.size > 0) {
			canvasAIError =
				succeededIds.size > 0
					? `Applied ${succeededIds.size} change(s). ${failedById.size} failed.`
					: `Unable to apply ${failedById.size} change(s).`;
		}
		if (succeededIds.size > 0) {
			writeTerminalLine(`\x1b[35m> Applied ${succeededIds.size} AI change(s).\x1b[0m`);
		}
	}

	async function sendCanvasAIMessage() {
		if (!aiEnabled) {
			canvasAIError = 'AI assistant is disabled for this room.';
			return;
		}
		if (isCanvasAIGenerating) {
			return;
		}
		const instruction = canvasAIPrompt.trim();
		if (!instruction) {
			canvasAIError = 'Enter a prompt for AI.';
			return;
		}
		const target = currentFileEntry();
		if (!target || target.isDir) {
			canvasAIError = 'Open a file before using AI.';
			return;
		}

		if (!editor?.getModel?.()) {
			canvasAIError = 'Editor is not ready yet.';
			return;
		}

		appendCanvasAIMessage('user', instruction);
		isCanvasAIGenerating = true;
		canvasAIError = '';
		fileExplorerError = '';
		canvasAIAbortController?.abort();
		canvasAIAbortController = new AbortController();

		try {
			const language = resolveExecutionLanguageForEntry(target);
			const aiResponse = await requestCanvasAIResponse(
				instruction,
				target,
				language,
				canvasAIAbortController.signal
			);
			const proposedChanges = await buildCanvasAIProposedChanges(aiResponse.changes);
			const assistantMessageId = appendCanvasAIMessage(
				'assistant',
				aiResponse.assistantReply,
				proposedChanges
			);
			if (proposedChanges.length > 0) {
				canvasAILastSuggestedMessageId = assistantMessageId;
			}
			canvasAIPrompt = '';
			resizeCanvasAIPromptInput();
			if (proposedChanges.length > 0) {
				writeTerminalLine(
					`\x1b[35m> AI prepared ${proposedChanges.length} change(s). Review diffs and accept as needed.\x1b[0m`
				);
			} else {
				writeTerminalLine('\x1b[35m> AI replied without code changes.\x1b[0m');
			}
		} catch (error) {
			const isAbortError =
				typeof error === 'object' &&
				error !== null &&
				'name' in error &&
				(error as { name?: string }).name === 'AbortError';
			if (isAbortError) {
				return;
			}
			const message = error instanceof Error ? error.message : 'Failed to generate code with AI.';
			canvasAIError = message;
			fileExplorerError = message;
		} finally {
			isCanvasAIGenerating = false;
			canvasAIAbortController = null;
		}
	}

	function parseCanvasAIPromptPixel(value: string) {
		const parsed = Number.parseFloat(value);
		return Number.isFinite(parsed) ? parsed : 0;
	}

	function resolveCanvasAIPromptTarget(target?: HTMLTextAreaElement | null) {
		if (target) {
			return target;
		}
		if (showCanvasAIPrompt && canvasAIPromptElement) {
			return canvasAIPromptElement;
		}
		if (activeSidebarView === 'canvas_ai' && canvasAISidebarPromptElement) {
			return canvasAISidebarPromptElement;
		}
		return canvasAIPromptElement || canvasAISidebarPromptElement;
	}

	function resizeCanvasAIPromptInput(target?: HTMLTextAreaElement | null) {
		const promptElement = resolveCanvasAIPromptTarget(target);
		if (!promptElement || typeof window === 'undefined') {
			return;
		}
		const styles = window.getComputedStyle(promptElement);
		const lineHeight = parseCanvasAIPromptPixel(styles.lineHeight) || 18;
		const verticalPadding =
			parseCanvasAIPromptPixel(styles.paddingTop) + parseCanvasAIPromptPixel(styles.paddingBottom);
		const verticalBorder =
			parseCanvasAIPromptPixel(styles.borderTopWidth) + parseCanvasAIPromptPixel(styles.borderBottomWidth);
		const minHeight = lineHeight + verticalPadding + verticalBorder;
		const maxHeight = lineHeight * 2 + verticalPadding + verticalBorder;
		promptElement.style.height = 'auto';
		const nextHeight = Math.max(minHeight, Math.min(promptElement.scrollHeight, maxHeight));
		promptElement.style.height = `${nextHeight}px`;
		promptElement.style.overflowY = promptElement.scrollHeight > maxHeight ? 'auto' : 'hidden';
	}

	function handleCanvasAIPromptInput(event?: Event) {
		const target = event?.currentTarget;
		resizeCanvasAIPromptInput(target instanceof HTMLTextAreaElement ? target : null);
		if (canvasAIError) {
			canvasAIError = '';
		}
	}

	function handleCanvasAIPromptKeydown(event: KeyboardEvent) {
		if (event.key === 'Escape') {
			event.preventDefault();
			closeCanvasAIPromptPanel();
			return;
		}
		if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
			event.preventDefault();
			void sendCanvasAIMessage();
		}
	}

	function handleSnippetComposerWindowKeydown(event: KeyboardEvent) {
		if (!showSnippetComposer) {
			return;
		}
		if (event.key === 'Escape') {
			event.preventDefault();
			event.stopPropagation();
			closeSnippetComposer();
			return;
		}
		event.stopPropagation();
	}

	function handleEditorCodeDrop(event: DragEvent) {
		event.preventDefault();
		event.stopPropagation();
		isDraggingCode = false;
		const droppedText = event.dataTransfer?.getData('text/plain') ?? '';
		openSnippetComposerFromDrop(droppedText || getCurrentSelectionText());
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
		if (Date.now() < suppressNativeExplorerContextMenuUntil) {
			return;
		}
		clearExplorerLongPressState();
		suppressExplorerClickUntil = 0;
		await openContextMenuAtPosition(event.clientX, event.clientY, target);
	}

	async function persistCurrentFileToFS(options?: { clearDirty?: boolean }) {
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
		if (options?.clearDirty) {
			clearFileDirty(normalized);
		}
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
		updateSelectionSnippetAction();
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
		selectedSnippetText = '';
		canSendSnippetFromSelection = false;
		hideSelectionSnippetAction();
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
			closeCanvasAIPromptPanel();
			closeEditorFindWidget();
			showEditorPane();
		}
		if (normalized === currentFile) {
			ensureTabOpen(normalized);
			const model = editor?.getModel?.();
			if (model && monacoApi) {
				monacoApi.editor.setModelLanguage(model, getLanguageFromExtension(normalized));
			}
			return;
		}
		const remoteEditors = countRemoteEditorsForFile(normalized);
		if (remoteEditors >= MAX_FILE_EDITORS) {
			if (typeof window !== 'undefined' && typeof window.alert === 'function') {
				window.alert('Maximum 3 users can edit this file at once');
			}
			showExplorerPane();
			return;
		}
		ensureTabOpen(normalized);
		expandedDirectories = ensureExpandedDirectoriesForPath(normalized);
		fileExplorerError = '';
		try {
			await persistCurrentFileToFS();
			currentFile = normalized;
			const model = editor?.getModel?.();
			if (model && monacoApi) {
				monacoApi.editor.setModelLanguage(model, getLanguageFromExtension(normalized));
			}
			await recreateBindingForCurrentFile();
			if (activeSidebarView === 'search' && sidebarSearchQuery.trim()) {
				updateSidebarSearchResults();
			}
			updateEditorAccessMode();
		} catch (error) {
			fileExplorerError = error instanceof Error ? error.message : 'Unable to open file';
		}
	}

	function handleExplorerEntryClick(event: MouseEvent, entry: ProjectFileEntry) {
		if (consumeSuppressedExplorerClick(event)) {
			return;
		}
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
		const target = entry && !entry.isDir ? entry : currentFileEntry() ?? firstFileEntry();
		if (!target || target.isDir) {
			fileExplorerError = 'Select a file to run';
			writeTerminalLine('\x1b[31mSelect a file to run.\x1b[0m');
			return;
		}
		if (isRunInProgress) {
			fileExplorerError = 'A run is already in progress';
			writeTerminalLine('\x1b[33mA run is already in progress.\x1b[0m');
			return;
		}
		fileExplorerError = '';
		try {
			activeTerminalPanelTab = 'out';
			clearTerminal();
			writeTerminalLine(`\x1b[36m> Executing ${target.name}...\x1b[0m`);
			let source = '';
			if (target.relativePath === currentFile && editor?.getModel?.()) {
				source = String(editor.getModel().getValue() || '');
			} else {
				source = String(await getActiveFS().promises.readFile(target.path, { encoding: 'utf8' }));
			}
			const stdin = await resolveExecutionStdin();
			const language = resolveExecutionLanguageForEntry(target);
			const activeRelativePath = normalizeProjectName(target.relativePath || target.name);
			const workspaceFiles = await buildExecutionWorkspaceFiles(activeRelativePath, source);
			await executeCode(language, source, target, stdin, workspaceFiles);
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

	function registerVSCodeStyleShortcuts(editorInstance: any, monaco: any) {
		if (!editorInstance || !monaco?.KeyMod || !monaco?.KeyCode) {
			return;
		}
		const bindCommand = (keybinding: number, commandId: string) => {
			editorInstance.addCommand(keybinding, () => {
				editorInstance.trigger('keyboard-shortcut', commandId, null);
			});
		};
		// Keep standard IDE shortcuts available inside Monaco even when app-level key capture is active.
		bindCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyA, 'editor.action.selectAll');
		bindCommand(
			monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyD,
			'editor.action.addSelectionToNextFindMatch'
		);
		bindCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyF, 'actions.find');
		bindCommand(
			monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyH,
			'editor.action.startFindReplaceAction'
		);
		bindCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Slash, 'editor.action.commentLine');
		bindCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyL, 'expandLineSelection');
		editorInstance.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => {
			void persistCurrentFileToFS({ clearDirty: true });
			scheduleCanvasSnapshotSave();
			writeTerminalLine('\x1b[36m> File saved.\x1b[0m');
		});
		editorInstance.addCommand(
			monaco.KeyMod.CtrlCmd | monaco.KeyMod.Shift | monaco.KeyCode.KeyF,
			() => {
				setActiveSidebarView('search');
			}
		);
		editorInstance.addCommand(
			monaco.KeyMod.CtrlCmd | monaco.KeyMod.Shift | monaco.KeyCode.KeyE,
			() => {
				setActiveSidebarView('explorer');
			}
		);
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

	$: if (activeSidebarView === 'search') {
		sidebarSearchQuery;
		sidebarSearchMatchCase;
		sidebarSearchUseRegex;
		updateSidebarSearchResults();
	}

	$: if (!aiEnabled) {
		if (activeSidebarView === 'canvas_ai') {
			activeSidebarView = 'explorer';
		}
		if (showCanvasAIPrompt) {
			closeCanvasAIPromptPanel();
		}
	}

	$: if (canvasEditorBodyElement && !terminalPanelCollapsed) {
		const { min, max } = getTerminalResizeBounds();
		const clampedHeight = Math.max(min, Math.min(max, terminalHeight));
		if (clampedHeight !== terminalHeight) {
			terminalHeight = clampedHeight;
		}
		terminalExpandedHeight = terminalHeight;
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
			const isAIPromptShortcut =
				(event.metaKey || event.ctrlKey) &&
				!event.altKey &&
				!event.shiftKey &&
				event.key.toLowerCase() === 'i';
			const isSidebarSearchShortcut =
				(event.metaKey || event.ctrlKey) &&
				event.shiftKey &&
				!event.altKey &&
				event.key.toLowerCase() === 'f';
			if (isSidebarSearchShortcut) {
				event.preventDefault();
				setActiveSidebarView('search');
				return;
			}
			if (isAIPromptShortcut && aiEnabled) {
				event.preventDefault();
				if (showCanvasAIPrompt) {
					void sendCanvasAIMessage();
					return;
				}
				openCanvasAIPromptPanel();
				return;
			}
			if (event.key === 'Escape' && showCanvasAIPrompt) {
				event.preventDefault();
				closeCanvasAIPromptPanel();
				return;
			}
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
		const onContextMenuCapture = (event: MouseEvent) => {
			if (Date.now() >= suppressNativeExplorerContextMenuUntil) {
				return;
			}
			event.preventDefault();
			event.stopPropagation();
			event.stopImmediatePropagation();
		};
		window.addEventListener('pointerdown', onPointerDown, true);
		window.addEventListener('keydown', onKeyDown, true);
		window.addEventListener('blur', onWindowBlur);
		window.addEventListener('contextmenu', onContextMenuCapture, true);
		return () => {
			window.removeEventListener('pointerdown', onPointerDown, true);
			window.removeEventListener('keydown', onKeyDown, true);
			window.removeEventListener('blur', onWindowBlur);
			window.removeEventListener('contextmenu', onContextMenuCapture, true);
		};
	}

	onMount(async () => {
		executionManager = new ExecutionManager();
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
			registerVSCodeStyleShortcuts(editor, monaco);

			const model = editor.getModel();
			if (!model) {
				return;
			}
			cursorSelectionDisposable = editor.onDidChangeCursorSelection(() => {
				syncLocalSelectionState();
				renderRemoteSelections();
				updateSelectionSnippetAction();
			});
			editorContentChangeDisposable = model.onDidChangeContent(() => {
				renderRemoteSelections();
				scheduleCurrentFilePersistToFS();
				markFileDirty(currentFile);
				if (activeSidebarView === 'search' && sidebarSearchQuery.trim()) {
					updateSidebarSearchResults();
				}
				updateSelectionSnippetAction();
			});
			editorScrollDisposable = editor.onDidScrollChange(() => {
				updateSelectionSnippetAction();
			});

			ydoc = new Y.Doc();
			ydocBeforeTransactionHandler = (transaction: { local?: boolean }) => {
				if (!ydoc || !isEphemeralRoom || isRevertingOversizedYDocState) {
					return;
				}
				if (!transaction?.local) {
					return;
				}
				// Capture a stable pre-change state so local oversized edits can be reverted.
				ydocSnapshotBeforeLocalTransaction = new Uint8Array(encodeStateAsUpdate(ydoc));
			};
			ydoc.on('beforeTransaction', ydocBeforeTransactionHandler);
			ydocUpdateHandler = (
				_update: Uint8Array,
				origin: unknown,
				_doc: unknown,
				transaction: { local?: boolean }
			) => {
				if (!ydoc || isRevertingOversizedYDocState || origin === YDOC_LIMIT_REVERT_ORIGIN) {
					return;
				}
				if (isEphemeralRoom && isLocalYDocTransaction(origin, transaction)) {
					const currentSnapshot = encodeStateAsUpdate(ydoc);
					if (currentSnapshot.byteLength > CODE_CANVAS_MEMORY_LIMIT_BYTES) {
						const rollbackSnapshot = ydocSnapshotBeforeLocalTransaction;
						if (rollbackSnapshot && rollbackSnapshot.byteLength > 0) {
							const shouldReconnectProvider =
								Boolean(provider) &&
								typeof provider?.disconnect === 'function' &&
								typeof provider?.connect === 'function';
							if (shouldReconnectProvider) {
								provider.disconnect();
							}
							isRevertingOversizedYDocState = true;
							try {
								applyUpdate(ydoc, rollbackSnapshot, YDOC_LIMIT_REVERT_ORIGIN);
							} finally {
								isRevertingOversizedYDocState = false;
								if (shouldReconnectProvider) {
									window.setTimeout(() => {
										if (provider && typeof provider.connect === 'function') {
											provider.connect();
										}
									}, 0);
								}
							}
						}
						notifyCodeCanvasMemoryLimitReached();
						return;
					}
					ydocSnapshotBeforeLocalTransaction = null;
				}
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
			await loadPersistedCanvasSnapshotFromServer();
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

			// Keep type reference alive for dynamic import consistency.
			void MonacoBinding;

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
		canvasAIAbortController?.abort();
		canvasAIAbortController = null;
		if (activeExecutionHandle && executionManager) {
			executionManager.stop(activeExecutionHandle.id);
		}
		resetExecutionState();
		executionManager?.dispose();
		executionManager = null;
		cursorSelectionDisposable?.dispose();
		cursorSelectionDisposable = null;
		editorContentChangeDisposable?.dispose();
		editorContentChangeDisposable = null;
		editorScrollDisposable?.dispose();
		editorScrollDisposable = null;
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
		if (ydoc && ydocBeforeTransactionHandler) {
			ydoc.off('beforeTransaction', ydocBeforeTransactionHandler);
		}
		ydocUpdateHandler = null;
		ydocBeforeTransactionHandler = null;
		ydocSnapshotBeforeLocalTransaction = null;
		isRevertingOversizedYDocState = false;
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

<svelte:window on:keydown|capture={handleSnippetComposerWindowKeydown} />

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
	{#if showSnippetComposer}
		<div class="snippet-composer-overlay" role="presentation" on:click|self={closeSnippetComposer}>
			<div
				class="snippet-composer-modal"
				role="dialog"
				tabindex="-1"
				aria-modal="true"
				aria-labelledby="snippet-composer-title"
				on:keydown|capture={handleSnippetComposerWindowKeydown}
			>
				<header class="snippet-composer-header">
					<h3 id="snippet-composer-title">Send Code Snippet</h3>
					<button
						type="button"
						class="snippet-composer-close"
						aria-label="Close snippet composer"
						on:click={closeSnippetComposer}
					>
						<svg viewBox="0 0 24 24" aria-hidden="true">
							<path d="M6 6l12 12M18 6 6 18" />
						</svg>
					</button>
				</header>
				<div class="snippet-preview-wrap">
					<pre class="snippet-preview"><code>{snippetDraft}</code></pre>
				</div>
				<div class="snippet-message-wrap">
					<textarea
						bind:this={snippetMessageInputElement}
						bind:value={snippetMessage}
						class="snippet-message-input"
						rows="3"
						placeholder="Add a message about this code (optional)..."
					></textarea>
				</div>
					<footer class="snippet-composer-footer">
						<button type="button" class="snippet-button secondary" on:click={closeSnippetComposer}>
							Cancel
						</button>
						<button
							type="button"
							class="snippet-button primary"
							on:click={sendSnippetMessage}
							disabled={!snippetDraft.trim()}
						>
							Send to Chat
						</button>
					</footer>
			</div>
		</div>
	{/if}
	<div class="canvas-side-region">
		<nav class="canvas-activity-bar" aria-label="Canvas activity bar">
			<button
				type="button"
				class="activity-button"
				class:active={activeSidebarView === 'explorer'}
				aria-label="Explorer"
				title="Explorer"
				on:click={() => setActiveSidebarView('explorer')}
			>
				<svg viewBox="0 0 24 24" aria-hidden="true">
					<path d="M4 5h16v5H4zM4 14h16v5H4z" />
				</svg>
			</button>
			<button
				type="button"
				class="activity-button"
				class:active={activeSidebarView === 'search'}
				aria-label="Search"
				title="Search"
				on:click={() => setActiveSidebarView('search')}
			>
				<svg viewBox="0 0 24 24" aria-hidden="true">
					<circle cx="11" cy="11" r="6.5" />
					<path d="m16 16 4 4" />
				</svg>
			</button>
			{#if aiEnabled}
				<button
					type="button"
					class="activity-button"
					class:active={activeSidebarView === 'canvas_ai'}
					aria-label="Canvas AI"
					title="Canvas AI"
					on:click={() => setActiveSidebarView('canvas_ai')}
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d="M12 3.4 13.9 8 18.6 10 13.9 12 12 16.6 10.1 12 5.4 10 10.1 8Z" />
						<path d="M18.5 4.8 19.2 6.5 21 7.2 19.2 8 18.5 9.7 17.8 8 16 7.2 17.8 6.5Z" />
					</svg>
				</button>
			{/if}
		</nav>
		<aside
			class="canvas-sidebar"
			class:drag-over={isSidebarDragOver}
			bind:this={sidebarElement}
			on:dragenter={handleSidebarDragEnter}
			on:dragover={handleSidebarDragOver}
			on:dragleave={handleSidebarDragLeave}
			on:drop={handleSidebarDrop}
		>
			{#if activeSidebarView === 'explorer'}
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
								on:touchstart={(event) => onExplorerEntryTouchStart(event, entry)}
								on:touchmove={onExplorerEntryTouchMove}
								on:touchend={onExplorerEntryTouchEnd}
								on:touchcancel={onExplorerEntryTouchCancel}
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
											on:click|stopPropagation={(event) => {
												if (consumeSuppressedExplorerClick(event)) {
													return;
												}
												toggleFolder(entry);
											}}
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
										on:click={(event) => handleExplorerEntryClick(event, entry)}
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
														<path
															d="M3.5 7.5h6l2 2h9v8.5a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2V7.5Z"
														/>
													</svg>
												{/if}
											{:else}
												<span class="file-extension-symbol" aria-hidden="true">
													{@html getFileIconSVG(entry.name)}
												</span>
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
									on:click|stopPropagation={(event) => {
										if (consumeSuppressedExplorerClick(event)) {
											return;
										}
										void openContextMenu(event, entry);
									}}
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
									on:click|stopPropagation={(event) => {
										if (consumeSuppressedExplorerClick(event)) {
											return;
										}
										openDeleteConfirmation(entry);
									}}
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
			{:else if activeSidebarView === 'search'}
				<div class="sidebar-panel-header">
					<span>Search & Replace</span>
					<button
						type="button"
						class="sidebar-panel-close"
						aria-label="Close search"
						on:click={() => setActiveSidebarView('explorer')}
					>
						×
					</button>
				</div>
				<div class="sidebar-search-row">
					<input
						bind:this={searchInputElement}
						type="search"
						class="sidebar-filter-input"
						placeholder="Search files, folders, and text..."
						bind:value={sidebarSearchQuery}
						on:keydown={(event) => {
							if (event.key === 'Enter') {
								event.preventDefault();
								if (event.shiftKey) {
									void searchPreviousResult();
									return;
								}
								void searchNextResult();
							}
						}}
					/>
					<input
						type="text"
						class="sidebar-filter-input"
						placeholder="Replace with..."
						bind:value={sidebarReplaceQuery}
					/>
				</div>
				<div class="sidebar-search-options">
					<button
						type="button"
						class="sidebar-toggle-chip"
						class:active={sidebarSearchMatchCase}
						on:click={() => (sidebarSearchMatchCase = !sidebarSearchMatchCase)}
					>
						Match Case
					</button>
					<button
						type="button"
						class="sidebar-toggle-chip"
						class:active={sidebarSearchUseRegex}
						on:click={() => (sidebarSearchUseRegex = !sidebarSearchUseRegex)}
					>
						Regex
					</button>
				</div>
				<div class="sidebar-search-actions">
					<button
						type="button"
						class="sidebar-action-btn"
						on:click={() => void searchPreviousResult()}
						disabled={sidebarTextResultCount === 0}
					>
						Prev
					</button>
					<button
						type="button"
						class="sidebar-action-btn"
						on:click={() => void searchNextResult()}
						disabled={sidebarTextResultCount === 0}
					>
						Next
					</button>
					<button
						type="button"
						class="sidebar-action-btn"
						on:click={replaceCurrentResult}
						disabled={sidebarTextResultCount === 0}
					>
						Replace
					</button>
					<button
						type="button"
						class="sidebar-action-btn"
						on:click={replaceAllResults}
						disabled={sidebarTextResultCount === 0}
					>
						Replace All
					</button>
				</div>
				<div class="sidebar-search-status">
					{#if sidebarSearchResults.length > 0}
						{sidebarSearchResults.length} results:
						{sidebarFolderResultCount} folders, {sidebarFileResultCount} files, {sidebarTextResultCount}
						text
					{:else if sidebarSearchQuery.trim()}
						No files, folders, or text matches
					{:else}
						Start typing to search your workspace
					{/if}
				</div>
				<div class="sidebar-search-results" role="list">
					{#if sidebarSearchResults.length === 0}
						<div class="sidebar-search-empty">Nothing to show</div>
					{:else}
						{#each sidebarSearchResults as result, index (result.key)}
							<button
								type="button"
								class="sidebar-result-item"
								class:active={index === sidebarActiveSearchIndex}
								on:click={() => void focusSidebarSearchResult(index)}
							>
								<span class={`sidebar-result-kind ${result.kind}`}>{result.kind}</span>
								<span class="sidebar-result-content">
									<span class="sidebar-result-path">
										{#each collectSidebarSearchHighlights(result.path) as segment, segmentIndex (`path-${result.key}-${segmentIndex}`)}
											{#if segment.isMatch}
												<mark class="sidebar-result-highlight">{segment.value}</mark>
											{:else}
												{segment.value}
											{/if}
										{/each}
									</span>
									{#if result.kind === 'text'}
										<span class="sidebar-result-line">
											Ln {result.lineNumber}, Col {result.startColumn}
										</span>
									{/if}
									<span class="sidebar-result-preview">
										{#if result.kind === 'text'}
											{#each collectSidebarSearchHighlights(result.preview) as segment, segmentIndex (`preview-${result.key}-${segmentIndex}`)}
												{#if segment.isMatch}
													<mark class="sidebar-result-highlight">{segment.value}</mark>
												{:else}
													{segment.value}
												{/if}
											{/each}
										{:else if result.kind === 'folder'}
											Open folder
										{:else}
											Open file
										{/if}
									</span>
								</span>
							</button>
						{/each}
					{/if}
				</div>
			{:else if aiEnabled}
				<div class="canvas-ai-sidebar">
					<div class="canvas-ai-panel-header">
						<div class="canvas-ai-panel-head-main">
							<span>Canvas AI</span>
							{#if currentFile}
								<span class="canvas-ai-file-pill">{getTabLabel(currentFile)}</span>
							{/if}
						</div>
					</div>
					<div class="canvas-ai-thread" bind:this={canvasAISidebarThreadElement}>
						{#if canvasAIChatMessages.length === 0}
							<div class="canvas-ai-empty">
								<p>Chat with AI about the currently selected file.</p>
								<p>AI proposes file-level diffs that you can accept one-by-one or all at once.</p>
							</div>
						{:else}
							{#each canvasAIChatMessages as message (message.id)}
								<article class="canvas-ai-message" class:user={message.role === 'user'}>
									<header class="canvas-ai-message-header">
										<strong>{message.role === 'user' ? 'You' : 'AI'}</strong>
										<time>
											{new Date(message.timestamp).toLocaleTimeString([], {
												hour: '2-digit',
												minute: '2-digit'
											})}
										</time>
									</header>
									<p class="canvas-ai-message-text">{message.text}</p>
									{#if message.changes && message.changes.length > 0}
										<div class="canvas-ai-change-list">
											<div class="canvas-ai-change-list-header">
												<span>{message.changes.length} proposed file change(s)</span>
												<button
													type="button"
													class="canvas-ai-action secondary"
													on:click={() => void applyAllCanvasAIChanges(message.id)}
													disabled={isCanvasAIGenerating || getCanvasAIPendingChangeCount(message) === 0}
												>
													Accept All
												</button>
											</div>
											{#each message.changes as change (change.id)}
												<section
													class="canvas-ai-code-block"
													class:is-applied={change.applyState === 'applied'}
													class:is-failed={change.applyState === 'failed'}
												>
													<div class="canvas-ai-change-headline">
														<div class="canvas-ai-change-meta">
															<strong class="canvas-ai-change-file">{change.filePath}</strong>
															<span class="canvas-ai-change-chip">{change.action.toUpperCase()}</span>
														</div>
														<span class="canvas-ai-change-location">{change.locationHint}</span>
													</div>
													<p class="canvas-ai-change-summary">{change.summary}</p>
													<pre class="canvas-ai-code">{change.diffText}</pre>
													{#if change.applyError}
														<div class="canvas-ai-change-error">{change.applyError}</div>
													{/if}
													<div class="canvas-ai-code-actions">
														<button
															type="button"
															class="canvas-ai-action primary"
															on:click={() => void applyCanvasAIChange(message.id, change.id)}
															disabled={isCanvasAIGenerating || change.applyState === 'applied'}
														>
															{change.applyState === 'applied' ? 'Applied' : 'Accept'}
														</button>
													</div>
												</section>
											{/each}
										</div>
									{/if}
								</article>
							{/each}
						{/if}
					</div>
					{#if canvasAIError}
						<div class="canvas-ai-error" role="status" aria-live="polite">{canvasAIError}</div>
					{/if}
					<textarea
						bind:this={canvasAISidebarPromptElement}
						bind:value={canvasAIPrompt}
						rows="2"
						class="canvas-ai-input"
						placeholder={currentFile
							? 'Ask AI what to change in this file...'
							: 'Open a file from Explorer to start code-aware AI chat...'}
						on:input={handleCanvasAIPromptInput}
						on:keydown={handleCanvasAIPromptKeydown}
						disabled={isCanvasAIGenerating || !currentFile}
					></textarea>
					<div class="canvas-ai-actions">
						<button
							type="button"
							class="canvas-ai-action secondary"
							on:click={() => {
								canvasAIPrompt = '';
								canvasAIError = '';
								resizeCanvasAIPromptInput(canvasAISidebarPromptElement);
							}}
							disabled={isCanvasAIGenerating || (!canvasAIPrompt.trim() && !canvasAIError)}
						>
							Clear
						</button>
						<button
							type="button"
							class="canvas-ai-action secondary"
							on:click={() => {
								const latest = resolveCanvasAILastSuggestedMessage();
								if (latest) {
									void applyAllCanvasAIChanges(latest.id);
								}
							}}
							disabled={isCanvasAIGenerating || getCanvasAILastPendingChangeCount() === 0}
						>
							Accept Latest
						</button>
						<button
							type="button"
							class="canvas-ai-action primary"
							on:click={() => void sendCanvasAIMessage()}
							disabled={isCanvasAIGenerating || !canvasAIPrompt.trim() || !currentFile}
						>
							{isCanvasAIGenerating ? 'Thinking...' : 'Send'}
						</button>
					</div>
				</div>
			{/if}
			{#if fileExplorerError && activeSidebarView !== 'canvas_ai'}
				<div class="file-error" role="status" aria-live="polite">{fileExplorerError}</div>
			{/if}
		</aside>
	</div>
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
								<span class="editor-tab-symbol" aria-hidden="true">
									{@html getFileIconSVG(tab)}
								</span>
								{getTabLabel(tab)}
								{#if isFileDirty(tab)}
									<span class="editor-tab-dirty-dot" aria-hidden="true"></span>
								{/if}
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
		<div class="editor-breadcrumb-bar">
			{#if currentFile}
				<div class="editor-breadcrumb-path">
					{#each currentFile.split('/') as segment, index (`${segment}-${index}`)}
						<span class="editor-breadcrumb-segment">{segment}</span>
						{#if index < currentFile.split('/').length - 1}
							<span class="editor-breadcrumb-separator">/</span>
						{/if}
					{/each}
				</div>
				<button
					type="button"
					class="editor-breadcrumb-copy"
					on:click={() => void copyEntryPathToClipboard(currentFileEntry())}
				>
					Copy Path
				</button>
			{:else}
				<div class="editor-breadcrumb-empty">No file selected</div>
			{/if}
		</div>
			<div class="canvas-editor-body" bind:this={canvasEditorBodyElement}>
				<div
					class="canvas-editor-pane"
					class:is-empty={openTabs.length === 0}
					role="region"
					aria-label="Code editor pane"
					on:dragstart|capture={handleEditorCodeDragStart}
					on:dragenter|capture={handleEditorCodeDragEnter}
					on:dragover|capture={handleEditorCodeDragOver}
					on:dragleave|capture={handleEditorCodeDragLeave}
					on:drop|capture={handleEditorCodeDrop}
					on:dragend|capture={handleEditorCodeDragEnd}
				>
					<div class="code-canvas" bind:this={editorContainer}></div>
					{#if aiEnabled && showCanvasAIPrompt}
						<div class="canvas-ai-overlay" role="presentation">
							<div class="canvas-ai-panel" role="dialog" aria-modal="true" aria-label="AI code prompt">
								<div class="canvas-ai-panel-header">
									<div class="canvas-ai-panel-head-main">
										<span>AI in Editor</span>
										{#if currentFile}
											<span class="canvas-ai-file-pill">{getTabLabel(currentFile)}</span>
										{/if}
									</div>
									<button
										type="button"
										class="canvas-ai-close"
										on:click={closeCanvasAIPromptPanel}
										aria-label="Close AI prompt"
									>
										×
									</button>
								</div>
								<div class="canvas-ai-thread" bind:this={canvasAIThreadElement}>
									{#if canvasAIChatMessages.length === 0}
										<div class="canvas-ai-empty">
											<p>Chat with AI about this file. Ask for refactors, fixes, or new features.</p>
											<p>AI responses include structured file diffs you can accept individually.</p>
										</div>
									{:else}
										{#each canvasAIChatMessages as message (message.id)}
											<article class="canvas-ai-message" class:user={message.role === 'user'}>
												<header class="canvas-ai-message-header">
													<strong>{message.role === 'user' ? 'You' : 'AI'}</strong>
													<time>
														{new Date(message.timestamp).toLocaleTimeString([], {
															hour: '2-digit',
															minute: '2-digit'
														})}
													</time>
												</header>
												<p class="canvas-ai-message-text">{message.text}</p>
												{#if message.changes && message.changes.length > 0}
													<div class="canvas-ai-change-list">
														<div class="canvas-ai-change-list-header">
															<span>{message.changes.length} proposed file change(s)</span>
															<button
																type="button"
																class="canvas-ai-action secondary"
																on:click={() => void applyAllCanvasAIChanges(message.id)}
																disabled={isCanvasAIGenerating || getCanvasAIPendingChangeCount(message) === 0}
															>
																Accept All
															</button>
														</div>
														{#each message.changes as change (change.id)}
															<section
																class="canvas-ai-code-block"
																class:is-applied={change.applyState === 'applied'}
																class:is-failed={change.applyState === 'failed'}
															>
																<div class="canvas-ai-change-headline">
																	<div class="canvas-ai-change-meta">
																		<strong class="canvas-ai-change-file">{change.filePath}</strong>
																		<span class="canvas-ai-change-chip">{change.action.toUpperCase()}</span>
																	</div>
																	<span class="canvas-ai-change-location">{change.locationHint}</span>
																</div>
																<p class="canvas-ai-change-summary">{change.summary}</p>
																<pre class="canvas-ai-code">{change.diffText}</pre>
																{#if change.applyError}
																	<div class="canvas-ai-change-error">{change.applyError}</div>
																{/if}
																<div class="canvas-ai-code-actions">
																	<button
																		type="button"
																		class="canvas-ai-action primary"
																		on:click={() => void applyCanvasAIChange(message.id, change.id)}
																		disabled={isCanvasAIGenerating || change.applyState === 'applied'}
																	>
																		{change.applyState === 'applied' ? 'Applied' : 'Accept'}
																	</button>
																</div>
															</section>
														{/each}
													</div>
												{/if}
											</article>
										{/each}
									{/if}
								</div>
								{#if canvasAIError}
									<div class="canvas-ai-error" role="status" aria-live="polite">{canvasAIError}</div>
								{/if}
								<textarea
									bind:this={canvasAIPromptElement}
									bind:value={canvasAIPrompt}
									rows="2"
									class="canvas-ai-input"
									placeholder="Ask AI what to change in this file..."
									on:input={handleCanvasAIPromptInput}
									on:keydown={handleCanvasAIPromptKeydown}
									disabled={isCanvasAIGenerating}
								></textarea>
								<div class="canvas-ai-actions">
									<button
										type="button"
										class="canvas-ai-action secondary"
										on:click={closeCanvasAIPromptPanel}
										disabled={isCanvasAIGenerating}
									>
										Cancel
									</button>
									<button
										type="button"
										class="canvas-ai-action secondary"
										on:click={() => {
											const latest = resolveCanvasAILastSuggestedMessage();
											if (latest) {
												void applyAllCanvasAIChanges(latest.id);
											}
										}}
										disabled={isCanvasAIGenerating || getCanvasAILastPendingChangeCount() === 0}
									>
										Accept Latest
									</button>
									<button
										type="button"
										class="canvas-ai-action primary"
										on:click={() => void sendCanvasAIMessage()}
										disabled={isCanvasAIGenerating || !canvasAIPrompt.trim()}
									>
										{isCanvasAIGenerating ? 'Thinking...' : 'Send'}
									</button>
								</div>
							</div>
						</div>
					{/if}
					{#if openTabs.length === 0}
						<div class="canvas-blank-state" role="status" aria-live="polite">
							Open a file from Explorer to start editing.
						</div>
					{/if}
					{#if isDraggingCode}
						<div class="canvas-code-drop-overlay">
							<div class="canvas-code-drop-box">
								<svg viewBox="0 0 24 24" class="canvas-code-drop-icon" aria-hidden="true">
									<path d="M4 7.5h16v9H4z" />
									<path d="m4 8 8 6 8-6" />
								</svg>
								<span>Drop code here to send to chat</span>
							</div>
						</div>
					{/if}
					{#if showSelectionSnippetAction}
						<button
							type="button"
							class="selection-snippet-action"
							style:left={`${selectionSnippetActionLeft}px`}
							style:top={`${selectionSnippetActionTop}px`}
							aria-label="Send selected code to chat"
							title="Send selected code to chat"
							on:pointerdown|preventDefault
							on:click={openSnippetComposerForSelection}
						>
							<svg viewBox="0 0 24 24" aria-hidden="true">
								<path d="M4 7.5h16v9H4z" />
								<path d="m4 8 8 6 8-6" />
							</svg>
						</button>
					{/if}
				</div>
				<div
					class="terminal-panel"
					class:is-collapsed={terminalPanelCollapsed}
					style={terminalPanelCollapsed ? '' : `height:${terminalHeight}px`}
				>
					{#if !terminalPanelCollapsed}
						<button
							type="button"
							class="terminal-resize-handle"
							on:pointerdown={startTerminalResize}
							aria-label="Resize terminal"
						>
							<span class="terminal-resize-grip" aria-hidden="true"></span>
						</button>
					{/if}
					<div class="terminal-header">
						<span class="terminal-title">
							{#if isRunInProgress && runningFilePath}
								Running {getTabLabel(runningFilePath)}
							{:else}
								Terminal
							{/if}
						</span>
						<div class="terminal-header-right">
							<div class="terminal-action-group">
								<button
									type="button"
									class="terminal-action-button terminal-action-run"
									on:click={() => void runFile(currentFileEntry() ?? firstFileEntry())}
									disabled={isRunInProgress}
								>
									{isRunInProgress ? 'Running...' : 'Run'}
								</button>
								<button
									type="button"
									class="terminal-action-button terminal-action-snippet"
									on:click={openSnippetComposerForSelection}
									disabled={!canSendSnippetFromSelection}
									title="Send selected code to chat"
									aria-label="Send selected code to chat"
								>
									<svg viewBox="0 0 24 24" aria-hidden="true">
										<path d="M4 7.5h16v9H4z" />
										<path d="m4 8 8 6 8-6" />
									</svg>
									<span>Snippet</span>
								</button>
								<button
									type="button"
									class="terminal-action-button terminal-action-stop"
									on:click={stopRunningCode}
									disabled={!isRunInProgress}
								>
									Stop
								</button>
								<button type="button" class="terminal-action-button" on:click={clearTerminal}>
									Clear
								</button>
							</div>
							<button
								type="button"
								class="terminal-action-button terminal-collapse-button"
								on:click={toggleTerminalPanelCollapse}
								aria-label={terminalPanelCollapsed ? 'Expand terminal' : 'Collapse terminal'}
								title={terminalPanelCollapsed ? 'Expand terminal' : 'Collapse terminal'}
							>
								<svg viewBox="0 0 24 24" aria-hidden="true">
									{#if terminalPanelCollapsed}
										<path d="M7 15l5-6 5 6" />
									{:else}
										<path d="m7 9 5 6 5-6" />
									{/if}
								</svg>
							</button>
						</div>
					</div>
					<div class="terminal-body" class:is-hidden={terminalPanelCollapsed}>
						<div class="terminal-tabs" role="tablist" aria-label="Terminal panels">
							<button
								type="button"
								class="terminal-tab-button"
								class:is-active={activeTerminalPanelTab === 'out'}
								role="tab"
								aria-selected={activeTerminalPanelTab === 'out'}
								on:click={() => switchTerminalPanelTab('out')}
							>
								Out
							</button>
							<button
								type="button"
								class="terminal-tab-button"
								class:is-active={activeTerminalPanelTab === 'in'}
								role="tab"
								aria-selected={activeTerminalPanelTab === 'in'}
								on:click={() => switchTerminalPanelTab('in')}
							>
								In
							</button>
						</div>
						<div class="terminal-tab-panel" class:is-active={activeTerminalPanelTab === 'out'}>
							<div class="terminal-container" bind:this={terminalContainer}></div>
						</div>
						<div class="terminal-tab-panel terminal-tab-panel-in" class:is-active={activeTerminalPanelTab === 'in'}>
							<textarea
								class="terminal-input-area"
								bind:value={terminalInputDraft}
								placeholder="Program stdin (optional). If empty, ToraEditorInput.txt is used."
								spellcheck="false"
								autocomplete="off"
								autocapitalize="off"
							></textarea>
						</div>
					</div>
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
				disabled={isRunInProgress}
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

	.canvas-side-region {
		width: 294px;
		flex: 0 0 294px;
		min-width: 0;
		min-height: 0;
		display: flex;
		border-right: 1px solid rgba(120, 134, 160, 0.35);
		background: rgba(10, 14, 22, 0.72);
	}

	.canvas-activity-bar {
		width: 40px;
		flex: 0 0 40px;
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 0.35rem;
		padding: 0.5rem 0.28rem;
		border-right: 1px solid rgba(120, 134, 160, 0.3);
		background: rgba(8, 12, 18, 0.85);
	}

	.activity-button {
		border: 1px solid rgba(102, 122, 154, 0.42);
		background: rgba(22, 31, 46, 0.88);
		color: #cad9ef;
		border-radius: 0.45rem;
		width: 1.8rem;
		height: 1.8rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		padding: 0;
		cursor: pointer;
	}

	.activity-button:hover {
		border-color: rgba(142, 169, 210, 0.75);
		background: rgba(37, 54, 83, 0.95);
	}

	.activity-button.active {
		border-color: rgba(123, 168, 244, 0.9);
		background: rgba(44, 75, 126, 0.95);
		color: #eff5ff;
	}

	.activity-button svg {
		width: 0.95rem;
		height: 0.95rem;
		stroke: currentColor;
		stroke-width: 1.9;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.canvas-sidebar {
		flex: 1;
		min-width: 0;
		min-height: 0;
		display: flex;
		flex-direction: column;
		gap: 0.55rem;
		background: transparent;
		padding: 0.55rem;
		transition:
			border-color 0.14s ease,
			box-shadow 0.14s ease,
			background 0.14s ease;
	}

	.canvas-ai-sidebar {
		flex: 1;
		min-height: 0;
		display: grid;
		grid-template-rows: auto minmax(0, 1fr) auto auto;
		gap: 0.45rem;
		padding: 0.1rem;
	}

	.canvas-ai-sidebar .canvas-ai-panel-header {
		font-size: 0.75rem;
		padding: 0.18rem 0.18rem 0.12rem;
	}

	.canvas-ai-sidebar .canvas-ai-file-pill {
		max-width: 10.5rem;
		font-size: 0.6rem;
	}

	.canvas-ai-sidebar .canvas-ai-thread {
		gap: 0.38rem;
		padding-right: 0.05rem;
	}

	.canvas-ai-sidebar .canvas-ai-empty p {
		font-size: 0.68rem;
	}

	.canvas-ai-sidebar .canvas-ai-message {
		padding: 0.45rem 0.5rem;
		gap: 0.24rem;
	}

	.canvas-ai-sidebar .canvas-ai-message-header strong {
		font-size: 0.6rem;
	}

	.canvas-ai-sidebar .canvas-ai-message-header time {
		font-size: 0.58rem;
	}

	.canvas-ai-sidebar .canvas-ai-message-text {
		font-size: 0.71rem;
		line-height: 1.4;
	}

	.canvas-ai-sidebar .canvas-ai-code {
		max-height: 150px;
		font-size: 0.66rem;
	}

	.canvas-ai-sidebar .canvas-ai-input {
		font-size: 0.74rem;
		padding: 0.42rem 0.5rem;
	}

	.canvas-ai-sidebar .canvas-ai-error {
		font-size: 0.67rem;
		padding: 0.3rem 0.42rem;
	}

	.canvas-ai-sidebar .canvas-ai-actions {
		gap: 0.34rem;
	}

	.canvas-ai-sidebar .canvas-ai-action {
		font-size: 0.66rem;
		padding: 0.34rem 0.56rem;
	}

	.canvas-sidebar.drag-over {
		background: rgba(16, 27, 44, 0.5);
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
		margin-top: auto;
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

	.sidebar-panel-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.45rem;
		color: #dfe8f7;
		font-size: 0.72rem;
		font-weight: 700;
		letter-spacing: 0.03em;
		text-transform: uppercase;
	}

	.sidebar-panel-close {
		border: 1px solid rgba(103, 125, 160, 0.52);
		background: rgba(24, 35, 52, 0.88);
		color: #dbe6f8;
		border-radius: 0.35rem;
		width: 1.4rem;
		height: 1.4rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		padding: 0;
		font-size: 0.9rem;
		line-height: 1;
		cursor: pointer;
	}

	.sidebar-panel-close:hover {
		border-color: rgba(139, 168, 211, 0.68);
		background: rgba(41, 61, 92, 0.92);
	}

	.sidebar-filter-input {
		width: 100%;
		min-width: 0;
		border: 1px solid rgba(103, 125, 160, 0.52);
		background: rgba(18, 27, 42, 0.86);
		color: #dbe6f8;
		border-radius: 0.35rem;
		padding: 0.4rem 0.5rem;
		font-size: 0.72rem;
		line-height: 1.2;
	}

	.sidebar-filter-input:focus {
		outline: none;
		border-color: rgba(117, 166, 248, 0.78);
		box-shadow: 0 0 0 2px rgba(117, 166, 248, 0.2);
	}

	.github-import-row {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: 0.3rem;
	}

	.sidebar-search-row {
		display: grid;
		grid-template-columns: 1fr;
		gap: 0.32rem;
	}

	.sidebar-search-options {
		display: flex;
		flex-wrap: wrap;
		gap: 0.3rem;
	}

	.sidebar-toggle-chip {
		border: 1px solid rgba(103, 125, 160, 0.52);
		background: rgba(24, 35, 52, 0.88);
		color: #dbe6f8;
		border-radius: 999px;
		padding: 0.2rem 0.5rem;
		font-size: 0.66rem;
		font-weight: 600;
		cursor: pointer;
	}

	.sidebar-toggle-chip.active {
		border-color: rgba(120, 174, 255, 0.86);
		background: rgba(42, 77, 132, 0.92);
	}

	.sidebar-search-actions {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.3rem;
	}

	.sidebar-action-btn {
		border: 1px solid rgba(103, 125, 160, 0.52);
		background: rgba(24, 35, 52, 0.88);
		color: #dbe6f8;
		border-radius: 0.35rem;
		padding: 0.26rem 0.42rem;
		font-size: 0.67rem;
		font-weight: 600;
		cursor: pointer;
	}

	.sidebar-action-btn:hover:not(:disabled) {
		border-color: rgba(139, 168, 211, 0.68);
		background: rgba(41, 61, 92, 0.92);
	}

	.sidebar-action-btn:disabled {
		opacity: 0.52;
		cursor: not-allowed;
	}

	.sidebar-search-status {
		font-size: 0.67rem;
		color: rgba(205, 220, 245, 0.82);
	}

	.sidebar-search-results {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
		min-height: 0;
		overflow: auto;
	}

	.sidebar-search-empty {
		font-size: 0.72rem;
		color: rgba(205, 220, 245, 0.72);
		padding: 0.5rem 0.22rem;
	}

	.sidebar-result-item {
		border: 1px solid rgba(103, 125, 160, 0.4);
		background: rgba(24, 35, 52, 0.72);
		color: #e0e9fb;
		border-radius: 0.35rem;
		padding: 0.32rem 0.42rem;
		display: grid;
		grid-template-columns: auto minmax(0, 1fr);
		gap: 0.4rem;
		align-items: flex-start;
		text-align: left;
		cursor: pointer;
	}

	.sidebar-result-item.active {
		border-color: rgba(118, 170, 255, 0.84);
		background: rgba(42, 72, 124, 0.9);
	}

	.sidebar-result-item:hover {
		border-color: rgba(139, 168, 211, 0.68);
	}

	.sidebar-result-kind {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		min-width: 2.85rem;
		padding: 0.12rem 0.34rem;
		border-radius: 999px;
		border: 1px solid rgba(112, 134, 170, 0.6);
		background: rgba(27, 40, 61, 0.86);
		color: #d8e6ff;
		font-size: 0.58rem;
		font-weight: 700;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		line-height: 1.15;
	}

	.sidebar-result-kind.file {
		border-color: rgba(90, 134, 211, 0.68);
		background: rgba(30, 56, 102, 0.84);
	}

	.sidebar-result-kind.folder {
		border-color: rgba(117, 154, 96, 0.66);
		background: rgba(43, 72, 37, 0.82);
	}

	.sidebar-result-kind.text {
		border-color: rgba(183, 132, 83, 0.68);
		background: rgba(86, 58, 31, 0.82);
	}

	.sidebar-result-content {
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 0.14rem;
	}

	.sidebar-result-path {
		min-width: 0;
		font-size: 0.69rem;
		color: #f0f5ff;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.sidebar-result-line {
		font-size: 0.62rem;
		color: rgba(171, 197, 238, 0.88);
	}

	.sidebar-result-preview {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: 0.67rem;
		color: rgba(203, 219, 246, 0.9);
	}

	.sidebar-result-highlight {
		background: rgba(255, 226, 126, 0.35);
		color: #fdf6d9;
		border-radius: 0.18rem;
		padding: 0 0.08rem;
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

	@media (pointer: coarse) {
		.file-entry-row,
		.file-entry-main,
		.file-entry-trigger,
		.file-entry-label {
			-webkit-touch-callout: none;
			-webkit-user-select: none;
			user-select: none;
			touch-action: manipulation;
		}
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

	.file-extension-symbol {
		width: 1rem;
		height: 1rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		flex: 0 0 auto;
	}

	.file-extension-symbol :global(svg) {
		width: 100%;
		height: 100%;
		display: block;
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
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
	}

	.editor-tab-symbol {
		flex: 0 0 auto;
		width: 1rem;
		height: 1rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
	}

	.editor-tab-symbol :global(svg) {
		width: 100%;
		height: 100%;
		display: block;
	}

	.editor-tab-dirty-dot {
		width: 0.36rem;
		height: 0.36rem;
		border-radius: 999px;
		background: #8fd0ff;
		flex: 0 0 auto;
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

	.editor-breadcrumb-bar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		padding: 0.26rem 0.55rem;
		border-bottom: 1px solid rgba(120, 134, 160, 0.25);
		background: rgba(13, 19, 30, 0.7);
	}

	.editor-breadcrumb-path {
		display: inline-flex;
		align-items: center;
		gap: 0.22rem;
		min-width: 0;
		overflow-x: auto;
	}

	.editor-breadcrumb-segment {
		font-size: 0.68rem;
		color: rgba(220, 231, 248, 0.88);
		white-space: nowrap;
	}

	.editor-breadcrumb-separator {
		font-size: 0.66rem;
		color: rgba(164, 181, 210, 0.84);
	}

	.editor-breadcrumb-copy {
		border: 1px solid rgba(103, 125, 160, 0.52);
		background: rgba(24, 35, 52, 0.88);
		color: #dbe6f8;
		border-radius: 0.35rem;
		padding: 0.2rem 0.46rem;
		font-size: 0.64rem;
		font-weight: 600;
		cursor: pointer;
		white-space: nowrap;
	}

	.editor-breadcrumb-copy:hover {
		border-color: rgba(139, 168, 211, 0.68);
		background: rgba(41, 61, 92, 0.92);
	}

	.editor-breadcrumb-empty {
		font-size: 0.67rem;
		color: rgba(199, 214, 239, 0.72);
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

	.canvas-code-drop-overlay {
		position: absolute;
		inset: 0;
		z-index: 7;
		display: flex;
		align-items: center;
		justify-content: center;
		background: rgba(4, 8, 14, 0.62);
		backdrop-filter: blur(2px);
		pointer-events: all;
	}

	.canvas-code-drop-box {
		display: inline-flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 0.6rem;
		min-width: min(90%, 22rem);
		max-width: min(92%, 30rem);
		padding: 1.2rem 1.35rem;
		border-radius: 0.95rem;
		border: 2px dashed rgba(218, 232, 252, 0.82);
		background: linear-gradient(180deg, rgba(49, 66, 92, 0.84) 0%, rgba(24, 32, 48, 0.86) 100%);
		box-shadow: 0 16px 38px rgba(0, 0, 0, 0.36);
		color: #eaf2ff;
		font-size: 0.92rem;
		font-weight: 700;
		letter-spacing: 0.01em;
		text-align: center;
	}

	.canvas-code-drop-icon {
		width: 1.9rem;
		height: 1.9rem;
		stroke: currentColor;
		stroke-width: 1.8;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
		opacity: 0.92;
	}

	.selection-snippet-action {
		position: absolute;
		z-index: 8;
		width: 2rem;
		height: 1.8rem;
		border: 1px solid rgba(104, 211, 145, 0.78);
		background: rgba(18, 116, 84, 0.86);
		color: #effff8;
		border-radius: 0.42rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		padding: 0;
		box-shadow: 0 8px 20px rgba(0, 0, 0, 0.36);
	}

	.selection-snippet-action:hover {
		border-color: rgba(134, 239, 172, 0.88);
		background: rgba(20, 145, 94, 0.9);
	}

	.selection-snippet-action svg {
		width: 0.9rem;
		height: 0.9rem;
		stroke: currentColor;
		stroke-width: 1.9;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.canvas-ai-overlay {
		position: fixed;
		inset: 0;
		z-index: 10040;
		background: rgba(5, 10, 18, 0.72);
		backdrop-filter: blur(7px) saturate(120%);
		-webkit-backdrop-filter: blur(7px) saturate(120%);
	}

	.canvas-ai-panel {
		position: fixed;
		inset: 0;
		z-index: 10041;
		display: grid;
		grid-template-rows: auto minmax(0, 1fr) auto auto;
		gap: 0.68rem;
		padding:
			max(0.9rem, env(safe-area-inset-top))
			max(1rem, env(safe-area-inset-right))
			max(0.9rem, env(safe-area-inset-bottom))
			max(1rem, env(safe-area-inset-left));
		background: linear-gradient(180deg, rgba(14, 20, 31, 0.99) 0%, rgba(10, 15, 24, 0.99) 100%);
		box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.06);
	}

	.canvas-ai-panel-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		color: #e2ebfb;
		font-size: 0.9rem;
		font-weight: 600;
		letter-spacing: 0.01em;
	}

	.canvas-ai-panel-head-main {
		display: inline-flex;
		align-items: center;
		gap: 0.45rem;
		min-width: 0;
	}

	.canvas-ai-file-pill {
		display: inline-flex;
		align-items: center;
		max-width: min(38rem, 70vw);
		padding: 0.2rem 0.55rem;
		border-radius: 999px;
		border: 1px solid rgba(126, 160, 212, 0.55);
		background: rgba(40, 57, 84, 0.74);
		color: #d8e9ff;
		font-size: 0.7rem;
		font-weight: 600;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.canvas-ai-close {
		border: 1px solid rgba(101, 121, 151, 0.56);
		background: rgba(27, 37, 53, 0.9);
		color: #d6e5fd;
		border-radius: 0.44rem;
		width: 1.8rem;
		height: 1.8rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		padding: 0;
		font-size: 0.95rem;
		line-height: 1;
	}

	.canvas-ai-close:hover:not(:disabled) {
		border-color: rgba(138, 165, 208, 0.78);
		background: rgba(43, 58, 82, 0.94);
	}

	.canvas-ai-thread {
		min-height: 0;
		overflow-y: auto;
		display: grid;
		align-content: start;
		gap: 0.55rem;
		padding-right: 0.2rem;
		overscroll-behavior: contain;
	}

	.canvas-ai-empty {
		border: 1px dashed rgba(109, 133, 168, 0.58);
		border-radius: 0.56rem;
		background: rgba(24, 34, 49, 0.68);
		padding: 0.62rem 0.68rem;
		display: grid;
		gap: 0.4rem;
	}

	.canvas-ai-empty p {
		margin: 0;
		font-size: 0.82rem;
		line-height: 1.4;
		color: rgba(197, 214, 238, 0.9);
	}

	.canvas-ai-message {
		border: 1px solid rgba(98, 122, 160, 0.62);
		border-radius: 0.66rem;
		background: rgba(22, 31, 44, 0.9);
		padding: 0.62rem 0.72rem;
		display: grid;
		gap: 0.4rem;
	}

	.canvas-ai-message.user {
		border-color: rgba(106, 154, 220, 0.72);
		background: rgba(41, 60, 90, 0.72);
	}

	.canvas-ai-message-header {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.5rem;
	}

	.canvas-ai-message-header strong {
		font-size: 0.74rem;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: rgba(188, 208, 239, 0.92);
	}

	.canvas-ai-message-header time {
		font-size: 0.68rem;
		color: rgba(165, 183, 212, 0.8);
	}

	.canvas-ai-message-text {
		margin: 0;
		font-size: 0.88rem;
		line-height: 1.5;
		color: #e4eefc;
		white-space: pre-wrap;
		word-break: break-word;
	}

	.canvas-ai-code-block {
		border: 1px solid rgba(86, 109, 145, 0.64);
		border-radius: 0.5rem;
		background: rgba(13, 19, 30, 0.94);
		display: grid;
		gap: 0.42rem;
		padding: 0.48rem;
	}

	.canvas-ai-code-block.is-applied {
		border-color: rgba(82, 162, 118, 0.78);
		background: rgba(13, 33, 25, 0.9);
	}

	.canvas-ai-code-block.is-failed {
		border-color: rgba(188, 94, 94, 0.78);
		background: rgba(40, 17, 20, 0.9);
	}

	.canvas-ai-change-list {
		display: grid;
		gap: 0.5rem;
	}

	.canvas-ai-change-list-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.6rem;
		font-size: 0.72rem;
		font-weight: 600;
		color: rgba(198, 213, 237, 0.9);
	}

	.canvas-ai-change-headline {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 0.5rem;
		flex-wrap: wrap;
	}

	.canvas-ai-change-meta {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		min-width: 0;
	}

	.canvas-ai-change-file {
		font-size: 0.74rem;
		color: #e7f1ff;
		word-break: break-word;
	}

	.canvas-ai-change-chip {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		padding: 0.08rem 0.34rem;
		border-radius: 999px;
		border: 1px solid rgba(109, 142, 191, 0.62);
		background: rgba(36, 56, 86, 0.88);
		color: #d7e8ff;
		font-size: 0.6rem;
		letter-spacing: 0.03em;
	}

	.canvas-ai-change-location {
		font-size: 0.66rem;
		color: rgba(183, 201, 227, 0.9);
	}

	.canvas-ai-change-summary {
		margin: 0;
		font-size: 0.76rem;
		line-height: 1.4;
		color: rgba(218, 231, 250, 0.95);
	}

	.canvas-ai-change-error {
		font-size: 0.7rem;
		color: #ffd1d1;
		background: rgba(143, 43, 43, 0.42);
		border: 1px solid rgba(203, 113, 113, 0.52);
		border-radius: 0.34rem;
		padding: 0.28rem 0.4rem;
	}

	.canvas-ai-code {
		margin: 0;
		max-height: min(40vh, 360px);
		overflow: auto;
		font-size: 0.8rem;
		line-height: 1.42;
		color: #dbe9ff;
		font-family:
			'SFMono-Regular',
			Consolas,
			'Liberation Mono',
			Menlo,
			monospace;
		white-space: pre;
	}

	.canvas-ai-code-actions {
		display: flex;
		justify-content: flex-end;
	}

	.canvas-ai-input {
		width: 100%;
		min-height: 0;
		border: 1px solid rgba(113, 134, 168, 0.56);
		background: rgba(22, 31, 44, 0.92);
		color: #eaf2ff;
		border-radius: 0.62rem;
		padding: 0.62rem 0.74rem;
		font-size: 0.9rem;
		line-height: 1.42;
		resize: none;
		overflow-y: hidden;
		box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.025);
	}

	.canvas-ai-input::placeholder {
		color: rgba(172, 191, 224, 0.76);
	}

	.canvas-ai-input:focus {
		outline: none;
		border-color: rgba(130, 172, 243, 0.88);
		box-shadow: 0 0 0 2px rgba(117, 166, 248, 0.2);
	}

	.canvas-ai-error {
		font-size: 0.82rem;
		font-weight: 500;
		color: #ffd7d7;
		background: rgba(132, 33, 33, 0.44);
		border: 1px solid rgba(227, 134, 134, 0.52);
		border-radius: 0.52rem;
		padding: 0.42rem 0.58rem;
	}

	.canvas-ai-actions {
		display: flex;
		align-items: center;
		justify-content: flex-end;
		gap: 0.5rem;
		padding-top: 0.08rem;
		flex-wrap: wrap;
	}

	.canvas-ai-action {
		border: 1px solid rgba(104, 126, 157, 0.62);
		border-radius: 0.45rem;
		padding: 0.5rem 0.86rem;
		font-size: 0.8rem;
		font-weight: 600;
		cursor: pointer;
		line-height: 1.16;
	}

	.canvas-ai-action.secondary {
		background: rgba(30, 40, 57, 0.9);
		color: #d5e2f7;
	}

	.canvas-ai-action.primary {
		border-color: rgba(81, 119, 177, 0.8);
		background: linear-gradient(180deg, rgba(57, 89, 137, 0.98) 0%, rgba(45, 73, 116, 0.98) 100%);
		color: #f2f8ff;
	}

	.canvas-ai-action:hover:not(:disabled) {
		border-color: rgba(139, 164, 205, 0.78);
		background: rgba(43, 58, 80, 0.95);
	}

	.canvas-ai-action.primary:hover:not(:disabled) {
		border-color: rgba(121, 161, 224, 0.88);
		background: linear-gradient(180deg, rgba(67, 103, 159, 0.98) 0%, rgba(52, 83, 131, 0.98) 100%);
	}

	.canvas-ai-action:disabled {
		opacity: 0.56;
		cursor: not-allowed;
	}

	@media (max-width: 900px) {
		.canvas-ai-panel {
			padding:
				max(0.72rem, env(safe-area-inset-top))
				max(0.72rem, env(safe-area-inset-right))
				max(0.72rem, env(safe-area-inset-bottom))
				max(0.72rem, env(safe-area-inset-left));
			gap: 0.56rem;
		}

		.canvas-ai-panel-header {
			font-size: 0.82rem;
		}

		.canvas-ai-file-pill {
			font-size: 0.66rem;
		}

		.canvas-ai-message {
			padding: 0.56rem 0.62rem;
		}

		.canvas-ai-message-text {
			font-size: 0.8rem;
		}

		.canvas-ai-input {
			font-size: 0.82rem;
		}

		.canvas-ai-action {
			font-size: 0.74rem;
			padding: 0.42rem 0.72rem;
		}
	}

	.snippet-composer-overlay {
		position: fixed;
		inset: 0;
		z-index: 10060;
		display: flex;
		align-items: flex-start;
		justify-content: center;
		padding: max(0.85rem, env(safe-area-inset-top)) 1rem max(0.85rem, env(safe-area-inset-bottom));
		overflow-y: auto;
		background: rgba(5, 9, 15, 0.62);
		backdrop-filter: blur(6px);
	}

	.snippet-composer-modal {
		width: min(42rem, 96%);
		max-height: min(92vh, 44rem);
		margin: auto;
		display: flex;
		flex-direction: column;
		gap: 0.85rem;
		padding: 0.95rem;
		border-radius: 0.8rem;
		border: 1px solid rgba(113, 136, 176, 0.48);
		background: linear-gradient(180deg, rgba(19, 27, 40, 0.98) 0%, rgba(12, 18, 30, 0.98) 100%);
		box-shadow: 0 20px 48px rgba(0, 0, 0, 0.45);
	}

	.snippet-composer-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}

	.snippet-composer-header h3 {
		margin: 0;
		font-size: 0.95rem;
		font-weight: 700;
		color: #e8f0ff;
		letter-spacing: 0.01em;
	}

	.snippet-composer-close {
		border: 1px solid rgba(120, 137, 165, 0.5);
		background: rgba(24, 35, 52, 0.88);
		color: #dbe6f8;
		border-radius: 0.4rem;
		width: 1.8rem;
		height: 1.8rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		padding: 0;
	}

	.snippet-composer-close:hover {
		border-color: rgba(185, 124, 124, 0.72);
		background: rgba(95, 36, 36, 0.72);
		color: #ffe8e8;
	}

	.snippet-composer-close svg {
		width: 0.88rem;
		height: 0.88rem;
		stroke: currentColor;
		stroke-width: 2;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.snippet-preview-wrap {
		border: 1px solid rgba(83, 107, 144, 0.55);
		border-radius: 0.6rem;
		overflow: hidden;
		background: #1e1e1e;
	}

	.snippet-preview {
		margin: 0;
		padding: 0.8rem 0.85rem;
		max-height: 200px;
		overflow-y: auto;
		color: #d4d4d4;
		font-size: 0.79rem;
		line-height: 1.45;
		font-family:
			'SFMono-Regular',
			Consolas,
			'Liberation Mono',
			Menlo,
			monospace;
		white-space: pre-wrap;
		word-break: break-word;
	}

	.snippet-message-wrap {
		display: flex;
	}

	.snippet-message-input {
		width: 100%;
		min-height: 4.8rem;
		border: 1px solid rgba(103, 125, 160, 0.58);
		background: rgba(16, 24, 37, 0.9);
		color: #dbe6f8;
		border-radius: 0.55rem;
		padding: 0.62rem 0.7rem;
		font-size: 0.8rem;
		line-height: 1.4;
		resize: vertical;
	}

	.snippet-message-input:focus {
		outline: none;
		border-color: rgba(117, 166, 248, 0.78);
		box-shadow: 0 0 0 2px rgba(117, 166, 248, 0.25);
	}

	.snippet-composer-footer {
		display: flex;
		align-items: center;
		justify-content: flex-end;
		gap: 0.5rem;
	}

	.snippet-button {
		border: 1px solid rgba(103, 125, 160, 0.52);
		border-radius: 0.45rem;
		padding: 0.38rem 0.78rem;
		font-size: 0.74rem;
		font-weight: 600;
		cursor: pointer;
	}

	.snippet-button.secondary {
		background: rgba(24, 35, 52, 0.88);
		color: #dbe6f8;
	}

	.snippet-button.primary {
		border-color: rgba(72, 187, 120, 0.76);
		background: rgba(17, 112, 80, 0.56);
		color: #ecfff4;
	}

	.snippet-button:hover:not(:disabled) {
		border-color: rgba(139, 168, 211, 0.72);
		background: rgba(38, 61, 96, 0.92);
	}

	.snippet-button.primary:hover:not(:disabled) {
		border-color: rgba(104, 211, 145, 0.82);
		background: rgba(20, 140, 92, 0.62);
	}

	.snippet-button:disabled {
		opacity: 0.55;
		cursor: not-allowed;
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

	.terminal-panel.is-collapsed {
		min-height: 0;
		height: auto !important;
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

	.terminal-header-right {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
		min-width: 0;
	}

	.terminal-action-group {
		display: inline-flex;
		align-items: center;
		gap: 0.34rem;
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
		display: inline-flex;
		align-items: center;
		gap: 0.28rem;
	}

	.terminal-action-button svg {
		width: 0.72rem;
		height: 0.72rem;
		stroke: currentColor;
		stroke-width: 1.9;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.terminal-action-button:disabled {
		opacity: 0.56;
		cursor: not-allowed;
	}

	.terminal-action-button:hover {
		border-color: rgba(139, 168, 211, 0.68);
		background: rgba(41, 61, 92, 0.92);
	}

	.terminal-action-button:disabled:hover {
		border-color: rgba(103, 125, 160, 0.52);
		background: rgba(24, 35, 52, 0.88);
	}

	.terminal-action-run {
		border-color: rgba(72, 187, 120, 0.7);
		background: rgba(16, 111, 78, 0.42);
	}

	.terminal-action-snippet {
		border-color: rgba(104, 211, 145, 0.72);
		background: rgba(18, 116, 84, 0.45);
	}

	.terminal-action-stop {
		border-color: rgba(239, 68, 68, 0.72);
		background: rgba(127, 29, 29, 0.45);
	}

	.terminal-collapse-button {
		padding: 0.22rem 0.38rem;
	}

	.terminal-body {
		display: flex;
		flex: 1;
		min-height: 0;
		flex-direction: column;
	}

	.terminal-body.is-hidden {
		display: none;
	}

	.terminal-tabs {
		display: inline-flex;
		align-items: center;
		gap: 0.28rem;
		padding: 0.38rem 0.72rem 0.2rem;
		border-bottom: 1px solid rgba(58, 73, 98, 0.48);
		background: rgba(11, 16, 25, 0.72);
	}

	.terminal-tab-button {
		border: 1px solid rgba(96, 117, 149, 0.45);
		background: rgba(23, 34, 51, 0.82);
		color: #c6d4ea;
		border-radius: 0.38rem;
		padding: 0.2rem 0.58rem;
		font-size: 0.66rem;
		font-weight: 700;
		cursor: pointer;
		text-transform: uppercase;
		letter-spacing: 0.02em;
	}

	.terminal-tab-button.is-active {
		border-color: rgba(110, 184, 255, 0.72);
		background: rgba(34, 82, 133, 0.72);
		color: #ecf6ff;
	}

	.terminal-tab-panel {
		display: none;
		flex: 1;
		min-height: 0;
	}

	.terminal-tab-panel.is-active {
		display: flex;
	}

	.terminal-tab-panel-in {
		padding: 0.65rem 0.72rem 0.72rem;
	}

	.terminal-input-area {
		width: 100%;
		height: 100%;
		min-height: 88px;
		border: 1px solid rgba(83, 109, 145, 0.65);
		border-radius: 0.5rem;
		background: rgba(10, 16, 24, 0.95);
		color: #dbe6f8;
		font-size: 0.78rem;
		line-height: 1.42;
		padding: 0.56rem 0.62rem;
		resize: none;
		font-family:
			'SFMono-Regular',
			Consolas,
			'Liberation Mono',
			Menlo,
			monospace;
	}

	.terminal-input-area:focus {
		outline: none;
		border-color: rgba(110, 184, 255, 0.78);
		box-shadow: 0 0 0 2px rgba(110, 184, 255, 0.22);
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

		.canvas-side-region {
			width: 100%;
			flex: 1 1 auto;
			max-height: none;
			border-right: none;
			border-bottom: none;
		}

		.canvas-activity-bar {
			width: 40px;
			flex: 0 0 40px;
			flex-direction: column;
			justify-content: flex-start;
			border-right: 1px solid rgba(120, 134, 160, 0.3);
			border-bottom: none;
			padding: 0.5rem 0.28rem;
		}

		.canvas-shell.show-mobile-explorer .canvas-editor {
			display: none;
		}

		.canvas-shell.show-mobile-editor .canvas-side-region {
			display: none;
		}

		.canvas-shell.show-mobile-explorer .canvas-side-region,
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
