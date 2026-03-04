import { browser } from '$app/environment';
import LightningFS from '@isomorphic-git/lightning-fs';

/** @type {Map<string, any>} */
let fsInstances = new Map();

/** @param {string | null | undefined} namespace */
function normalizeNamespace(namespace) {
	const value = String(namespace || 'tora-canvas-fs').trim();
	if (!value) {
		return 'tora-canvas-fs';
	}
	return `tora-canvas-fs-${value.replace(/[^a-zA-Z0-9_-]+/g, '-')}`;
}

/** @param {string | null | undefined} namespace */
function ensureFSInstance(namespace) {
	if (!browser) {
		return null;
	}
	const key = normalizeNamespace(namespace);
	let instance = fsInstances.get(key) ?? null;
	if (!instance) {
		instance = new LightningFS(key);
		fsInstances.set(key, instance);
	}
	return instance;
}

/** @param {string | null | undefined} namespace */
export async function createInitialFiles(namespace) {
	const activeFS = ensureFSInstance(namespace);
	if (!activeFS) {
		return null;
	}
	try {
		await activeFS.promises.stat('/project');
	} catch {
		await activeFS.promises.mkdir('/project');
	}
	return activeFS;
}

/** @param {string | null | undefined} namespace */
export async function initFileSystem(namespace) {
	const activeFS = ensureFSInstance(namespace);
	if (!activeFS) {
		return null;
	}
	await createInitialFiles(namespace);
	return activeFS;
}

/** @param {string | null | undefined} namespace */
export function getFS(namespace) {
	if (!browser) {
		return null;
	}
	return fsInstances.get(normalizeNamespace(namespace)) ?? null;
}
