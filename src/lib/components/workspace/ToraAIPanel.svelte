<script lang="ts">
	import { editAITimeline, projectTimeline, timelineLoading } from '$lib/stores/timeline';
	import { initializeTaskStoreForRoom } from '$lib/stores/tasks';
	import { normalizeRoomIDValue } from '$lib/utils/chat/core';

	export let roomId = '';

	type ToraMessage = {
		id: string;
		role: 'user' | 'assistant';
		text: string;
		timestamp: number;
	};

	let draft = '';
	let messages: ToraMessage[] = [];
	let submitError = '';

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://localhost:8080';

	$: normalizedRoomID = normalizeRoomIDValue(roomId);
	$: currentState = $projectTimeline;

	function createMessageID() {
		if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
			return crypto.randomUUID();
		}
		return `msg-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
	}

	function appendMessage(role: ToraMessage['role'], text: string) {
		messages = [
			...messages,
			{
				id: createMessageID(),
				role,
				text,
				timestamp: Date.now()
			}
		];
	}

	function formatAssistantSuccessMessage() {
		if (!currentState) {
			return 'Timeline updated.';
		}
		const missing = currentState.missing_sprints ?? [];
		if (currentState.is_partial && missing.length > 0) {
			return `Updated with limits: pending sprint tasks for ${missing.join(', ')}.`;
		}
		return 'Timeline updated and synced across workspace tabs.';
	}

	async function submitEditPrompt() {
		submitError = '';
		const prompt = draft.trim();
		if (!prompt) {
			return;
		}

		appendMessage('user', prompt);
		draft = '';

		if (!normalizedRoomID) {
			appendMessage('assistant', 'Room id is missing, so AI edits cannot run right now.');
			return;
		}
		if (!currentState) {
			appendMessage('assistant', 'Initialize a project first so I can edit the current state.');
			return;
		}

		try {
			await editAITimeline(normalizedRoomID, prompt, currentState);
			await initializeTaskStoreForRoom(normalizedRoomID, {
				apiBase: API_BASE
			});
			appendMessage('assistant', formatAssistantSuccessMessage());
		} catch (error) {
			submitError = error instanceof Error ? error.message : 'Failed to apply Tora AI edit.';
			appendMessage('assistant', submitError);
		}
	}
</script>

<section class="tora-ai-panel" aria-label="Tora AI editor">
	<div class="message-thread">
		{#if messages.length === 0}
			<div class="empty-thread">
				Ask Tora to mutate your project state. Example: "Add a QA phase taking 2 days after Sprint 2".
			</div>
		{:else}
			{#each messages as message (message.id)}
				<article class="chat-row" class:user={message.role === 'user'}>
					<header>
						<strong>{message.role === 'user' ? 'You' : 'Tora AI'}</strong>
						<small>{new Date(message.timestamp).toLocaleTimeString()}</small>
					</header>
					<p>{message.text}</p>
				</article>
			{/each}
		{/if}
	</div>

	<form
		class="composer"
		on:submit|preventDefault={() => {
			void submitEditPrompt();
		}}
	>
		<textarea
			bind:value={draft}
			rows="3"
			placeholder="Add a QA phase taking 2 days, then rebalance workload in Sprint 3."
		></textarea>
		<div class="composer-row">
			{#if submitError}
				<p class="submit-error">{submitError}</p>
			{/if}
			<button type="submit" disabled={$timelineLoading || !draft.trim()}>
				{$timelineLoading ? 'Applying...' : 'Apply Edit'}
			</button>
		</div>
	</form>
</section>

<style>
	.tora-ai-panel {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: 1fr auto;
		gap: 0.74rem;
		padding: 0.95rem;
		background:
			radial-gradient(circle at 14% -10%, rgba(255, 255, 255, 0.08), transparent 34%),
			#0d0d12;
	}

	.message-thread {
		min-height: 0;
		overflow: auto;
		display: grid;
		gap: 0.52rem;
		align-content: start;
	}

	.empty-thread,
	.chat-row,
	.composer {
		border: 1px solid rgba(255, 255, 255, 0.12);
		border-radius: 14px;
		background: rgba(255, 255, 255, 0.03);
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
	}

	.empty-thread {
		padding: 0.85rem;
		font-size: 0.8rem;
		line-height: 1.45;
		color: rgba(188, 202, 231, 0.82);
	}

	.chat-row {
		padding: 0.62rem 0.68rem;
		display: grid;
		gap: 0.26rem;
	}

	.chat-row header {
		display: flex;
		justify-content: space-between;
		gap: 0.56rem;
		align-items: baseline;
	}

	.chat-row strong {
		font-size: 0.72rem;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: rgba(186, 198, 227, 0.86);
	}

	.chat-row small {
		font-size: 0.68rem;
		color: rgba(163, 178, 211, 0.74);
	}

	.chat-row p {
		margin: 0;
		font-size: 0.8rem;
		line-height: 1.45;
		color: #edf5ff;
	}

	.chat-row.user {
		border-color: rgba(141, 189, 255, 0.58);
		background: rgba(104, 163, 250, 0.2);
	}

	.composer {
		position: sticky;
		bottom: 0;
		padding: 0.62rem;
		display: grid;
		gap: 0.56rem;
	}

	.composer textarea {
		width: 100%;
		border: 1px solid rgba(255, 255, 255, 0.14);
		border-radius: 11px;
		background: rgba(255, 255, 255, 0.04);
		color: #eff5ff;
		padding: 0.58rem 0.64rem;
		resize: none;
	}

	.composer textarea::placeholder {
		color: rgba(184, 198, 228, 0.66);
	}

	.composer-row {
		display: flex;
		justify-content: space-between;
		gap: 0.62rem;
		align-items: center;
	}

	.submit-error {
		margin: 0;
		font-size: 0.74rem;
		color: rgba(255, 171, 171, 0.93);
	}

	.composer button {
		border: 1px solid rgba(132, 185, 255, 0.58);
		border-radius: 10px;
		background: rgba(104, 162, 253, 0.24);
		color: #edf5ff;
		padding: 0.52rem 0.74rem;
		font-size: 0.78rem;
		cursor: pointer;
	}

	.composer button:hover:not(:disabled) {
		background: rgba(104, 162, 253, 0.34);
	}

	.composer button:disabled {
		opacity: 0.56;
		cursor: not-allowed;
	}
</style>
