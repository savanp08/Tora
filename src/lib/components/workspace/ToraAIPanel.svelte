<script lang="ts">
	import { tick } from 'svelte';
	import RichTextContent from '$lib/components/chat/RichTextContent.svelte';
	import {
		type AITimelineConversationMessage,
		type AITimelineIntent,
		type StreamAIStatusMeta,
		editAITimeline,
		isTimelineRequestStoppedError,
		projectTimeline,
		stopActiveTimelineRequest,
		timelineLoading
	} from '$lib/stores/timeline';
	import { initializeTaskStoreForRoom } from '$lib/stores/tasks';
	import type { OnlineMember } from '$lib/types/chat';
	import { normalizeRoomIDValue } from '$lib/utils/chat/core';

	export let roomId = '';
	export let contextKey = 'taskboard';
	export let onlineMembers: OnlineMember[] = [];

	type ToraMessage = {
		id: string;
		role: 'user' | 'assistant';
		text: string;
		timestamp: number;
		intent?: AITimelineIntent;
	};

	type PersistedToraConversation = {
		version: 1;
		messages: ToraMessage[];
	};
	type LiveWorkflowEntry = {
		id: string;
		title: string;
		detail?: string;
		progress?: string;
		tone: 'status' | 'success' | 'warning';
		stepKey?: string;
		timing?: {
			startedAt: number;
			endedAt?: number;
			stepBudgetMs?: number;
			promptBudgetMs?: number;
			strategy?: string;
		};
	};

	let draft = '';
	let messages: ToraMessage[] = [];
	let submitError = '';
	let liveStatus = '';
	let liveAssistantPreview = '';
	let liveAppliedCount = 0;
	let liveOperationTotal = 0;
	let liveWorkflowEntries: LiveWorkflowEntry[] = [];
	let lastWorkflowStatusKey = '';
	let activeWorkflowEntryId = '';
	let activeWorkflowStepKey = '';
	let liveWorkflowRunStartedAt = 0;
	let liveWorkflowRunFinishedAt = 0;
	let liveWorkflowPromptBudgetMs = 15 * 60 * 1000;
	let liveWorkflowClockNow = Date.now();
	let liveWorkflowTimer: number | null = null;
	let threadElement: HTMLDivElement | null = null;
	let composerTextarea: HTMLTextAreaElement | null = null;

	function autoResize(node: HTMLTextAreaElement, _value?: string) {
		function resize() {
			node.style.height = 'auto';
			node.style.height = node.scrollHeight + 'px';
		}
		node.addEventListener('input', resize);
		resize();
		return {
			update() { resize(); },
			destroy() { node.removeEventListener('input', resize); }
		};
	}
	let loadedConversationKey = '';

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';
	const TORA_CHAT_STORAGE_PREFIX = 'tora_ai_chat';
	const TORA_CHAT_HISTORY_LIMIT = 80;
	const TORA_AI_CONTEXT_USER_LIMIT = 40;
	const TORA_PROMPT_SUGGESTIONS = [
		'Summarize the current sprint: what is done, in progress, and blocked?',
		'Based on the task board, what tasks or areas are at risk of delay?',
		'Generate a concise weekly status report for this project. Include: completed work, in-progress items, blockers, and any budget notes.',
		'Looking at task assignments and time logs, which team members are taking on the most work? Any imbalance?',
		'Given the current state of the project, what are the most important next 3 things the team should focus on?'
	];

	$: normalizedRoomID = normalizeRoomIDValue(roomId);
	$: currentState = $projectTimeline;
	$: sprints = currentState?.sprints ?? [];
	$: totalTasks = sprints.flatMap((sprint) => sprint.tasks).length;
	$: isLargeProject = totalTasks > 60;
	$: normalizedContextKey = normalizeRoomIDValue(contextKey) || 'taskboard';
	$: conversationStorageKey = `${TORA_CHAT_STORAGE_PREFIX}:${normalizedRoomID}:${normalizedContextKey}`;
	$: activeUsers = onlineMembers
		.filter((member) => member?.isOnline)
		.map((member) => {
			const memberID = (member.id || '').trim();
			if (!memberID) {
				return '';
			}
			const memberName = (member.name || '').trim() || 'Unknown';
			return `${memberName} (id: ${memberID})`;
		})
		.filter(Boolean)
		.slice(0, TORA_AI_CONTEXT_USER_LIMIT)
		.join(', ');
	$: boardStatusText = currentState
		? `${sprints.length} sprints \u00B7 ${totalTasks} tasks`
		: 'No project loaded';
	$: liveProgressLabel =
		liveOperationTotal > 0 ? `${Math.min(liveAppliedCount, liveOperationTotal)}/${liveOperationTotal} changes applied` : '';
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
		const sanitized: ToraMessage[] = [];
		for (const entry of candidate) {
			if (!entry || typeof entry !== 'object') {
				continue;
			}
			const source = entry as Partial<ToraMessage>;
			const role = source.role === 'assistant' ? 'assistant' : 'user';
			const text = typeof source.text === 'string' ? source.text.trim() : '';
			if (!text) {
				continue;
			}
			const timestamp =
				typeof source.timestamp === 'number' && Number.isFinite(source.timestamp)
					? source.timestamp
					: Date.now();
			const rawIntent = typeof source.intent === 'string' ? source.intent.trim().toLowerCase() : '';
			const intent =
				rawIntent === 'chat' ||
				rawIntent === 'modify_project' ||
				rawIntent === 'generate_project' ||
				rawIntent === 'clarify'
					? (rawIntent as AITimelineIntent)
					: undefined;
			const message: ToraMessage = {
				id: createMessageID(),
				role,
				text,
				timestamp
			};
			if (intent) {
				message.intent = intent;
			}
			sanitized.push(message);
		}
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

	function clearConversationForKey(storageKey: string) {
		if (!isBrowser() || !storageKey) {
			messages = [];
			submitError = '';
			draft = '';
			return;
		}
		messages = [];
		submitError = '';
		draft = '';
		try {
			window.localStorage.removeItem(storageKey);
		} catch {
			// Best-effort clearing only.
		}
		scrollThreadToBottom();
	}

	function appendMessage(role: ToraMessage['role'], text: string, intent?: AITimelineIntent) {
		const normalizedText = String(text || '').trim();
		if (!normalizedText) {
			return;
		}
		messages = [
			...messages,
			{ id: createMessageID(), role, text: normalizedText, timestamp: Date.now(), intent }
		].slice(-TORA_CHAT_HISTORY_LIMIT);
		persistConversationForKey(conversationStorageKey);
		scrollThreadToBottom();
	}

	function buildConversationPayload(source: ToraMessage[]): AITimelineConversationMessage[] {
		return source.map((message) => ({
			role: message.role,
			text: message.text,
			intent: message.intent
		}));
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

	function buildAgenticEditPrompt(userPrompt: string) {
		const validAssigneeContext =
			activeUsers.trim() || 'none (no online members were detected for this room)';
		return `${userPrompt}\n\n[SYSTEM CONTEXT: Valid Assignee IDs for tasks: ${validAssigneeContext}. Use only these IDs for assigneeId/assignee_id updates. If none are listed, do not modify task assignees.]`;
	}

	function formatWorkflowDuration(ms: number) {
		if (!Number.isFinite(ms) || ms <= 0) {
			return '00:00';
		}
		const totalSeconds = Math.max(0, Math.floor(ms / 1000));
		const hours = Math.floor(totalSeconds / 3600);
		const minutes = Math.floor((totalSeconds % 3600) / 60);
		const seconds = totalSeconds % 60;
		if (hours > 0) {
			return `${hours}:${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`;
		}
		return `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`;
	}

	function startLiveWorkflowClock(startedAt = Date.now()) {
		stopLiveWorkflowClock(startedAt);
		liveWorkflowClockNow = startedAt;
		if (typeof window === 'undefined') {
			return;
		}
		liveWorkflowTimer = window.setInterval(() => {
			liveWorkflowClockNow = Date.now();
		}, 1000);
	}

	function stopLiveWorkflowClock(finishedAt = Date.now()) {
		liveWorkflowClockNow = finishedAt;
		if (liveWorkflowTimer && typeof window !== 'undefined') {
			window.clearInterval(liveWorkflowTimer);
		}
		liveWorkflowTimer = null;
	}

	function getLiveWorkflowReferenceNow() {
		if ($timelineLoading && liveWorkflowRunStartedAt > 0) {
			return liveWorkflowClockNow || Date.now();
		}
		return liveWorkflowRunFinishedAt || liveWorkflowClockNow || Date.now();
	}

	function getLiveWorkflowEntry(entryId: string) {
		return liveWorkflowEntries.find((entry) => entry.id === entryId) ?? null;
	}

	function getLiveWorkflowEntryElapsedMs(entry: LiveWorkflowEntry) {
		if (!entry.timing) {
			return 0;
		}
		const end = entry.timing.endedAt ?? getLiveWorkflowReferenceNow();
		return Math.max(0, end - entry.timing.startedAt);
	}

	function getLiveWorkflowEntryTimingChips(entry: LiveWorkflowEntry) {
		if (!entry.timing) {
			return [] as string[];
		}
		const chips: string[] = [];
		const elapsedMs = getLiveWorkflowEntryElapsedMs(entry);
		if (elapsedMs > 0) {
			chips.push(`Step ${formatWorkflowDuration(elapsedMs)}`);
		}
		if (entry.timing.stepBudgetMs && entry.timing.stepBudgetMs > 0) {
			chips.push(`Budget ${formatWorkflowDuration(entry.timing.stepBudgetMs)}`);
		}
		if (entry.timing.strategy) {
			chips.push(entry.timing.strategy.replace(/_/g, ' '));
		}
		return chips;
	}

	function getLiveWorkflowTotalElapsedMs() {
		if (!liveWorkflowRunStartedAt) {
			return 0;
		}
		return Math.max(0, getLiveWorkflowReferenceNow() - liveWorkflowRunStartedAt);
	}

	function getLiveWorkflowCurrentStepElapsedMs() {
		const entry = activeWorkflowEntryId ? getLiveWorkflowEntry(activeWorkflowEntryId) : null;
		return entry ? getLiveWorkflowEntryElapsedMs(entry) : 0;
	}

	function getLiveWorkflowSummary() {
		if (!liveWorkflowRunStartedAt) {
			return '';
		}
		const parts = [`Elapsed ${formatWorkflowDuration(getLiveWorkflowTotalElapsedMs())}`];
		const currentStepElapsedMs = getLiveWorkflowCurrentStepElapsedMs();
		if (currentStepElapsedMs > 0) {
			parts.push(`Current step ${formatWorkflowDuration(currentStepElapsedMs)}`);
		}
		if (liveWorkflowPromptBudgetMs > 0) {
			parts.push(`Prompt budget ${formatWorkflowDuration(liveWorkflowPromptBudgetMs)}`);
		}
		return parts.join(' • ');
	}

	function startLiveWorkflowRun(promptBudgetMs = 15 * 60 * 1000) {
		const startedAt = Date.now();
		liveWorkflowRunStartedAt = startedAt;
		liveWorkflowRunFinishedAt = 0;
		liveWorkflowPromptBudgetMs = promptBudgetMs;
		startLiveWorkflowClock(startedAt);
	}

	function finalizeActiveWorkflowEntry(finishedAt = Date.now()) {
		if (!activeWorkflowEntryId) {
			return;
		}
		liveWorkflowEntries = liveWorkflowEntries.map((entry) =>
			entry.id === activeWorkflowEntryId && entry.timing
				? {
						...entry,
						timing: {
							...entry.timing,
							endedAt: entry.timing.endedAt ?? finishedAt
						}
					}
				: entry
		);
		activeWorkflowEntryId = '';
		activeWorkflowStepKey = '';
	}

	function finishLiveWorkflowRun(finishedAt = Date.now()) {
		finalizeActiveWorkflowEntry(finishedAt);
		liveWorkflowRunFinishedAt = finishedAt;
		stopLiveWorkflowClock(finishedAt);
	}

	function resetLiveState() {
		stopLiveWorkflowClock();
		liveStatus = '';
		liveAssistantPreview = '';
		liveAppliedCount = 0;
		liveOperationTotal = 0;
		liveWorkflowEntries = [];
		lastWorkflowStatusKey = '';
		activeWorkflowEntryId = '';
		activeWorkflowStepKey = '';
		liveWorkflowRunStartedAt = 0;
		liveWorkflowRunFinishedAt = 0;
		liveWorkflowPromptBudgetMs = 15 * 60 * 1000;
	}

	function appendWorkflowEntry(entry: Omit<LiveWorkflowEntry, 'id'>) {
		const nextEntry = { id: createMessageID(), ...entry };
		liveWorkflowEntries = [...liveWorkflowEntries, nextEntry].slice(-20);
		scrollThreadToBottom();
		return nextEntry.id;
	}

	function addWorkflowStatus(
		stepKey: string,
		label: string,
		progress?: string,
		meta?: StreamAIStatusMeta
	) {
		const normalizedLabel = String(label || '').trim();
		if (!normalizedLabel) {
			return;
		}
		const normalizedStepKey = String(stepKey || normalizedLabel).trim() || normalizedLabel;
		if (meta?.promptTimeoutMs && meta.promptTimeoutMs > 0) {
			liveWorkflowPromptBudgetMs = meta.promptTimeoutMs;
		}
		const statusKey = `${normalizedStepKey}::${normalizedLabel}::${progress ?? ''}`;
		if (activeWorkflowEntryId && normalizedStepKey === activeWorkflowStepKey) {
			liveWorkflowEntries = liveWorkflowEntries.map((entry) =>
				entry.id === activeWorkflowEntryId
					? {
							...entry,
							title: normalizedLabel,
							progress,
							timing: entry.timing
								? {
										...entry.timing,
										stepBudgetMs: meta?.timeoutMs ?? entry.timing.stepBudgetMs,
										promptBudgetMs: meta?.promptTimeoutMs ?? entry.timing.promptBudgetMs,
										strategy: meta?.strategy ?? entry.timing.strategy
									}
								: entry.timing
						}
					: entry
			);
			lastWorkflowStatusKey = statusKey;
			return;
		}
		if (statusKey === lastWorkflowStatusKey) {
			return;
		}
		finalizeActiveWorkflowEntry();
		lastWorkflowStatusKey = statusKey;
		const entryId = appendWorkflowEntry({
			title: normalizedLabel,
			progress,
			tone: 'status',
			stepKey: normalizedStepKey,
			timing: {
				startedAt: Date.now(),
				stepBudgetMs: meta?.timeoutMs,
				promptBudgetMs: meta?.promptTimeoutMs,
				strategy: meta?.strategy
			}
		});
		activeWorkflowEntryId = entryId;
		activeWorkflowStepKey = normalizedStepKey;
	}

	function stopBoardAIRequest() {
		stopActiveTimelineRequest();
	}

	async function submitEditPrompt() {
		submitError = '';
		resetLiveState();
		const prompt = draft.trim();
		if (!prompt) {
			return;
		}
		if ([...prompt].length > 3000) {
			submitError = 'Prompt is too long (max 3,000 characters). Please shorten it.';
			return;
		}

		appendMessage('user', prompt);
		const conversationPayload = buildConversationPayload(messages);
		draft = '';
		appendWorkflowEntry({
			title: 'Starting board update',
			detail: 'Tora is reading the board and preparing the first operations.',
			tone: 'status'
		});

		if (!normalizedRoomID) {
			appendMessage('assistant', 'Room id is missing. AI edits cannot run right now.');
			return;
		}
		if (!currentState) {
			appendMessage('assistant', 'Initialize a project first so Tora has a board state to edit.');
			return;
		}

		startLiveWorkflowRun();
		try {
			const agenticPrompt = buildAgenticEditPrompt(prompt);
			const result = await editAITimeline(
				normalizedRoomID,
				agenticPrompt,
				currentState,
				conversationPayload,
				{
					onStatus: (step, label, appliedCount, operationTotal, meta) => {
						liveStatus = label || 'Applying board updates...';
						if (meta?.promptTimeoutMs && meta.promptTimeoutMs > 0) {
							liveWorkflowPromptBudgetMs = meta.promptTimeoutMs;
						}
						if (typeof appliedCount === 'number') {
							liveAppliedCount = appliedCount;
						}
						if (typeof operationTotal === 'number') {
							liveOperationTotal = operationTotal;
						}
						const progressLabel =
							typeof operationTotal === 'number' && operationTotal > 0
								? `${Math.min(appliedCount ?? 0, operationTotal)}/${operationTotal}`
								: undefined;
						addWorkflowStatus(
							step || liveStatus,
							liveStatus,
							progressLabel,
							meta
						);
					},
					onPlan: (assistantReply, operationTotal) => {
						liveAssistantPreview = assistantReply || 'Preparing board changes...';
						if (typeof operationTotal === 'number') {
							liveOperationTotal = operationTotal;
						}
						appendWorkflowEntry({
							title: 'Plan ready',
							detail: assistantReply || 'Board change plan prepared.',
							progress: operationTotal > 0 ? `${operationTotal} ops` : undefined,
							tone: 'success'
						});
					},
					onOperation: (summary, appliedCount, operationTotal) => {
						liveStatus = summary || 'Applied a board change.';
						liveAppliedCount = appliedCount;
						liveOperationTotal = operationTotal;
						appendWorkflowEntry({
							title: summary || 'Applied a board change.',
							detail:
								operationTotal > 0
									? `${Math.min(appliedCount, operationTotal)} of ${operationTotal} changes applied.`
									: 'Board state updated.',
							progress: operationTotal > 0 ? `${Math.min(appliedCount, operationTotal)}/${operationTotal}` : undefined,
							tone: 'success'
						});
					},
					onChat: (_intent, assistantReply) => {
						liveAssistantPreview = assistantReply || '';
					},
					onError: (message, meta) => {
						finishLiveWorkflowRun();
						if (meta?.isStopped) {
							appendWorkflowEntry({
								title: 'Board update stopped',
								detail: message,
								tone: 'warning'
							});
							return;
						}
						submitError = message;
						if (!liveStatus) {
							liveStatus = message;
						}
						appendWorkflowEntry({
							title: 'Board update interrupted',
							detail: message,
							tone: 'warning'
						});
					}
				}
			);
			finishLiveWorkflowRun();
			if (result.intent !== 'chat' && result.intent !== 'clarify') {
				await initializeTaskStoreForRoom(normalizedRoomID, { apiBase: API_BASE });
			}
			appendMessage('assistant', result.assistantReply || formatSuccessMessage(), result.intent);
		} catch (error) {
			finishLiveWorkflowRun();
			if (isTimelineRequestStoppedError(error)) {
				submitError = '';
				appendMessage(
					'assistant',
					error.timeline
						? 'Stopped. Any changes already applied are still on the board.'
						: 'Stopped before any board changes were applied.'
				);
				return;
			}
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
			<button
				type="button"
				class="tora-clear-btn"
				on:click={() => clearConversationForKey(conversationStorageKey)}
				disabled={$timelineLoading || messages.length === 0}
			>
				Clear chat
			</button>
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
								{#if message.intent === 'clarify'}
									<span class="ai-intent-chip">Needs detail</span>
								{:else if message.intent === 'chat'}
									<span class="ai-intent-chip">Discussion</span>
								{/if}
								<span class="ai-time-chip">{formatMessageTime(message.timestamp)}</span>
							</div>
						</div>
						<div class="ai-response-body">
							<RichTextContent text={message.text} />
						</div>
					</article>
				{/if}
			{/each}
		{/if}
		{#if ($timelineLoading || liveWorkflowEntries.length > 0) && currentState}
			<div class="ai-loading-panel">
				{#if $timelineLoading}
					<div class="ai-loading-row">
						<span class="ai-spinner" aria-hidden="true"></span>
						<div class="ai-loading-copy">
							<strong>{liveStatus || 'Applying board updates...'}</strong>
							{#if liveAssistantPreview}
								<p>{liveAssistantPreview}</p>
							{/if}
							{#if getLiveWorkflowSummary()}
								<span class="ai-loading-progress">{getLiveWorkflowSummary()}</span>
							{/if}
							{#if liveProgressLabel}
								<span class="ai-loading-progress">{liveProgressLabel}</span>
							{/if}
						</div>
					</div>
				{:else}
					<div class="ai-loading-copy">
						<strong>Latest board AI workflow</strong>
						<p>The last run has finished. Its step history stays visible here for debugging and review.</p>
						{#if getLiveWorkflowSummary()}
							<span class="ai-loading-progress">{getLiveWorkflowSummary()}</span>
						{/if}
					</div>
				{/if}
				{#if liveWorkflowEntries.length > 0}
					<div class="ai-workflow-list" role="status" aria-live="polite">
						{#each liveWorkflowEntries as entry (entry.id)}
							<section class={`ai-workflow-entry tone-${entry.tone}`}>
								<div class="ai-workflow-entry-head">
									<strong>{entry.title}</strong>
									{#if entry.progress}
										<span class="ai-workflow-progress">{entry.progress}</span>
									{/if}
								</div>
								{#if entry.detail}
									<p>{entry.detail}</p>
								{/if}
								{#if getLiveWorkflowEntryTimingChips(entry).length > 0}
									<div class="ai-workflow-tags">
										{#each getLiveWorkflowEntryTimingChips(entry) as chip}
											<span>{chip}</span>
										{/each}
									</div>
								{/if}
							</section>
						{/each}
					</div>
				{/if}
			</div>
		{/if}
	</div>

	{#if messages.length === 0}
		<div class="suggestions-panel">
			{#each TORA_PROMPT_SUGGESTIONS.slice(0, 3) as suggestion}
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
	{/if}

	{#if submitError}
		<div class="tora-error" role="status" aria-live="polite">{submitError}</div>
	{/if}

	<form class="tora-composer" on:submit|preventDefault={() => void submitEditPrompt()}>
		<div class="tora-input-box">
			<textarea
				class="tora-textarea"
				bind:this={composerTextarea}
				bind:value={draft}
				use:autoResize={draft}
				rows="1"
				placeholder="Ask Tora to update this sprint plan..."
				on:keydown={handleComposerKeydown}
				disabled={$timelineLoading || !currentState}
			></textarea>
			<div class="tora-toolbar">
				<span class="tora-hint" class:tora-hint-warn={[...draft].length > 2700}>
					{#if !currentState}
						Create a project to start chatting
					{:else if [...draft].length > 2700}
						{[...draft].length}/3000
					{:else if isLargeProject}
						Large board mode enabled
					{:else}
						Enter to send
					{/if}
				</span>
				<div class="toolbar-spacer"></div>
				{#if $timelineLoading}
					<button
						type="button"
						class="send-btn is-stop"
						on:click={stopBoardAIRequest}
						aria-label="Stop board AI request"
						title="Stop"
					>
						<svg viewBox="0 0 14 14" aria-hidden="true">
							<rect x="3.2" y="3.2" width="7.6" height="7.6" rx="1.2"></rect>
						</svg>
					</button>
				{:else}
					<button
						type="submit"
						class="send-btn"
						disabled={!draft.trim() || !currentState}
						aria-label="Send message"
					>
						<svg viewBox="0 0 14 14" aria-hidden="true">
							<path d="M2 7h10M8 3l4 4-4 4"></path>
						</svg>
					</button>
				{/if}
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

	.tora-clear-btn {
		border-radius: 999px;
		border: 1px solid rgba(255, 255, 255, 0.16);
		background: rgba(255, 255, 255, 0.08);
		color: #bdc1c6;
		padding: 0.14rem 0.48rem;
		font-size: 0.6rem;
		font-weight: 600;
		cursor: pointer;
	}

	.tora-clear-btn:hover:not(:disabled) {
		background: rgba(255, 255, 255, 0.14);
	}

	.tora-clear-btn:disabled {
		opacity: 0.5;
		cursor: not-allowed;
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

	.ai-intent-chip {
		border-radius: 999px;
		padding: 0.1rem 0.42rem;
		font-size: 0.6rem;
		border: 1px solid rgba(255, 255, 255, 0.16);
		background: rgba(255, 255, 255, 0.08);
		color: #bdc1c6;
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

	.ai-loading-panel {
		display: grid;
		gap: 0.7rem;
		padding: 0.78rem 0.82rem;
		border-radius: 0.96rem;
		background:
			linear-gradient(180deg, rgba(255, 255, 255, 0.05), rgba(255, 255, 255, 0.03)),
			rgba(16, 18, 22, 0.9);
		border: 1px solid rgba(255, 255, 255, 0.06);
		color: #c9d1d9;
	}

	.ai-loading-row {
		display: flex;
		align-items: flex-start;
		gap: 0.65rem;
	}

	.ai-loading-copy {
		display: grid;
		gap: 0.2rem;
		min-width: 0;
	}

	.ai-loading-copy strong {
		font-size: 0.8rem;
		font-weight: 600;
		color: #e8eaed;
	}

	.ai-loading-copy p {
		margin: 0;
		font-size: 0.74rem;
		line-height: 1.45;
		color: #9aa0a6;
	}

	.ai-loading-progress {
		font-size: 0.7rem;
		color: #8ab4f8;
	}

	.ai-workflow-list {
		display: grid;
		gap: 0.46rem;
	}

	.ai-workflow-entry {
		display: grid;
		gap: 0.24rem;
		padding: 0.52rem 0.58rem;
		border-radius: 12px;
		border: 1px solid rgba(255, 255, 255, 0.08);
		background: rgba(255, 255, 255, 0.03);
	}

	.ai-workflow-entry-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}

	.ai-workflow-entry-head strong {
		font-size: 0.76rem;
		color: #eef2f7;
	}

	.ai-workflow-entry p {
		margin: 0;
		font-size: 0.72rem;
		line-height: 1.45;
		color: #b8c0ca;
	}

	.ai-workflow-progress {
		border-radius: 999px;
		padding: 0.12rem 0.4rem;
		font-size: 0.62rem;
		font-weight: 700;
		background: rgba(26, 115, 232, 0.14);
		color: #8ab4f8;
	}

	.ai-workflow-tags {
		display: flex;
		flex-wrap: wrap;
		gap: 0.35rem;
	}

	.ai-workflow-tags span {
		border-radius: 999px;
		padding: 0.14rem 0.42rem;
		font-size: 0.62rem;
		background: rgba(255, 255, 255, 0.08);
		color: #bdc1c6;
	}

	.ai-workflow-entry.tone-success {
		border-color: rgba(86, 179, 127, 0.26);
		background: rgba(86, 179, 127, 0.08);
	}

	.ai-workflow-entry.tone-warning {
		border-color: rgba(245, 158, 11, 0.28);
		background: rgba(245, 158, 11, 0.08);
	}

	.ai-spinner {
		flex: 0 0 auto;
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
		max-height: calc(0.84rem * 1.46 * 3);
		overflow-y: auto;
		border: none;
		background: transparent;
		color: #e8eaed;
		caret-color: #e8eaed;
		padding: 0;
		font-family: inherit;
		font-size: 0.84rem;
		line-height: 1.46;
		letter-spacing: normal;
		word-spacing: normal;
		font-kerning: none;
		font-variant-ligatures: none;
		font-feature-settings:
			'liga' 0,
			'calt' 0;
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
		transition: color 0.15s;
	}

	.tora-hint.tora-hint-warn {
		color: #f59e0b;
		font-weight: 600;
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

	.send-btn.is-stop {
		background: #b3261e;
		box-shadow: 0 2px 10px rgba(179, 38, 30, 0.34);
	}

	.send-btn.is-stop:hover:not(:disabled) {
		background: #8f1f19;
		box-shadow: 0 4px 16px rgba(179, 38, 30, 0.4);
	}

	.send-btn:disabled {
		background: rgba(255, 255, 255, 0.08);
		cursor: not-allowed;
		box-shadow: none;
		transform: none;
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
