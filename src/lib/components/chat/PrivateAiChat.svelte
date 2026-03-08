<script lang="ts">
	import { createEventDispatcher, onDestroy, tick } from 'svelte';
	import type { ChatMessage } from '$lib/types/chat';

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://localhost:8080';
	const PRIVATE_AI_ROOM_ID = 'private-ai';
	const DEVICE_ID_STORAGE_KEY = 'privateAiDeviceId';

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
		const headers: Record<string, string> = {
			'Content-Type': 'application/json',
			'X-User-Id': currentUserId || '',
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
				<h2>Private AI Assistant</h2>
				<button type="button" class="close-btn" on:click={() => dispatch('close')} aria-label="Close">
					×
				</button>
			</header>

			<div class="private-ai-messages" bind:this={viewportEl}>
				{#if messages.length === 0}
					<div class="empty-state">Start a private conversation with Tora.</div>
				{:else}
					{#each messages as message (message.id)}
						{@const isMine = message.senderId === currentUserId}
						<article class="bubble-row {isMine ? 'mine' : 'other'}">
							<div class="bubble">
								<div class="bubble-author">{isMine ? 'You' : 'Tora'}</div>
								<div class="bubble-content">{message.content}</div>
							</div>
						</article>
					{/each}
				{/if}
			</div>

			{#if errorText}
				<div class="private-ai-error">{errorText}</div>
			{/if}

			<footer class="private-ai-input-row">
				<textarea
					rows="1"
					bind:value={draft}
					placeholder="Ask something privately..."
					on:keydown={onInputKeyDown}
					disabled={isSending}
				></textarea>
				<button type="button" on:click={() => void sendPrompt()} disabled={!canSend}>
					{isSending ? '...' : 'Send'}
				</button>
			</footer>
		</div>
	</div>
{/if}

<style>
	.private-ai-overlay {
		position: fixed;
		inset: 0;
		z-index: 1080;
		background: rgba(15, 23, 42, 0.28);
		display: flex;
		justify-content: flex-end;
	}

	.private-ai-overlay.theme-dark {
		background: rgba(2, 6, 14, 0.58);
	}

	.private-ai-drawer {
		width: min(420px, 100%);
		height: 100%;
		background: #f9fbff;
		border-left: 1px solid #d7deea;
		display: grid;
		grid-template-rows: auto minmax(0, 1fr) auto auto;
		animation: ai-drawer-slide-in 170ms ease-out;
	}

	.theme-dark .private-ai-drawer {
		background: #171c24;
		border-left-color: #2f3742;
	}

	.private-ai-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 0.75rem 0.85rem;
		border-bottom: 1px solid #d7deea;
	}

	.theme-dark .private-ai-header {
		border-bottom-color: #2f3742;
	}

	.private-ai-header h2 {
		margin: 0;
		font-size: 0.98rem;
		font-weight: 700;
		color: #111827;
	}

	.theme-dark .private-ai-header h2 {
		color: #f3f4f6;
	}

	.close-btn {
		width: 1.9rem;
		height: 1.9rem;
		border: 1px solid #c9d1de;
		border-radius: 9px;
		background: #f4f6fb;
		color: #334155;
		cursor: pointer;
		font-size: 1.2rem;
		line-height: 1;
	}

	.theme-dark .close-btn {
		background: #242b36;
		color: #e5e7eb;
		border-color: #3b4653;
	}

	.private-ai-messages {
		padding: 0.8rem;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: 0.56rem;
	}

	.empty-state {
		padding: 0.7rem;
		border-radius: 10px;
		background: #eef2f8;
		color: #475569;
		font-size: 0.84rem;
	}

	.theme-dark .empty-state {
		background: #252d39;
		color: #cbd5e1;
	}

	.bubble-row {
		display: flex;
	}

	.bubble-row.mine {
		justify-content: flex-end;
	}

	.bubble-row.other {
		justify-content: flex-start;
	}

	.bubble {
		max-width: 84%;
		padding: 0.5rem 0.6rem;
		border-radius: 12px;
		background: #e8eef8;
		color: #1f2937;
	}

	.bubble-row.mine .bubble {
		background: #dce9ff;
	}

	.theme-dark .bubble {
		background: #252d39;
		color: #e5e7eb;
	}

	.theme-dark .bubble-row.mine .bubble {
		background: #324054;
	}

	.bubble-author {
		font-size: 0.68rem;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		opacity: 0.74;
		margin-bottom: 0.16rem;
	}

	.bubble-content {
		white-space: pre-wrap;
		word-break: break-word;
		line-height: 1.4;
		font-size: 0.9rem;
	}

	.private-ai-error {
		padding: 0.42rem 0.78rem;
		font-size: 0.76rem;
		color: #b91c1c;
		border-top: 1px solid #f0d4d4;
		background: #fff5f5;
	}

	.theme-dark .private-ai-error {
		color: #fecaca;
		border-top-color: #5f2a2a;
		background: #352024;
	}

	.private-ai-input-row {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: 0.45rem;
		padding: 0.72rem 0.78rem;
		border-top: 1px solid #d7deea;
		background: #f5f8fd;
	}

	.theme-dark .private-ai-input-row {
		border-top-color: #2f3742;
		background: #1a2028;
	}

	.private-ai-input-row textarea {
		resize: none;
		min-height: 2.2rem;
		max-height: 6.5rem;
		border-radius: 10px;
		border: 1px solid #c7cfdd;
		background: #ffffff;
		color: #111827;
		padding: 0.48rem 0.54rem;
		font: inherit;
		font-size: 0.9rem;
		line-height: 1.35;
	}

	.theme-dark .private-ai-input-row textarea {
		border-color: #3a4553;
		background: #202734;
		color: #e5e7eb;
	}

	.private-ai-input-row button {
		border-radius: 10px;
		border: 1px solid #1f4fa0;
		background: #2a65c8;
		color: #ffffff;
		padding: 0.4rem 0.72rem;
		font-size: 0.82rem;
		font-weight: 600;
		cursor: pointer;
	}

	.private-ai-input-row button:disabled {
		cursor: not-allowed;
		opacity: 0.65;
	}

	.theme-dark .private-ai-input-row button {
		border-color: #4b5c76;
		background: #394a63;
	}

	@keyframes ai-drawer-slide-in {
		from {
			transform: translateX(20px);
			opacity: 0;
		}
		to {
			transform: translateX(0);
			opacity: 1;
		}
	}
</style>
