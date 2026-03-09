import { APP_LIMITS } from '$lib/config/limits';
export type AttachmentType = 'media' | 'file';

const VIDEO_LIMIT_BYTES = APP_LIMITS.media.maxVideoBytes;

export async function compressImage(file: File): Promise<File> {
	try {
		const mod = await import('browser-image-compression');
		const imageCompression = mod.default;
		const compressed = await imageCompression(file, {
			maxSizeMB: APP_LIMITS.media.imageCompressionMaxSizeMB,
			maxWidthOrHeight: APP_LIMITS.media.imageCompressionMaxWidthOrHeight,
			useWebWorker: true
		});
		return compressed as File;
	} catch {
		return file;
	}
}

export async function compressVideo(file: File): Promise<File> {
	if (file.size > VIDEO_LIMIT_BYTES) {
		throw new Error(
			`Video is too large. Max supported size is ${Math.max(
				1,
				Math.round(VIDEO_LIMIT_BYTES / (1024 * 1024))
			)}MB.`
		);
	}
	return file;
}

export async function handleAttachment(file: File, type: AttachmentType): Promise<File> {
	if (type === 'file') {
		return file;
	}

	if (file.type.startsWith('image/')) {
		return compressImage(file);
	}

	if (file.type.startsWith('video/')) {
		return compressVideo(file);
	}

	return file;
}
