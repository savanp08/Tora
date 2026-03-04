import { browser } from '$app/environment';
import LightningFS from '@isomorphic-git/lightning-fs';

/** @type {any | null} */
let fsInstance = null;

function ensureFSInstance() {
	if (!browser) {
		return null;
	}
	if (!fsInstance) {
		fsInstance = new LightningFS('tora-canvas-fs');
	}
	return fsInstance;
}

export async function createInitialFiles() {
	const activeFS = ensureFSInstance();
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

export async function initFileSystem() {
	const activeFS = ensureFSInstance();
	if (!activeFS) {
		return null;
	}
	await createInitialFiles();
	return activeFS;
}

export function getFS() {
	return fsInstance;
}
