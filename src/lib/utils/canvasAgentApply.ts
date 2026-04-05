export type CanvasAgenticChange = {
	kind?: string;
	file_path?: string;
	content?: string;
	description?: string;
	lines?: number;
	operation?: string;
};

export type CanvasAgenticApplyResult = {
	applied: number;
	failed: number;
	changesApplied: number;
	foldersCreated: number;
	filesCreated: number;
	filesUpdated: number;
};

type SharedTreeEntry = {
	isDir: boolean;
};

function normalizeCanvasRelativePath(value: string) {
	return (value || '')
		.trim()
		.replace(/\\/g, '/')
		.replace(/^\/+/, '')
		.split('/')
		.filter(Boolean)
		.join('/');
}

function normalizeSharedTreeEntry(value: unknown): SharedTreeEntry | null {
	if (!value || typeof value !== 'object') {
		return null;
	}
	return {
		isDir: Boolean((value as { isDir?: unknown }).isDir)
	};
}

function yTextKeyForFile(filePath: string) {
	return `file:${normalizeCanvasRelativePath(filePath)}`;
}

function collectParentDirectories(relativePath: string) {
	const normalized = normalizeCanvasRelativePath(relativePath);
	if (!normalized || !normalized.includes('/')) {
		return [] as string[];
	}
	const segments = normalized.split('/');
	const parents: string[] = [];
	for (let index = 0; index < segments.length - 1; index += 1) {
		parents.push(segments.slice(0, index + 1).join('/'));
	}
	return parents;
}

function inferCanvasMirrorLanguage(filePath: string) {
	const normalized = normalizeCanvasRelativePath(filePath);
	const extension = normalized.includes('.') ? normalized.split('.').pop()?.toLowerCase() ?? '' : '';
	switch (extension) {
		case 'js':
		case 'mjs':
		case 'cjs':
			return 'javascript';
		case 'ts':
		case 'tsx':
			return 'typescript';
		case 'jsx':
			return 'javascriptreact';
		case 'py':
			return 'python';
		case 'go':
			return 'go';
		case 'rs':
			return 'rust';
		case 'java':
			return 'java';
		case 'c':
			return 'c';
		case 'cpp':
		case 'cc':
		case 'cxx':
		case 'hpp':
		case 'h':
			return 'cpp';
		case 'json':
			return 'json';
		case 'html':
		case 'htm':
			return 'html';
		case 'css':
		case 'scss':
		case 'sass':
			return 'css';
		case 'md':
		case 'mdx':
			return 'markdown';
		case 'sh':
		case 'bash':
		case 'zsh':
			return 'shell';
		default:
			return '';
	}
}

function syncYTextValue(ytext: {
	length?: number;
	delete: (index: number, length: number) => void;
	insert: (index: number, text: string) => void;
	toString: () => string;
}, content: string) {
	const existingLength =
		typeof ytext.length === 'number' && Number.isFinite(ytext.length)
			? Math.max(0, ytext.length)
			: ytext.toString().length;
	if (existingLength > 0) {
		ytext.delete(0, existingLength);
	}
	if (content) {
		ytext.insert(0, content);
	}
}

function buildSnapshotURL(apiBase: string, roomId: string) {
	return `${apiBase}/api/canvas/${encodeURIComponent(roomId)}/snapshot`;
}

function buildMirrorURL(apiBase: string, roomId: string) {
	return `${apiBase}/api/canvas/${encodeURIComponent(roomId)}/files`;
}

async function loadRoomCanvasDoc(apiBase: string, roomId: string) {
	const { Doc, applyUpdate } = await import('yjs');
	const doc = new Doc();
	const response = await fetch(buildSnapshotURL(apiBase, roomId), {
		cache: 'no-store'
	});
	if (response.status === 404 || response.status === 204) {
		return doc;
	}
	if (!response.ok) {
		throw new Error(`Failed to load room canvas snapshot (${response.status}).`);
	}
	const snapshotBytes = new Uint8Array(await response.arrayBuffer());
	if (snapshotBytes.byteLength > 0) {
		applyUpdate(doc, snapshotBytes);
	}
	return doc;
}

function buildMirrorFilesFromDoc(doc: any) {
	const fileTree = doc.getMap('fileTree') as {
		entries: () => IterableIterator<[string, unknown]>;
	};
	const files: Array<{ path: string; language: string; content: string }> = [];
	for (const [key, rawEntry] of fileTree.entries()) {
		const relativePath = normalizeCanvasRelativePath(String(key));
		const entry = normalizeSharedTreeEntry(rawEntry);
		if (!relativePath || !entry || entry.isDir) {
			continue;
		}
		files.push({
			path: relativePath,
			language: inferCanvasMirrorLanguage(relativePath),
			content: String(doc.getText(yTextKeyForFile(relativePath)).toString() || '')
		});
	}
	files.sort((left, right) => left.path.localeCompare(right.path));
	return files;
}

async function persistRoomCanvasDoc(apiBase: string, roomId: string, doc: any) {
	const { encodeStateAsUpdate } = await import('yjs');
	const snapshotBytes = new Uint8Array(encodeStateAsUpdate(doc));
	const snapshotResponse = await fetch(buildSnapshotURL(apiBase, roomId), {
		method: 'POST',
		headers: {
			'Content-Type': 'application/octet-stream'
		},
		body: snapshotBytes
	});
	if (!snapshotResponse.ok) {
		throw new Error(`Failed to save room canvas snapshot (${snapshotResponse.status}).`);
	}

	const files = buildMirrorFilesFromDoc(doc);
	const mirrorResponse = await fetch(buildMirrorURL(apiBase, roomId), {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json'
		},
		body: JSON.stringify({ files })
	});
	if (!mirrorResponse.ok) {
		throw new Error(`Failed to sync room canvas files (${mirrorResponse.status}).`);
	}
}

export async function loadPersistedRoomCanvasFileContents(input: {
	apiBase: string;
	roomId: string;
}): Promise<Record<string, string>> {
	const apiBase = String(input.apiBase || '').trim();
	const roomId = String(input.roomId || '').trim();
	if (!apiBase || !roomId) {
		return {};
	}

	const doc = await loadRoomCanvasDoc(apiBase, roomId);
	const files = buildMirrorFilesFromDoc(doc);
	const byPath: Record<string, string> = {};
	for (const file of files) {
		const normalizedPath = normalizeCanvasRelativePath(file.path);
		if (!normalizedPath) {
			continue;
		}
		byPath[normalizedPath] = String(file.content || '');
	}
	return byPath;
}

export async function applyCanvasChangesToPersistedRoomCanvas(input: {
	apiBase: string;
	roomId: string;
	changes: CanvasAgenticChange[];
}): Promise<CanvasAgenticApplyResult> {
	const apiBase = String(input.apiBase || '').trim();
	const roomId = String(input.roomId || '').trim();
	if (!apiBase) {
		throw new Error('Canvas API base is unavailable.');
	}
	if (!roomId) {
		throw new Error('Canvas room id is unavailable.');
	}

	const latestByPath = new Map<string, CanvasAgenticChange>();
	for (const rawChange of input.changes || []) {
		const filePath = normalizeCanvasRelativePath(String(rawChange?.file_path || ''));
		const content = typeof rawChange?.content === 'string' ? rawChange.content : '';
		if (!filePath || !content.trim()) {
			continue;
		}
		latestByPath.set(filePath, {
			...rawChange,
			file_path: filePath,
			content
		});
	}
	if (latestByPath.size === 0) {
		throw new Error('No valid canvas changes were included in this proposal.');
	}

	const doc = await loadRoomCanvasDoc(apiBase, roomId);
	const fileTree = doc.getMap('fileTree') as {
		get: (key: string) => unknown;
		set: (key: string, value: unknown) => unknown;
	};

	const createdDirectories = new Set<string>();
	let filesCreated = 0;
	let filesUpdated = 0;

	for (const [filePath, change] of latestByPath.entries()) {
		const existingEntry = normalizeSharedTreeEntry(fileTree.get(filePath));
		const fileAlreadyExists = Boolean(existingEntry) && !existingEntry?.isDir;
		if (fileAlreadyExists) {
			filesUpdated += 1;
		} else {
			filesCreated += 1;
		}

		for (const directoryPath of collectParentDirectories(filePath)) {
			const existingDirectoryEntry = normalizeSharedTreeEntry(fileTree.get(directoryPath));
			if (!existingDirectoryEntry?.isDir) {
				fileTree.set(directoryPath, { isDir: true });
				createdDirectories.add(directoryPath);
			}
		}

		fileTree.set(filePath, { isDir: false });
		const ytext = doc.getText(yTextKeyForFile(filePath)) as {
			length?: number;
			delete: (index: number, length: number) => void;
			insert: (index: number, text: string) => void;
			toString: () => string;
		};
		syncYTextValue(ytext, change.content || '');
	}

	await persistRoomCanvasDoc(apiBase, roomId, doc);

	return {
		applied: latestByPath.size,
		failed: 0,
		changesApplied: latestByPath.size,
		foldersCreated: createdDirectories.size,
		filesCreated,
		filesUpdated
	};
}
