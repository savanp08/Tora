declare module 'https://cdn.jsdelivr.net/pyodide/v0.25.0/full/pyodide.mjs' {
	export function loadPyodide(options?: { indexURL?: string }): Promise<{
		runPythonAsync: (code: string) => Promise<unknown>;
		runPython: (code: string) => unknown;
	}>;
}
