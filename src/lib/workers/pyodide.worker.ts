/// <reference lib="webworker" />

type PyodideRuntime = {
	runPythonAsync: (code: string) => Promise<unknown>;
	setStdout: (options: { batched?: (value: string) => void }) => void;
	setStderr: (options: { batched?: (value: string) => void }) => void;
};

type PyodideExecuteMessage = {
	id: string;
	code: string;
};

type PyodideWorkerMessage = {
	id: string;
	status: 'running' | 'success' | 'error';
	stream?: 'stdout' | 'stderr';
	output?: string;
	error?: string;
};

declare const self: DedicatedWorkerGlobalScope & {
	importScripts: (...urls: string[]) => void;
	loadPyodide?: (options?: { indexURL?: string }) => Promise<PyodideRuntime>;
};

const PYODIDE_BASE_URL = 'https://cdn.jsdelivr.net/pyodide/v0.25.0/full/';
const PYODIDE_SCRIPT_URL = `${PYODIDE_BASE_URL}pyodide.js`;

let pyodideReady: Promise<PyodideRuntime> | null = null;
let executionInProgress = false;

function emit(message: PyodideWorkerMessage) {
	self.postMessage(message);
}

async function initPyodide() {
	if (!pyodideReady) {
		pyodideReady = (async () => {
			self.importScripts(PYODIDE_SCRIPT_URL);
			if (typeof self.loadPyodide !== 'function') {
				throw new Error('Pyodide loader is unavailable in worker scope');
			}
			return self.loadPyodide({ indexURL: PYODIDE_BASE_URL });
		})();
	}
	return pyodideReady;
}

// Initialize runtime as soon as the worker is created.
void initPyodide();

self.addEventListener('message', async (event: MessageEvent<PyodideExecuteMessage>) => {
	const payload = event.data;
	if (!payload || typeof payload.id !== 'string' || typeof payload.code !== 'string') {
		return;
	}
	if (executionInProgress) {
		emit({
			id: payload.id,
			status: 'error',
			error: 'A Python execution is already in progress'
		});
		return;
	}
	executionInProgress = true;

	try {
		emit({
			id: payload.id,
			status: 'running',
			stream: 'stdout',
			output: 'Preparing Python runtime...\n'
		});
		const pyodide = await initPyodide();
			pyodide.setStdout({
				batched: (chunk: string) => {
					if (!chunk) {
						return;
					}
					emit({
						id: payload.id,
						status: 'running',
						stream: 'stdout',
						output: chunk
					});
				}
			});
			pyodide.setStderr({
				batched: (chunk: string) => {
					if (!chunk) {
						return;
					}
					emit({
						id: payload.id,
						status: 'running',
						stream: 'stderr',
						output: chunk
					});
				}
			});

		await pyodide.runPythonAsync(payload.code);
		emit({
			id: payload.id,
			status: 'success'
		});
	} catch (error) {
		emit({
			id: payload.id,
			status: 'error',
			error: error instanceof Error ? error.message : String(error)
		});
	} finally {
		executionInProgress = false;
	}
});

export {};
