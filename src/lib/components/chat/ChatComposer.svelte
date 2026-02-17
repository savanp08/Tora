<script lang="ts">
	import IconSet from '$lib/components/icons/IconSet.svelte';
	import {
		compressMedia,
		inferMediaMessageType,
		uploadToR2,
		type MediaMessageType
	} from '$lib/utils/media';
	import { createEventDispatcher } from 'svelte';

	export let draftMessage = '';
	export let attachedFile: File | null = null;

	let mediaInput: HTMLInputElement | null = null;
	let fileInput: HTMLInputElement | null = null;
	let showAttachMenu = false;
	let attachError = '';
	let isProcessingAttachment = false;

	const dispatch = createEventDispatcher<{
		send: { type: MediaMessageType; content: string; fileName?: string } | undefined;
		attach: { file: File | null; type: 'media' | 'file'; error?: string };
		removeAttachment: void;
	}>();

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

	async function onFilePicked(event: Event, pickerType: 'media' | 'file') {
		const target = event.currentTarget as HTMLInputElement;
		const selected = target.files?.[0] ?? null;
		if (!selected) {
			return;
		}

		isProcessingAttachment = true;
		attachError = '';
		try {
			attachedFile = selected;
			const compressed = await compressMedia(selected);
			const uploaded = await uploadToR2(compressed);
			const messageType: MediaMessageType =
				pickerType === 'file' ? 'file' : inferMediaMessageType(compressed);

			dispatch('send', {
				type: messageType,
				content: uploaded.fileUrl,
				fileName: selected.name
			});
			dispatch('attach', { file: null, type: pickerType });
			attachedFile = null;
		} catch (error) {
			attachedFile = null;
			const message = error instanceof Error ? error.message : 'Attachment failed';
			attachError = message;
			dispatch('attach', { file: null, type: pickerType, error: message });
		} finally {
			isProcessingAttachment = false;
			target.value = '';
		}
	}

	function removeAttachment() {
		attachedFile = null;
		attachError = '';
		dispatch('removeAttachment');
	}

	function onComposerKeyDown(event: KeyboardEvent) {
		if (event.key === 'Enter' && !event.shiftKey) {
			event.preventDefault();
			dispatch('send', undefined);
		}
	}
</script>

<footer class="composer">
	{#if attachedFile}
		<div class="attachment-pill">
			<span>{attachedFile.name}</span>
			<button type="button" on:click={removeAttachment}>x</button>
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
			<button type="button" class="attach-button" on:click={toggleAttachMenu}>
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
			placeholder={isProcessingAttachment ? 'Uploading media...' : 'Type a message'}
			on:keydown={onComposerKeyDown}
			disabled={isProcessingAttachment}
		></textarea>
		<button
			type="button"
			class="send-button"
			on:click={() => dispatch('send', undefined)}
			disabled={isProcessingAttachment}
		>
			Send
		</button>
	</div>
</footer>

<style>
	.composer {
		border-top: 1px solid #d9dee4;
		background: #f6f8fa;
		padding: 0.75rem;
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}

	.attachment-pill {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		padding: 0.35rem 0.6rem;
		background: #dbeafe;
		color: #1e3a8a;
		border-radius: 999px;
		width: fit-content;
		font-size: 0.82rem;
	}

	.attachment-pill button {
		border: none;
		background: transparent;
		color: inherit;
		cursor: pointer;
		font-weight: 700;
	}

	.attachment-error {
		font-size: 0.79rem;
		color: #b91c1c;
	}

	.attachment-progress {
		font-size: 0.79rem;
		color: #1d4ed8;
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
		border: 1px solid #cfd8e3;
		background: #ffffff;
		border-radius: 8px;
		padding: 0.52rem 0.72rem;
		font-size: 0.85rem;
		cursor: pointer;
	}

	.send-button {
		background: #1f9d4c;
		border-color: #1f9d4c;
		color: #ffffff;
	}

	.attach-menu {
		position: absolute;
		left: 0;
		bottom: calc(100% + 8px);
		background: #ffffff;
		border: 1px solid #d8e0e9;
		border-radius: 10px;
		box-shadow: 0 8px 20px rgba(15, 23, 42, 0.12);
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
		color: #0f172a;
	}

	.attach-menu button:hover {
		background: #f1f5f9;
	}

	textarea {
		width: 100%;
		resize: none;
		min-height: 40px;
		max-height: 110px;
		border: 1px solid #cfd8e3;
		border-radius: 9px;
		padding: 0.55rem 0.66rem;
		font-size: 0.91rem;
		font-family: inherit;
	}
</style>
