/// <reference lib="webworker" />

type PyodideRuntime = {
	runPythonAsync: (code: string) => Promise<unknown>;
	runPython: (code: string) => unknown;
};

type LoadPyodideFn = (options: { indexURL: string }) => Promise<PyodideRuntime>;

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
	stdout?: string;
	stderr?: string;
};

declare const self: DedicatedWorkerGlobalScope;

const PYODIDE_BASE_URL = 'https://cdn.jsdelivr.net/pyodide/v0.25.0/full/';
const INITIALIZE_STD_STREAMS_CODE = `
import io
import sys

sys.stdout = io.StringIO()
sys.stderr = io.StringIO()
`;
const CLEAR_STD_STREAMS_CODE = `
import sys

sys.stdout.seek(0)
sys.stdout.truncate(0)
sys.stderr.seek(0)
sys.stderr.truncate(0)
`;
const READ_STDOUT_CODE = `
import sys
sys.stdout.getvalue()
`;
const READ_STDERR_CODE = `
import sys
sys.stderr.getvalue()
`;

let pyodideReady: Promise<PyodideRuntime> | null = null;
let loadPyodideFn: LoadPyodideFn | null = null;
let executionInProgress = false;

function emit(message: PyodideWorkerMessage) {
	self.postMessage(message);
}

function readBuffers(pyodide: PyodideRuntime) {
	return {
		stdout: String(pyodide.runPython(READ_STDOUT_CODE)),
		stderr: String(pyodide.runPython(READ_STDERR_CODE))
	};
}

function emitBufferedOutput(id: string, buffers: { stdout: string; stderr: string }) {
	if (buffers.stdout) {
		emit({
			id,
			status: 'running',
			stream: 'stdout',
			output: buffers.stdout
		});
	}
	if (buffers.stderr) {
		emit({
			id,
			status: 'running',
			stream: 'stderr',
			output: buffers.stderr
		});
	}
}

async function initPyodide() {
	if (!pyodideReady) {
		pyodideReady = (async () => {
			const loadPyodide = await resolveLoadPyodide();
			const pyodide = await loadPyodide({ indexURL: PYODIDE_BASE_URL });
			await pyodide.runPythonAsync(INITIALIZE_STD_STREAMS_CODE);
			return pyodide;
		})();
	}
	return pyodideReady;
}

async function resolveLoadPyodide(): Promise<LoadPyodideFn> {
	if (loadPyodideFn) {
		return loadPyodideFn;
	}
	const globalLoadPyodide = (self as unknown as { loadPyodide?: unknown }).loadPyodide;
	if (typeof globalLoadPyodide === 'function') {
		loadPyodideFn = globalLoadPyodide as LoadPyodideFn;
		return loadPyodideFn;
	}
	try {
		const module =
			(await import('https://cdn.jsdelivr.net/pyodide/v0.25.0/full/pyodide.mjs')) as Record<
				string,
				unknown
			>;
		const moduleLoadPyodide = module.loadPyodide;
		if (typeof moduleLoadPyodide === 'function') {
			loadPyodideFn = moduleLoadPyodide as LoadPyodideFn;
			return loadPyodideFn;
		}
	} catch {
		// Fall through to script-based fallback.
	}
	try {
		importScripts('https://cdn.jsdelivr.net/pyodide/v0.25.0/full/pyodide.js');
		const legacyLoadPyodide = (self as unknown as { loadPyodide?: unknown }).loadPyodide;
		if (typeof legacyLoadPyodide === 'function') {
			loadPyodideFn = legacyLoadPyodide as LoadPyodideFn;
			return loadPyodideFn;
		}
	} catch {
		// Ignore and throw a single consistent error below.
	}
	throw new Error('Unable to initialize Pyodide runtime in worker');
}

self.onmessage = async (event: MessageEvent<PyodideExecuteMessage>) => {
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
		const pyodide = await initPyodide();
		await pyodide.runPythonAsync(CLEAR_STD_STREAMS_CODE);

		await pyodide.runPythonAsync(payload.code);
		const buffers = readBuffers(pyodide);
		emitBufferedOutput(payload.id, buffers);
		emit({
			id: payload.id,
			status: 'success',
			stdout: buffers.stdout,
			stderr: buffers.stderr
		});
	} catch (error) {
		let stdout = '';
		let stderr = '';
		try {
			const pyodide = await initPyodide();
			const buffers = readBuffers(pyodide);
			stdout = buffers.stdout;
			stderr = buffers.stderr;
			emitBufferedOutput(payload.id, buffers);
		} catch {
			// Ignore secondary errors while collecting buffered output.
		}
		emit({
			id: payload.id,
			status: 'error',
			error: error instanceof Error ? error.message : String(error),
			stdout,
			stderr
		});
	} finally {
		executionInProgress = false;
	}
};

export {};
