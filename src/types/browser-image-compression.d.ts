declare module 'browser-image-compression' {
	type ImageCompressionOptions = {
		maxSizeMB?: number;
		maxWidthOrHeight?: number;
		useWebWorker?: boolean;
	};

	function imageCompression(file: File, options: ImageCompressionOptions): Promise<File | Blob>;
	export default imageCompression;
}
