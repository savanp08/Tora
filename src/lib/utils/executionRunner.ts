import { writable } from 'svelte/store';
import { executeCodeWithRouter } from '$lib/utils/executionManager';

export type ExecutionRunnerState = {
	isLoading: boolean;
	output: string;
	error: string;
};

export function createExecutionRunner() {
	const state = writable<ExecutionRunnerState>({
		isLoading: false,
		output: '',
		error: ''
	});
	let pythonWorker: Worker | null = null;

	function ensurePythonWorker() {
		if (!pythonWorker) {
			pythonWorker = new Worker(new URL('../workers/pyodide.worker.ts', import.meta.url), {
				type: 'module'
			});
		}
		return pythonWorker;
	}

	async function executeCode(code: string, language: string) {
		state.update((current) => ({
			...current,
			isLoading: true,
			error: ''
		}));
		try {
			const normalizedLanguage = (language || '').trim().toLowerCase();
			const result = await executeCodeWithRouter(code, normalizedLanguage, {
				pythonWorker:
					normalizedLanguage === 'python' || normalizedLanguage === 'py'
						? ensurePythonWorker()
						: undefined
			});
			const terminalOutput = [result.output, result.error].filter(Boolean).join('\n').trim();
			state.set({
				isLoading: false,
				output: terminalOutput,
				error: ''
			});
			return result;
		} catch (error) {
			const message = error instanceof Error ? error.message : String(error);
			state.set({
				isLoading: false,
				output: '',
				error: message
			});
			throw error;
		}
	}

	function reset() {
		state.set({
			isLoading: false,
			output: '',
			error: ''
		});
	}

	function destroy() {
		pythonWorker?.terminate();
		pythonWorker = null;
	}

	return {
		state,
		executeCode,
		reset,
		destroy
	};
}
