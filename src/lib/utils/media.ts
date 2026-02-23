const API_BASE = (import.meta.env.VITE_API_BASE as string | undefined) ?? 'http://localhost:8080';
const MB = 1024 * 1024;
const VIDEO_LIMIT_BYTES = 50 * MB;

export type MediaMessageType = 'image' | 'video' | 'file' | 'audio';

type PresignedUploadResponse = {
	uploadUrl: string;
	fileUrl: string;
	fileId: string;
};

export async function compressMedia(file: File): Promise<File> {
	if (file.type.startsWith('image/')) {
		const mod = await import('browser-image-compression');
		const imageCompression = mod.default;
		const compressed = await imageCompression(file, {
			maxSizeMB: 1,
			maxWidthOrHeight: 1920,
			useWebWorker: true
		});
		return compressed as File;
	}

	if (file.type.startsWith('video/') && file.size > VIDEO_LIMIT_BYTES) {
		throw new Error('File too large for free tier');
	}

	return file;
}

export async function uploadToR2(file: File): Promise<{ fileUrl: string; fileId: string }> {
	let presignedData:
		| (Partial<PresignedUploadResponse> & Record<string, unknown>)
		| null = null;
	let presignedError = '';

	try {
		const presignedRes = await fetch(`${API_BASE}/api/upload/presigned`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({
				filename: file.name,
				filetype: file.type || 'application/octet-stream',
				filesize: file.size
			})
		});
		presignedData = (await presignedRes
			.json()
			.catch(() => ({}))) as Partial<PresignedUploadResponse> & Record<string, unknown>;
		if (
			!presignedRes.ok ||
			!presignedData.uploadUrl ||
			!presignedData.fileUrl ||
			!presignedData.fileId
		) {
			presignedError =
				typeof presignedData.error === 'string' ? presignedData.error : 'Failed to request upload URL';
		}
	} catch (error) {
		presignedError = error instanceof Error ? error.message : 'Failed to request upload URL';
	}

	if (presignedData?.uploadUrl && presignedData.fileUrl && presignedData.fileId) {
		try {
			const uploadRes = await fetch(presignedData.uploadUrl, {
				method: 'PUT',
				body: file,
				headers: file.type ? { 'Content-Type': file.type } : undefined
			});
			if (!uploadRes.ok) {
				throw new Error(`Upload failed (${uploadRes.status})`);
			}
			return {
				fileUrl: toAbsoluteAPIURL(String(presignedData.fileUrl)),
				fileId: String(presignedData.fileId)
			};
		} catch {
			return uploadViaProxy(file, true);
		}
	}

	try {
		return await uploadViaProxy(file, false);
	} catch (proxyError) {
		if (presignedError) {
			throw new Error(`${presignedError}. Proxy upload failed.`);
		}
		throw proxyError;
	}
}

export function inferMediaMessageType(file: File): MediaMessageType {
	if (file.type.startsWith('image/')) {
		return 'image';
	}
	if (file.type.startsWith('video/')) {
		return 'video';
	}
	if (file.type.startsWith('audio/')) {
		return 'audio';
	}
	return 'file';
}

function toAbsoluteAPIURL(value: string): string {
	const trimmed = value.trim();
	if (!trimmed) {
		return trimmed;
	}
	if (/^https?:\/\//i.test(trimmed)) {
		try {
			const parsed = new URL(trimmed);
			if (parsed.hostname.endsWith('.r2.cloudflarestorage.com')) {
				const parts = parsed.pathname.split('/').filter(Boolean);
				if (parts.length >= 2) {
					const objectKey = decodeIfNeeded(parts.slice(1).join('/'));
					return `${API_BASE}/api/upload/object/${encodeURIComponent(objectKey)}`;
				}
			}
		} catch {
			return trimmed;
		}
		return trimmed;
	}
	if (trimmed.startsWith('/')) {
		return `${API_BASE}${trimmed}`;
	}
	return `${API_BASE}/${trimmed}`;
}

async function uploadViaProxy(
	file: File,
	alreadyCountedByPresigned: boolean
): Promise<{ fileUrl: string; fileId: string }> {
	const payload = new FormData();
	payload.append('file', file, file.name);
	const endpoint = `${API_BASE}/api/upload${alreadyCountedByPresigned ? '?counted=1' : ''}`;

	const res = await fetch(endpoint, {
		method: 'POST',
		body: payload
	});

	const data = (await res
		.json()
		.catch(() => ({}))) as Partial<PresignedUploadResponse> & Record<string, unknown>;
	if (!res.ok || !data.fileUrl || !data.fileId) {
		throw new Error(typeof data.error === 'string' ? data.error : 'Upload failed');
	}

	return {
		fileUrl: toAbsoluteAPIURL(String(data.fileUrl)),
		fileId: String(data.fileId)
	};
}

function decodeIfNeeded(input: string): string {
	try {
		return decodeURIComponent(input);
	} catch {
		return input;
	}
}
