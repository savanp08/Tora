import { APP_LIMITS } from '$lib/config/limits';

const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';
const VIDEO_LIMIT_BYTES = APP_LIMITS.media.maxVideoBytes;
export const STORAGE_FULL_UPLOAD_MESSAGE =
	'Server storage is temporarily full. Uploads will be available again once older rooms expire.';

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
			maxSizeMB: APP_LIMITS.media.imageCompressionMaxSizeMB,
			maxWidthOrHeight: APP_LIMITS.media.imageCompressionMaxWidthOrHeight,
			useWebWorker: true
		});
		return compressed as File;
	}

	if (file.type.startsWith('video/') && file.size > VIDEO_LIMIT_BYTES) {
		throw new Error('File too large for free tier');
	}

	return file;
}

export async function uploadToR2(
	file: File,
	roomId = ''
): Promise<{ fileUrl: string; fileId: string }> {
	return uploadViaProxy(file, roomId, false);
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
	roomId: string,
	alreadyCountedByPresigned: boolean
): Promise<{ fileUrl: string; fileId: string }> {
	const payload = new FormData();
	payload.append('file', file, file.name);
	const queryParams = new URLSearchParams();
	if (alreadyCountedByPresigned) {
		queryParams.set('counted', '1');
	}
	if (roomId) {
		queryParams.set('roomId', roomId);
	}
	const endpoint = `${API_BASE}/api/upload${queryParams.toString() ? `?${queryParams.toString()}` : ''}`;

	const res = await fetch(endpoint, {
		method: 'POST',
		body: payload
	});

	const data = (await res
		.json()
		.catch(() => ({}))) as Partial<PresignedUploadResponse> & Record<string, unknown>;
	if (!res.ok || !data.fileUrl || !data.fileId) {
		if (res.status === 507) {
			throw new Error(STORAGE_FULL_UPLOAD_MESSAGE);
		}
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
