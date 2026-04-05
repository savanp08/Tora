<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import { submitChangeRequest, type ChangeRequestAction } from '$lib/stores/changeRequests';

	export let open = false;
	export let roomId = '';
	export let userId = '';
	export let userName = '';
	export let action: ChangeRequestAction = 'edit_task';
	export let targetLabel = '';
	/** Pre-filled payload that will be attached to the request */
	export let payload: Record<string, unknown> = {};

	const dispatch = createEventDispatcher<{ submitted: { id: string }; cancel: void }>();

	let note = '';
	let submitting = false;

	const actionLabels: Record<ChangeRequestAction, string> = {
		add_task: 'Add task',
		edit_task: 'Edit task',
		delete_task: 'Delete task',
		add_sprint: 'Add sprint',
		edit_sprint: 'Edit sprint',
		delete_sprint: 'Delete sprint',
		edit_timeline: 'Edit timeline',
		edit_cost: 'Edit cost / budget',
		import_sheet: 'Import spreadsheet',
		edit_field_schema: 'Edit custom field',
		remove_member: 'Remove member',
		edit_canvas: 'Edit canvas'
	};

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') cancel();
	}

	function cancel() {
		note = '';
		dispatch('cancel');
	}

	async function submit() {
		if (submitting) return;
		submitting = true;
		const fullPayload = note.trim() ? { ...payload, note: note.trim() } : { ...payload };
		const req = submitChangeRequest(roomId, userId, userName, action, targetLabel, fullPayload);
		submitting = false;
		note = '';
		dispatch('submitted', { id: req.id });
	}
</script>

<svelte:window on:keydown={handleKeydown} />

{#if open}
	<!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
	<div class="cr-backdrop" on:click={cancel}></div>
	<div class="cr-modal" role="dialog" aria-modal="true" aria-label="Request change">
		<header class="cr-header">
			<span class="cr-icon" aria-hidden="true">
				<svg viewBox="0 0 24 24">
					<path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm1 15h-2v-2h2v2zm0-4h-2V7h2v6z" />
				</svg>
			</span>
			<div>
				<h3>Request Change</h3>
				<p>You don't have permission to make this change directly. Submit a request for admin review.</p>
			</div>
		</header>

		<div class="cr-body">
			<div class="cr-row">
				<span class="cr-label">Action</span>
				<span class="cr-value cr-badge">{actionLabels[action]}</span>
			</div>
			{#if targetLabel}
				<div class="cr-row">
					<span class="cr-label">Target</span>
					<span class="cr-value">{targetLabel}</span>
				</div>
			{/if}

			<label class="cr-note-label" for="cr-note">
				Note <span class="cr-opt">(optional)</span>
			</label>
			<textarea
				id="cr-note"
				class="cr-note"
				placeholder="Add context for the admin…"
				bind:value={note}
				rows="3"
				maxlength="500"
			></textarea>
		</div>

		<footer class="cr-footer">
			<button type="button" class="cr-btn cr-cancel" on:click={cancel}>Cancel</button>
			<button
				type="button"
				class="cr-btn cr-submit"
				on:click={() => void submit()}
				disabled={submitting}
			>
				{submitting ? 'Submitting…' : 'Submit Request'}
			</button>
		</footer>
	</div>
{/if}

<style>
	.cr-backdrop {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.42);
		z-index: 1200;
		backdrop-filter: blur(2px);
	}

	.cr-modal {
		position: fixed;
		top: 50%;
		left: 50%;
		transform: translate(-50%, -50%);
		z-index: 1201;
		width: min(440px, calc(100vw - 2rem));
		background: var(--ws-surface, #1e1e2e);
		border: 1px solid color-mix(in srgb, var(--ws-border, #3a3a52) 80%, transparent);
		border-radius: 14px;
		box-shadow: 0 24px 60px rgba(0, 0, 0, 0.45);
		display: flex;
		flex-direction: column;
		overflow: hidden;
	}

	.cr-header {
		display: flex;
		align-items: flex-start;
		gap: 0.75rem;
		padding: 1rem 1.1rem 0.8rem;
		border-bottom: 1px solid color-mix(in srgb, var(--ws-border, #3a3a52) 60%, transparent);
	}

	.cr-icon {
		flex-shrink: 0;
		width: 32px;
		height: 32px;
		border-radius: 8px;
		background: color-mix(in srgb, #f59e0b 18%, transparent);
		display: flex;
		align-items: center;
		justify-content: center;
	}

	.cr-icon svg {
		width: 18px;
		height: 18px;
		fill: #f59e0b;
	}

	.cr-header h3 {
		margin: 0 0 0.2rem;
		font-size: 0.92rem;
		font-weight: 700;
		color: var(--ws-text, #e2e2f0);
	}

	.cr-header p {
		margin: 0;
		font-size: 0.72rem;
		color: var(--ws-muted, #8888a8);
		line-height: 1.5;
	}

	.cr-body {
		padding: 0.9rem 1.1rem;
		display: flex;
		flex-direction: column;
		gap: 0.6rem;
	}

	.cr-row {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}

	.cr-label {
		font-size: 0.7rem;
		font-weight: 600;
		color: var(--ws-muted, #8888a8);
		min-width: 52px;
	}

	.cr-value {
		font-size: 0.74rem;
		color: var(--ws-text, #e2e2f0);
	}

	.cr-badge {
		padding: 0.18rem 0.5rem;
		border-radius: 5px;
		background: color-mix(in srgb, var(--ws-surface, #1e1e2e) 60%, var(--ws-border, #3a3a52) 40%);
		border: 1px solid color-mix(in srgb, var(--ws-border, #3a3a52) 80%, transparent);
		font-weight: 600;
	}

	.cr-note-label {
		font-size: 0.72rem;
		font-weight: 600;
		color: var(--ws-text, #e2e2f0);
	}

	.cr-opt {
		font-weight: 400;
		color: var(--ws-muted, #8888a8);
	}

	.cr-note {
		width: 100%;
		box-sizing: border-box;
		background: color-mix(in srgb, var(--ws-surface, #1e1e2e) 70%, #000 30%);
		border: 1px solid color-mix(in srgb, var(--ws-border, #3a3a52) 80%, transparent);
		border-radius: 8px;
		color: var(--ws-text, #e2e2f0);
		font-size: 0.75rem;
		padding: 0.5rem 0.65rem;
		resize: vertical;
		outline: none;
		font-family: inherit;
		line-height: 1.5;
	}

	.cr-note:focus {
		border-color: color-mix(in srgb, #6366f1 60%, var(--ws-border, #3a3a52));
	}

	.cr-footer {
		display: flex;
		justify-content: flex-end;
		gap: 0.5rem;
		padding: 0.75rem 1.1rem;
		border-top: 1px solid color-mix(in srgb, var(--ws-border, #3a3a52) 60%, transparent);
	}

	.cr-btn {
		height: 1.92rem;
		padding: 0 0.9rem;
		border-radius: 8px;
		font-size: 0.74rem;
		font-weight: 600;
		cursor: pointer;
		border: 1px solid transparent;
		transition: opacity 0.12s, background 0.12s;
	}

	.cr-cancel {
		background: transparent;
		border-color: color-mix(in srgb, var(--ws-border, #3a3a52) 80%, transparent);
		color: var(--ws-muted, #8888a8);
	}

	.cr-cancel:hover {
		color: var(--ws-text, #e2e2f0);
		background: color-mix(in srgb, var(--ws-surface, #1e1e2e) 50%, var(--ws-border, #3a3a52) 50%);
	}

	.cr-submit {
		background: #6366f1;
		color: #fff;
		border-color: transparent;
	}

	.cr-submit:hover:not(:disabled) {
		background: #4f52d4;
	}

	.cr-submit:disabled {
		opacity: 0.55;
		cursor: not-allowed;
	}
</style>
