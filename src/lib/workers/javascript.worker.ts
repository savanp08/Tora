/// <reference lib="webworker" />

type JavaScriptExecuteMessage = {
	id: string;
	code: string;
};

type JavaScriptWorkerMessage = {
	id: string;
	status: 'running' | 'success' | 'error';
	stream?: 'stdout' | 'stderr';
	output?: string;
	error?: string;
};

declare const self: DedicatedWorkerGlobalScope;

let executionInProgress = false;

function emit(message: JavaScriptWorkerMessage) {
	self.postMessage(message);
}

function formatArg(value: unknown) {
	if (typeof value === 'string') {
		return value;
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

self.addEventListener('message', async (event: MessageEvent<JavaScriptExecuteMessage>) => {
	const payload = event.data;
	if (!payload || typeof payload.id !== 'string' || typeof payload.code !== 'string') {
		return;
	}
	if (executionInProgress) {
		emit({
			id: payload.id,
			status: 'error',
			error: 'A JavaScript execution is already in progress'
		});
		return;
	}
	executionInProgress = true;
	try {
		const streamOutput = (stream: 'stdout' | 'stderr', ...args: unknown[]) => {
			emit({
				id: payload.id,
				status: 'running',
				stream,
				output: `${args.map((arg) => formatArg(arg)).join(' ')}\n`
			});
		};

		const sandboxConsole = {
			log: (...args: unknown[]) => streamOutput('stdout', ...args),
			info: (...args: unknown[]) => streamOutput('stdout', ...args),
			warn: (...args: unknown[]) => streamOutput('stderr', ...args),
			error: (...args: unknown[]) => streamOutput('stderr', ...args),
			debug: (...args: unknown[]) => streamOutput('stdout', ...args),
			clear: () => {
				emit({
					id: payload.id,
					status: 'running',
					stream: 'stdout',
					output: '\x1bc\n'
				});
			}
		};

		const execute = new Function(
			'console',
			'print',
			`"use strict";\nreturn (async () => {\n${payload.code}\n})();`
		) as (
			consoleObject: typeof sandboxConsole,
			printFunction: (...args: unknown[]) => void
		) => unknown;

		await Promise.resolve(execute(sandboxConsole, (...args: unknown[]) => streamOutput('stdout', ...args)));
		emit({
			id: payload.id,
			status: 'success'
		});
	} catch (error) {
		emit({
			id: payload.id,
			status: 'error',
			error: error instanceof Error ? error.stack || error.message : String(error)
		});
	} finally {
		executionInProgress = false;
	}
});

export {};
