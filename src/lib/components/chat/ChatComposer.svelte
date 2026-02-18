<script lang="ts">
	import IconSet from '$lib/components/icons/IconSet.svelte';
	import {
		compressMedia,
		inferMediaMessageType,
		uploadToR2,
		type MediaMessageType
	} from '$lib/utils/media';
	import { createEventDispatcher, onDestroy } from 'svelte';

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

	const dispatch = createEventDispatcher<{
		send: { type: MediaMessageType; content: string; fileName?: string } | undefined;
		attach: { file: File | null; type: 'media' | 'file'; error?: string };
		removeAttachment: void;
	}>();

	onDestroy(() => {
		clearAttachmentPreview();
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

		<div class="attach-wrap">
			<button type="button" class="attach-button" on:click={toggleAttachMenu} disabled={isProcessingAttachment}>
				<IconSet name="paperclip" size={14} />
				<span>Attach</span>
			</button>
			{#if showAttachMenu}
				<div class="attach-menu">
					<button type="button" on:click={() => chooseAttachmentType('media')}>
						<IconSet name="image" size={14} />
						<span>📷 Media</span>
					</button>
					<button type="button" on:click={() => chooseAttachmentType('file')}>
						<IconSet name="file" size={14} />
						<span>📎 File</span>
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
		<button type="button" class="send-button" on:click={onSend} disabled={isProcessingAttachment}>
			{attachedFile ? 'Send Attachment' : 'Send'}
		</button>
	</div>
</footer>

<style>
	.composer {
		position: relative;
		border-top: 1px solid #dcdcdc;
		background: #ffffff;
		padding: 0.75rem;
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
		flex-shrink: 0;
	}

	.attachment-preview-panel {
		border: 1px solid #d8d8d8;
		background: #f7f7f7;
		border-radius: 10px;
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
		border: 1px solid #c9c9c9;
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
		grid-template-columns: auto 1fr auto;
		gap: 0.55rem;
		align-items: end;
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
		gap: 0.35rem;
		border: 1px solid #c9c9c9;
		background: #ffffff;
		border-radius: 8px;
		padding: 0.52rem 0.72rem;
		font-size: 0.85rem;
		cursor: pointer;
		white-space: nowrap;
		color: #111111;
	}

	.attach-button:disabled,
	.send-button:disabled {
		opacity: 0.7;
		cursor: not-allowed;
	}

	.send-button {
		background: #111111;
		border-color: #111111;
		color: #ffffff;
	}

	.attach-menu {
		position: absolute;
		left: 0;
		bottom: calc(100% + 8px);
		background: #ffffff;
		border: 1px solid #d0d0d0;
		border-radius: 10px;
		box-shadow: 0 10px 22px rgba(0, 0, 0, 0.12);
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
		background: #f0f0f0;
	}

	textarea {
		width: 100%;
		resize: none;
		min-height: 40px;
		max-height: 110px;
		border: 1px solid #cfcfcf;
		border-radius: 9px;
		padding: 0.55rem 0.66rem;
		font-size: 0.91rem;
		font-family: inherit;
		background: #fbfbfb;
		color: #111111;
	}

	@media (max-width: 700px) {
		.composer {
			padding: 0.62rem;
		}

		.composer-row {
			gap: 0.4rem;
		}

		.attach-button,
		.send-button {
			padding: 0.48rem 0.58rem;
			font-size: 0.78rem;
		}

		textarea {
			font-size: 0.86rem;
		}
	}
</style>
