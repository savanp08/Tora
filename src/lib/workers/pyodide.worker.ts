/// <reference lib="webworker" />

type PyodideCommand = 'SYNC_WORKSPACE' | 'RUN_CODE';

type WorkspaceFile = {
	name: string;
	content: string;
};

type PyodideRuntime = {
	runPythonAsync: (code: string) => Promise<unknown>;
	runPython: (code: string) => unknown;
	FS: {
		writeFile: (path: string, data: string) => void;
		readFile: (path: string, options?: { encoding?: 'utf8' }) => string | Uint8Array;
		readdir: (path: string) => string[];
		unlink: (path: string) => void;
		mkdirTree: (path: string) => void;
		rmdir: (path: string) => void;
	};
};

type LoadPyodideFn = (options: { indexURL: string }) => Promise<PyodideRuntime>;

type PyodideExecuteMessage = {
	id: string;
	command?: PyodideCommand;
	code?: string;
	main_file?: string;
	files?: WorkspaceFile[] | Record<string, string>;
};

type PyodideWorkerMessage = {
	id: string;
	command?: PyodideCommand;
	phase?: 'synced';
	status: 'running' | 'success' | 'error';
	stream?: 'stdout' | 'stderr';
	output?: string;
	error?: string;
	stdout?: string;
	stderr?: string;
	artifacts?: WorkspaceFile[];
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
const INVALIDATE_IMPORT_CACHES_CODE = `
import importlib
importlib.invalidate_caches()
`;

type MountedWorkspace = {
	files: string[];
	directories: string[];
};

type WorkspaceSession = {
	files: WorkspaceFile[];
	defaultMainFile: string;
	mounted: MountedWorkspace;
};

let pyodideReady: Promise<PyodideRuntime> | null = null;
let loadPyodideFn: LoadPyodideFn | null = null;
let executionInProgress = false;
const workspaceSessionById = new Map<string, WorkspaceSession>();

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

function normalizeWorkspacePath(path: string) {
	const normalized = (path || '')
		.trim()
		.replace(/\\/g, '/')
		.replace(/^\/+/, '')
		.replace(/\/+/g, '/');
	if (!normalized) {
		return '';
	}
	const segments = normalized.split('/');
	if (segments.some((segment) => segment === '' || segment === '.' || segment === '..')) {
		return '';
	}
	return normalized;
}

function normalizeWorkspaceFiles(files: WorkspaceFile[] | Record<string, string> | undefined) {
	if (!files) {
		return [] as WorkspaceFile[];
	}
	const rawFiles = Array.isArray(files)
		? files.map((file) => [file?.name, file?.content] as const)
		: Object.entries(files);
	return rawFiles
		.map(([name, content]) => ({
			name: normalizeWorkspacePath(typeof name === 'string' ? name : ''),
			content: typeof content === 'string' ? content : ''
		}))
		.filter((file) => file.name !== '');
}

function resolveWorkspacePayload(payload: PyodideExecuteMessage) {
	const files = normalizeWorkspaceFiles(payload.files);
	const fallbackMainFile = normalizeWorkspacePath(
		typeof payload.main_file === 'string' ? payload.main_file : 'main.py'
	);

	if (files.length === 0 && typeof payload.code === 'string') {
		files.push({
			name: fallbackMainFile || 'main.py',
			content: payload.code
		});
	}

	if (files.length === 0) {
		throw new Error('No workspace files were provided for Python execution');
	}

	const normalizedMainFile = normalizeWorkspacePath(
		typeof payload.main_file === 'string' ? payload.main_file : ''
	);
	const defaultMainFile = normalizedMainFile || files[0].name;
	return {
		files,
		defaultMainFile
	};
}

function mountWorkspace(pyodide: PyodideRuntime, files: WorkspaceFile[]) {
	const mountedFiles = new Set<string>();
	const mountedDirectories = new Set<string>();
	for (const file of files) {
		const separatorIndex = file.name.lastIndexOf('/');
		if (separatorIndex > 0) {
			const directory = file.name.slice(0, separatorIndex);
			pyodide.FS.mkdirTree(directory);
			mountedDirectories.add(directory);
		}
		pyodide.FS.writeFile(file.name, file.content);
		mountedFiles.add(file.name);
	}
	return {
		files: Array.from(mountedFiles),
		directories: Array.from(mountedDirectories).sort(
			(left, right) => right.split('/').length - left.split('/').length
		)
	} satisfies MountedWorkspace;
}

function cleanupWorkspace(pyodide: PyodideRuntime, mountedWorkspace: MountedWorkspace) {
	for (const filePath of mountedWorkspace.files) {
		try {
			pyodide.FS.unlink(filePath);
		} catch {
			// Ignore cleanup errors so worker remains available for next execution.
		}
	}
	for (const directoryPath of mountedWorkspace.directories) {
		try {
			pyodide.FS.rmdir(directoryPath);
		} catch {
			// Ignore cleanup errors; directories may contain user-generated files.
		}
	}
}

function clearWorkspaceSessions(pyodide: PyodideRuntime) {
	for (const session of workspaceSessionById.values()) {
		cleanupWorkspace(pyodide, session.mounted);
	}
	workspaceSessionById.clear();
}

async function invalidateImportCaches(pyodide: PyodideRuntime) {
	await pyodide.runPythonAsync(INVALIDATE_IMPORT_CACHES_CODE);
}

// Pre-flight: ensure non-main files are materialized so imports and open() work naturally.
function writeSupportFilesToMemFS(pyodide: PyodideRuntime, files: WorkspaceFile[], mainFile: string) {
	for (const file of files) {
		if (file.name === mainFile) {
			continue;
		}
		const separatorIndex = file.name.lastIndexOf('/');
		if (separatorIndex > 0) {
			pyodide.FS.mkdirTree(file.name.slice(0, separatorIndex));
		}
		pyodide.FS.writeFile(file.name, file.content);
	}
}

async function syncWorkspace(pyodide: PyodideRuntime, payload: PyodideExecuteMessage) {
	if (executionInProgress) {
		throw new Error('A Python execution is already in progress');
	}

	clearWorkspaceSessions(pyodide);
	const workspace = resolveWorkspacePayload(payload);
	const mounted = mountWorkspace(pyodide, workspace.files);
	await invalidateImportCaches(pyodide);

	workspaceSessionById.set(payload.id, {
		files: workspace.files,
		defaultMainFile: workspace.defaultMainFile,
		mounted
	});

	emit({
		id: payload.id,
		command: 'SYNC_WORKSPACE',
		phase: 'synced',
		status: 'running'
	});
}

async function runWorkspace(pyodide: PyodideRuntime, payload: PyodideExecuteMessage) {
	const session = workspaceSessionById.get(payload.id);
	if (!session) {
		throw new Error('Workspace not synced for this execution');
	}
	if (executionInProgress) {
		throw new Error('A Python execution is already in progress');
	}

	const requestedMainFile = normalizeWorkspacePath(
		typeof payload.main_file === 'string' ? payload.main_file : ''
	);
	const mainFile = requestedMainFile || session.defaultMainFile;
	const mainDirSeparator = mainFile.lastIndexOf('/');
	const mainDir = mainDirSeparator > 0 ? mainFile.slice(0, mainDirSeparator) : '';
	const mainEntry = session.files.find((file) => file.name === mainFile);
	if (!mainEntry) {
		throw new Error('main_file must match one of files[].name');
	}

	executionInProgress = true;
	try {
		await pyodide.runPythonAsync(CLEAR_STD_STREAMS_CODE);
		writeSupportFilesToMemFS(pyodide, session.files, mainFile);
		await invalidateImportCaches(pyodide);

		const executeScript = `
import os
import sys

os.chdir('/home/pyodide')

main_file = ${JSON.stringify(mainFile)}
main_dir = os.path.dirname(main_file)
abs_main_file = os.path.abspath(main_file)

if main_dir and os.path.exists(main_dir):
    os.chdir(main_dir)

if '/home/pyodide' not in sys.path:
    sys.path.insert(0, '/home/pyodide')
if main_dir and os.path.abspath(main_dir) not in sys.path:
    sys.path.insert(0, os.path.abspath(main_dir))

with open(abs_main_file, 'r', encoding='utf-8') as _f:
    _code = _f.read()

_globals = {'__name__': '__main__', '__file__': abs_main_file, '__builtins__': __builtins__}
exec(compile(_code, abs_main_file, 'exec'), _globals)
`;
		await pyodide.runPythonAsync(executeScript);
		const artifactByName = new Map<string, string>();
		const originalByName = new Map(session.files.map((file) => [file.name, file.content] as const));
		const newlyCreatedFiles = new Set<string>();
		for (const file of session.files) {
			try {
				const currentContent = pyodide.FS.readFile(file.name, { encoding: 'utf8' });
				if (typeof currentContent === 'string' && currentContent !== file.content) {
					artifactByName.set(file.name, currentContent);
				}
			} catch {
				// Ignore missing files; user code may have deleted them.
			}
		}
		try {
			const discoveredEntries = pyodide.FS.readdir(mainDir || '.');
			for (const entryName of discoveredEntries) {
				if (entryName === '.' || entryName === '..') {
					continue;
				}
				const discoveredPath = mainDir ? `${mainDir}/${entryName}` : entryName;
				if (originalByName.has(discoveredPath) || artifactByName.has(discoveredPath)) {
					continue;
				}
				try {
					const discoveredContent = pyodide.FS.readFile(discoveredPath, { encoding: 'utf8' });
					if (typeof discoveredContent === 'string') {
						artifactByName.set(discoveredPath, discoveredContent);
						newlyCreatedFiles.add(discoveredPath);
					}
				} catch {
					// Ignore directories and non-readable paths.
				}
			}
		} catch {
			// Ignore directory scan errors and continue with collected artifacts.
		}
		const artifacts = Array.from(artifactByName.entries()).map(([name, content]) => ({
			name,
			content
		}));
		if (newlyCreatedFiles.size > 0) {
			const mountedFiles = new Set(session.mounted.files);
			for (const filePath of newlyCreatedFiles) {
				if (mountedFiles.has(filePath)) {
					continue;
				}
				session.mounted.files.push(filePath);
				mountedFiles.add(filePath);
			}
		}
		const buffers = readBuffers(pyodide);
		emitBufferedOutput(payload.id, buffers);
		emit({
			id: payload.id,
			command: 'RUN_CODE',
			status: 'success',
			stdout: buffers.stdout,
			stderr: buffers.stderr,
			artifacts
		});
	} catch (error) {
		let stdout = '';
		let stderr = '';
		try {
			const buffers = readBuffers(pyodide);
			stdout = buffers.stdout;
			stderr = buffers.stderr;
			emitBufferedOutput(payload.id, buffers);
		} catch {
			// Ignore secondary errors while collecting buffered output.
		}
		emit({
			id: payload.id,
			command: 'RUN_CODE',
			status: 'error',
			error: error instanceof Error ? error.message : String(error),
			stdout,
			stderr
		});
	} finally {
		try {
			pyodide.runPython('import os; os.chdir("/home/pyodide")');
		} catch {
			// Ignore CWD reset errors; workspace cleanup still proceeds.
		}
		cleanupWorkspace(pyodide, session.mounted);
		workspaceSessionById.delete(payload.id);
		executionInProgress = false;
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

void initPyodide().catch((error) => {
	emit({
		id: '__worker_init__',
		status: 'error',
		error: error instanceof Error ? error.message : String(error)
	});
});

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
	if (!payload || typeof payload.id !== 'string') {
		return;
	}

	try {
		const pyodide = await initPyodide();
		if (payload.command === 'SYNC_WORKSPACE') {
			await syncWorkspace(pyodide, payload);
			return;
		}
		if (payload.command === 'RUN_CODE') {
			await runWorkspace(pyodide, payload);
			return;
		}

		// Legacy one-shot mode keeps backward compatibility with older callers.
		await syncWorkspace(pyodide, {
			...payload,
			command: 'SYNC_WORKSPACE'
		});
		await runWorkspace(pyodide, {
			id: payload.id,
			command: 'RUN_CODE',
			main_file: payload.main_file
		});
	} catch (error) {
		emit({
			id: payload.id,
			command: payload.command,
			status: 'error',
			error: error instanceof Error ? error.message : String(error)
		});
	}
};

export {};
