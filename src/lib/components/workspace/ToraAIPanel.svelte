<script lang="ts">
	import { tick } from 'svelte';
	import {
		editAITimeline,
		projectTimeline,
		timelineLoading
	} from '$lib/stores/timeline';
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
	let composerTextarea: HTMLTextAreaElement | null = null;
	let loadedConversationKey = '';

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';
	const TORA_CHAT_STORAGE_PREFIX = 'tora_ai_chat';
	const TORA_CHAT_HISTORY_LIMIT = 80;
	const TORA_PROMPT_SUGGESTIONS = [
		'Rebalance unfinished tasks into the next sprint and keep priority order.',
		'Identify blockers and add follow-up tasks with owners and due dates.',
		'Review sprint budget vs spent and suggest where to reduce scope.'
	];

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
			if (result.intent !== 'chat') {
				await initializeTaskStoreForRoom(normalizedRoomID, { apiBase: API_BASE });
			}
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

	function applyToraSuggestion(prompt: string) {
		draft = prompt;
		submitError = '';
		void tick().then(() => composerTextarea?.focus());
	}

	function formatMessageTime(timestamp: number) {
		return new Date(timestamp).toLocaleTimeString([], {
			hour: '2-digit',
			minute: '2-digit'
		});
	}
</script>

<section class="tora-chat" aria-label="Tora AI chat">
	<header class="tora-chat-header">
		<div class="tora-brand">
			<span class="tora-brand-icon" aria-hidden="true">✦</span>
			<div class="tora-brand-copy">
				<h2>Tora AI</h2>
				<p>Taskboard agent</p>
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
			<div class="empty-state-v2">
				<div class="es-icon" aria-hidden="true">✦</div>
				<h4>Start planning with Tora</h4>
				<p>Ask Tora to reorganize tasks, budgets, priorities, and sprint structure in real time.</p>
			</div>
		{:else}
			{#each messages as message (message.id)}
				{#if message.role === 'user'}
					<div class="user-bubble-row">
						<article class="user-bubble">{message.text}</article>
					</div>
				{:else}
					<article class="ai-response-block">
						<div class="ai-response-meta">
							<span class="ai-response-dot" aria-hidden="true"></span>
							<div class="ai-response-title">
								Tora AI
								<span class="ai-time-chip">{formatMessageTime(message.timestamp)}</span>
							</div>
						</div>
						<div class="ai-response-body">{message.text}</div>
					</article>
				{/if}
			{/each}
		{/if}
		{#if $timelineLoading && currentState}
			<div class="ai-loading-row">
				<span class="ai-spinner" aria-hidden="true"></span>
				Applying board updates...
			</div>
		{/if}
	</div>

	<div class="suggestions-panel">
		{#each TORA_PROMPT_SUGGESTIONS as suggestion}
			<button
				type="button"
				class="suggestion-item"
				on:click={() => applyToraSuggestion(suggestion)}
				disabled={$timelineLoading || !currentState}
			>
				<span class="suggestion-arrow" aria-hidden="true">→</span>
				<span class="suggestion-text">{suggestion}</span>
			</button>
		{/each}
	</div>

	{#if submitError}
		<div class="tora-error" role="status" aria-live="polite">{submitError}</div>
	{/if}

	<form class="tora-composer" on:submit|preventDefault={() => void submitEditPrompt()}>
		<div class="tora-input-box">
			<textarea
				class="tora-textarea"
				bind:this={composerTextarea}
				bind:value={draft}
				rows="1"
				placeholder="Ask Tora to update this sprint plan..."
				on:keydown={handleComposerKeydown}
				disabled={$timelineLoading || !currentState}
			></textarea>
			<div class="tora-toolbar">
				<span class="tora-hint">
					{#if !currentState}
						Create a project to start chatting
					{:else if isLargeProject}
						Large board mode enabled
					{:else}
						Enter to send
					{/if}
				</span>
				<div class="toolbar-spacer"></div>
				<button
					type="submit"
					class="send-btn"
					disabled={$timelineLoading || !draft.trim() || !currentState}
					aria-label="Send message"
				>
					{#if $timelineLoading}
						<span class="ai-spinner" aria-hidden="true"></span>
					{:else}
						<svg viewBox="0 0 14 14" aria-hidden="true">
							<path d="M2 7h10M8 3l4 4-4 4"></path>
						</svg>
					{/if}
				</button>
			</div>
		</div>
	</form>
</section>

<style>
	.tora-chat {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: auto minmax(0, 1fr) auto auto;
		background: #1e1f24;
		color: #e8eaed;
		border-radius: 1rem;
		overflow: hidden;
		border: 1px solid rgba(255, 255, 255, 0.08);
		box-shadow: 0 8px 40px rgba(0, 0, 0, 0.45);
	}

	.tora-chat-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		padding: 0.72rem 0.9rem;
		border-bottom: 1px solid rgba(255, 255, 255, 0.07);
		background: rgba(255, 255, 255, 0.02);
	}

	.tora-brand {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		min-width: 0;
	}

	.tora-brand-icon {
		width: 1.4rem;
		height: 1.4rem;
		border-radius: 8px;
		border: 1px solid rgba(26, 115, 232, 0.26);
		background: rgba(26, 115, 232, 0.14);
		display: inline-flex;
		align-items: center;
		justify-content: center;
		font-size: 0.7rem;
		color: #8ab4f8;
		flex: 0 0 auto;
	}

	.tora-brand-copy {
		min-width: 0;
		display: grid;
		gap: 0.14rem;
	}

	.tora-brand-copy h2 {
		margin: 0;
		font-size: 0.86rem;
		font-weight: 600;
	}

	.tora-brand-copy p {
		margin: 0;
		font-size: 0.68rem;
		color: #9aa0a6;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.tora-meta {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
		font-size: 0.66rem;
		color: #9aa0a6;
		white-space: nowrap;
	}

	.tora-meta-badge {
		border: 1px solid rgba(255, 255, 255, 0.12);
		background: rgba(255, 255, 255, 0.07);
		color: #bdc1c6;
		border-radius: 999px;
		padding: 0.14rem 0.4rem;
		font-size: 0.58rem;
		font-weight: 600;
		letter-spacing: 0.05em;
		text-transform: uppercase;
	}

	.tora-thread {
		min-height: 0;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: 0.86rem;
		padding: 0.86rem;
	}

	.tora-thread::-webkit-scrollbar {
		width: 4px;
	}

	.tora-thread::-webkit-scrollbar-thumb {
		background: rgba(255, 255, 255, 0.12);
		border-radius: 4px;
	}

	.empty-state-v2 {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		text-align: center;
		padding: 1.4rem 1rem;
		gap: 0.54rem;
	}

	.empty-state-v2 .es-icon {
		width: 42px;
		height: 42px;
		border-radius: 999px;
		border: 1px solid rgba(26, 115, 232, 0.2);
		background: rgba(26, 115, 232, 0.12);
		color: #8ab4f8;
		display: grid;
		place-items: center;
	}

	.empty-state-v2 h4 {
		margin: 0;
		font-size: 0.86rem;
		color: #e8eaed;
	}

	.empty-state-v2 p {
		margin: 0;
		font-size: 0.76rem;
		line-height: 1.55;
		color: #9aa0a6;
		max-width: 320px;
	}

	.ai-response-block {
		display: grid;
		gap: 0.26rem;
	}

	.ai-response-meta {
		display: inline-flex;
		align-items: center;
		gap: 0.46rem;
	}

	.ai-response-dot {
		width: 6px;
		height: 6px;
		border-radius: 999px;
		background: #1a73e8;
	}

	.ai-response-title {
		display: inline-flex;
		align-items: center;
		gap: 0.46rem;
		font-size: 0.8rem;
		font-weight: 600;
		color: #e8eaed;
	}

	.ai-time-chip {
		border-radius: 999px;
		padding: 0.1rem 0.44rem;
		font-size: 0.62rem;
		border: 1px solid rgba(255, 255, 255, 0.1);
		background: rgba(255, 255, 255, 0.06);
		color: #9aa0a6;
	}

	.ai-response-body {
		margin-left: 0.72rem;
		padding-left: 0.72rem;
		border-left: 2px solid rgba(255, 255, 255, 0.1);
		margin: 0;
		font-size: 0.8rem;
		line-height: 1.6;
		color: #bdc1c6;
		white-space: pre-wrap;
		word-break: break-word;
	}

	.user-bubble-row {
		display: flex;
		justify-content: flex-end;
	}

	.user-bubble {
		max-width: 88%;
		border-radius: 18px 18px 4px 18px;
		border: 1px solid rgba(26, 115, 232, 0.25);
		background: rgba(26, 115, 232, 0.15);
		color: #e8eaed;
		padding: 0.56rem 0.74rem;
		font-size: 0.8rem;
		line-height: 1.46;
		white-space: pre-wrap;
		word-break: break-word;
	}

	.ai-loading-row {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		font-size: 0.78rem;
		color: #9aa0a6;
		font-style: italic;
	}

	.ai-spinner {
		width: 14px;
		height: 14px;
		border: 2px solid rgba(255, 255, 255, 0.12);
		border-top-color: #1a73e8;
		border-radius: 999px;
		animation: tora-spin 0.8s linear infinite;
	}

	.suggestions-panel {
		display: grid;
		padding: 0.34rem 0;
		background: rgba(255, 255, 255, 0.03);
		border-top: 1px solid rgba(255, 255, 255, 0.07);
	}

	.suggestion-item {
		border: none;
		background: transparent;
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		text-align: left;
		padding: 0.46rem 0.82rem;
		cursor: pointer;
	}

	.suggestion-item:hover:not(:disabled) {
		background: rgba(26, 115, 232, 0.12);
	}

	.suggestion-item:disabled {
		opacity: 0.58;
		cursor: not-allowed;
	}

	.suggestion-arrow {
		font-size: 0.82rem;
		color: #9aa0a6;
	}

	.suggestion-text {
		font-size: 0.75rem;
		line-height: 1.32;
		color: #bdc1c6;
	}

	.tora-error {
		margin: 0 0.82rem 0.58rem;
		font-size: 0.76rem;
		color: #ffd7d7;
		background: rgba(132, 33, 33, 0.44);
		border: 1px solid rgba(227, 134, 134, 0.52);
		border-radius: 10px;
		padding: 0.48rem 0.62rem;
	}

	.tora-composer {
		padding: 0.66rem 0.74rem 0.8rem;
		border-top: 1px solid rgba(255, 255, 255, 0.07);
		background: rgba(255, 255, 255, 0.02);
	}

	.tora-input-box {
		border: 1px solid rgba(255, 255, 255, 0.08);
		border-radius: 14px;
		background: rgba(255, 255, 255, 0.04);
		padding: 0.58rem 0.62rem;
		display: grid;
		gap: 0.42rem;
		transition:
			border-color 0.18s ease,
			background 0.18s ease;
	}

	.tora-input-box:focus-within {
		border-color: rgba(26, 115, 232, 0.5);
		background: rgba(26, 115, 232, 0.04);
	}

	.tora-textarea {
		min-height: 22px;
		max-height: 120px;
		border: none;
		background: transparent;
		color: #e8eaed;
		padding: 0;
		font-size: 0.84rem;
		line-height: 1.46;
		resize: none;
	}

	.tora-textarea:focus {
		outline: none;
	}

	.tora-textarea::placeholder {
		color: #5f6368;
	}

	.tora-textarea:disabled {
		opacity: 0.58;
	}

	.tora-toolbar {
		display: inline-flex;
		align-items: center;
		gap: 0.46rem;
	}

	.tora-hint {
		font-size: 0.68rem;
		color: #9aa0a6;
	}

	.toolbar-spacer {
		flex: 1;
	}

	.send-btn {
		width: 32px;
		height: 32px;
		border-radius: 50%;
		border: none;
		background: #1a73e8;
		color: #fff;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		box-shadow: 0 2px 10px rgba(26, 115, 232, 0.38);
		transition:
			background 0.18s ease,
			transform 0.18s ease,
			box-shadow 0.18s ease;
	}

	.send-btn svg {
		width: 14px;
		height: 14px;
		stroke: currentColor;
		stroke-width: 1.5;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.send-btn:hover:not(:disabled) {
		background: #1967d2;
		transform: scale(1.05);
		box-shadow: 0 4px 16px rgba(26, 115, 232, 0.48);
	}

	.send-btn:disabled {
		background: rgba(255, 255, 255, 0.08);
		cursor: not-allowed;
		box-shadow: none;
		transform: none;
	}

	.send-btn .ai-spinner {
		width: 13px;
		height: 13px;
		border-width: 1.8px;
		border-color: rgba(255, 255, 255, 0.42);
		border-top-color: #fff;
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
			padding: 0.72rem 0.72rem;
		}
	}
</style>
