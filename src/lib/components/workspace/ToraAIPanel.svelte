<script lang="ts">
	import {
		editAITimeline,
		projectTimeline,
		timelineLoading,
		AI_TIMELINE_FORMAT_HINT
	} from '$lib/stores/timeline';
	import { addBoardActivity } from '$lib/stores/boardActivity';
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
	let showFormatGuide = false;

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';

	$: normalizedRoomID = normalizeRoomIDValue(roomId);
	$: currentState = $projectTimeline;
	$: sprints = currentState?.sprints ?? [];
	$: totalTasks = sprints.flatMap((s) => s.tasks).length;
	$: isLargeProject = totalTasks > 60;
	$: stateSize = currentState ? JSON.stringify(currentState).length : 0;
	$: isCompressed = stateSize > 24000;

	function createMessageID() {
		if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
			return crypto.randomUUID();
		}
		return `msg-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
	}

	function appendMessage(role: ToraMessage['role'], text: string) {
		messages = [
			...messages,
			{ id: createMessageID(), role, text, timestamp: Date.now() }
		];
	}

	function formatSuccessMessage() {
		if (!currentState) return 'Timeline updated.';
		const missing = currentState.missing_sprints ?? [];
		if (currentState.is_partial && missing.length > 0) {
			return `Updated (partial): pending sprint tasks for ${missing.join(', ')}.`;
		}
		return 'Board updated and synced across all tabs.';
	}

	async function submitEditPrompt() {
		submitError = '';
		const prompt = draft.trim();
		if (!prompt) return;

		appendMessage('user', prompt);
		draft = '';

		if (!normalizedRoomID) {
			appendMessage('assistant', 'Room id is missing — AI edits cannot run right now.');
			return;
		}
		if (!currentState) {
			appendMessage('assistant', 'Initialize a project first so Tora has a current state to edit.');
			return;
		}

		try {
			await editAITimeline(normalizedRoomID, prompt, currentState);
			await initializeTaskStoreForRoom(normalizedRoomID, { apiBase: API_BASE });
			addBoardActivity({
				type: 'board_edited',
				title: 'Board edited via Tora AI',
				subtitle: prompt.length > 60 ? prompt.slice(0, 57) + '…' : prompt
			});
			appendMessage('assistant', formatSuccessMessage());
		} catch (error) {
			submitError = error instanceof Error ? error.message : 'Failed to apply Tora AI edit.';
			appendMessage('assistant', `Error: ${submitError}`);
		}
	}

	function handleKeydown(event: KeyboardEvent) {
		if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
			void submitEditPrompt();
		}
	}
</script>

<section class="tora-panel" aria-label="Tora AI editor">
	<!-- ── Header ──────────────────────────────────────────────────────── -->
	<header class="tora-header">
		<div class="tora-title">
			<span class="tora-sparkle" aria-hidden="true">✦</span>
			<h2>Tora AI</h2>
		</div>

		{#if currentState}
			<div class="state-summary">
				<span class="ss-item">
					<strong>{sprints.length}</strong> sprints
				</span>
				<span class="ss-sep">·</span>
				<span class="ss-item">
					<strong>{totalTasks}</strong> tasks
				</span>
				{#if isLargeProject}
					<span class="ss-sep">·</span>
					<span class="ss-badge large-badge" title="Large project — state auto-compressed when sending to AI">
						Large
					</span>
				{/if}
				{#if isCompressed}
					<span class="ss-sep">·</span>
					<span class="ss-badge compressed-badge" title="Descriptions stripped to reduce token usage">
						Compressed
					</span>
				{/if}
			</div>
		{:else}
			<span class="no-project">No project loaded</span>
		{/if}

		<button
			type="button"
			class="guide-toggle"
			on:click={() => (showFormatGuide = !showFormatGuide)}
			aria-expanded={showFormatGuide}
		>
			{showFormatGuide ? 'Hide schema' : 'Show schema'}
		</button>
	</header>

	<!-- ── Format guide (collapsible) ─────────────────────────────────── -->
	{#if showFormatGuide}
		<section class="format-guide" aria-label="AI output schema">
			<p class="guide-note">
				This schema is automatically prepended to every prompt, telling the AI what JSON to return.
				You never need to repeat this in your message.
			</p>
			<pre class="guide-pre">{AI_TIMELINE_FORMAT_HINT}</pre>
		</section>
	{/if}

	<!-- ── Message thread ──────────────────────────────────────────────── -->
	<div class="message-thread">
		{#if messages.length === 0}
			<div class="empty-thread">
				<p>Describe what you want Tora to change about the current board.</p>
				<ul class="example-list">
					<li>"Add a QA sprint after Sprint 2 with 3 days of testing tasks"</li>
					<li>"Mark all backend tasks in Sprint 1 as in progress"</li>
					<li>"Increase the budget for Sprint 3 to $15k and add 2 frontend tasks"</li>
					<li>"Add a critical task: Payment Integration, effort 9, assignee Backend Dev"</li>
				</ul>
				{#if isLargeProject}
					<p class="large-note">
						This is a large project ({totalTasks} tasks).
						Task descriptions are automatically stripped when sending state to the AI to stay within token limits.
						Task IDs, titles, statuses, and priorities are always preserved.
					</p>
				{/if}
			</div>
		{:else}
			{#each messages as message (message.id)}
				<article class="chat-row" class:user={message.role === 'user'}>
					<header class="chat-row-head">
						<strong>{message.role === 'user' ? 'You' : 'Tora AI'}</strong>
						<time>{new Date(message.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</time>
					</header>
					<p>{message.text}</p>
				</article>
			{/each}
		{/if}
	</div>

	<!-- ── Composer ────────────────────────────────────────────────────── -->
	<form
		class="composer"
		on:submit|preventDefault={() => void submitEditPrompt()}
	>
		<textarea
			bind:value={draft}
			rows="3"
			placeholder="Add a QA sprint after Sprint 2 with 3 days of testing…"
			on:keydown={handleKeydown}
			disabled={$timelineLoading}
		></textarea>

		<div class="composer-footer">
			<span class="composer-hint">⌘ Enter to send</span>
			{#if submitError}
				<p class="submit-error">{submitError}</p>
			{/if}
			<button type="submit" disabled={$timelineLoading || !draft.trim() || !currentState}>
				{#if $timelineLoading}
					<span class="btn-spinner" aria-hidden="true"></span>
					Applying…
				{:else}
					Apply Edit
				{/if}
			</button>
		</div>
	</form>
</section>

<style>
	/* ── Theme tokens ────────────────────────────────────────────────── */
	:global(:root) {
		--tora-bg: #0d0d12;
		--tora-surface: rgba(255, 255, 255, 0.03);
		--tora-border: rgba(255, 255, 255, 0.1);
		--tora-text: #edf5ff;
		--tora-muted: rgba(188, 202, 232, 0.78);
		--tora-accent: #7ab5ff;
		--tora-accent-soft: rgba(122, 181, 255, 0.18);
		--tora-user-bg: rgba(104, 163, 250, 0.18);
		--tora-user-border: rgba(122, 181, 255, 0.5);
	}

	:global(:root[data-theme='light']),
	:global(.theme-light) {
		--tora-bg: #f0f4fb;
		--tora-surface: rgba(255, 255, 255, 0.8);
		--tora-border: rgba(160, 182, 220, 0.4);
		--tora-text: #142443;
		--tora-muted: rgba(68, 88, 124, 0.72);
		--tora-accent: #2b6fd6;
		--tora-accent-soft: rgba(43, 111, 214, 0.1);
		--tora-user-bg: rgba(43, 111, 214, 0.1);
		--tora-user-border: rgba(43, 111, 214, 0.36);
	}

	/* ── Panel shell ─────────────────────────────────────────────────── */
	.tora-panel {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: auto auto 1fr auto;
		gap: 0;
		background: var(--tora-bg);
		color: var(--tora-text);
	}

	/* ── Header ──────────────────────────────────────────────────────── */
	.tora-header {
		padding: 0.82rem 1rem;
		display: flex;
		align-items: center;
		gap: 0.72rem;
		border-bottom: 1px solid var(--tora-border);
		background: var(--tora-surface);
		flex-wrap: wrap;
	}

	.tora-title {
		display: flex;
		align-items: center;
		gap: 0.44rem;
	}

	.tora-title h2 {
		margin: 0;
		font-size: 0.92rem;
		font-weight: 700;
		letter-spacing: 0.03em;
	}

	.tora-sparkle {
		font-size: 0.9rem;
		color: var(--tora-accent);
	}

	.state-summary {
		display: flex;
		align-items: center;
		gap: 0.32rem;
		font-size: 0.72rem;
		color: var(--tora-muted);
	}

	.ss-item strong {
		color: var(--tora-text);
	}

	.ss-sep {
		opacity: 0.45;
	}

	.ss-badge {
		font-size: 0.6rem;
		font-weight: 600;
		letter-spacing: 0.04em;
		padding: 0.1rem 0.36rem;
		border-radius: 999px;
	}

	.large-badge {
		background: rgba(249, 115, 22, 0.18);
		border: 1px solid rgba(249, 115, 22, 0.4);
		color: #fdba74;
	}

	.compressed-badge {
		background: rgba(234, 179, 8, 0.15);
		border: 1px solid rgba(234, 179, 8, 0.38);
		color: #fde68a;
	}

	.no-project {
		font-size: 0.72rem;
		color: var(--tora-muted);
	}

	.guide-toggle {
		margin-left: auto;
		border: 1px solid var(--tora-border);
		border-radius: 8px;
		background: transparent;
		color: var(--tora-muted);
		font-size: 0.7rem;
		padding: 0.26rem 0.54rem;
		cursor: pointer;
	}

	.guide-toggle:hover {
		color: var(--tora-text);
		border-color: var(--tora-accent);
	}

	/* ── Format guide ────────────────────────────────────────────────── */
	.format-guide {
		padding: 0.7rem 1rem;
		border-bottom: 1px solid var(--tora-border);
		background: rgba(0, 0, 0, 0.18);
		display: grid;
		gap: 0.4rem;
	}

	:global(.theme-light) .format-guide {
		background: rgba(200, 220, 250, 0.12);
	}

	.guide-note {
		margin: 0;
		font-size: 0.72rem;
		color: var(--tora-muted);
		line-height: 1.4;
	}

	.guide-pre {
		margin: 0;
		font-size: 0.64rem;
		line-height: 1.5;
		color: var(--tora-muted);
		white-space: pre-wrap;
		overflow-x: auto;
		max-height: 220px;
		overflow-y: auto;
		background: rgba(0, 0, 0, 0.2);
		padding: 0.5rem 0.6rem;
		border-radius: 8px;
		border: 1px solid var(--tora-border);
	}

	/* ── Message thread ──────────────────────────────────────────────── */
	.message-thread {
		min-height: 0;
		overflow-y: auto;
		padding: 0.8rem 1rem;
		display: grid;
		gap: 0.5rem;
		align-content: start;
	}

	.empty-thread {
		border: 1px solid var(--tora-border);
		border-radius: 12px;
		background: var(--tora-surface);
		padding: 0.9rem;
		display: grid;
		gap: 0.56rem;
	}

	.empty-thread p {
		margin: 0;
		font-size: 0.8rem;
		color: var(--tora-muted);
		line-height: 1.45;
	}

	.example-list {
		margin: 0;
		padding: 0 0 0 1rem;
		display: grid;
		gap: 0.26rem;
	}

	.example-list li {
		font-size: 0.74rem;
		color: var(--tora-muted);
		font-style: italic;
	}

	.large-note {
		margin: 0;
		font-size: 0.72rem;
		color: rgba(253, 186, 116, 0.88);
		background: rgba(249, 115, 22, 0.1);
		border: 1px solid rgba(249, 115, 22, 0.3);
		border-radius: 8px;
		padding: 0.44rem 0.56rem;
		line-height: 1.42;
	}

	.chat-row {
		border: 1px solid var(--tora-border);
		border-radius: 12px;
		background: var(--tora-surface);
		padding: 0.62rem 0.7rem;
		display: grid;
		gap: 0.24rem;
	}

	.chat-row.user {
		border-color: var(--tora-user-border);
		background: var(--tora-user-bg);
	}

	.chat-row-head {
		display: flex;
		justify-content: space-between;
		align-items: baseline;
		gap: 0.5rem;
	}

	.chat-row-head strong {
		font-size: 0.7rem;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--tora-muted);
	}

	.chat-row-head time {
		font-size: 0.64rem;
		color: var(--tora-muted);
		opacity: 0.75;
	}

	.chat-row p {
		margin: 0;
		font-size: 0.8rem;
		line-height: 1.46;
		color: var(--tora-text);
	}

	/* ── Composer ────────────────────────────────────────────────────── */
	.composer {
		border-top: 1px solid var(--tora-border);
		background: var(--tora-surface);
		padding: 0.7rem 1rem;
		display: grid;
		gap: 0.48rem;
	}

	.composer textarea {
		width: 100%;
		border: 1px solid var(--tora-border);
		border-radius: 10px;
		background: rgba(0, 0, 0, 0.15);
		color: var(--tora-text);
		padding: 0.58rem 0.66rem;
		font-size: 0.82rem;
		line-height: 1.44;
		resize: none;
		transition: border-color 0.15s;
	}

	:global(.theme-light) .composer textarea {
		background: rgba(255, 255, 255, 0.72);
	}

	.composer textarea:focus {
		outline: none;
		border-color: var(--tora-accent);
	}

	.composer textarea::placeholder {
		color: var(--tora-muted);
		opacity: 0.7;
	}

	.composer textarea:disabled {
		opacity: 0.55;
	}

	.composer-footer {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.6rem;
	}

	.composer-hint {
		font-size: 0.66rem;
		color: var(--tora-muted);
		opacity: 0.6;
	}

	.submit-error {
		margin: 0;
		flex: 1;
		font-size: 0.72rem;
		color: rgba(255, 160, 160, 0.9);
	}

	.composer-footer button {
		display: flex;
		align-items: center;
		gap: 0.38rem;
		border: 1px solid rgba(122, 181, 255, 0.5);
		border-radius: 9px;
		background: var(--tora-accent-soft);
		color: var(--tora-text);
		padding: 0.5rem 0.86rem;
		font-size: 0.78rem;
		cursor: pointer;
		transition:
			background 0.15s,
			border-color 0.15s;
		white-space: nowrap;
	}

	.composer-footer button:hover:not(:disabled) {
		background: rgba(122, 181, 255, 0.28);
		border-color: var(--tora-accent);
	}

	.composer-footer button:disabled {
		opacity: 0.48;
		cursor: not-allowed;
	}

	.btn-spinner {
		display: inline-block;
		width: 0.7rem;
		height: 0.7rem;
		border-radius: 50%;
		border: 1.5px solid rgba(255, 255, 255, 0.3);
		border-top-color: currentColor;
		animation: spin 0.65s linear infinite;
	}

	@keyframes spin {
		to { transform: rotate(360deg); }
	}
</style>
