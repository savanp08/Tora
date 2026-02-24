<script lang="ts">
	import { createEventDispatcher, tick } from 'svelte';
	import type { RoomMenuMode, UiDialogState } from '$lib/types/chat';

	export let dialog: UiDialogState = { kind: 'none' };
	export let promptSubmitDisabled = false;
	export let roomActionSubmitDisabled = false;

	const dispatch = createEventDispatcher<{
		close: void;
		confirm: void;
		promptInput: { value: string };
		roomModeChange: { mode: RoomMenuMode };
		roomNameInput: { value: string };
	}>();

	let inputEl: HTMLInputElement | HTMLTextAreaElement | null = null;
	let previousDialogKind: UiDialogState['kind'] = 'none';

	$: {
		const nextDialogKind = dialog.kind;
		if (nextDialogKind !== previousDialogKind && nextDialogKind !== 'none') {
			void focusDialogInputSoon();
		}
		previousDialogKind = nextDialogKind;
	}

	function isConfirmDisabled() {
		return (
			(dialog.kind === 'prompt' && promptSubmitDisabled) ||
			(dialog.kind === 'roomAction' && roomActionSubmitDisabled)
		);
	}

	function onBackdropClick() {
		dispatch('close');
	}

	function onConfirm() {
		if (isConfirmDisabled()) {
			return;
		}
		dispatch('confirm');
	}

	function onKeyDown(event: KeyboardEvent) {
		if (dialog.kind === 'none') {
			return;
		}
		if (event.key === 'Escape') {
			event.preventDefault();
			dispatch('close');
			return;
		}
		if (event.key !== 'Enter') {
			return;
		}
		if (dialog.kind === 'prompt' && dialog.multiline && event.shiftKey) {
			return;
		}
		event.preventDefault();
		onConfirm();
	}

	async function focusDialogInputSoon() {
		await tick();
		inputEl?.focus();
		if (inputEl instanceof HTMLInputElement) {
			inputEl.select();
		}
	}
</script>

{#if dialog.kind !== 'none'}
	<button
		type="button"
		class="ui-dialog-backdrop"
		aria-label="Close dialog"
		on:click={onBackdropClick}
	></button>
	<div
		class="ui-dialog"
		role="dialog"
		aria-modal="true"
		aria-labelledby="ui-dialog-title"
		tabindex="-1"
		on:keydown={onKeyDown}
	>
		<header class="ui-dialog-header">
			<h3 id="ui-dialog-title">{dialog.title}</h3>
		</header>
		<div class="ui-dialog-body">
			<p>{dialog.message}</p>
			{#if dialog.kind === 'prompt'}
				{#if dialog.multiline}
					<textarea
						class="ui-dialog-input ui-dialog-textarea"
						value={dialog.value}
						placeholder={dialog.placeholder}
						maxlength={dialog.maxLength}
						rows={5}
						bind:this={inputEl}
						on:input={(event) =>
							dispatch('promptInput', {
								value: (event.currentTarget as HTMLTextAreaElement).value
							})}
					></textarea>
				{:else}
					<input
						class="ui-dialog-input"
						type="text"
						value={dialog.value}
						placeholder={dialog.placeholder}
						maxlength={dialog.maxLength}
						bind:this={inputEl}
						on:input={(event) =>
							dispatch('promptInput', {
								value: (event.currentTarget as HTMLInputElement).value
							})}
					/>
				{/if}
			{:else if dialog.kind === 'roomAction'}
				<div class="ui-dialog-mode-toggle">
					<button
						type="button"
						class="ui-dialog-mode-btn {dialog.mode === 'create' ? 'active' : ''}"
						on:click={() => dispatch('roomModeChange', { mode: 'create' })}
					>
						New
					</button>
					<button
						type="button"
						class="ui-dialog-mode-btn {dialog.mode === 'join' ? 'active' : ''}"
						on:click={() => dispatch('roomModeChange', { mode: 'join' })}
					>
						Existing
					</button>
				</div>
				<input
					class="ui-dialog-input"
					type="text"
					value={dialog.roomName}
					placeholder="Room name"
					maxlength={20}
					bind:this={inputEl}
					on:input={(event) =>
						dispatch('roomNameInput', {
							value: (event.currentTarget as HTMLInputElement).value
						})}
				/>
			{/if}
		</div>
		<footer class="ui-dialog-actions">
			<button type="button" class="ui-dialog-btn" on:click={() => dispatch('close')}>
				{dialog.cancelLabel}
			</button>
			<button
				type="button"
				class="ui-dialog-btn primary {dialog.kind === 'confirm' && dialog.danger ? 'danger' : ''}"
				on:click={onConfirm}
				disabled={isConfirmDisabled()}
			>
				{dialog.confirmLabel}
			</button>
		</footer>
	</div>
{/if}

<style>
	.ui-dialog-backdrop {
		position: fixed;
		inset: 0;
		border: none;
		background: rgba(12, 12, 16, 0.5);
		z-index: 1200;
	}

	.ui-dialog {
		position: fixed;
		left: 50%;
		top: 50%;
		transform: translate(-50%, -50%);
		width: min(92vw, 460px);
		background: #fcfcfd;
		border: 1px solid #d9d9e0;
		border-radius: 14px;
		box-shadow: 0 24px 48px rgba(0, 0, 0, 0.22);
		z-index: 1210;
		display: flex;
		flex-direction: column;
		overflow: hidden;
	}

	.ui-dialog-header {
		padding: 0.9rem 1rem 0.45rem;
		border-bottom: 1px solid #ececf1;
	}

	.ui-dialog-header h3 {
		margin: 0;
		font-size: 1rem;
		color: #1f1f26;
	}

	.ui-dialog-body {
		padding: 0.8rem 1rem;
		display: flex;
		flex-direction: column;
		gap: 0.65rem;
	}

	.ui-dialog-body p {
		margin: 0;
		font-size: 0.84rem;
		color: #4b4b56;
		line-height: 1.35;
	}

	.ui-dialog-input {
		width: 100%;
		border: 1px solid #d6d6dc;
		border-radius: 8px;
		padding: 0.55rem 0.65rem;
		font-size: 0.88rem;
		background: #ffffff;
		color: #17171d;
		box-sizing: border-box;
	}

	.ui-dialog-textarea {
		resize: vertical;
		min-height: 110px;
		font-family: inherit;
		line-height: 1.35;
	}

	.ui-dialog-mode-toggle {
		display: inline-flex;
		gap: 0.35rem;
	}

	.ui-dialog-mode-btn {
		border: 1px solid #d1d1d8;
		background: #f3f3f6;
		color: #393944;
		border-radius: 999px;
		padding: 0.28rem 0.74rem;
		font-size: 0.78rem;
		font-weight: 600;
		cursor: pointer;
	}

	.ui-dialog-mode-btn.active {
		background: #25252d;
		border-color: #25252d;
		color: #ffffff;
	}

	.ui-dialog-actions {
		display: flex;
		justify-content: flex-end;
		gap: 0.5rem;
		padding: 0.75rem 1rem 0.95rem;
		border-top: 1px solid #ececf1;
	}

	.ui-dialog-btn {
		border: 1px solid #d1d1d8;
		background: #f8f8fa;
		color: #34343e;
		border-radius: 8px;
		padding: 0.4rem 0.7rem;
		font-size: 0.8rem;
		font-weight: 600;
		cursor: pointer;
	}

	.ui-dialog-btn.primary {
		background: #222228;
		border-color: #222228;
		color: #ffffff;
	}

	.ui-dialog-btn.primary.danger {
		background: #8f1d1d;
		border-color: #8f1d1d;
	}

	.ui-dialog-btn:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}
</style>
