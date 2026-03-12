<script lang="ts">
	import { createEventDispatcher, onDestroy, tick } from 'svelte';
	import type { ChatMessage } from '$lib/types/chat';

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';
	const PRIVATE_AI_ROOM_ID = 'private-ai';
	const DEVICE_ID_STORAGE_KEY = 'privateAiDeviceId';
	const PRIVATE_AI_SUGGESTIONS = [
		'Summarize key risks in this room and how to mitigate them.',
		'Draft an action plan for the next 3 days based on current tasks.',
		'What blockers should we resolve first to keep momentum?'
	];

	export let open = false;
	export let isDarkMode = false;
	export let currentUserId = '';
	export let currentUsername = 'You';
	export let roomId = '';

	const dispatch = createEventDispatcher<{
		close: void;
	}>();

	let messages: ChatMessage[] = [];
	let draft = '';
	let isSending = false;
	let errorText = '';
	let viewportEl: HTMLDivElement | null = null;
	let composerTextarea: HTMLTextAreaElement | null = null;
	let requestAbortController: AbortController | null = null;

	$: canSend = !isSending && draft.trim().length > 0;

	$: if (open) {
		void scrollToBottom();
	}

	onDestroy(() => {
		requestAbortController?.abort();
		requestAbortController = null;
	});

	function createMessageId(prefix = 'private_ai') {
		const random = Math.random().toString(36).slice(2, 8);
		return `${prefix}_${Date.now()}_${random}`;
	}

	function resolveStoredDeviceId() {
		if (typeof window === 'undefined') {
			return `web-${createMessageId('device')}`;
		}
		const existing = (window.localStorage.getItem(DEVICE_ID_STORAGE_KEY) || '').trim();
		if (existing) {
			return existing;
		}
		const generated =
			typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
				? crypto.randomUUID()
				: `web-${createMessageId('device')}`;
		window.localStorage.setItem(DEVICE_ID_STORAGE_KEY, generated);
		return generated;
	}

	function pushLocalMessage(message: ChatMessage) {
		messages = [...messages, message];
		void scrollToBottom();
	}

	async function scrollToBottom() {
		await tick();
		if (!viewportEl) {
			return;
		}
		viewportEl.scrollTop = viewportEl.scrollHeight;
	}

	async function requestAIReply(prompt: string, deviceId: string) {
		const normalizedUserID = (currentUserId || '').trim();
		const effectiveUserID =
			normalizedUserID && normalizedUserID.toLowerCase() !== 'guest'
				? normalizedUserID
				: `anon:${deviceId}`;
		const headers: Record<string, string> = {
			'Content-Type': 'application/json',
			'X-User-Id': effectiveUserID,
			'X-Username': currentUsername || ''
		};
		const body = JSON.stringify({ prompt, deviceId, roomId });

		let response = await fetch(`${API_BASE}/api/ai/chat`, {
			method: 'POST',
			headers,
			body,
			signal: requestAbortController?.signal
		});
		// Backward compatible fallback while backend route migration completes.
		if (response.status === 404) {
			response = await fetch(`${API_BASE}/api/ai/private-chat`, {
				method: 'POST',
				headers,
				body,
				signal: requestAbortController?.signal
			});
		}

		const payload = (await response.json().catch(() => ({}))) as Record<string, unknown>;
		if (!response.ok) {
			const details = typeof payload.error === 'string' ? payload.error : '';
			throw new Error(details || `AI request failed (${response.status})`);
		}

		const text =
			typeof payload.response === 'string'
				? payload.response.trim()
				: typeof payload.message === 'string'
					? payload.message.trim()
					: '';
		if (!text) {
			throw new Error('AI response was empty');
		}
		return text;
	}

	async function sendPrompt() {
		const prompt = draft.trim();
		if (!prompt || isSending) {
			return;
		}

		errorText = '';
		isSending = true;
		draft = '';

		const localUserId = (currentUserId || '').trim() || 'user';
		const localUsername = (currentUsername || '').trim() || 'You';
		const deviceId = resolveStoredDeviceId();
		const now = Date.now();

		pushLocalMessage({
			id: createMessageId('user'),
			roomId: PRIVATE_AI_ROOM_ID,
			senderId: localUserId,
			senderName: localUsername,
			content: prompt,
			type: 'text',
			createdAt: now
		});

		const pendingId = createMessageId('ai_pending');
		pushLocalMessage({
			id: pendingId,
			roomId: PRIVATE_AI_ROOM_ID,
			senderId: 'Tora-Bot',
			senderName: 'Tora-Bot',
			content: '...',
			type: 'text',
			createdAt: Date.now(),
			pending: true
		});

		requestAbortController?.abort();
		requestAbortController = new AbortController();

		try {
			const responseText = await requestAIReply(prompt, deviceId);
			messages = messages.map((message) => {
				if (message.id !== pendingId) {
					return message;
				}
				return {
					...message,
					content: responseText,
					pending: false
				};
			});
		} catch (error) {
			const message = error instanceof Error ? error.message : 'Failed to fetch AI response';
			errorText = message;
			messages = messages.map((entry) => {
				if (entry.id !== pendingId) {
					return entry;
				}
				return {
					...entry,
					content: `Error: ${message}`,
					pending: false
				};
			});
		} finally {
			isSending = false;
			void scrollToBottom();
		}
	}

	function onInputKeyDown(event: KeyboardEvent) {
		if (event.key === 'Enter' && !event.shiftKey) {
			event.preventDefault();
			void sendPrompt();
		}
	}

	function onOverlayClick(event: MouseEvent) {
		if (event.target === event.currentTarget) {
			dispatch('close');
		}
	}

	function applyPrivateSuggestion(prompt: string) {
		draft = prompt;
		errorText = '';
		void tick().then(() => composerTextarea?.focus());
	}

	function formatMessageTime(timestamp: number) {
		return new Date(timestamp).toLocaleTimeString([], {
			hour: '2-digit',
			minute: '2-digit'
		});
	}
</script>

{#if open}
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<div
		class="private-ai-overlay {isDarkMode ? 'theme-dark' : ''}"
		role="presentation"
		on:click={onOverlayClick}
	>
		<div class="private-ai-drawer" role="dialog" aria-modal="true" aria-label="Private AI Assistant">
			<header class="private-ai-header">
				<div class="private-ai-title-wrap">
					<h2>Private Tora AI</h2>
					<p>Only visible to you</p>
				</div>
				<div class="private-ai-header-actions">
					<span class="model-chip">
						<span class="model-dot" aria-hidden="true"></span>
						ToraAI
					</span>
					<button type="button" class="close-btn" on:click={() => dispatch('close')} aria-label="Close">
						<svg viewBox="0 0 12 12" aria-hidden="true">
							<path d="M2 2l8 8M10 2 2 10"></path>
						</svg>
					</button>
				</div>
			</header>

			<div class="private-ai-messages" bind:this={viewportEl}>
				{#if messages.length === 0}
					<div class="empty-state-v2">
						<div class="es-icon" aria-hidden="true">✦</div>
						<h4>Start a private conversation</h4>
						<p>Ask Tora about code, tasks, blockers, or planning for this room.</p>
					</div>
				{:else}
					{#each messages as message (message.id)}
						{@const isMine = message.senderId === currentUserId}
						{#if isMine}
							<div class="user-bubble-row">
								<article class="user-bubble">{message.content}</article>
							</div>
						{:else if message.pending}
							<div class="ai-loading-row">
								<span class="ai-spinner" aria-hidden="true"></span>
								Tora is thinking...
							</div>
						{:else}
							<article class="ai-response-block">
								<div class="ai-response-meta">
									<span class="ai-response-dot" aria-hidden="true"></span>
									<div class="ai-response-title">
										Tora AI
										<span class="ai-time-chip">{formatMessageTime(message.createdAt)}</span>
									</div>
								</div>
								<div class="ai-response-body">{message.content}</div>
							</article>
						{/if}
					{/each}
				{/if}
			</div>

			<div class="suggestions-panel">
				{#each PRIVATE_AI_SUGGESTIONS as suggestion}
					<button
						type="button"
						class="suggestion-item"
						on:click={() => applyPrivateSuggestion(suggestion)}
						disabled={isSending}
					>
						<span class="suggestion-arrow" aria-hidden="true">→</span>
						<span class="suggestion-text">{suggestion}</span>
					</button>
				{/each}
			</div>

			{#if errorText}
				<div class="private-ai-error">{errorText}</div>
			{/if}

			<footer class="private-ai-input-area">
				<div class="private-ai-input-box">
					<textarea
						rows="1"
						bind:this={composerTextarea}
						bind:value={draft}
						class="private-ai-textarea"
						placeholder="Ask Tora anything..."
						on:keydown={onInputKeyDown}
						disabled={isSending}
					></textarea>
					<div class="private-ai-toolbar">
						<span class="private-ai-hint">Enter to send</span>
						<div class="toolbar-spacer"></div>
						<button
							type="button"
							class="send-btn"
							on:click={() => void sendPrompt()}
							disabled={!canSend}
							aria-label="Send prompt"
						>
							<svg viewBox="0 0 14 14" aria-hidden="true">
								<path d="M2 7h10M8 3l4 4-4 4"></path>
							</svg>
						</button>
					</div>
				</div>
			</footer>
		</div>
	</div>
{/if}

<style>
	.private-ai-overlay {
		position: fixed;
		inset: 0;
		z-index: 1080;
		background: rgba(0, 0, 0, 0.56);
		backdrop-filter: blur(4px);
		-webkit-backdrop-filter: blur(4px);
		display: flex;
		justify-content: flex-end;
		animation: private-ai-fade-in 0.18s ease;
	}

	.private-ai-drawer {
		width: min(400px, 100%);
		height: 100%;
		background: #1e1f24;
		border-left: 1px solid rgba(255, 255, 255, 0.08);
		display: grid;
		grid-template-rows: auto minmax(0, 1fr) auto auto;
		animation: private-ai-slide-in 0.22s cubic-bezier(0.22, 1, 0.36, 1);
		box-shadow: -8px 0 40px rgba(0, 0, 0, 0.5);
	}

	.private-ai-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		padding: 0.75rem 0.85rem;
		border-bottom: 1px solid rgba(255, 255, 255, 0.07);
		background: rgba(255, 255, 255, 0.02);
	}

	.private-ai-title-wrap {
		display: grid;
		gap: 0.1rem;
		min-width: 0;
	}

	.private-ai-title-wrap h2 {
		margin: 0;
		font-size: 0.88rem;
		font-weight: 600;
		color: #e8eaed;
	}

	.private-ai-title-wrap p {
		margin: 0;
		font-size: 0.72rem;
		color: #9aa0a6;
	}

	.private-ai-header-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
	}

	.model-chip {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		background: rgba(255, 255, 255, 0.05);
		border: 1px solid rgba(255, 255, 255, 0.12);
		border-radius: 8px;
		padding: 0.22rem 0.48rem;
		font-size: 0.68rem;
		font-weight: 600;
		color: #bdc1c6;
	}

	.model-dot {
		width: 10px;
		height: 10px;
		border-radius: 999px;
		background: linear-gradient(135deg, #1a73e8, #34a853);
	}

	.close-btn {
		width: 32px;
		height: 32px;
		border-radius: 8px;
		border: none;
		background: transparent;
		color: #9aa0a6;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		transition:
			background 0.15s ease,
			color 0.15s ease;
	}

	.close-btn svg {
		width: 12px;
		height: 12px;
		stroke: currentColor;
		stroke-width: 1.5;
		fill: none;
		stroke-linecap: round;
	}

	.close-btn:hover {
		background: rgba(255, 100, 100, 0.12);
		color: #ff6b6b;
	}

	.private-ai-messages {
		padding: 0.92rem;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: 0.9rem;
	}

	.private-ai-messages::-webkit-scrollbar {
		width: 4px;
	}

	.private-ai-messages::-webkit-scrollbar-thumb {
		background: rgba(255, 255, 255, 0.12);
		border-radius: 4px;
	}

	.empty-state-v2 {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		text-align: center;
		padding: 1.6rem 1rem;
		gap: 0.52rem;
	}

	.empty-state-v2 .es-icon {
		width: 44px;
		height: 44px;
		border-radius: 999px;
		border: 1px solid rgba(26, 115, 232, 0.24);
		background: rgba(26, 115, 232, 0.15);
		color: #8ab4f8;
		display: grid;
		place-items: center;
		font-size: 1rem;
	}

	.empty-state-v2 h4 {
		margin: 0;
		font-size: 0.88rem;
		color: #e8eaed;
	}

	.empty-state-v2 p {
		margin: 0;
		font-size: 0.78rem;
		color: #9aa0a6;
		line-height: 1.55;
		max-width: 280px;
	}

	.ai-loading-row {
		display: inline-flex;
		align-items: center;
		gap: 0.55rem;
		font-size: 0.81rem;
		color: #9aa0a6;
		font-style: italic;
	}

	.ai-spinner {
		width: 14px;
		height: 14px;
		border: 2px solid rgba(255, 255, 255, 0.12);
		border-top-color: #1a73e8;
		border-radius: 999px;
		animation: private-ai-spin 0.8s linear infinite;
	}

	.ai-response-block {
		display: grid;
		gap: 0.26rem;
	}

	.ai-response-meta {
		display: inline-flex;
		align-items: center;
		gap: 0.45rem;
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
		gap: 0.45rem;
		font-size: 0.82rem;
		font-weight: 600;
		color: #e8eaed;
	}

	.ai-time-chip {
		border-radius: 999px;
		padding: 0.1rem 0.44rem;
		font-size: 0.64rem;
		font-weight: 500;
		border: 1px solid rgba(255, 255, 255, 0.1);
		background: rgba(255, 255, 255, 0.06);
		color: #9aa0a6;
	}

	.ai-response-body {
		margin-left: 0.72rem;
		padding-left: 0.72rem;
		border-left: 2px solid rgba(255, 255, 255, 0.1);
		font-size: 0.82rem;
		color: #bdc1c6;
		line-height: 1.65;
		white-space: pre-wrap;
		word-break: break-word;
	}

	.user-bubble-row {
		display: flex;
		justify-content: flex-end;
	}

	.user-bubble {
		max-width: 85%;
		background: rgba(26, 115, 232, 0.15);
		border: 1px solid rgba(26, 115, 232, 0.25);
		border-radius: 18px 18px 4px 18px;
		padding: 0.56rem 0.72rem;
		font-size: 0.82rem;
		color: #e8eaed;
		line-height: 1.5;
		white-space: pre-wrap;
		word-break: break-word;
	}

	.suggestions-panel {
		display: grid;
		border-top: 1px solid rgba(255, 255, 255, 0.07);
		background: rgba(255, 255, 255, 0.03);
		padding: 0.34rem 0;
	}

	.suggestion-item {
		border: none;
		background: transparent;
		display: inline-flex;
		align-items: center;
		gap: 0.52rem;
		text-align: left;
		padding: 0.48rem 0.82rem;
		cursor: pointer;
	}

	.suggestion-item:hover:not(:disabled) {
		background: rgba(26, 115, 232, 0.12);
	}

	.suggestion-item:disabled {
		opacity: 0.62;
		cursor: not-allowed;
	}

	.suggestion-arrow {
		font-size: 0.82rem;
		color: #9aa0a6;
	}

	.suggestion-text {
		font-size: 0.76rem;
		color: #bdc1c6;
		line-height: 1.3;
	}

	.private-ai-error {
		margin: 0 0.82rem 0.58rem;
		border-radius: 10px;
		padding: 0.48rem 0.62rem;
		font-size: 0.76rem;
		color: #ffd7d7;
		border: 1px solid rgba(227, 134, 134, 0.52);
		background: rgba(132, 33, 33, 0.44);
	}

	.private-ai-input-area {
		border-top: 1px solid rgba(255, 255, 255, 0.07);
		background: rgba(255, 255, 255, 0.02);
		padding: 0.65rem 0.72rem 0.82rem;
	}

	.private-ai-input-box {
		border-radius: 14px;
		border: 1px solid rgba(255, 255, 255, 0.08);
		background: rgba(255, 255, 255, 0.04);
		padding: 0.58rem 0.62rem;
		display: grid;
		gap: 0.45rem;
		transition:
			border-color 0.18s ease,
			background 0.18s ease;
	}

	.private-ai-input-box:focus-within {
		border-color: rgba(26, 115, 232, 0.5);
		background: rgba(26, 115, 232, 0.04);
	}

	.private-ai-textarea {
		resize: none;
		min-height: 20px;
		max-height: 120px;
		border: none;
		outline: none;
		background: transparent;
		color: #e8eaed;
		padding: 0;
		font: inherit;
		font-size: 0.86rem;
		line-height: 1.48;
	}

	.private-ai-textarea::placeholder {
		color: #5f6368;
	}

	.private-ai-toolbar {
		display: inline-flex;
		align-items: center;
		gap: 0.42rem;
	}

	.private-ai-hint {
		font-size: 0.68rem;
		color: #9aa0a6;
	}

	.toolbar-spacer {
		flex: 1;
	}

	.send-btn {
		width: 32px;
		height: 32px;
		border-radius: 999px;
		border: none;
		background: #1a73e8;
		color: #fff;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: pointer;
		box-shadow: 0 2px 10px rgba(26, 115, 232, 0.4);
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
		box-shadow: 0 4px 16px rgba(26, 115, 232, 0.5);
	}

	.send-btn:disabled {
		background: rgba(255, 255, 255, 0.08);
		cursor: not-allowed;
		box-shadow: none;
		transform: none;
	}

	@keyframes private-ai-fade-in {
		from {
			opacity: 0;
		}
		to {
			opacity: 1;
		}
	}

	@keyframes private-ai-slide-in {
		from {
			transform: translateX(100%);
		}
		to {
			transform: translateX(0);
		}
	}

	@keyframes private-ai-spin {
		to {
			transform: rotate(360deg);
		}
	}
</style>
