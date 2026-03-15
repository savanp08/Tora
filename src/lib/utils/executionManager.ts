import { executeNodeWorkspace } from './webcontainer';

const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';
const EXECUTE_ENDPOINT = `${API_BASE}/api/execute`;

type PythonWorkerCommand = 'SYNC_WORKSPACE' | 'RUN_CODE';
type ExecutionArtifact = {
	name: string;
	content: string;
};

type WorkerInboundMessage = {
	id: string;
	code?: string;
	command?: PythonWorkerCommand;
	main_file?: string;
	files?: ExecutionWorkspaceFile[] | Record<string, string>;
};

type WorkerOutboundMessage = {
	id: string;
	command?: PythonWorkerCommand;
	phase?: 'synced';
	status: 'running' | 'success' | 'error';
	stream?: 'stdout' | 'stderr';
	output?: string;
	error?: string;
	stdout?: string;
	stderr?: string;
	artifacts?: ExecutionArtifact[];
};

type WorkerFactory = () => Promise<Worker>;

type RemoteExecutionPayload = {
	stdout: string;
	stderr: string;
	artifacts?: ExecutionArtifact[];
};

type WorkerExecutionStrategy = {
	mode: 'worker';
	language: string;
	aliases: string[];
	workerFactory: WorkerFactory;
};

type RemoteExecutionStrategy = {
	mode: 'remote';
	language: string;
	aliases: string[];
	runRemote: (
		language: string,
		signal: AbortSignal,
		stdin: string,
		workspace: NormalizedExecutionWorkspace,
		requestOptions?: RemoteExecutionRequestOptions
	) => Promise<RemoteExecutionPayload>;
};

type ExecutionStrategy = WorkerExecutionStrategy | RemoteExecutionStrategy;

type RunContext = {
	id: string;
	language: string;
	runtimeLanguage: string;
	strategyMode: 'worker' | 'remote';
	resolve: (value: ExecutionRunResult) => void;
	reject: (reason: Error) => void;
	timeoutId: ReturnType<typeof setTimeout> | null;
	abortController: AbortController | null;
	subscribers: Set<ExecutionOutputCallback>;
	outputBufferByStream: {
		stdout: string;
		stderr: string;
	};
	onArtifacts?: (files: ExecutionArtifact[]) => void;
	settled: boolean;
};

type WorkerRuntime = {
	worker: Worker;
	onMessage: (event: MessageEvent<WorkerOutboundMessage>) => void;
	onError: (event: ErrorEvent) => void;
};

export type ExecutionOutputLine = {
	id: string;
	language: string;
	line: string;
	stream: 'stdout' | 'stderr';
	status: 'running' | 'success' | 'error';
};

export type ExecutionOutputCallback = (line: ExecutionOutputLine) => void;

export type ExecutionRunResult = {
	id: string;
	language: string;
	status: 'success';
};

export type ExecutionRunHandle = {
	id: string;
	language: string;
	result: Promise<ExecutionRunResult>;
	subscribe: (callback: ExecutionOutputCallback) => () => void;
	cancel: () => void;
};

export type ExecutionWorkspaceFile = ExecutionArtifact;
export type ExecutionArtifactsCallback = (files: ExecutionWorkspaceFile[]) => void;
export type ExecutionRequestHeaders = Record<string, string>;

export type ExecutionWorkspaceInput = {
	activeFile: string;
	workspaceFiles: ExecutionWorkspaceFile[];
	onArtifacts?: ExecutionArtifactsCallback;
	endpoint?: string;
	requestHeaders?: ExecutionRequestHeaders;
};

type RemoteExecutionRequestOptions = {
	endpoint?: string;
	requestHeaders?: ExecutionRequestHeaders;
};

type NormalizedExecutionWorkspace = {
	mainFile: string;
	files: ExecutionWorkspaceFile[];
};

type RemoteExecutionRequest = {
	language: string;
	stdin: string;
	main_file: string;
	files: Array<{
		name: string;
		content: string;
	}>;
};

class RemoteExecutionError extends Error {
	readonly stdout: string;
	readonly stderr: string;

	constructor(message: string, stdout = '', stderr = '') {
		super(message);
		this.name = 'RemoteExecutionError';
		this.stdout = stdout;
		this.stderr = stderr;
	}
}

type RoutedExecutionResult = {
	output: string;
	error: string;
};

function createExecutionId() {
	if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
		return crypto.randomUUID();
	}
	return `exec-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function createAbortError() {
	const error = new Error('Execution aborted') as Error & { name: string };
	error.name = 'AbortError';
	return error;
}

function isCrossOriginIsolatedPage() {
	return typeof crossOriginIsolated === 'boolean' && crossOriginIsolated;
}

function usesNodeRuntimeSyntax(source: string) {
	const text = String(source || '');
	if (!text) {
		return false;
	}
	return (
		/\brequire\s*\(/.test(text) ||
		/\bprocess\b/.test(text) ||
		/\b__dirname\b/.test(text) ||
		/\b__filename\b/.test(text) ||
		/\bnode:/.test(text) ||
		/\bfrom\s+['"](?:node:)?(?:fs|path|os|crypto|child_process|http|https|url|util)['"]/.test(text)
	);
}

function crossOriginIsolationRequiredError() {
	return new Error(
		'Node-style JavaScript execution requires WebContainer, but this page is not cross-origin isolated (`crossOriginIsolated=false`). Use COOP/COEP headers, open the app directly (not in an iframe), use localhost or HTTPS, then restart dev server and hard refresh.'
	);
}

function resolveMainWorkspaceSource(workspace: NormalizedExecutionWorkspace, fallbackCode = '') {
	const mainFile = workspace.mainFile;
	const entry = workspace.files.find((file) => file.name === mainFile);
	if (entry) {
		return String(entry.content ?? '');
	}
	return String(fallbackCode ?? '');
}

async function executeJavaScriptWorkerFallback(
	code: string,
	options?: {
		signal?: AbortSignal;
	}
) {
	const runId = createExecutionId();
	const { default: JavaScriptWorker } = await import('$lib/workers/javascript.worker?worker');
	const worker = new JavaScriptWorker();
	let stdout = '';
	let stderr = '';

	return await new Promise<RemoteExecutionPayload>((resolve, reject) => {
		const signal = options?.signal;
		const cleanup = () => {
			worker.removeEventListener('message', onMessage);
			worker.removeEventListener('error', onError);
			signal?.removeEventListener('abort', onAbort);
			worker.terminate();
		};
		const onAbort = () => {
			cleanup();
			reject(createAbortError());
		};
		const onError = (event: ErrorEvent) => {
			cleanup();
			reject(new Error(event.message || 'JavaScript worker crashed'));
		};
		const onMessage = (event: MessageEvent<WorkerOutboundMessage>) => {
			const payload = event.data;
			if (!payload || payload.id !== runId) {
				return;
			}
			if (payload.status === 'running' && payload.output) {
				if (payload.stream === 'stderr') {
					stderr += payload.output;
				} else {
					stdout += payload.output;
				}
				return;
			}
			if (payload.status === 'success') {
				cleanup();
				resolve({
					stdout,
					stderr
				});
				return;
			}
			if (payload.status === 'error') {
				const message =
					(payload.error || stderr || 'JavaScript execution failed in fallback runtime').trim();
				cleanup();
				reject(new RemoteExecutionError(message, stdout, stderr));
			}
		};

		worker.addEventListener('message', onMessage);
		worker.addEventListener('error', onError);
		if (signal) {
			if (signal.aborted) {
				onAbort();
				return;
			}
			signal.addEventListener('abort', onAbort, { once: true });
		}
		worker.postMessage({
			id: runId,
			code: String(code ?? '')
		});
	});
}

function encodeCodeAsBase64(code: string) {
	const text = String(code ?? '');
	const bytes = new TextEncoder().encode(text);
	let binary = '';
	const chunkSize = 0x8000;
	for (let index = 0; index < bytes.length; index += chunkSize) {
		const chunk = bytes.subarray(index, index + chunkSize);
		binary += String.fromCharCode(...chunk);
	}
	return btoa(binary);
}

function defaultSourceFilename(language: string) {
	switch ((language || '').trim().toLowerCase()) {
		case 'java':
			return 'Main.java';
		case 'cpp':
		case 'c++':
			return 'main.cpp';
		case 'c':
			return 'main.c';
		case 'go':
		case 'golang':
			return 'main.go';
		case 'rust':
		case 'rs':
			return 'main.rs';
		case 'python':
		case 'py':
			return 'main.py';
		case 'typescript':
		case 'ts':
			return 'main.ts';
		case 'javascript':
		case 'js':
		case 'node':
		case 'mjs':
		case 'cjs':
			return 'main.js';
		default:
			return 'main.txt';
	}
}

function ensureMainFileFirst(files: ExecutionWorkspaceFile[], mainFile: string) {
	const mainIndex = files.findIndex((file) => file.name === mainFile);
	if (mainIndex <= 0) {
		return files;
	}
	const ordered = files.slice();
	const [mainEntry] = ordered.splice(mainIndex, 1);
	ordered.unshift(mainEntry);
	return ordered;
}

function extensionOfFileName(name: string) {
	const normalizedName = String(name || '').trim().toLowerCase();
	const lastDotIndex = normalizedName.lastIndexOf('.');
	if (lastDotIndex <= 0 || lastDotIndex === normalizedName.length - 1) {
		return '';
	}
	return normalizedName.slice(lastDotIndex);
}

function shouldIncludeFileForLanguage(fileName: string, normalizedLanguage: string) {
	const extension = extensionOfFileName(fileName);
	if (!extension) {
		return true;
	}

	const cFamilyLanguages = new Set(['cpp', 'c++', 'c']);
	const jsFamilyLanguages = new Set(['javascript', 'js', 'node']);
	const pythonFamilyLanguages = new Set(['python', 'py']);
	const javaLanguages = new Set(['java']);

	if (cFamilyLanguages.has(normalizedLanguage)) {
		return !new Set(['.js', '.ts', '.py', '.java', '.go']).has(extension);
	}
	if (jsFamilyLanguages.has(normalizedLanguage)) {
		return !new Set(['.cpp', '.c', '.py', '.java', '.go']).has(extension);
	}
	if (pythonFamilyLanguages.has(normalizedLanguage)) {
		return !new Set(['.cpp', '.c', '.js', '.ts', '.java', '.go']).has(extension);
	}
	if (javaLanguages.has(normalizedLanguage)) {
		return !new Set(['.cpp', '.c', '.h', '.hpp', '.js', '.ts', '.py', '.go']).has(extension);
	}
	return true;
}

function parseRemoteExecutionArtifacts(payload: Record<string, unknown> | null) {
	if (!payload || !Array.isArray(payload.files)) {
		return [] as ExecutionArtifact[];
	}
	return payload.files
		.map((entry) => {
			if (!entry || typeof entry !== 'object') {
				return null;
			}
			const file = entry as Record<string, unknown>;
			const name = typeof file.name === 'string' ? file.name.trim() : '';
			if (!name) {
				return null;
			}
			return {
				name,
				content: typeof file.content === 'string' ? file.content : String(file.content ?? '')
			} satisfies ExecutionArtifact;
		})
		.filter((file): file is ExecutionArtifact => Boolean(file));
}

function resolveExecutionWorkspace(
	language: string,
	code: string,
	input?: ExecutionWorkspaceInput
): NormalizedExecutionWorkspace {
	const normalizedLanguage = (language || '').trim().toLowerCase();
	const defaultMainFile = defaultSourceFilename(normalizedLanguage);
	const requestedMainFile = (input?.activeFile || '').trim();
	const mainFile = requestedMainFile || defaultMainFile;
	const nextFiles = (input?.workspaceFiles || [])
		.map((file) => ({
			name: (file?.name || '').trim(),
			content: String(file?.content ?? '')
		}))
		.filter((file) => file.name.length > 0);

	const normalizedCode = String(code ?? '');
	const existingMainFileIndex = nextFiles.findIndex((file) => file.name === mainFile);
	if (existingMainFileIndex >= 0) {
		nextFiles[existingMainFileIndex] = {
			name: mainFile,
			content: normalizedCode
		};
	} else {
		nextFiles.unshift({
			name: mainFile,
			content: normalizedCode
		});
	}

	return {
		mainFile,
		files: ensureMainFileFirst(nextFiles, mainFile)
	};
}

export function buildExecutionPayload(
	language: string,
	stdin: string,
	activeFile: string,
	workspaceFiles: ExecutionWorkspaceFile[]
): RemoteExecutionRequest {
	const normalizedLanguage = (language || '').trim().toLowerCase();
	const normalizedMainFile = (activeFile || '').trim() || defaultSourceFilename(normalizedLanguage);
	const slashIndex = normalizedMainFile.lastIndexOf('/');
	const mainFileDir = slashIndex > 0 ? normalizedMainFile.slice(0, slashIndex) : '';
	const normalizedWorkspaceFiles = (workspaceFiles || [])
		.map((file) => {
			let name = (file?.name || '').trim();
			if (mainFileDir && name.startsWith(`${mainFileDir}/`)) {
				name = name.slice(mainFileDir.length + 1);
			}
			return {
				name,
				content: String(file?.content ?? '')
			};
		})
		.filter(
			(file) =>
				file.name.length > 0 && shouldIncludeFileForLanguage(file.name, normalizedLanguage)
		);
	const finalMainFile = mainFileDir
		? normalizedMainFile.slice(mainFileDir.length + 1)
		: normalizedMainFile;

	const workspaceWithMain = ensureMainFileFirst(
		normalizedWorkspaceFiles.some((file) => file.name === finalMainFile)
			? normalizedWorkspaceFiles
			: [{ name: finalMainFile, content: '' }, ...normalizedWorkspaceFiles],
		finalMainFile
	);

	return {
		language: normalizedLanguage,
		stdin: String(stdin ?? ''),
		main_file: finalMainFile,
		files: workspaceWithMain.map((file) => ({
			name: file.name,
			content: encodeCodeAsBase64(file.content)
		}))
	};
}

type ExecutePythonWorkspaceOptions = {
	signal?: AbortSignal;
	executionId?: string;
	onOutput?: (payload: { stream: 'stdout' | 'stderr'; output: string }) => void;
	onArtifacts?: ExecutionArtifactsCallback;
};

export async function executePythonWorkspace(
	worker: Worker,
	files: Record<string, string>,
	activeFile: string,
	options?: ExecutePythonWorkspaceOptions
): Promise<RemoteExecutionPayload> {
	const runId = options?.executionId || createExecutionId();
	const normalizedActiveFile = (activeFile || '').trim();
	if (!normalizedActiveFile) {
		throw new Error('activeFile is required for Python workspace execution');
	}

	let stdout = '';
	let stderr = '';
	return await new Promise<RemoteExecutionPayload>((resolve, reject) => {
		const signal = options?.signal;
		let runTriggered = false;

		const cleanup = () => {
			worker.removeEventListener('message', onMessage);
			worker.removeEventListener('error', onError);
			signal?.removeEventListener('abort', onAbort);
		};
		const finishResolve = (payload: RemoteExecutionPayload) => {
			cleanup();
			resolve(payload);
		};
		const finishReject = (error: Error) => {
			cleanup();
			reject(error);
		};

		const onMessage = (event: MessageEvent<WorkerOutboundMessage>) => {
			const payload = event.data;
			if (!payload || payload.id !== runId) {
				return;
			}

			if (payload.status === 'running' && payload.output) {
				const stream: 'stdout' | 'stderr' = payload.stream === 'stderr' ? 'stderr' : 'stdout';
				if (stream === 'stderr') {
					stderr += payload.output;
				} else {
					stdout += payload.output;
				}
				options?.onOutput?.({
					stream,
					output: payload.output
				});
			}

			if (
				payload.command === 'SYNC_WORKSPACE' &&
				payload.phase === 'synced' &&
				payload.status === 'running'
			) {
				if (runTriggered) {
					return;
				}
				runTriggered = true;
				worker.postMessage({
					id: runId,
					command: 'RUN_CODE',
					main_file: normalizedActiveFile
				} satisfies WorkerInboundMessage);
				return;
			}

			if (payload.status === 'success') {
				const artifacts =
					Array.isArray(payload.artifacts) && payload.artifacts.length > 0
						? payload.artifacts
								.map((file) => ({
									name: (file?.name || '').trim(),
									content: String(file?.content ?? '')
								}))
								.filter((file) => file.name.length > 0)
						: [];
				if (artifacts.length > 0 && options?.onArtifacts) {
					try {
						options.onArtifacts(artifacts);
					} catch {
						// Ignore artifact callback errors and allow execution response to resolve.
					}
				}
				finishResolve({
					stdout: payload.stdout ?? stdout,
					stderr: payload.stderr ?? stderr
				});
				return;
			}

			if (payload.status === 'error') {
				const fallbackMessage = stderr || payload.error || 'Python execution failed';
				finishReject(new Error((fallbackMessage || 'Python execution failed').trim()));
			}
		};
		const onError = (event: ErrorEvent) => {
			event.preventDefault?.();
			const messageFromError =
				typeof event.error === 'object' &&
				event.error !== null &&
				'message' in event.error &&
				typeof (event.error as { message?: unknown }).message === 'string'
					? (event.error as { message: string }).message
					: '';
			const message = event.message || messageFromError || 'Python worker crashed';
			finishReject(new Error(message));
		};
		const onAbort = () => {
			finishReject(new Error('Execution aborted'));
		};

		worker.addEventListener('message', onMessage);
		worker.addEventListener('error', onError);
		if (signal) {
			if (signal.aborted) {
				onAbort();
				return;
			}
			signal.addEventListener('abort', onAbort, { once: true });
		}

		worker.postMessage({
			id: runId,
			command: 'SYNC_WORKSPACE',
			files
		} satisfies WorkerInboundMessage);
	});
}

export async function executeCodeWithRouter(
	code: string,
	language: string,
	options?: {
		pythonWorker?: Worker;
		signal?: AbortSignal;
		endpoint?: string;
		requestHeaders?: ExecutionRequestHeaders;
		stdin?: string;
		activeFile?: string;
		workspaceFiles?: ExecutionWorkspaceFile[];
		onArtifacts?: ExecutionArtifactsCallback;
	}
): Promise<RoutedExecutionResult> {
	const normalizedLanguage = (language || '').trim().toLowerCase();
	const signal = options?.signal;
	const workspace = resolveExecutionWorkspace(normalizedLanguage, code, {
		activeFile: options?.activeFile || '',
		workspaceFiles: options?.workspaceFiles || []
	});
	if (normalizedLanguage === 'python' || normalizedLanguage === 'py') {
		let worker = options?.pythonWorker ?? null;
		let ownsWorker = false;
		if (!worker) {
			worker = new Worker(new URL('../workers/pyodide.worker.ts', import.meta.url), {
				type: 'module'
			});
			ownsWorker = true;
		}
		if (!worker) {
			throw new Error('Python worker is not available');
		}
		try {
			const workspaceFileMap = Object.fromEntries(
				workspace.files.map((file) => [file.name, file.content])
			);
			const result = await executePythonWorkspace(worker, workspaceFileMap, workspace.mainFile, {
				signal,
				onArtifacts: options?.onArtifacts
			});
			return {
				output: result.stdout,
				error: result.stderr
			};
		} finally {
			if (ownsWorker) {
				worker.terminate();
			}
		}
	}

	if (
		normalizedLanguage === 'javascript' ||
		normalizedLanguage === 'js' ||
		normalizedLanguage === 'node' ||
		normalizedLanguage === 'mjs' ||
		normalizedLanguage === 'cjs'
	) {
		if (signal?.aborted) {
			throw createAbortError();
		}
		if (!isCrossOriginIsolatedPage()) {
			const fallbackCode = resolveMainWorkspaceSource(workspace, code);
			if (usesNodeRuntimeSyntax(fallbackCode)) {
				throw crossOriginIsolationRequiredError();
			}
			const fallbackResult = await executeJavaScriptWorkerFallback(fallbackCode, { signal });
			return {
				output: fallbackResult.stdout,
				error: fallbackResult.stderr
			};
		}
		const result = await executeNodeWorkspace(workspace.files, workspace.mainFile);
		if (signal?.aborted) {
			throw createAbortError();
		}
		if (result.exitCode !== 0) {
			const failureMessage =
				result.stderr.trim() || `JavaScript execution failed (exit code ${result.exitCode})`;
			throw new RemoteExecutionError(failureMessage, result.stdout, result.stderr);
		}
		if (result.artifacts.length > 0 && options?.onArtifacts) {
			try {
				options.onArtifacts(result.artifacts);
			} catch {
				// Ignore artifact callback failures and keep the execution result.
			}
		}
		return {
			output: result.stdout,
			error: result.stderr
		};
	}

	if (
		normalizedLanguage === 'cpp' ||
		normalizedLanguage === 'c++' ||
		normalizedLanguage === 'c' ||
		normalizedLanguage === 'java' ||
		normalizedLanguage === 'go' ||
		normalizedLanguage === 'golang' ||
		normalizedLanguage === 'rust' ||
		normalizedLanguage === 'rs'
	) {
		const endpoint = (options?.endpoint || EXECUTE_ENDPOINT).trim() || EXECUTE_ENDPOINT;
		const requestHeaders: Record<string, string> = {
			'Content-Type': 'application/json',
			...(options?.requestHeaders || {})
		};
		const requestPayload = buildExecutionPayload(
			normalizedLanguage,
			options?.stdin || '',
			workspace.mainFile,
			workspace.files
		);
		const response = await fetch(endpoint, {
			method: 'POST',
			headers: requestHeaders,
			body: JSON.stringify(requestPayload),
			signal
		});
		const payload = (await response.json().catch(() => null)) as Record<string, unknown> | null;
		const stdout = typeof payload?.stdout === 'string' ? payload.stdout : '';
		const stderr = typeof payload?.stderr === 'string' ? payload.stderr : '';
		const artifacts = parseRemoteExecutionArtifacts(payload);
		if (!response.ok) {
			const errorMessage =
				typeof payload?.error === 'string' && payload.error.trim()
					? payload.error.trim()
					: `Execution request failed (${response.status})`;
			throw new Error(errorMessage);
		}
		if (artifacts.length > 0 && options?.onArtifacts) {
			try {
				options.onArtifacts(artifacts);
			} catch {
				// Ignore artifact callback errors and keep execution output flowing.
			}
		}
		return {
			output: stdout,
			error: stderr
		};
	}

	throw new Error(`Unsupported language "${language}"`);
}

export class ExecutionManager {
	private readonly strategies = new Map<string, ExecutionStrategy>();
	private readonly strategyByLanguage = new Map<string, ExecutionStrategy>();
	private readonly workerRuntimeByLanguage = new Map<string, WorkerRuntime>();
	private readonly activeRunById = new Map<string, RunContext>();
	private readonly activeRunIdByRuntimeLanguage = new Map<string, string>();

	constructor() {
		const pythonStrategy: WorkerExecutionStrategy = {
			mode: 'worker',
			language: 'python',
			aliases: ['py'],
			workerFactory: async () =>
				new Worker(new URL('../workers/pyodide.worker.ts', import.meta.url), {
					type: 'module'
				})
		};
		const webcontainerStrategy: RemoteExecutionStrategy = {
			mode: 'remote',
			language: 'javascript',
			aliases: ['js', 'node', 'mjs', 'cjs'],
			runRemote: (_language, signal, _stdin, workspace) =>
				this.runJavaScriptWorkspace(signal, workspace)
		};

		const remoteStrategies: RemoteExecutionStrategy[] = [
			{
				mode: 'remote',
				language: 'cpp',
				aliases: ['c++'],
				runRemote: (language, signal, stdin, workspace, requestOptions) =>
					this.runRemote(language, signal, stdin, workspace, requestOptions)
			},
			{
				mode: 'remote',
				language: 'c',
				aliases: [],
				runRemote: (language, signal, stdin, workspace, requestOptions) =>
					this.runRemote(language, signal, stdin, workspace, requestOptions)
			},
			{
				mode: 'remote',
				language: 'java',
				aliases: [],
				runRemote: (language, signal, stdin, workspace, requestOptions) =>
					this.runRemote(language, signal, stdin, workspace, requestOptions)
			},
			{
				mode: 'remote',
				language: 'go',
				aliases: ['golang'],
				runRemote: (language, signal, stdin, workspace, requestOptions) =>
					this.runRemote(language, signal, stdin, workspace, requestOptions)
			},
			{
				mode: 'remote',
				language: 'rust',
				aliases: ['rs'],
				runRemote: (language, signal, stdin, workspace, requestOptions) =>
					this.runRemote(language, signal, stdin, workspace, requestOptions)
			}
		];

		this.registerStrategy(pythonStrategy);
		this.registerStrategy(webcontainerStrategy);
		for (const strategy of remoteStrategies) {
			this.registerStrategy(strategy);
		}
	}

	getSupportedLanguages() {
		return new Map(this.strategies);
	}

	async run(
		language: string,
		code: string,
		timeoutMs = 5000,
		stdin = '',
		workspaceInput?: ExecutionWorkspaceInput
	): Promise<ExecutionRunHandle> {
		const normalizedLanguage = this.normalizeLanguage(language);
		const strategy = this.strategyByLanguage.get(normalizedLanguage);
		if (!strategy) {
			throw new Error(
				`Unsupported language "${language}". Supported: ${[...this.strategies.keys()].join(', ')}`
			);
		}
		const runtimeLanguage = strategy.language;
		const workspace = resolveExecutionWorkspace(normalizedLanguage, code, workspaceInput);
		if (this.activeRunIdByRuntimeLanguage.has(runtimeLanguage)) {
			throw new Error(
				`Execution for ${runtimeLanguage} is already in progress. Wait for it to finish or cancel it first.`
			);
		}

		let worker: Worker | null = null;
		if (strategy.mode === 'worker') {
			worker = await this.ensureWorker(runtimeLanguage, strategy);
		}

		const executionId = createExecutionId();

		let resolveResult: (value: ExecutionRunResult) => void = () => {};
		let rejectResult: (reason: Error) => void = () => {};
		const result = new Promise<ExecutionRunResult>((resolve, reject) => {
			resolveResult = resolve;
			rejectResult = reject;
		});
		const context: RunContext = {
			id: executionId,
			language: strategy.language,
			runtimeLanguage,
			strategyMode: strategy.mode,
			resolve: resolveResult,
			reject: rejectResult,
			timeoutId: null,
			abortController: null,
			subscribers: new Set<ExecutionOutputCallback>(),
			outputBufferByStream: {
				stdout: '',
				stderr: ''
			},
			onArtifacts: workspaceInput?.onArtifacts,
			settled: false
		};

		context.timeoutId = setTimeout(() => {
			this.timeoutRun(context, timeoutMs);
		}, Math.max(1, timeoutMs));

		this.activeRunById.set(executionId, context);
		this.activeRunIdByRuntimeLanguage.set(runtimeLanguage, executionId);

		if (strategy.mode === 'worker') {
			if (runtimeLanguage === 'python' && worker) {
				this.startPythonWorkspaceRun(worker, executionId, workspace);
			} else {
				worker?.postMessage({
					code,
					id: executionId
				} satisfies WorkerInboundMessage);
			}
		} else {
			const abortController = new AbortController();
			context.abortController = abortController;
			void this.executeRemoteRun(
				context,
				strategy,
				normalizedLanguage,
				abortController.signal,
				stdin,
				workspace,
				{
					endpoint: workspaceInput?.endpoint,
					requestHeaders: workspaceInput?.requestHeaders
				}
			);
		}

		return {
			id: executionId,
			language: strategy.language,
			result,
			subscribe: (callback: ExecutionOutputCallback) => {
				context.subscribers.add(callback);
				return () => {
					context.subscribers.delete(callback);
				};
			},
			cancel: () => {
				this.timeoutRun(context, timeoutMs, true);
			}
		};
	}

	stop(executionId: string) {
		const context = this.activeRunById.get(executionId);
		if (!context) {
			return false;
		}
		this.timeoutRun(context, 0, true);
		return true;
	}

	dispose() {
		for (const context of this.activeRunById.values()) {
			if (context.strategyMode === 'remote') {
				context.abortController?.abort();
			}
			this.finishWithError(context, new Error('Execution manager disposed'));
		}
		for (const [workerLanguage] of this.workerRuntimeByLanguage.entries()) {
			this.terminateWorker(workerLanguage);
		}
	}

	resetWorker(language: string) {
		const normalizedLanguage = this.normalizeLanguage(language);
		const strategy = this.strategyByLanguage.get(normalizedLanguage);
		if (!strategy || strategy.mode !== 'worker') {
			return;
		}

		const runtimeLanguage = strategy.language;
		const activeRunId = this.activeRunIdByRuntimeLanguage.get(runtimeLanguage);
		if (activeRunId) {
			const context = this.activeRunById.get(activeRunId);
			if (context) {
				this.timeoutRun(context, 0, true);
				return;
			}
			this.activeRunIdByRuntimeLanguage.delete(runtimeLanguage);
		}

		this.terminateWorker(runtimeLanguage);
	}

	private registerStrategy(strategy: ExecutionStrategy) {
		this.strategies.set(strategy.language, strategy);
		this.strategyByLanguage.set(strategy.language, strategy);
		for (const alias of strategy.aliases) {
			this.strategyByLanguage.set(alias, strategy);
		}
	}

	private startPythonWorkspaceRun(
		worker: Worker,
		executionId: string,
		workspace: NormalizedExecutionWorkspace
	) {
		const workspaceFileMap = Object.fromEntries(
			workspace.files.map((file) => [file.name, file.content])
		);
		const onWorkspaceSynced = (event: MessageEvent<WorkerOutboundMessage>) => {
			const payload = event.data;
			if (!payload || payload.id !== executionId) {
				return;
			}
			if (payload.command !== 'SYNC_WORKSPACE') {
				return;
			}
			if (payload.status === 'error') {
				worker.removeEventListener('message', onWorkspaceSynced);
				return;
			}
			if (payload.phase !== 'synced' || payload.status !== 'running') {
				return;
			}
			worker.removeEventListener('message', onWorkspaceSynced);
			worker.postMessage({
				id: executionId,
				command: 'RUN_CODE',
				main_file: workspace.mainFile
			} satisfies WorkerInboundMessage);
		};
		worker.addEventListener('message', onWorkspaceSynced);
		worker.postMessage({
			id: executionId,
			command: 'SYNC_WORKSPACE',
			files: workspaceFileMap
		} satisfies WorkerInboundMessage);
	}

	private normalizeLanguage(language: string) {
		return (language || '').trim().toLowerCase();
	}

	private async ensureWorker(workerLanguage: string, strategy: WorkerExecutionStrategy) {
		const existing = this.workerRuntimeByLanguage.get(workerLanguage);
		if (existing) {
			return existing.worker;
		}
		const worker = await strategy.workerFactory();
		const onMessage = (event: MessageEvent<WorkerOutboundMessage>) => {
			this.handleWorkerMessage(workerLanguage, event.data);
		};
		const onError = (event: ErrorEvent) => {
			event.preventDefault?.();
			const activeRunId = this.activeRunIdByRuntimeLanguage.get(workerLanguage);
			if (!activeRunId) {
				this.terminateWorker(workerLanguage);
				return;
			}
			const context = this.activeRunById.get(activeRunId);
			if (!context) {
				this.terminateWorker(workerLanguage);
				return;
			}
			const messageFromError =
				typeof event.error === 'object' &&
				event.error !== null &&
				'message' in event.error &&
				typeof (event.error as { message?: unknown }).message === 'string'
					? (event.error as { message: string }).message
					: '';
			const errorMessage =
				event.message || messageFromError || `Worker error for ${context.language}`;
			this.finishWithError(context, new Error(errorMessage));
			this.terminateWorker(workerLanguage);
		};
		worker.addEventListener('message', onMessage);
		worker.addEventListener('error', onError);
		this.workerRuntimeByLanguage.set(workerLanguage, {
			worker,
			onMessage,
			onError
		});
		return worker;
	}

	private handleWorkerMessage(workerLanguage: string, message: WorkerOutboundMessage) {
		if (!message || typeof message.id !== 'string') {
			return;
		}
		const context = this.activeRunById.get(message.id);
		if (!context) {
			return;
		}
		const stream: 'stdout' | 'stderr' =
			message.stream ?? (message.status === 'error' ? 'stderr' : 'stdout');
		if (message.status === 'running') {
			if (message.output) {
				this.streamOutput(context, message.output, 'running', stream);
			}
			return;
		}
		if (message.output) {
			this.streamOutput(context, message.output, message.status, stream);
		}
		if (message.status === 'success') {
			const artifacts =
				Array.isArray(message.artifacts) && message.artifacts.length > 0
					? message.artifacts
							.map((file) => ({
								name: (file?.name || '').trim(),
								content: String(file?.content ?? '')
							}))
							.filter((file) => file.name.length > 0)
					: [];
			if (artifacts.length > 0 && context.onArtifacts) {
				try {
					context.onArtifacts(artifacts);
				} catch {
					// Ignore callback errors so terminal output + success status still propagate.
				}
			}
			this.flushBufferedOutput(context, 'success', 'stdout');
			this.flushBufferedOutput(context, 'success', 'stderr');
			this.finishWithSuccess(context);
			return;
		}
		const messageError = message.error || `Execution failed for ${context.language}`;
		this.flushBufferedOutput(context, 'error', 'stdout');
		this.flushBufferedOutput(context, 'error', 'stderr');
		this.finishWithError(context, new Error(messageError));
		if (this.activeRunIdByRuntimeLanguage.get(workerLanguage) === context.id) {
			this.activeRunIdByRuntimeLanguage.delete(workerLanguage);
		}
	}

	private async executeRemoteRun(
		context: RunContext,
		strategy: RemoteExecutionStrategy,
		language: string,
		signal: AbortSignal,
		stdin: string,
		workspace: NormalizedExecutionWorkspace,
		requestOptions?: RemoteExecutionRequestOptions
	) {
		try {
			const remotePayload = await strategy.runRemote(
				language,
				signal,
				stdin,
				workspace,
				requestOptions
			);
			if (context.settled) {
				return;
			}
			const artifacts =
				Array.isArray(remotePayload.artifacts) && remotePayload.artifacts.length > 0
					? remotePayload.artifacts
							.map((file) => ({
								name: (file?.name || '').trim(),
								content: String(file?.content ?? '')
							}))
							.filter((file) => file.name.length > 0)
					: [];
			if (artifacts.length > 0 && context.onArtifacts) {
				try {
					context.onArtifacts(artifacts);
				} catch {
					// Ignore callback errors so execution output still completes.
				}
			}
			if (remotePayload.stdout) {
				this.streamOutput(context, remotePayload.stdout, 'running', 'stdout');
			}
			if (remotePayload.stderr) {
				this.streamOutput(context, remotePayload.stderr, 'running', 'stderr');
			}
			this.flushBufferedOutput(context, 'success', 'stdout');
			this.flushBufferedOutput(context, 'success', 'stderr');
			this.finishWithSuccess(context);
		} catch (error) {
			if (context.settled) {
				return;
			}

			let stdout = '';
			let stderr = '';
			let message = '';
			if (error instanceof RemoteExecutionError) {
				stdout = error.stdout;
				stderr = error.stderr;
				message = error.message;
			} else {
				message = error instanceof Error ? error.message : String(error);
			}

			if (stdout) {
				this.streamOutput(context, stdout, 'running', 'stdout');
			}
			if (stderr) {
				this.streamOutput(context, stderr, 'running', 'stderr');
			}
			this.flushBufferedOutput(context, 'error', 'stdout');
			this.flushBufferedOutput(context, 'error', 'stderr');

			const fallbackMessage = `Execution failed for ${context.language}`;
			const errorMessage = (message || fallbackMessage).trim() || fallbackMessage;
			this.emitLine(context, errorMessage, 'error', 'stderr');
			this.finishWithError(context, new Error(errorMessage));
		}
	}

	private async runJavaScriptWorkspace(signal: AbortSignal, workspace: NormalizedExecutionWorkspace) {
		if (signal.aborted) {
			throw createAbortError();
		}
		if (!isCrossOriginIsolatedPage()) {
			const fallbackCode = resolveMainWorkspaceSource(workspace);
			if (usesNodeRuntimeSyntax(fallbackCode)) {
				throw crossOriginIsolationRequiredError();
			}
			return await executeJavaScriptWorkerFallback(fallbackCode, { signal });
		}
		const result = await executeNodeWorkspace(workspace.files, workspace.mainFile);
		if (signal.aborted) {
			throw createAbortError();
		}
		if (result.exitCode !== 0) {
			const failureMessage =
				result.stderr.trim() || `JavaScript execution failed (exit code ${result.exitCode})`;
			throw new RemoteExecutionError(failureMessage, result.stdout, result.stderr);
		}
		return {
			stdout: result.stdout,
			stderr: result.stderr,
			artifacts: result.artifacts
		} satisfies RemoteExecutionPayload;
	}

	private async runRemote(
		language: string,
		signal: AbortSignal,
		stdin: string,
		workspace: NormalizedExecutionWorkspace,
		requestOptions?: RemoteExecutionRequestOptions
	) {
		const requestPayload = buildExecutionPayload(language, stdin, workspace.mainFile, workspace.files);
		const endpoint = (requestOptions?.endpoint || EXECUTE_ENDPOINT).trim() || EXECUTE_ENDPOINT;
		const requestHeaders: Record<string, string> = {
			'Content-Type': 'application/json',
			...(requestOptions?.requestHeaders || {})
		};
		let response: Response;
		try {
			response = await fetch(endpoint, {
				method: 'POST',
				headers: requestHeaders,
				body: JSON.stringify(requestPayload),
				signal
			});
		} catch (error) {
			const isAbortError =
				typeof error === 'object' &&
				error !== null &&
				'name' in error &&
				(error as { name?: string }).name === 'AbortError';
			if (isAbortError) {
				throw error;
			}
			throw new RemoteExecutionError(
				error instanceof Error ? error.message : 'Failed to reach execution server'
			);
		}

		const payload = (await response.json().catch(() => null)) as Record<string, unknown> | null;
		const stdout = typeof payload?.stdout === 'string' ? payload.stdout : '';
		const stderr = typeof payload?.stderr === 'string' ? payload.stderr : '';
		const artifacts = parseRemoteExecutionArtifacts(payload);
		if (!response.ok) {
			const serverMessage =
				typeof payload?.error === 'string' && payload.error.trim()
					? payload.error.trim()
					: `Execution request failed (${response.status})`;
			throw new RemoteExecutionError(serverMessage, stdout, stderr);
		}

		return {
			stdout,
			stderr,
			artifacts
		} satisfies RemoteExecutionPayload;
	}

	private streamOutput(
		context: RunContext,
		chunk: string,
		status: 'running' | 'success' | 'error' = 'running',
		stream: 'stdout' | 'stderr' = 'stdout'
	) {
		const normalizedChunk = chunk.replace(/\r\n/g, '\n');
		context.outputBufferByStream[stream] += normalizedChunk;
		let newlineIndex = context.outputBufferByStream[stream].indexOf('\n');
		while (newlineIndex >= 0) {
			const line = context.outputBufferByStream[stream].slice(0, newlineIndex);
			context.outputBufferByStream[stream] = context.outputBufferByStream[stream].slice(
				newlineIndex + 1
			);
			this.emitLine(context, line, status, stream);
			newlineIndex = context.outputBufferByStream[stream].indexOf('\n');
		}
	}

	private flushBufferedOutput(
		context: RunContext,
		status: 'success' | 'error',
		stream: 'stdout' | 'stderr'
	) {
		if (!context.outputBufferByStream[stream]) {
			return;
		}
		const line = context.outputBufferByStream[stream];
		context.outputBufferByStream[stream] = '';
		this.emitLine(context, line, status, stream);
	}

	private emitLine(
		context: RunContext,
		line: string,
		status: 'running' | 'success' | 'error',
		stream: 'stdout' | 'stderr'
	) {
		const payload: ExecutionOutputLine = {
			id: context.id,
			language: context.language,
			line,
			stream,
			status
		};
		for (const callback of context.subscribers) {
			callback(payload);
		}
	}

	private finishWithSuccess(context: RunContext) {
		if (context.settled) {
			return;
		}
		context.settled = true;
		this.clearContextTimer(context);
		context.abortController = null;
		this.activeRunById.delete(context.id);
		this.activeRunIdByRuntimeLanguage.delete(context.runtimeLanguage);
		context.resolve({
			id: context.id,
			language: context.language,
			status: 'success'
		});
	}

	private finishWithError(context: RunContext, error: Error) {
		if (context.settled) {
			return;
		}
		context.settled = true;
		this.clearContextTimer(context);
		context.abortController = null;
		this.activeRunById.delete(context.id);
		this.activeRunIdByRuntimeLanguage.delete(context.runtimeLanguage);
		context.reject(error);
	}

	private clearContextTimer(context: RunContext) {
		if (context.timeoutId !== null) {
			clearTimeout(context.timeoutId);
			context.timeoutId = null;
		}
	}

	private timeoutRun(context: RunContext, timeoutMs: number, cancelled = false) {
		if (context.settled) {
			return;
		}
		const strategyMode = context.strategyMode;
		const runtimeLanguage = context.runtimeLanguage;
		const abortController = context.abortController;
		this.flushBufferedOutput(context, 'error', 'stdout');
		this.flushBufferedOutput(context, 'error', 'stderr');
		this.emitLine(
			context,
			cancelled
				? 'Execution cancelled'
				: `Execution timed out after ${Math.max(1, timeoutMs)}ms`,
			'error',
			'stderr'
		);
		this.finishWithError(
			context,
			new Error(
				cancelled
					? `Execution cancelled (${context.language})`
					: `Execution timed out after ${Math.max(1, timeoutMs)}ms (${context.language})`
			)
		);
		if (strategyMode === 'worker') {
			this.terminateWorker(runtimeLanguage);
			return;
		}
		abortController?.abort();
	}

	private terminateWorker(workerLanguage: string) {
		const runtime = this.workerRuntimeByLanguage.get(workerLanguage);
		if (!runtime) {
			return;
		}
		runtime.worker.removeEventListener('message', runtime.onMessage);
		runtime.worker.removeEventListener('error', runtime.onError);
		runtime.worker.terminate();
		this.workerRuntimeByLanguage.delete(workerLanguage);
	}
}
