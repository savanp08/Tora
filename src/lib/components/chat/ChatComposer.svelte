<script lang="ts">
	import IconSet from '$lib/components/icons/IconSet.svelte';
	import { getUTF8ByteLength, MESSAGE_TEXT_MAX_BYTES } from '$lib/utils/chat/core';
	import {
		compressMedia,
		inferMediaMessageType,
		uploadToR2,
		type MediaMessageType
	} from '$lib/utils/media';
	import type { ReplyTarget, TaskChecklistItem } from '$lib/types/chat';
	import { stringifyTaskMessagePayload } from '$lib/utils/chat/task';
	import { createEventDispatcher, onDestroy, onMount } from 'svelte';
	const API_BASE = (import.meta.env.VITE_API_BASE as string | undefined) ?? 'http://localhost:8080';

	export let draftMessage = '';
	export let attachedFile: File | null = null;
	export let activeReply: ReplyTarget | null = null;
	export let isDarkMode = false;
	export let messageLimit = MESSAGE_TEXT_MAX_BYTES;
	export let currentUsername = 'You';
	export let roomId = '';
	export let disabled = false;

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
	let taskDraftOpen = false;
	let taskDraftTitle = '';
	let taskDraftItems: TaskChecklistItem[] = [];
	let taskNewItemText = '';
	let taskAddInputOpen = false;
	let taskDraftError = '';
	let isRecording = false;
	let mediaRecorder: MediaRecorder | null = null;
	let audioChunks: Blob[] = [];
	let recordingStream: MediaStream | null = null;

	$: normalizedDraftMessage = draftMessage.trim();
	$: draftMessageBytes = getUTF8ByteLength(normalizedDraftMessage);
	$: isOverMessageLimit = draftMessageBytes > messageLimit;
	$: overLimitBy = Math.max(0, draftMessageBytes - messageLimit);
	$: taskDraftReady = taskDraftOpen && taskDraftTitle.trim() !== '' && taskDraftItems.length > 0;
	$: showSendButton =
		!isRecording && !taskDraftOpen && (!!attachedFile || normalizedDraftMessage.length > 0);
	$: composerDisabled = disabled || isProcessingAttachment || isRecording || taskDraftOpen;
	$: composerPlaceholder = disabled
		? 'This room has expired. Extend time to continue chatting.'
		: isRecording
			? 'Recording... Click mic to send.'
			: taskDraftOpen
				? 'Task mode active. Press send when ready.'
				: attachedFile
					? 'Add a caption (optional)'
					: 'Type a message';

	const dispatch = createEventDispatcher<{
		send:
			| { type: MediaMessageType | 'task'; content: string; fileName?: string; text?: string }
			| undefined;
		attach: { file: File | null; type: 'media' | 'file'; error?: string };
		removeAttachment: void;
		cancelReply: void;
		typing: { value: string };
	}>();

	onDestroy(() => {
		clearAttachmentPreview();
		if (isRecording && mediaRecorder && mediaRecorder.state !== 'inactive') {
			mediaRecorder.stop();
		}
		stopRecordingStream();
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
		if (disabled) {
			return;
		}
		showAttachMenu = !showAttachMenu;
	}

	function chooseAttachmentType(type: 'media' | 'file' | 'task') {
		if (disabled) {
			return;
		}
		showAttachMenu = false;
		attachError = '';
		taskDraftError = '';
		if (type === 'task') {
			clearAttachmentPreview();
			attachedFile = null;
			attachedMessageType = null;
			dispatch('attach', { file: null, type: 'file' });
			taskDraftOpen = true;
			taskAddInputOpen = false;
			if (taskDraftTitle.trim() === '') {
				taskDraftTitle = 'Task';
			}
			return;
		}
		taskDraftOpen = false;
		taskAddInputOpen = false;
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
			if (file.type.startsWith('audio/')) {
				return 'audio';
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
		if (disabled) {
			return;
		}
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
			const uploaded = await uploadToR2(compressed, roomId);
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
		if (disabled || isProcessingAttachment || isOverMessageLimit || isRecording) {
			return;
		}
		if (taskDraftOpen) {
			submitTaskDraft();
			return;
		}
		if (attachedFile) {
			void sendAttachment();
			return;
		}
		dispatch('send', undefined);
	}

	function onComposerKeyDown(event: KeyboardEvent) {
		if (disabled) {
			return;
		}
		if (event.key === 'Enter' && !event.shiftKey) {
			event.preventDefault();
			onSend();
		}
	}

	function onComposerInput() {
		if (disabled) {
			return;
		}
		dispatch('typing', { value: draftMessage });
	}

	function stopRecordingStream() {
		if (!recordingStream) {
			return;
		}
		for (const track of recordingStream.getTracks()) {
			track.stop();
		}
		recordingStream = null;
	}

	function toAbsoluteUploadURL(value: string) {
		const trimmed = (value || '').trim();
		if (!trimmed) {
			return '';
		}
		if (
			/^https?:\/\//i.test(trimmed) ||
			trimmed.startsWith('blob:') ||
			trimmed.startsWith('data:')
		) {
			return trimmed;
		}
		if (trimmed.startsWith('/')) {
			return `${API_BASE}${trimmed}`;
		}
		return `${API_BASE}/${trimmed}`;
	}

	async function uploadRecordedAudio(audioBlob: Blob) {
		const payload = new FormData();
		const fileName = `voice-message-${Date.now()}.webm`;
		payload.append('file', audioBlob, fileName);
		const roomIdParam = roomId ? `?roomId=${encodeURIComponent(roomId)}` : '';
		const res = await fetch(`${API_BASE}/api/upload${roomIdParam}`, {
			method: 'POST',
			body: payload
		});
		const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
		const rawFileURL = typeof data.fileUrl === 'string' ? data.fileUrl : '';
		const uploadedURL = toAbsoluteUploadURL(rawFileURL);
		if (!res.ok || !uploadedURL) {
			throw new Error(
				typeof data.error === 'string' ? data.error : `Voice upload failed (${res.status})`
			);
		}
		return { uploadedURL, fileName };
	}

	async function handleRecordingStop() {
		const hasAudio = audioChunks.some((chunk) => chunk.size > 0);
		if (!hasAudio) {
			audioChunks = [];
			mediaRecorder = null;
			return;
		}

		isProcessingAttachment = true;
		attachError = '';
		try {
			const audioBlob = new Blob(audioChunks, { type: 'audio/webm' });
			const { uploadedURL, fileName } = await uploadRecordedAudio(audioBlob);
			dispatch('send', {
				type: 'audio',
				content: uploadedURL,
				text: 'Voice message',
				fileName
			});
			draftMessage = '';
		} catch (error) {
			attachError = error instanceof Error ? error.message : 'Voice recording failed';
		} finally {
			audioChunks = [];
			mediaRecorder = null;
			isProcessingAttachment = false;
		}
	}

	async function toggleRecording() {
		if (disabled || isProcessingAttachment || attachedFile || taskDraftOpen) {
			return;
		}

		if (!isRecording) {
			if (typeof navigator === 'undefined' || !navigator.mediaDevices?.getUserMedia) {
				attachError = 'Microphone is not available in this browser.';
				return;
			}
			if (typeof MediaRecorder === 'undefined') {
				attachError = 'Media recording is not supported in this browser.';
				return;
			}

			try {
				attachError = '';
				audioChunks = [];
				recordingStream = await navigator.mediaDevices.getUserMedia({ audio: true });
				const recorder = new MediaRecorder(recordingStream);
				recorder.ondataavailable = (event: BlobEvent) => {
					if (event.data && event.data.size > 0) {
						audioChunks = [...audioChunks, event.data];
					}
				};
				recorder.onstop = () => {
					void handleRecordingStop();
				};
				mediaRecorder = recorder;
				recorder.start();
				isRecording = true;
			} catch (error) {
				stopRecordingStream();
				mediaRecorder = null;
				isRecording = false;
				attachError =
					error instanceof Error ? error.message : 'Unable to access microphone for recording.';
			}
			return;
		}

		isRecording = false;
		if (mediaRecorder && mediaRecorder.state !== 'inactive') {
			mediaRecorder.stop();
		}
		stopRecordingStream();
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

	function clearTaskDraft() {
		taskDraftOpen = false;
		taskDraftTitle = '';
		taskDraftItems = [];
		taskNewItemText = '';
		taskAddInputOpen = false;
		taskDraftError = '';
	}

	function openTaskDraftAddInput() {
		taskAddInputOpen = true;
		taskDraftError = '';
	}

	function cancelTaskDraftAddInput() {
		taskAddInputOpen = false;
		taskNewItemText = '';
	}

	function addTaskDraftItem() {
		const text = (taskNewItemText || '').trim();
		if (!text) {
			return;
		}
		taskDraftItems = [
			...taskDraftItems,
			{
				text,
				completed: false,
				completedBy: '',
				timestamp: 0,
				createdBy: (currentUsername || 'You').trim() || 'You',
				createdAt: Date.now()
			}
		];
		taskNewItemText = '';
		taskAddInputOpen = false;
		taskDraftError = '';
	}

	function removeTaskDraftItem(index: number) {
		if (index < 0 || index >= taskDraftItems.length) {
			return;
		}
		taskDraftItems = taskDraftItems.filter((_, itemIndex) => itemIndex !== index);
	}

	function onTaskDraftItemKeyDown(event: KeyboardEvent) {
		if (event.key === 'Enter') {
			event.preventDefault();
			addTaskDraftItem();
			return;
		}
		if (event.key === 'Escape') {
			event.preventDefault();
			cancelTaskDraftAddInput();
		}
	}

	function submitTaskDraft() {
		const title = taskDraftTitle.trim();
		if (!title) {
			taskDraftError = 'Add a title for this task card.';
			return;
		}
		if (taskDraftItems.length === 0) {
			taskDraftError = 'Add at least one task item.';
			return;
		}
		const content = stringifyTaskMessagePayload({
			title,
			tasks: taskDraftItems
		});
		dispatch('send', {
			type: 'task',
			content
		});
		clearTaskDraft();
	}

	function formatTaskMeta(timestamp: number) {
		if (!Number.isFinite(timestamp) || timestamp <= 0) {
			return '';
		}
		return new Date(timestamp).toLocaleString([], {
			month: 'short',
			day: 'numeric',
			hour: 'numeric',
			minute: '2-digit'
		});
	}

	function onTaskDraftBackdropClick(event: MouseEvent) {
		if (event.target === event.currentTarget) {
			clearTaskDraft();
		}
	}
</script>

{#if taskDraftOpen}
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<div
		class="task-draft-shell"
		data-mode={isDarkMode ? 'dark' : 'light'}
		role="presentation"
		on:click={onTaskDraftBackdropClick}
	>
		<section class="task-draft-card" role="group" aria-label="Task preview card">
			<div class="task-draft-header">
				<div class="task-draft-kicker">Task Preview</div>
				<button type="button" class="task-draft-close" on:click={clearTaskDraft}>Cancel</button>
			</div>
			<input
				type="text"
				class="task-draft-title"
				bind:value={taskDraftTitle}
				placeholder="Title"
			/>
			<div class="task-draft-list">
				{#if taskDraftItems.length === 0}
					<div class="task-draft-empty">No tasks yet. Add your first item.</div>
				{:else}
					{#each taskDraftItems as task, index}
						<div class="task-draft-item">
							<input type="checkbox" checked={task.completed} disabled />
							<div class="task-draft-item-content">
								<div class="task-draft-item-name">{task.text}</div>
								<div class="task-draft-meta-line">
									<span>{task.createdBy}</span>
									<span aria-hidden="true">•</span>
									<span>{formatTaskMeta(task.createdAt)}</span>
									<span aria-hidden="true">•</span>
									<span class="task-draft-meta-finished">open</span>
								</div>
							</div>
							<button
								type="button"
								class="task-draft-remove"
								on:click={() => removeTaskDraftItem(index)}
								aria-label="Remove task item"
								title="Remove"
							>
								×
							</button>
						</div>
					{/each}
				{/if}
			</div>
			{#if taskAddInputOpen}
				<div class="task-draft-add-row">
					<input type="checkbox" disabled aria-hidden="true" />
					<input
						type="text"
						bind:value={taskNewItemText}
						placeholder="Task name"
						on:keydown={onTaskDraftItemKeyDown}
					/>
					<button type="button" class="add-row-action confirm" on:click={addTaskDraftItem}>Add</button>
					<button
						type="button"
						class="add-row-action"
						on:click={cancelTaskDraftAddInput}
					>
						Cancel
					</button>
				</div>
			{:else}
				<button type="button" class="task-draft-add-trigger" on:click={openTaskDraftAddInput}>
					<span class="plus-pill">+</span>
					<span>Add Task</span>
				</button>
			{/if}
			{#if taskDraftError}
				<div class="task-draft-error">{taskDraftError}</div>
			{/if}
			<div class="task-draft-footer">
				<button type="button" class="task-draft-footer-btn ghost" on:click={clearTaskDraft}>
					Cancel
				</button>
				<button type="button" class="task-draft-footer-btn submit" on:click={submitTaskDraft}>
					Create Task
				</button>
			</div>
		</section>
	</div>
{/if}

<footer class="composer" data-mode={isDarkMode ? 'dark' : 'light'}>
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
				disabled={disabled || isProcessingAttachment || isRecording}
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
					<button type="button" on:click={() => chooseAttachmentType('task')}>
						<IconSet name="list-vertical" size={14} />
						<span>Task</span>
					</button>
				</div>
			{/if}
		</div>

		<textarea
			bind:value={draftMessage}
			rows="1"
			placeholder={composerPlaceholder}
			on:input={onComposerInput}
			on:keydown={onComposerKeyDown}
			disabled={composerDisabled}
		></textarea>
		{#if showSendButton}
			<button
				type="button"
				class="send-button"
				on:click={onSend}
				disabled={disabled ||
					isProcessingAttachment ||
					isOverMessageLimit ||
					isRecording ||
					(taskDraftOpen && !taskDraftReady)}
				aria-label={attachedFile ? 'Send attachment' : taskDraftOpen ? 'Send task' : 'Send message'}
				title={isOverMessageLimit
					? `Message is too long (${draftMessageBytes}/${messageLimit})`
					: attachedFile
						? 'Send attachment'
						: taskDraftOpen
							? 'Send task card'
							: 'Send message'}
			>
				<IconSet name="send" size={15} />
			</button>
		{:else}
			<button
				type="button"
				class="mic-button {isRecording ? 'recording' : ''}"
				on:click={toggleRecording}
				disabled={disabled || isProcessingAttachment || !!attachedFile || taskDraftOpen}
				aria-label={isRecording ? 'Stop recording and send voice message' : 'Record voice message'}
				title={isRecording ? 'Stop recording and send voice message' : 'Record voice message'}
			>
				<svg
					width="14"
					height="14"
					viewBox="0 0 24 24"
					fill="none"
					stroke="currentColor"
					stroke-width="2"
					stroke-linecap="round"
					stroke-linejoin="round"
					aria-hidden="true"
				>
					<rect x="9" y="2" width="6" height="12" rx="3"></rect>
					<path d="M5 10a7 7 0 0 0 14 0"></path>
					<line x1="12" y1="17" x2="12" y2="22"></line>
					<line x1="8" y1="22" x2="16" y2="22"></line>
				</svg>
			</button>
		{/if}
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
		border-top: 1px solid var(--border-default);
		background: linear-gradient(180deg, var(--surface-secondary) 0%, var(--bg-secondary) 100%);
		padding: 0.72rem 0.78rem 0.82rem;
		display: flex;
		flex-direction: column;
		gap: 0.48rem;
		flex-shrink: 0;
		box-shadow: 0 -10px 24px var(--overlay-soft);
		backdrop-filter: blur(8px);
	}

	.composer::before {
		content: '';
		position: absolute;
		left: 0;
		right: 0;
		top: 0;
		height: 2px;
		background: var(--gemini-gradient);
		opacity: 0.42;
	}

	.reply-preview-panel {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		border-radius: 10px;
		padding: 0.56rem 0.62rem;
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		grid-template-rows: auto auto;
		column-gap: 0.5rem;
		row-gap: 0.18rem;
		align-items: center;
	}

	.reply-preview-label {
		grid-column: 1;
		font-size: 0.7rem;
		font-weight: 700;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--text-secondary);
	}

	.reply-preview-content {
		grid-column: 1;
		font-size: 0.8rem;
		color: var(--text-primary);
		line-height: 1.28;
		word-break: break-word;
	}

	.reply-preview-cancel {
		grid-column: 2;
		grid-row: 1 / span 2;
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		border-radius: 8px;
		padding: 0.28rem 0.52rem;
		font-size: 0.72rem;
		cursor: pointer;
		color: var(--text-secondary);
	}

	.attachment-preview-panel {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
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
		color: var(--text-primary);
	}

	.preview-remove {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		border-radius: 6px;
		width: 24px;
		height: 24px;
		cursor: pointer;
		color: var(--text-secondary);
	}

	.attachment-preview-image,
	.attachment-preview-video {
		display: block;
		width: min(100%, 320px);
		max-height: 230px;
		border: 1px solid var(--border-default);
		border-radius: 8px;
		background: var(--bg-tertiary);
		object-fit: cover;
	}

	.attachment-preview-file {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		color: var(--text-secondary);
		font-size: 0.84rem;
		padding: 0.35rem 0.15rem;
	}

	.attachment-error {
		font-size: 0.79rem;
		color: var(--accent-danger);
		background: var(--state-danger-bg);
		border: 1px solid var(--state-danger-border);
		border-radius: 8px;
		padding: 0.36rem 0.5rem;
	}

	.attachment-progress {
		font-size: 0.79rem;
		color: var(--accent-primary);
		background: var(--state-info-bg);
		border: 1px solid var(--state-info-border);
		border-radius: 8px;
		padding: 0.36rem 0.5rem;
	}

	.task-draft-shell {
		position: fixed;
		inset: 0;
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 1.2rem;
		background: var(--overlay-soft);
		backdrop-filter: blur(8px);
		-webkit-backdrop-filter: blur(8px);
		z-index: 520;
	}

	.task-draft-card {
		width: min(100%, 54rem);
		max-height: min(92vh, 820px);
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		border-radius: 14px;
		padding: 0.72rem 0.76rem;
		display: flex;
		flex-direction: column;
		gap: 0.56rem;
		overflow: auto;
		box-shadow: var(--shadow-lg);
	}

	.task-draft-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 0.4rem;
	}

	.task-draft-kicker {
		font-size: 0.68rem;
		font-weight: 700;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--text-secondary);
	}

	.task-draft-close {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		color: var(--text-secondary);
		border-radius: 9px;
		padding: 0.24rem 0.56rem;
		font-size: 0.72rem;
		font-weight: 700;
		cursor: pointer;
	}

	.task-draft-title {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		color: var(--text-primary);
		border-radius: 10px;
		padding: 0.52rem 0.64rem;
		font-size: 0.95rem;
		font-weight: 700;
	}

	.task-draft-title:focus {
		outline: none;
		border-color: var(--border-focus);
		box-shadow: 0 0 0 2px var(--interactive-focus);
	}

	.task-draft-list {
		display: flex;
		flex-direction: column;
		gap: 0.45rem;
	}

	.task-draft-empty {
		border: 1px dashed var(--border-default);
		background: var(--surface-secondary);
		border-radius: 10px;
		padding: 0.5rem 0.6rem;
		font-size: 0.78rem;
		color: var(--text-secondary);
	}

	.task-draft-item {
		display: grid;
		grid-template-columns: 1rem minmax(0, 1fr) auto;
		gap: 0.48rem;
		align-items: center;
		padding: 0.5rem 0.56rem;
		border: 1px solid var(--border-default);
		border-radius: 10px;
		background: var(--surface-primary);
	}

	.task-draft-item input[type='checkbox'] {
		width: 0.95rem;
		height: 0.95rem;
		accent-color: var(--accent-success);
	}

	.task-draft-item-content {
		display: flex;
		flex-direction: column;
		gap: 0.16rem;
		min-width: 0;
	}

	.task-draft-item-name {
		font-size: 0.82rem;
		color: var(--text-primary);
		word-break: break-word;
		font-weight: 600;
	}

	.task-draft-meta-line {
		display: inline-flex;
		align-items: center;
		gap: 0.28rem;
		font-size: 0.67rem;
		color: var(--text-secondary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.task-draft-meta-finished {
		color: var(--text-tertiary);
	}

	.task-draft-remove {
		border: 1px solid var(--border-default);
		background: var(--surface-secondary);
		color: var(--text-secondary);
		border-radius: 8px;
		width: 1.55rem;
		height: 1.55rem;
		cursor: pointer;
		font-size: 1rem;
		line-height: 1;
	}

	.task-draft-add-trigger {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
		border: 1.5px solid var(--accent-success);
		background: var(--state-success-bg);
		color: var(--accent-success);
		border-radius: 10px;
		padding: 0.38rem 0.66rem;
		font-size: 0.79rem;
		font-weight: 700;
		cursor: pointer;
	}

	.plus-pill {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 1.1rem;
		height: 1.1rem;
		border-radius: 6px;
		border: 1px solid var(--state-success-border);
		background: var(--surface-primary);
		font-size: 0.9rem;
		line-height: 1;
	}

	.task-draft-add-row {
		display: grid;
		grid-template-columns: 1rem minmax(0, 1fr) auto auto;
		gap: 0.34rem;
		align-items: center;
	}

	.task-draft-add-row input[type='checkbox'] {
		width: 0.95rem;
		height: 0.95rem;
		accent-color: var(--accent-success);
	}

	.task-draft-add-row input[type='text'] {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		color: var(--text-primary);
		border-radius: 9px;
		padding: 0.36rem 0.52rem;
		font-size: 0.79rem;
		min-width: 0;
	}

	.add-row-action {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		color: var(--text-secondary);
		border-radius: 9px;
		padding: 0.31rem 0.56rem;
		font-size: 0.73rem;
		font-weight: 700;
		cursor: pointer;
	}

	.add-row-action.confirm {
		border-color: var(--accent-success);
		background: var(--state-success-bg);
		color: var(--accent-success);
	}

	.task-draft-footer {
		position: sticky;
		bottom: -0.72rem;
		margin-top: 0.2rem;
		margin-inline: -0.76rem;
		padding: 0.58rem 0.76rem 0.74rem;
		display: flex;
		justify-content: flex-end;
		gap: 0.45rem;
		background: linear-gradient(180deg, var(--surface-primary) 0%, var(--surface-secondary) 30%);
		border-top: 1px solid var(--border-default);
	}

	.task-draft-footer-btn {
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		color: var(--text-secondary);
		border-radius: 10px;
		padding: 0.43rem 0.78rem;
		font-size: 0.78rem;
		font-weight: 700;
		cursor: pointer;
	}

	.task-draft-footer-btn.submit {
		border-color: var(--accent-success);
		background: var(--state-success-bg);
		color: var(--accent-success);
	}

	.task-draft-footer-btn.ghost {
		background: var(--surface-secondary);
	}

	.task-draft-error {
		font-size: 0.74rem;
		color: var(--accent-danger);
		background: var(--state-danger-bg);
		border: 1px solid var(--state-danger-border);
		border-radius: 8px;
		padding: 0.32rem 0.48rem;
	}

	@media (max-width: 640px) {
		.task-draft-card {
			width: min(100%, 100vw - 1rem);
			max-height: min(88vh, 760px);
			padding: 0.62rem;
		}

		.task-draft-footer {
			bottom: -0.62rem;
			margin-inline: -0.62rem;
			padding-inline: 0.62rem;
		}

		.task-draft-add-row {
			grid-template-columns: 1rem minmax(0, 1fr);
		}

		.add-row-action {
			justify-self: start;
		}
	}

	.composer-limit-hint {
		font-size: 0.74rem;
		line-height: 1.2;
		color: var(--accent-danger);
		opacity: 0.92;
		padding: 0 0.2rem;
	}

	.composer-row {
		display: grid;
		grid-template-columns: 2.2rem minmax(0, 1fr) 2.2rem;
		gap: 0.42rem;
		align-items: center;
		border: 1px solid var(--border-default);
		background: var(--surface-primary);
		border-radius: 16px;
		padding: 0.32rem 0.34rem;
		box-shadow:
			0 7px 18px var(--overlay-soft),
			inset 0 1px 0 var(--surface-secondary);
	}

	.hidden-file-input {
		display: none;
	}

	.attach-wrap {
		position: relative;
	}

	.attach-button,
	.mic-button,
	.send-button {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		border: 1px solid var(--border-default);
		background: var(--surface-secondary);
		border-radius: 10px;
		width: 2.1rem;
		height: 2.1rem;
		cursor: pointer;
		color: var(--text-secondary);
		padding: 0;
	}

	.attach-button:disabled,
	.mic-button:disabled,
	.send-button:disabled {
		opacity: 0.7;
		cursor: not-allowed;
	}

	.attach-button:hover:not(:disabled),
	.mic-button:hover:not(:disabled),
	.send-button:hover:not(:disabled) {
		background: var(--surface-hover);
	}

	.mic-button.recording {
		border-color: var(--accent-danger);
		background: var(--accent-danger);
		color: var(--text-inverse);
		animation: mic-pulse 1.1s ease-in-out infinite;
	}

	.send-button {
		background: var(--accent-primary);
		border-color: var(--accent-primary);
		color: var(--text-inverse);
	}

	.send-button:hover:not(:disabled) {
		background: var(--accent-primary-hover);
		border-color: var(--accent-primary-hover);
	}

	.attach-menu {
		position: absolute;
		left: 0;
		bottom: calc(100% + 8px);
		background: var(--surface-primary);
		border: 1px solid var(--border-default);
		border-radius: 10px;
		box-shadow: var(--shadow-md);
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
		color: var(--text-primary);
	}

	.attach-menu button:hover {
		background: var(--surface-hover);
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
		background: var(--surface-primary);
		color: var(--text-primary);
		box-sizing: border-box;
	}

	.composer-row textarea:focus {
		outline: none;
		border-color: var(--border-focus);
		background: var(--surface-secondary);
	}

	.composer-row textarea::placeholder {
		color: var(--text-placeholder);
	}

	@media (max-width: 700px) {
		.composer {
			padding: 0.56rem 0.58rem 0.62rem;
		}

		.composer-row {
			gap: 0.34rem;
		}

		.attach-button,
		.mic-button,
		.send-button {
			width: 2rem;
			height: 2rem;
		}

		textarea {
			font-size: 0.86rem;
		}
	}

	@keyframes mic-pulse {
		0% {
			box-shadow: 0 0 0 0 var(--state-danger-border);
		}
		70% {
			box-shadow: 0 0 0 9px transparent;
		}
		100% {
			box-shadow: 0 0 0 0 transparent;
		}
	}
</style>
