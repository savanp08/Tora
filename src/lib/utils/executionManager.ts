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
};

type WorkerFactory = () => Promise<Worker>;

type ExecutionStrategy = {
	language: string;
	aliases: string[];
	workerFactory: WorkerFactory;
};

type RunContext = {
	id: string;
	language: string;
	workerLanguage: string;
	resolve: (value: ExecutionRunResult) => void;
	reject: (reason: Error) => void;
	timeoutId: ReturnType<typeof setTimeout> | null;
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

function createExecutionId() {
	if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
		return crypto.randomUUID();
	}
	return `exec-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

export class ExecutionManager {
	private readonly strategies = new Map<string, ExecutionStrategy>();
	private readonly strategyByLanguage = new Map<string, ExecutionStrategy>();
	private readonly workerRuntimeByLanguage = new Map<string, WorkerRuntime>();
	private readonly activeRunById = new Map<string, RunContext>();
	private readonly activeRunIdByWorkerLanguage = new Map<string, string>();

	constructor() {
		const pythonStrategy: ExecutionStrategy = {
			language: 'python',
			aliases: ['py'],
			workerFactory: async () =>
				new Worker(new URL('../workers/pyodide.worker.ts', import.meta.url), {
					type: 'module'
				})
		};
		const javascriptStrategy: ExecutionStrategy = {
			language: 'javascript',
			aliases: ['js', 'mjs', 'cjs'],
			workerFactory: async () => {
				const { default: JavaScriptWorker } = await import('$lib/workers/javascript.worker?worker');
				return new JavaScriptWorker();
			}
		};
		this.registerStrategy(pythonStrategy);
		this.registerStrategy(javascriptStrategy);
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
		const workerLanguage = strategy.language;
		if (this.activeRunIdByWorkerLanguage.has(workerLanguage)) {
			throw new Error(
				`Execution for ${workerLanguage} is already in progress. Wait for it to finish or cancel it first.`
			);
		}
		const worker = await this.ensureWorker(workerLanguage, strategy);
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
			workerLanguage,
			resolve: resolveResult,
			reject: rejectResult,
			timeoutId: null,
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
		this.activeRunIdByWorkerLanguage.set(workerLanguage, executionId);
		worker.postMessage({
			code,
			id: executionId
		} satisfies WorkerInboundMessage);

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

	private async ensureWorker(workerLanguage: string, strategy: ExecutionStrategy) {
		const existing = this.workerRuntimeByLanguage.get(workerLanguage);
		if (existing) {
			return existing.worker;
		}
		const worker = await strategy.workerFactory();
		const onMessage = (event: MessageEvent<WorkerOutboundMessage>) => {
			this.handleWorkerMessage(workerLanguage, event.data);
		};
		const onError = (event: ErrorEvent) => {
			const activeRunId = this.activeRunIdByWorkerLanguage.get(workerLanguage);
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
		if (this.activeRunIdByWorkerLanguage.get(workerLanguage) === context.id) {
			this.activeRunIdByWorkerLanguage.delete(workerLanguage);
		}
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
		this.activeRunById.delete(context.id);
		this.activeRunIdByWorkerLanguage.delete(context.workerLanguage);
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
		this.activeRunById.delete(context.id);
		this.activeRunIdByWorkerLanguage.delete(context.workerLanguage);
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
		this.terminateWorker(context.workerLanguage);
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
