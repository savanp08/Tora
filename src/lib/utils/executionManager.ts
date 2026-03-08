const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://localhost:8080';
const EXECUTE_ENDPOINT = `${API_BASE}/api/execute`;

type WorkerInboundMessage = {
	code: string;
	id: string;
};

type WorkerOutboundMessage = {
	id: string;
	status: 'running' | 'success' | 'error';
	stream?: 'stdout' | 'stderr';
	output?: string;
	error?: string;
	stdout?: string;
	stderr?: string;
};

type WorkerFactory = () => Promise<Worker>;

type RemoteExecutionPayload = {
	stdout: string;
	stderr: string;
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
		code: string,
		language: string,
		runId: string,
		signal: AbortSignal
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

function encodeCodeAsBase64(code: string) {
	return btoa(unescape(encodeURIComponent(code)));
}

export async function executeCodeWithRouter(
	code: string,
	language: string,
	options?: {
		pythonWorker?: Worker;
		signal?: AbortSignal;
		endpoint?: string;
	}
): Promise<RoutedExecutionResult> {
	const normalizedLanguage = (language || '').trim().toLowerCase();
	const signal = options?.signal;
	if (normalizedLanguage === 'python' || normalizedLanguage === 'py') {
		let worker = options?.pythonWorker ?? null;
		let ownsWorker = false;
		if (!worker) {
			worker = new Worker(new URL('../workers/pyodide.worker.ts', import.meta.url), {
				type: 'module'
			});
			ownsWorker = true;
		}

		const runId = createExecutionId();
		let stdout = '';
		let stderr = '';
		return await new Promise<RoutedExecutionResult>((resolve, reject) => {
			if (!worker) {
				reject(new Error('Python worker is not available'));
				return;
			}
			const cleanup = () => {
				worker?.removeEventListener('message', onMessage);
				worker?.removeEventListener('error', onError);
				signal?.removeEventListener('abort', onAbort);
				if (ownsWorker && worker) {
					worker.terminate();
				}
			};
			const finishResolve = (value: RoutedExecutionResult) => {
				cleanup();
				resolve(value);
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
				if (payload.status === 'running') {
					if (payload.stream === 'stderr') {
						stderr += payload.output || '';
					} else {
						stdout += payload.output || '';
					}
					return;
				}
				if (payload.stdout) {
					stdout = payload.stdout;
				}
				if (payload.stderr) {
					stderr = payload.stderr;
				}
				if (payload.status === 'error') {
					const errorMessage =
						(payload.error || stderr || 'Python execution failed').trim() ||
						'Python execution failed';
					finishReject(new Error(errorMessage));
					return;
				}
				finishResolve({
					output: stdout,
					error: stderr
				});
			};
			const onError = (event: ErrorEvent) => {
				const message = event.message || 'Python worker crashed';
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
				code
			} satisfies WorkerInboundMessage);
		});
	}

	if (normalizedLanguage === 'cpp' || normalizedLanguage === 'c' || normalizedLanguage === 'java') {
		const endpoint = (options?.endpoint || EXECUTE_ENDPOINT).trim() || EXECUTE_ENDPOINT;
		const response = await fetch(endpoint, {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json'
			},
			body: JSON.stringify({
				language: normalizedLanguage,
				code: encodeCodeAsBase64(code)
			}),
			signal
		});
		const payload = (await response.json().catch(() => null)) as Record<string, unknown> | null;
		const stdout = typeof payload?.stdout === 'string' ? payload.stdout : '';
		const stderr = typeof payload?.stderr === 'string' ? payload.stderr : '';
		if (!response.ok) {
			const errorMessage =
				typeof payload?.error === 'string' && payload.error.trim()
					? payload.error.trim()
					: `Execution request failed (${response.status})`;
			throw new Error(errorMessage);
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
		const javascriptStrategy: WorkerExecutionStrategy = {
			mode: 'worker',
			language: 'javascript',
			aliases: ['js', 'mjs', 'cjs'],
			workerFactory: async () => {
				const { default: JavaScriptWorker } = await import('$lib/workers/javascript.worker?worker');
				return new JavaScriptWorker();
			}
		};

		const remoteStrategies: RemoteExecutionStrategy[] = [
			{
				mode: 'remote',
				language: 'cpp',
				aliases: ['c++'],
				runRemote: (code, language, runId, signal) => this.runRemote(code, language, runId, signal)
			},
			{
				mode: 'remote',
				language: 'c',
				aliases: [],
				runRemote: (code, language, runId, signal) => this.runRemote(code, language, runId, signal)
			},
			{
				mode: 'remote',
				language: 'java',
				aliases: [],
				runRemote: (code, language, runId, signal) => this.runRemote(code, language, runId, signal)
			},
			{
				mode: 'remote',
				language: 'go',
				aliases: ['golang'],
				runRemote: (code, language, runId, signal) => this.runRemote(code, language, runId, signal)
			},
			{
				mode: 'remote',
				language: 'rust',
				aliases: ['rs'],
				runRemote: (code, language, runId, signal) => this.runRemote(code, language, runId, signal)
			}
		];

		this.registerStrategy(pythonStrategy);
		this.registerStrategy(javascriptStrategy);
		for (const strategy of remoteStrategies) {
			this.registerStrategy(strategy);
		}
	}

	getSupportedLanguages() {
		return new Map(this.strategies);
	}

	async run(language: string, code: string, timeoutMs = 5000): Promise<ExecutionRunHandle> {
		const normalizedLanguage = this.normalizeLanguage(language);
		const strategy = this.strategyByLanguage.get(normalizedLanguage);
		if (!strategy) {
			throw new Error(
				`Unsupported language "${language}". Supported: ${[...this.strategies.keys()].join(', ')}`
			);
		}
		const runtimeLanguage = strategy.language;
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
			settled: false
		};

		context.timeoutId = setTimeout(() => {
			this.timeoutRun(context, timeoutMs);
		}, Math.max(1, timeoutMs));

		this.activeRunById.set(executionId, context);
		this.activeRunIdByRuntimeLanguage.set(runtimeLanguage, executionId);

		if (strategy.mode === 'worker') {
			worker?.postMessage({
				code,
				id: executionId
			} satisfies WorkerInboundMessage);
		} else {
			const abortController = new AbortController();
			context.abortController = abortController;
			void this.executeRemoteRun(
				context,
				strategy,
				code,
				normalizedLanguage,
				abortController.signal
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

	private registerStrategy(strategy: ExecutionStrategy) {
		this.strategies.set(strategy.language, strategy);
		this.strategyByLanguage.set(strategy.language, strategy);
		for (const alias of strategy.aliases) {
			this.strategyByLanguage.set(alias, strategy);
		}
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
			const activeRunId = this.activeRunIdByRuntimeLanguage.get(workerLanguage);
			if (!activeRunId) {
				return;
			}
			const context = this.activeRunById.get(activeRunId);
			if (!context) {
				return;
			}
			const errorMessage = event.message || `Worker error for ${context.language}`;
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
		code: string,
		language: string,
		signal: AbortSignal
	) {
		try {
			console.info(
				`[execution][client] User triggered remote execution runId=${context.id} language=${language} source_chars=${code.length}`
			);
			const remotePayload = await strategy.runRemote(code, language, context.id, signal);
			if (context.settled) {
				return;
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
			console.error(
				`[execution][client] Remote execution failed runId=${context.id} language=${language} message=${errorMessage} stdout_chars=${stdout.length} stderr_chars=${stderr.length}`
			);
			this.emitLine(context, errorMessage, 'error', 'stderr');
			this.finishWithError(context, new Error(errorMessage));
		}
	}

	private async runRemote(code: string, language: string, runId: string, signal: AbortSignal) {
		const base64Code = encodeCodeAsBase64(code);
		console.info(
			`[execution][client] Sending execution request to server runId=${runId} endpoint=${EXECUTE_ENDPOINT} language=${language} source_chars=${code.length} base64_chars=${base64Code.length}`
		);
		let response: Response;
		try {
			response = await fetch(EXECUTE_ENDPOINT, {
				method: 'POST',
				headers: {
					'Content-Type': 'application/json'
				},
				body: JSON.stringify({
					language,
					code: base64Code
				}),
				signal
			});
		} catch (error) {
			const isAbortError =
				typeof error === 'object' &&
				error !== null &&
				'name' in error &&
				(error as { name?: string }).name === 'AbortError';
			if (isAbortError) {
				console.info(
					`[execution][client] Execution request was cancelled before completion runId=${runId} language=${language}`
				);
				throw error;
			}
			console.error(
				`[execution][client] Failed to reach execution server runId=${runId} language=${language} error=${
					error instanceof Error ? error.message : String(error)
				}`
			);
			throw new RemoteExecutionError(
				error instanceof Error ? error.message : 'Failed to reach execution server'
			);
		}

		const payload = (await response.json().catch(() => null)) as Record<string, unknown> | null;
		const stdout = typeof payload?.stdout === 'string' ? payload.stdout : '';
		const stderr = typeof payload?.stderr === 'string' ? payload.stderr : '';
		const serverError = typeof payload?.error === 'string' ? payload.error : '';
		console.info(
			`[execution][client] Received execution response from server runId=${runId} language=${language} status=${response.status} ok=${response.ok} stdout_chars=${stdout.length} stderr_chars=${stderr.length} error_message=${serverError || '<none>'}`
		);
		if (!response.ok) {
			const serverMessage =
				typeof payload?.error === 'string' && payload.error.trim()
					? payload.error.trim()
					: `Execution request failed (${response.status})`;
			throw new RemoteExecutionError(serverMessage, stdout, stderr);
		}

		return {
			stdout,
			stderr
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
