<script lang="ts">
	import IconSet from '$lib/components/icons/IconSet.svelte';
	import {
		compressMedia,
		inferMediaMessageType,
		uploadToR2,
		type MediaMessageType
	} from '$lib/utils/media';
	import { createEventDispatcher, onDestroy, onMount } from 'svelte';

	export let draftMessage = '';
	export let attachedFile: File | null = null;

	let mediaInput: HTMLInputElement | null = null;
	let fileInput: HTMLInputElement | null = null;
	let showAttachMenu = false;
	let attachError = '';
	let isProcessingAttachment = false;
	let attachedMessageType: MediaMessageType | null = null;
	let attachedPickerType: 'media' | 'file' = 'file';
	let attachmentPreviewURL = '';
	let attachWrapEl: HTMLDivElement | null = null;

	const dispatch = createEventDispatcher<{
		send: { type: MediaMessageType; content: string; fileName?: string } | undefined;
		attach: { file: File | null; type: 'media' | 'file'; error?: string };
		removeAttachment: void;
	}>();

	onDestroy(() => {
		clearAttachmentPreview();
	});

	onMount(() => {
		const onDocumentPointerDown = (event: PointerEvent) => {
			if (!showAttachMenu) {
				return;
			}
			const target = event.target;
			if (!(target instanceof Node)) {
				return;
			}
			if (attachWrapEl && !attachWrapEl.contains(target)) {
				showAttachMenu = false;
			}
		};

		window.addEventListener('pointerdown', onDocumentPointerDown);
		return () => {
			window.removeEventListener('pointerdown', onDocumentPointerDown);
		};
	});

	function toggleAttachMenu() {
		showAttachMenu = !showAttachMenu;
	}

	function chooseAttachmentType(type: 'media' | 'file') {
		showAttachMenu = false;
		attachError = '';
		if (type === 'media') {
			mediaInput?.click();
			return;
		}
		fileInput?.click();
	}

	function resolveMessageType(file: File, pickerType: 'media' | 'file'): MediaMessageType {
		if (pickerType === 'file') {
			if (file.type.startsWith('image/')) {
				return 'image';
			}
			if (file.type.startsWith('video/')) {
				return 'video';
			}
			return 'file';
		}
		return inferMediaMessageType(file);
	}

	function clearAttachmentPreview() {
		if (attachmentPreviewURL) {
			URL.revokeObjectURL(attachmentPreviewURL);
			attachmentPreviewURL = '';
		}
	}

	function setAttachmentPreview(file: File, messageType: MediaMessageType) {
		clearAttachmentPreview();
		if (messageType === 'image' || messageType === 'video') {
			attachmentPreviewURL = URL.createObjectURL(file);
		}
	}

	async function onFilePicked(event: Event, pickerType: 'media' | 'file') {
		const target = event.currentTarget as HTMLInputElement;
		const selected = target.files?.[0] ?? null;
		target.value = '';
		if (!selected) {
			return;
		}

		const messageType = resolveMessageType(selected, pickerType);
		attachError = '';
		attachedFile = selected;
		attachedMessageType = messageType;
		attachedPickerType = pickerType;
		setAttachmentPreview(selected, messageType);
		dispatch('attach', { file: selected, type: pickerType });
	}

	async function sendAttachment() {
		if (!attachedFile || !attachedMessageType) {
			dispatch('send', undefined);
			return;
		}

		isProcessingAttachment = true;
		attachError = '';
		try {
			const compressed = await compressMedia(attachedFile);
			const uploaded = await uploadToR2(compressed);
			dispatch('send', {
				type: attachedMessageType,
				content: uploaded.fileUrl,
				fileName: attachedFile.name
			});
			clearAttachmentPreview();
			attachedFile = null;
			attachedMessageType = null;
			dispatch('attach', { file: null, type: attachedPickerType });
		} catch (error) {
			const message = error instanceof Error ? error.message : 'Attachment failed';
			attachError = message;
			dispatch('attach', { file: attachedFile, type: attachedPickerType, error: message });
		} finally {
			isProcessingAttachment = false;
		}
	}

	function removeAttachment() {
		clearAttachmentPreview();
		attachedFile = null;
		attachedMessageType = null;
		attachError = '';
		dispatch('removeAttachment');
	}

	function onSend() {
		if (isProcessingAttachment) {
			return;
		}
		if (attachedFile) {
			void sendAttachment();
			return;
		}
		dispatch('send', undefined);
	}

	function onComposerKeyDown(event: KeyboardEvent) {
		if (event.key === 'Enter' && !event.shiftKey) {
			event.preventDefault();
			onSend();
		}
	}

	function getAttachmentLabel(type: MediaMessageType | null) {
		if (type === 'image') {
			return 'Image ready to send';
		}
		if (type === 'video') {
			return 'Video ready to send';
		}
		if (type === 'file') {
			return 'File ready to send';
		}
		return 'Attachment ready to send';
	}
</script>

<footer class="composer">
	{#if attachedFile}
		<div class="attachment-preview-panel">
			<div class="attachment-preview-header">
				<div class="attachment-preview-title">{getAttachmentLabel(attachedMessageType)}</div>
				<button type="button" class="preview-remove" on:click={removeAttachment}>x</button>
			</div>
			{#if attachedMessageType === 'image' && attachmentPreviewURL}
				<img src={attachmentPreviewURL} alt={attachedFile.name} class="attachment-preview-image" />
			{:else if attachedMessageType === 'video' && attachmentPreviewURL}
				<!-- svelte-ignore a11y_media_has_caption -->
				<video src={attachmentPreviewURL} class="attachment-preview-video" controls preload="metadata"></video>
			{:else}
				<div class="attachment-preview-file">
					<IconSet name="file" size={18} />
					<span>{attachedFile.name}</span>
				</div>
			{/if}
		</div>
	{/if}
	{#if attachError}
		<div class="attachment-error">{attachError}</div>
	{/if}
	{#if isProcessingAttachment}
		<div class="attachment-progress">Compressing &amp; Uploading...</div>
	{/if}
	<div class="composer-row">
		<input
			bind:this={mediaInput}
			type="file"
			class="hidden-file-input"
			accept="image/*,video/*"
			on:change={(event) => void onFilePicked(event, 'media')}
		/>
		<input
			bind:this={fileInput}
			type="file"
			class="hidden-file-input"
			accept="*"
			on:change={(event) => void onFilePicked(event, 'file')}
		/>

		<div class="attach-wrap" bind:this={attachWrapEl}>
			<button
				type="button"
				class="attach-button"
				on:click={toggleAttachMenu}
				disabled={isProcessingAttachment}
				aria-label="Attach"
				title="Attach"
			>
				<IconSet name="paperclip" size={14} />
			</button>
			{#if showAttachMenu}
				<div class="attach-menu">
					<button type="button" on:click={() => chooseAttachmentType('media')}>
						<IconSet name="image" size={14} />
						<span>Media</span>
					</button>
					<button type="button" on:click={() => chooseAttachmentType('file')}>
						<IconSet name="file" size={14} />
						<span>File</span>
					</button>
				</div>
			{/if}
		</div>

		<textarea
			bind:value={draftMessage}
			rows="1"
			placeholder={attachedFile ? 'Add a caption (optional)' : 'Type a message'}
			on:keydown={onComposerKeyDown}
			disabled={isProcessingAttachment}
		></textarea>
			<button
				type="button"
				class="send-button"
				on:click={onSend}
				disabled={isProcessingAttachment}
				aria-label={attachedFile ? 'Send attachment' : 'Send message'}
				title={attachedFile ? 'Send attachment' : 'Send message'}
			>
				<IconSet name="send" size={15} />
			</button>
		</div>
	</footer>

<style>
	.composer {
		position: relative;
		border-top: 1px solid #d6deea;
		background: linear-gradient(180deg, #f7f9fc 0%, #f1f4f9 100%);
		padding: 0.54rem 0.6rem;
		display: flex;
		flex-direction: column;
		gap: 0.4rem;
		flex-shrink: 0;
	}

	.attachment-preview-panel {
		border: 1px solid #d2dbe8;
		background: #f8fafd;
		border-radius: 12px;
		padding: 0.55rem;
		display: flex;
		flex-direction: column;
		gap: 0.45rem;
	}

	.attachment-preview-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}

	.attachment-preview-title {
		font-size: 0.78rem;
		font-weight: 600;
		color: #222222;
	}

	.preview-remove {
		border: 1px solid #c8d2df;
		background: #ffffff;
		border-radius: 6px;
		width: 24px;
		height: 24px;
		cursor: pointer;
		color: #222222;
	}

	.attachment-preview-image,
	.attachment-preview-video {
		display: block;
		width: min(100%, 320px);
		max-height: 230px;
		border: 1px solid #d0d0d0;
		border-radius: 8px;
		background: #111111;
		object-fit: cover;
	}

	.attachment-preview-file {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		color: #141414;
		font-size: 0.84rem;
		padding: 0.35rem 0.15rem;
	}

	.attachment-error {
		font-size: 0.79rem;
		color: #2a2a2a;
	}

	.attachment-progress {
		font-size: 0.79rem;
		color: #2a2a2a;
	}

	.composer-row {
		display: grid;
		grid-template-columns: 2.2rem minmax(0, 1fr) 2.2rem;
		gap: 0.42rem;
		align-items: center;
		border: 1px solid #cfd8e6;
		background: #ffffff;
		border-radius: 14px;
		padding: 0.28rem 0.3rem;
		box-shadow: 0 4px 14px rgba(15, 23, 42, 0.06);
	}

	.hidden-file-input {
		display: none;
	}

	.attach-wrap {
		position: relative;
	}

	.attach-button,
	.send-button {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		border: 1px solid #c7d2e1;
		background: #f8fafd;
		border-radius: 10px;
		width: 2.1rem;
		height: 2.1rem;
		cursor: pointer;
		color: #243244;
		padding: 0;
	}

	.attach-button:disabled,
	.send-button:disabled {
		opacity: 0.7;
		cursor: not-allowed;
	}

	.attach-button:hover:not(:disabled),
	.send-button:hover:not(:disabled) {
		background: #eef3f9;
	}

	.send-button {
		background: #263445;
		border-color: #263445;
		color: #ffffff;
	}

	.send-button:hover:not(:disabled) {
		background: #1e2a38;
	}

	.attach-menu {
		position: absolute;
		left: 0;
		bottom: calc(100% + 8px);
		background: #fbfcfe;
		border: 1px solid #d4dcea;
		border-radius: 10px;
		box-shadow: 0 12px 24px rgba(15, 23, 42, 0.14);
		padding: 0.3rem;
		z-index: 120;
		min-width: 132px;
	}

	.attach-menu button {
		width: 100%;
		display: flex;
		align-items: center;
		gap: 0.4rem;
		border: none;
		background: transparent;
		padding: 0.45rem 0.55rem;
		cursor: pointer;
		border-radius: 7px;
		font-size: 0.84rem;
		color: #111111;
	}

	.attach-menu button:hover {
		background: #eef2f7;
	}

	textarea {
		width: 100%;
		min-width: 0;
		resize: none;
		min-height: 2.1rem;
		max-height: 110px;
		border: 1px solid #d3dbe8;
		border-radius: 10px;
		padding: 0.4rem 0.56rem;
		font-size: 0.9rem;
		line-height: 1.32;
		font-family: inherit;
		background: #fbfcfe;
		color: #111111;
		box-sizing: border-box;
	}

	textarea:focus {
		outline: none;
		border-color: #6b7c93;
	}

	@media (max-width: 700px) {
		.composer {
			padding: 0.48rem;
		}

		.composer-row {
			gap: 0.34rem;
		}

		.attach-button,
		.send-button {
			width: 2rem;
			height: 2rem;
		}

		textarea {
			font-size: 0.86rem;
		}
	}
</style>
