<script lang="ts">
	import { tick } from 'svelte';
	import {
		editAITimeline,
		projectTimeline,
		timelineLoading
	} from '$lib/stores/timeline';
	import { addBoardActivity } from '$lib/stores/boardActivity';
	import { initializeTaskStoreForRoom } from '$lib/stores/tasks';
	import { normalizeRoomIDValue } from '$lib/utils/chat/core';

	export let roomId = '';
	export let contextKey = 'taskboard';

	type ToraMessage = {
		id: string;
		role: 'user' | 'assistant';
		text: string;
		timestamp: number;
	};

	type PersistedToraConversation = {
		version: 1;
		messages: ToraMessage[];
	};

	let draft = '';
	let messages: ToraMessage[] = [];
	let submitError = '';
	let threadElement: HTMLDivElement | null = null;
	let loadedConversationKey = '';

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';
	const TORA_CHAT_STORAGE_PREFIX = 'tora_ai_chat';
	const TORA_CHAT_HISTORY_LIMIT = 80;

	$: normalizedRoomID = normalizeRoomIDValue(roomId);
	$: currentState = $projectTimeline;
	$: sprints = currentState?.sprints ?? [];
	$: totalTasks = sprints.flatMap((sprint) => sprint.tasks).length;
	$: isLargeProject = totalTasks > 60;
	$: normalizedContextKey = normalizeRoomIDValue(contextKey) || 'taskboard';
	$: conversationStorageKey = `${TORA_CHAT_STORAGE_PREFIX}:${normalizedRoomID}:${normalizedContextKey}`;
	$: boardStatusText = currentState
		? `${sprints.length} sprints \u00B7 ${totalTasks} tasks`
		: 'No project loaded';
	$: if (conversationStorageKey !== loadedConversationKey) {
		loadedConversationKey = conversationStorageKey;
		loadConversationForKey(conversationStorageKey);
		draft = '';
		submitError = '';
	}

	function createMessageID() {
		if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
			return crypto.randomUUID();
		}
		return `msg-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
	}

	function scrollThreadToBottom() {
		void tick().then(() => {
			if (!threadElement) {
				return;
			}
			threadElement.scrollTop = threadElement.scrollHeight;
		});
	}

	function isBrowser() {
		return typeof window !== 'undefined' && Boolean(window.localStorage);
	}

	function sanitizePersistedMessages(candidate: unknown) {
		if (!Array.isArray(candidate)) {
			return [] as ToraMessage[];
		}
		const sanitized = candidate
			.filter((entry) => entry && typeof entry === 'object')
			.map((entry) => {
				const source = entry as Partial<ToraMessage>;
				const role = source.role === 'assistant' ? 'assistant' : 'user';
				const text = typeof source.text === 'string' ? source.text.trim() : '';
				const timestamp =
					typeof source.timestamp === 'number' && Number.isFinite(source.timestamp)
						? source.timestamp
						: Date.now();
				if (!text) {
					return null;
				}
				return {
					id: createMessageID(),
					role,
					text,
					timestamp
				} satisfies ToraMessage;
			})
			.filter((entry): entry is ToraMessage => Boolean(entry));
		return sanitized.slice(-TORA_CHAT_HISTORY_LIMIT);
	}

	function loadConversationForKey(storageKey: string) {
		if (!isBrowser() || !storageKey) {
			messages = [];
			return;
		}
		try {
			const raw = window.localStorage.getItem(storageKey);
			if (!raw) {
				messages = [];
				return;
			}
			const parsed = JSON.parse(raw) as PersistedToraConversation | ToraMessage[] | null;
			const nextMessages = Array.isArray(parsed)
				? sanitizePersistedMessages(parsed)
				: sanitizePersistedMessages((parsed as PersistedToraConversation | null)?.messages ?? []);
			messages = nextMessages;
		} catch {
			messages = [];
		}
		scrollThreadToBottom();
	}

	function persistConversationForKey(storageKey: string) {
		if (!isBrowser() || !storageKey) {
			return;
		}
		try {
			const payload: PersistedToraConversation = {
				version: 1,
				messages: messages.slice(-TORA_CHAT_HISTORY_LIMIT)
			};
			window.localStorage.setItem(storageKey, JSON.stringify(payload));
		} catch {
			// Best-effort persistence only.
		}
	}

	function appendMessage(role: ToraMessage['role'], text: string) {
		const normalizedText = String(text || '').trim();
		if (!normalizedText) {
			return;
		}
		messages = [
			...messages,
			{ id: createMessageID(), role, text: normalizedText, timestamp: Date.now() }
		].slice(-TORA_CHAT_HISTORY_LIMIT);
		persistConversationForKey(conversationStorageKey);
		scrollThreadToBottom();
	}

	function formatSuccessMessage() {
		if (!currentState) {
			return 'Timeline updated.';
		}
		const missing = currentState.missing_sprints ?? [];
		if (currentState.is_partial && missing.length > 0) {
			return `Updated (partial): pending sprint tasks for ${missing.join(', ')}.`;
		}
		return 'Board updated and synced across all tabs.';
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
			appendMessage('assistant', 'Room id is missing. AI edits cannot run right now.');
			return;
		}
		if (!currentState) {
			appendMessage('assistant', 'Initialize a project first so Tora has a board state to edit.');
			return;
		}

		try {
			const result = await editAITimeline(normalizedRoomID, prompt, currentState);
			await initializeTaskStoreForRoom(normalizedRoomID, { apiBase: API_BASE });
			addBoardActivity({
				type: 'board_edited',
				title: 'Board edited via Tora AI',
				subtitle: prompt.length > 64 ? `${prompt.slice(0, 61)}...` : prompt
			});
			appendMessage('assistant', result.assistantReply || formatSuccessMessage());
		} catch (error) {
			submitError = error instanceof Error ? error.message : 'Failed to apply Tora AI edit.';
			appendMessage('assistant', `Error: ${submitError}`);
		}
	}

	function handleComposerKeydown(event: KeyboardEvent) {
		if (event.key !== 'Enter' || event.shiftKey) {
			return;
		}
		event.preventDefault();
		void submitEditPrompt();
	}
</script>

<section class="tora-chat" aria-label="Tora AI chat">
	<header class="tora-chat-header">
		<div class="tora-brand">
			<span class="tora-brand-icon" aria-hidden="true">✦</span>
			<div class="tora-brand-copy">
				<h2>Tora AI</h2>
				<p>Chat to update the current task board</p>
			</div>
		</div>
		<div class="tora-meta">
			<span>{boardStatusText}</span>
			{#if isLargeProject}
				<span class="tora-meta-badge">Large project</span>
			{/if}
		</div>
	</header>

	<div class="tora-thread" bind:this={threadElement}>
		{#if messages.length === 0}
			<article class="tora-message assistant starter">
				<header>
					<strong>Tora AI</strong>
					<time>Now</time>
				</header>
				<p>
					Ask me to add sprints, rebalance priorities, adjust budgets, or update task status. I will
					apply changes directly to this board.
				</p>
			</article>
		{:else}
			{#each messages as message (message.id)}
				<article class="tora-message" class:user={message.role === 'user'}>
					<header>
						<strong>{message.role === 'user' ? 'You' : 'Tora AI'}</strong>
						<time>
							{new Date(message.timestamp).toLocaleTimeString([], {
								hour: '2-digit',
								minute: '2-digit'
							})}
						</time>
					</header>
					<p>{message.text}</p>
				</article>
			{/each}
		{/if}
	</div>

	{#if submitError}
		<div class="tora-error" role="status" aria-live="polite">{submitError}</div>
	{/if}

	<form class="tora-composer" on:submit|preventDefault={() => void submitEditPrompt()}>
		<div class="tora-input-shell">
			<textarea
				bind:value={draft}
				rows="1"
				placeholder="Ask Tora to update this sprint plan..."
				on:keydown={handleComposerKeydown}
				disabled={$timelineLoading || !currentState}
			></textarea>
			<button type="submit" disabled={$timelineLoading || !draft.trim() || !currentState}>
				{#if $timelineLoading}
					<span class="send-spinner" aria-hidden="true"></span>
					Applying
				{:else}
					Send
				{/if}
			</button>
		</div>
		<div class="tora-composer-meta">
			<span>Enter to send</span>
			{#if !currentState}
				<span>Create a project to start chatting</span>
			{:else if isLargeProject}
				<span>Large board: state payload is auto-compressed for AI</span>
			{/if}
		</div>
	</form>
</section>

<style>
	:global(:root) {
		--tora-bg: #0d1016;
		--tora-surface: rgba(255, 255, 255, 0.03);
		--tora-border: rgba(157, 179, 214, 0.24);
		--tora-text: #eaf1ff;
		--tora-muted: rgba(188, 205, 235, 0.78);
		--tora-accent: #73adff;
		--tora-accent-soft: rgba(115, 173, 255, 0.18);
		--tora-user-bg: rgba(63, 103, 165, 0.36);
		--tora-user-border: rgba(120, 171, 245, 0.5);
		--tora-assistant-bg: rgba(24, 34, 52, 0.85);
	}

	:global(:root[data-theme='light']),
	:global(.theme-light) {
		--tora-bg: #f4f7fc;
		--tora-surface: rgba(255, 255, 255, 0.84);
		--tora-border: rgba(145, 168, 209, 0.32);
		--tora-text: #132442;
		--tora-muted: rgba(65, 88, 128, 0.76);
		--tora-accent: #1f63ce;
		--tora-accent-soft: rgba(31, 99, 206, 0.14);
		--tora-user-bg: rgba(31, 99, 206, 0.14);
		--tora-user-border: rgba(31, 99, 206, 0.35);
		--tora-assistant-bg: rgba(255, 255, 255, 0.9);
	}

	.tora-chat {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: auto minmax(0, 1fr) auto auto;
		background: var(--tora-bg);
		color: var(--tora-text);
		border-radius: 0.88rem;
		overflow: hidden;
	}

	.tora-chat-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.85rem;
		padding: 0.86rem 1rem;
		border-bottom: 1px solid var(--tora-border);
		background: var(--tora-surface);
	}

	.tora-brand {
		display: inline-flex;
		align-items: center;
		gap: 0.58rem;
		min-width: 0;
	}

	.tora-brand-icon {
		width: 1.55rem;
		height: 1.55rem;
		border-radius: 0.46rem;
		border: 1px solid color-mix(in srgb, var(--tora-accent) 40%, var(--tora-border));
		background: color-mix(in srgb, var(--tora-accent-soft) 68%, var(--tora-surface));
		display: inline-flex;
		align-items: center;
		justify-content: center;
		font-size: 0.82rem;
		color: var(--tora-accent);
		flex: 0 0 auto;
	}

	.tora-brand-copy {
		min-width: 0;
		display: grid;
		gap: 0.14rem;
	}

	.tora-brand-copy h2 {
		margin: 0;
		font-size: 0.88rem;
		font-weight: 700;
		letter-spacing: 0.03em;
	}

	.tora-brand-copy p {
		margin: 0;
		font-size: 0.72rem;
		color: var(--tora-muted);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.tora-meta {
		display: inline-flex;
		align-items: center;
		gap: 0.45rem;
		font-size: 0.7rem;
		color: var(--tora-muted);
		white-space: nowrap;
	}

	.tora-meta-badge {
		border: 1px solid rgba(238, 170, 75, 0.45);
		background: rgba(238, 170, 75, 0.16);
		color: #f4cf9a;
		border-radius: 999px;
		padding: 0.15rem 0.44rem;
		font-size: 0.62rem;
		font-weight: 600;
		letter-spacing: 0.04em;
		text-transform: uppercase;
	}

	.tora-thread {
		min-height: 0;
		overflow-y: auto;
		display: grid;
		align-content: start;
		gap: 0.56rem;
		padding: 0.88rem 1rem;
	}

	.tora-message {
		border: 1px solid var(--tora-border);
		border-radius: 0.72rem;
		background: var(--tora-assistant-bg);
		padding: 0.68rem 0.74rem;
		display: grid;
		gap: 0.32rem;
		max-width: min(52rem, 100%);
	}

	.tora-message.user {
		margin-left: auto;
		border-color: var(--tora-user-border);
		background: var(--tora-user-bg);
	}

	.tora-message.starter {
		border-color: color-mix(in srgb, var(--tora-accent) 34%, var(--tora-border));
		background: color-mix(in srgb, var(--tora-accent-soft) 70%, var(--tora-assistant-bg));
	}

	.tora-message header {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.6rem;
	}

	.tora-message strong {
		font-size: 0.67rem;
		font-weight: 700;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--tora-muted);
	}

	.tora-message time {
		font-size: 0.63rem;
		color: var(--tora-muted);
	}

	.tora-message p {
		margin: 0;
		font-size: 0.82rem;
		line-height: 1.46;
		color: var(--tora-text);
		white-space: pre-wrap;
	}

	.tora-error {
		margin: 0 1rem;
		margin-bottom: 0.45rem;
		font-size: 0.74rem;
		color: #ffd2d2;
		background: rgba(153, 27, 27, 0.32);
		border: 1px solid rgba(224, 128, 128, 0.5);
		border-radius: 0.56rem;
		padding: 0.42rem 0.55rem;
	}

	.tora-composer {
		border-top: 1px solid var(--tora-border);
		background: var(--tora-surface);
		padding: 0.72rem 1rem 0.86rem;
		display: grid;
		gap: 0.42rem;
	}

	.tora-input-shell {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: 0.52rem;
		align-items: end;
	}

	.tora-input-shell textarea {
		min-height: 2.35rem;
		max-height: 7.5rem;
		border: 1px solid var(--tora-border);
		border-radius: 0.66rem;
		background: rgba(0, 0, 0, 0.16);
		color: var(--tora-text);
		padding: 0.58rem 0.72rem;
		font-size: 0.82rem;
		line-height: 1.42;
		resize: vertical;
	}

	:global(.theme-light) .tora-input-shell textarea {
		background: rgba(255, 255, 255, 0.84);
	}

	.tora-input-shell textarea:focus {
		outline: none;
		border-color: var(--tora-accent);
		box-shadow: 0 0 0 2px color-mix(in srgb, var(--tora-accent) 24%, transparent);
	}

	.tora-input-shell textarea::placeholder {
		color: var(--tora-muted);
	}

	.tora-input-shell textarea:disabled {
		opacity: 0.58;
	}

	.tora-input-shell button {
		border: 1px solid color-mix(in srgb, var(--tora-accent) 56%, var(--tora-border));
		border-radius: 0.58rem;
		background: color-mix(in srgb, var(--tora-accent-soft) 70%, var(--tora-surface));
		color: var(--tora-text);
		min-width: 5.4rem;
		height: 2.35rem;
		padding: 0 0.9rem;
		font-size: 0.76rem;
		font-weight: 600;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		gap: 0.38rem;
		cursor: pointer;
	}

	.tora-input-shell button:hover:not(:disabled) {
		border-color: var(--tora-accent);
		background: color-mix(in srgb, var(--tora-accent-soft) 88%, var(--tora-surface));
	}

	.tora-input-shell button:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.send-spinner {
		display: inline-block;
		width: 0.72rem;
		height: 0.72rem;
		border-radius: 50%;
		border: 1.6px solid rgba(255, 255, 255, 0.35);
		border-top-color: currentColor;
		animation: tora-spin 0.72s linear infinite;
	}

	.tora-composer-meta {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.64rem;
		font-size: 0.68rem;
		color: var(--tora-muted);
	}

	@keyframes tora-spin {
		to {
			transform: rotate(360deg);
		}
	}

	@media (max-width: 860px) {
		.tora-chat-header {
			flex-wrap: wrap;
			align-items: flex-start;
		}

		.tora-meta {
			width: 100%;
			justify-content: space-between;
		}

		.tora-thread {
			padding: 0.72rem 0.8rem;
		}

		.tora-composer {
			padding: 0.64rem 0.8rem 0.8rem;
		}
	}
</style>
