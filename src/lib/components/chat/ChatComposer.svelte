<script lang="ts">
	import IconSet from '$lib/components/icons/IconSet.svelte';
	import { getUTF8ByteLength, MESSAGE_TEXT_MAX_BYTES } from '$lib/utils/chat/core';
	import {
		compressMedia,
		inferMediaMessageType,
		uploadToR2,
		type MediaMessageType
	} from '$lib/utils/media';
	import type { ReplyTarget } from '$lib/types/chat';
	import { createEventDispatcher, onDestroy, onMount } from 'svelte';

	export let draftMessage = '';
	export let attachedFile: File | null = null;
	export let activeReply: ReplyTarget | null = null;
	export let isDarkMode = false;
	export let messageLimit = MESSAGE_TEXT_MAX_BYTES;

	let mediaInput: HTMLInputElement | null = null;
	let fileInput: HTMLInputElement | null = null;
	let showAttachMenu = false;
	let attachError = '';
	let isProcessingAttachment = false;
	let attachedMessageType: MediaMessageType | null = null;
	let attachedPickerType: 'media' | 'file' = 'file';
	let attachmentPreviewURL = '';
	let attachWrapEl: HTMLDivElement | null = null;
	let normalizedDraftMessage = '';
	let draftMessageBytes = 0;

	$: normalizedDraftMessage = draftMessage.trim();
	$: draftMessageBytes = getUTF8ByteLength(normalizedDraftMessage);
	$: isOverMessageLimit = draftMessageBytes > messageLimit;
	$: overLimitBy = Math.max(0, draftMessageBytes - messageLimit);

	const dispatch = createEventDispatcher<{
		send: { type: MediaMessageType; content: string; fileName?: string } | undefined;
		attach: { file: File | null; type: 'media' | 'file'; error?: string };
		removeAttachment: void;
		cancelReply: void;
		typing: { value: string };
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

	function cancelReply() {
		dispatch('cancelReply');
	}

	function onSend() {
		if (isProcessingAttachment || isOverMessageLimit) {
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

	function onComposerInput() {
		dispatch('typing', { value: draftMessage });
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

	function getReplyPreviewText() {
		if (!activeReply) {
			return '';
		}
		const normalized = `${activeReply.senderName}: ${activeReply.content}`.trim();
		if (normalized.length <= 120) {
			return normalized;
		}
		return `${normalized.slice(0, 117)}...`;
	}
</script>

<footer class="composer {isDarkMode ? 'theme-dark' : ''}">
	{#if activeReply}
		<div class="reply-preview-panel">
			<div class="reply-preview-label">Replying to</div>
			<div class="reply-preview-content">{getReplyPreviewText()}</div>
			<button type="button" class="reply-preview-cancel" on:click={cancelReply}>Cancel</button>
		</div>
	{/if}
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
				<video
					src={attachmentPreviewURL}
					class="attachment-preview-video"
					controls
					preload="metadata"
				></video>
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
			on:input={onComposerInput}
			on:keydown={onComposerKeyDown}
			disabled={isProcessingAttachment}
		></textarea>
		<button
			type="button"
			class="send-button"
			on:click={onSend}
			disabled={isProcessingAttachment || isOverMessageLimit}
			aria-label={attachedFile ? 'Send attachment' : 'Send message'}
			title={isOverMessageLimit
				? `Message is too long (${draftMessageBytes}/${messageLimit})`
				: attachedFile
					? 'Send attachment'
					: 'Send message'}
		>
			<IconSet name="send" size={15} />
		</button>
	</div>
	{#if isOverMessageLimit}
		<div class="composer-limit-hint" role="status" aria-live="polite">
			Message is too long by {overLimitBy}. Max {messageLimit}.
		</div>
	{/if}
</footer>

<style>
	.composer {
		position: relative;
		border-top: 1px solid #becadd;
		background: linear-gradient(180deg, #e8eef7 0%, #dde6f1 100%);
		padding: 0.72rem 0.78rem 0.82rem;
		display: flex;
		flex-direction: column;
		gap: 0.48rem;
		flex-shrink: 0;
		box-shadow: 0 -10px 24px rgba(15, 23, 42, 0.16);
		backdrop-filter: blur(8px);
	}

	.composer::before {
		content: '';
		position: absolute;
		left: 0;
		right: 0;
		top: 0;
		height: 2px;
		background: linear-gradient(90deg, #5f7198 0%, #5f8e8a 100%);
		opacity: 0.85;
	}

	.composer.theme-dark {
		border-top-color: #2f3b52;
		background: linear-gradient(180deg, #111a2e 0%, #0c1528 100%);
		box-shadow: 0 -12px 28px rgba(2, 8, 23, 0.48);
	}

	.composer.theme-dark::before {
		background: linear-gradient(90deg, #60a5fa 0%, #22d3ee 100%);
	}

	.reply-preview-panel {
		border: 1px solid #bfcbdf;
		background: linear-gradient(180deg, #edf2f9 0%, #e5ecf7 100%);
		border-radius: 10px;
		padding: 0.56rem 0.62rem;
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		grid-template-rows: auto auto;
		column-gap: 0.5rem;
		row-gap: 0.18rem;
		align-items: center;
	}

	.composer.theme-dark .reply-preview-panel {
		border-color: #354662;
		background: linear-gradient(180deg, #12203a 0%, #0f1b32 100%);
	}

	.reply-preview-label {
		grid-column: 1;
		font-size: 0.7rem;
		font-weight: 700;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: #445775;
	}

	.composer.theme-dark .reply-preview-label {
		color: #93c5fd;
	}

	.reply-preview-content {
		grid-column: 1;
		font-size: 0.8rem;
		color: #2a3a50;
		line-height: 1.28;
		word-break: break-word;
	}

	.composer.theme-dark .reply-preview-content {
		color: #dbe7ff;
	}

	.reply-preview-cancel {
		grid-column: 2;
		grid-row: 1 / span 2;
		border: 1px solid #bec9dc;
		background: #f7f9fc;
		border-radius: 8px;
		padding: 0.28rem 0.52rem;
		font-size: 0.72rem;
		cursor: pointer;
		color: #34445d;
	}

	.composer.theme-dark .reply-preview-cancel {
		border-color: #3b4d6a;
		background: #0f1a30;
		color: #d6e4fb;
	}

	.attachment-preview-panel {
		border: 1px solid #bfcbdf;
		background: linear-gradient(180deg, #edf2f9 0%, #e5ecf7 100%);
		border-radius: 12px;
		padding: 0.55rem;
		display: flex;
		flex-direction: column;
		gap: 0.45rem;
	}

	.composer.theme-dark .attachment-preview-panel {
		border-color: #354764;
		background: linear-gradient(180deg, #11203a 0%, #0e1a30 100%);
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
		color: #2c3a4f;
	}

	.composer.theme-dark .attachment-preview-title {
		color: #dbe8ff;
	}

	.preview-remove {
		border: 1px solid #bec9dc;
		background: #f7f9fc;
		border-radius: 6px;
		width: 24px;
		height: 24px;
		cursor: pointer;
		color: #35445d;
	}

	.composer.theme-dark .preview-remove {
		border-color: #3a4b67;
		background: #0f1a30;
		color: #d6e4fb;
	}

	.attachment-preview-image,
	.attachment-preview-video {
		display: block;
		width: min(100%, 320px);
		max-height: 230px;
		border: 1px solid #bcc7d8;
		border-radius: 8px;
		background: #202a3b;
		object-fit: cover;
	}

	.attachment-preview-file {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		color: #2b394f;
		font-size: 0.84rem;
		padding: 0.35rem 0.15rem;
	}

	.composer.theme-dark .attachment-preview-file {
		color: #d7e5fb;
	}

	.attachment-error {
		font-size: 0.79rem;
		color: #7f1d1d;
		background: rgba(248, 113, 113, 0.12);
		border: 1px solid rgba(220, 38, 38, 0.25);
		border-radius: 8px;
		padding: 0.36rem 0.5rem;
	}

	.attachment-progress {
		font-size: 0.79rem;
		color: #1d4ed8;
		background: rgba(59, 130, 246, 0.1);
		border: 1px solid rgba(37, 99, 235, 0.2);
		border-radius: 8px;
		padding: 0.36rem 0.5rem;
	}

	.composer-limit-hint {
		font-size: 0.74rem;
		line-height: 1.2;
		color: #8a2d2d;
		opacity: 0.92;
		padding: 0 0.2rem;
	}

	.composer.theme-dark .composer-limit-hint {
		color: #fca5a5;
		opacity: 0.86;
	}

	.composer-row {
		display: grid;
		grid-template-columns: 2.2rem minmax(0, 1fr) 2.2rem;
		gap: 0.42rem;
		align-items: center;
		border: 1px solid #b1bfd6;
		background: #edf2f8;
		border-radius: 16px;
		padding: 0.32rem 0.34rem;
		box-shadow:
			0 7px 18px rgba(15, 23, 42, 0.1),
			inset 0 1px 0 rgba(255, 255, 255, 0.68);
	}

	.composer.theme-dark .composer-row {
		border-color: #32445f;
		background: #111d33;
		box-shadow:
			0 8px 20px rgba(2, 8, 23, 0.5),
			inset 0 1px 0 rgba(255, 255, 255, 0.06);
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
		border: 1px solid #b8c5da;
		background: #dfe7f3;
		border-radius: 10px;
		width: 2.1rem;
		height: 2.1rem;
		cursor: pointer;
		color: #33445e;
		padding: 0;
	}

	.composer.theme-dark .attach-button {
		border-color: #3a4c69;
		background: #15223b;
		color: #d6e4fb;
	}

	.attach-button:disabled,
	.send-button:disabled {
		opacity: 0.7;
		cursor: not-allowed;
	}

	.attach-button:hover:not(:disabled),
	.send-button:hover:not(:disabled) {
		background: #d4ddeb;
	}

	.composer.theme-dark .attach-button:hover:not(:disabled) {
		background: #1b2c49;
	}

	.send-button {
		background: linear-gradient(180deg, #2563eb 0%, #1d4ed8 100%);
		border-color: #1d4ed8;
		color: #ffffff;
	}

	.send-button:hover:not(:disabled) {
		background: linear-gradient(180deg, #1e55d5 0%, #1841b4 100%);
	}

	.attach-menu {
		position: absolute;
		left: 0;
		bottom: calc(100% + 8px);
		background: #f3f7fc;
		border: 1px solid #cad3df;
		border-radius: 10px;
		box-shadow: 0 12px 24px rgba(15, 23, 42, 0.14);
		padding: 0.3rem;
		z-index: 120;
		min-width: 132px;
	}

	.composer.theme-dark .attach-menu {
		background: #111d33;
		border-color: #32465f;
		box-shadow: 0 14px 26px rgba(2, 8, 23, 0.5);
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
		color: #2d3d56;
	}

	.composer.theme-dark .attach-menu button {
		color: #dbe8ff;
	}

	.attach-menu button:hover {
		background: #e6edf6;
	}

	.composer.theme-dark .attach-menu button:hover {
		background: #1a2c47;
	}

	.composer-row textarea {
		width: 100%;
		min-width: 0;
		resize: none;
		min-height: 2.1rem;
		max-height: 110px;
		border: 1px solid transparent;
		border-radius: 10px;
		padding: 0.44rem 0.56rem;
		font-size: 0.9rem;
		line-height: 1.32;
		font-family: inherit;
		background: #f7f9fc;
		color: #1b273a;
		box-sizing: border-box;
	}

	.composer.theme-dark .composer-row textarea {
		background: #111d33;
		color: #e2ecff;
	}

	.composer-row textarea:focus {
		outline: none;
		border-color: #93c5fd;
		background: #eef3fb;
	}

	.composer.theme-dark .composer-row textarea:focus {
		border-color: #60a5fa;
		background: #162640;
	}

	.composer-row textarea::placeholder {
		color: #687991;
	}

	.composer.theme-dark .composer-row textarea::placeholder {
		color: #90a4c4;
	}

	@media (max-width: 700px) {
		.composer {
			padding: 0.56rem 0.58rem 0.62rem;
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
