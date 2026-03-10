import { WebContainer, type FileSystemTree } from '@webcontainer/api';

export type WebContainerWorkspaceFile = {
	name: string;
	content: string;
};

export type NodeWorkspaceExecutionResult = {
	stdout: string;
	stderr: string;
	artifacts: WebContainerWorkspaceFile[];
	exitCode: number;
};

const WORKSPACE_MOUNT_POINT = 'tora-workspace';
let webcontainerPromise: Promise<WebContainer> | null = null;
let executionLock: Promise<void> = Promise.resolve();

function assertCrossOriginIsolationReady() {
	const isolated = typeof crossOriginIsolated === 'boolean' ? crossOriginIsolated : false;
	if (isolated) {
		return;
	}
	throw new Error(
		'JavaScript execution requires a cross-origin-isolated page. Ensure COOP/COEP headers are set, use HTTPS (or localhost), open the app directly (not in an iframe), then hard refresh.'
	);
}

export async function initWebContainer() {
	assertCrossOriginIsolationReady();
	if (!webcontainerPromise) {
		webcontainerPromise = WebContainer.boot().catch((error) => {
			webcontainerPromise = null;
			throw error;
		});
	}
	return webcontainerPromise;
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

function buildFileSystemTree(files: WebContainerWorkspaceFile[]) {
	const tree: FileSystemTree = {};
	for (const file of files) {
		const segments = file.name.split('/').filter(Boolean);
		if (segments.length === 0) {
			continue;
		}
		let cursor: FileSystemTree = tree;
		for (let index = 0; index < segments.length; index += 1) {
			const segment = segments[index] as string;
			const isLeaf = index === segments.length - 1;
			if (isLeaf) {
				cursor[segment] = {
					file: {
						contents: file.content
					}
				};
				continue;
			}
			const currentNode = cursor[segment];
			if (!currentNode || !('directory' in currentNode)) {
				cursor[segment] = {
					directory: {}
				};
			}
			cursor = (cursor[segment] as { directory: FileSystemTree }).directory;
		}
	}
	return tree satisfies FileSystemTree;
}

async function readProcessStream(stream: ReadableStream<string>) {
	let output = '';
	const reader = stream.getReader();
	try {
		while (true) {
			const { value, done } = await reader.read();
			if (done) {
				break;
			}
			if (typeof value === 'string') {
				output += value;
			}
		}
	} finally {
		reader.releaseLock();
	}
	return output;
}

function joinPath(parent: string, child: string) {
	return parent ? `${parent}/${child}` : child;
}

async function collectWorkspaceFiles(
	webcontainer: WebContainer,
	directoryPath: string,
	relativePrefix = ''
): Promise<WebContainerWorkspaceFile[]> {
	const entries = await webcontainer.fs.readdir(directoryPath, { withFileTypes: true });
	const files: WebContainerWorkspaceFile[] = [];
	for (const entry of entries) {
		const name = entry.name;
		if (name === '.' || name === '..') {
			continue;
		}
		const absolutePath = joinPath(directoryPath, name);
		const relativePath = joinPath(relativePrefix, name);
		if (entry.isDirectory()) {
			files.push(...(await collectWorkspaceFiles(webcontainer, absolutePath, relativePath)));
			continue;
		}
		try {
			const content = await webcontainer.fs.readFile(absolutePath, 'utf8');
			files.push({
				name: relativePath,
				content
			});
		} catch {
			// Ignore unreadable/binary files and continue collecting artifacts.
		}
	}
	return files;
}

async function cleanupWorkspace(webcontainer: WebContainer) {
	try {
		await webcontainer.fs.rm(WORKSPACE_MOUNT_POINT, {
			force: true,
			recursive: true
		});
	} catch {
		// Ignore cleanup errors to keep runtime reusable.
	}
}

async function runExclusive<T>(run: () => Promise<T>) {
	const previousLock = executionLock;
	let releaseLock: () => void = () => {};
	executionLock = new Promise<void>((resolve) => {
		releaseLock = resolve;
	});
	await previousLock;
	try {
		return await run();
	} finally {
		releaseLock();
	}
}

export async function executeNodeWorkspace(
	files: WebContainerWorkspaceFile[],
	mainFile: string
): Promise<NodeWorkspaceExecutionResult> {
	return runExclusive(async () => {
		const normalizedMainFile = normalizeWorkspacePath(mainFile);
		if (!normalizedMainFile) {
			throw new Error('mainFile is required for Node.js workspace execution');
		}

		const normalizedFiles = (files || [])
			.map((file) => ({
				name: normalizeWorkspacePath(file?.name || ''),
				content: String(file?.content ?? '')
			}))
			.filter((file) => file.name !== '');
		const mainExists = normalizedFiles.some((file) => file.name === normalizedMainFile);
		if (!mainExists) {
			normalizedFiles.unshift({
				name: normalizedMainFile,
				content: ''
			});
		}

		const originalByName = new Map(normalizedFiles.map((file) => [file.name, file.content] as const));
		const webcontainer = await initWebContainer();
		await cleanupWorkspace(webcontainer);
		try {
			await webcontainer.fs.mkdir(WORKSPACE_MOUNT_POINT, { recursive: true });
			await webcontainer.mount(buildFileSystemTree(normalizedFiles), {
				mountPoint: WORKSPACE_MOUNT_POINT
			});

			const slashIndex = normalizedMainFile.lastIndexOf('/');
			const mainDirectory = slashIndex > 0 ? normalizedMainFile.slice(0, slashIndex) : '';
			const mainBasename =
				slashIndex >= 0 ? normalizedMainFile.slice(slashIndex + 1) : normalizedMainFile;
			const cwd = mainDirectory
				? `${WORKSPACE_MOUNT_POINT}/${mainDirectory}`
				: WORKSPACE_MOUNT_POINT;

			const process = await webcontainer.spawn('node', [mainBasename], { cwd });
			const outputPromise = readProcessStream(process.output);
			const exitCode = await process.exit;
			const combinedOutput = await outputPromise;
			const stdout = exitCode === 0 ? combinedOutput : '';
			const stderr = exitCode === 0 ? '' : combinedOutput;

			const snapshotFiles = await collectWorkspaceFiles(webcontainer, WORKSPACE_MOUNT_POINT);
			const artifacts = snapshotFiles.filter((file) => {
				const originalContent = originalByName.get(file.name);
				if (typeof originalContent !== 'string') {
					return true;
				}
				return originalContent !== file.content;
			});

			return {
				stdout,
				stderr,
				artifacts,
				exitCode
			};
		} finally {
			await cleanupWorkspace(webcontainer);
		}
	});
}
