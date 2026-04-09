import { writable } from 'svelte/store';
import { browser } from '$app/environment';

export type AIEffortLevel = 'fast' | 'extended' | 'max';

export type AIModelInfo = {
	id: string;
	label: string;
	provider: string;
	tier: string;
	icon: string;
};

export type AIEffortInfo = {
	id: AIEffortLevel;
	label: string;
	description: string;
	tier: string;
};

export type AISettings = {
	modelId: string; // specific model ID or 'auto'
	effort: AIEffortLevel;
};

// Effort levels ordered by speed ascending (fastest first).
export const effortLevels: AIEffortInfo[] = [
	{ id: 'fast', label: 'Fast', description: 'Quickest responses, light reasoning', tier: 'light' },
	{ id: 'extended', label: 'Extended', description: 'Balanced quality and speed', tier: 'standard' },
	{ id: 'max', label: 'Max', description: 'Deepest reasoning, best quality', tier: 'heavy' }
];

const STORAGE_KEY = 'tora_ai_settings';
const DEFAULTS: AISettings = { modelId: 'auto', effort: 'extended' };

function loadFromStorage(): AISettings {
	if (!browser) return { ...DEFAULTS };
	try {
		const raw = localStorage.getItem(STORAGE_KEY);
		if (raw) {
			const parsed = JSON.parse(raw) as Partial<AISettings>;
			return {
				modelId: typeof parsed.modelId === 'string' ? parsed.modelId : DEFAULTS.modelId,
				effort:
					parsed.effort === 'fast' || parsed.effort === 'extended' || parsed.effort === 'max'
						? parsed.effort
						: DEFAULTS.effort
			};
		}
	} catch {
		// ignore
	}
	return { ...DEFAULTS };
}

function persist(settings: AISettings) {
	if (browser) {
		localStorage.setItem(STORAGE_KEY, JSON.stringify(settings));
	}
}

function createAISettingsStore() {
	const { subscribe, set, update } = writable<AISettings>(loadFromStorage());

	return {
		subscribe,
		setModel(modelId: string) {
			update((s) => {
				const next = { ...s, modelId };
				persist(next);
				return next;
			});
		},
		setEffort(effort: AIEffortLevel) {
			update((s) => {
				const next = { ...s, effort };
				persist(next);
				return next;
			});
		},
		reset() {
			set({ ...DEFAULTS });
			persist({ ...DEFAULTS });
		}
	};
}

export const aiSettings = createAISettingsStore();

// Cache of models fetched from the backend.
export const availableModels = writable<AIModelInfo[]>([]);
export const modelsLoading = writable(false);

const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';

let fetchedOnce = false;

export async function fetchAvailableModels(): Promise<void> {
	if (fetchedOnce) return;
	fetchedOnce = true;
	modelsLoading.set(true);
	try {
		const res = await fetch(`${API_BASE}/api/ai/models`);
		if (!res.ok) return;
		const data = (await res.json()) as { models: AIModelInfo[]; efforts: AIEffortInfo[] };
		if (Array.isArray(data.models)) {
			availableModels.set(data.models);
		}
	} catch {
		// silently fail — selector will show effort only
	} finally {
		modelsLoading.set(false);
	}
}

// Compact a conversation and return the summary.
// universal=true caches the result server-side for shared reuse.
export async function compactContext(
	messages: { role: string; content: string }[],
	roomId = '',
	universal = false
): Promise<{ summary: string; cacheKey: string; cached: boolean } | null> {
	try {
		const res = await fetch(`${API_BASE}/api/ai/context/compact`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ messages, roomId, universal })
		});
		if (!res.ok) return null;
		return (await res.json()) as { summary: string; cacheKey: string; cached: boolean };
	} catch {
		return null;
	}
}
