<script lang="ts">
	import { tick } from 'svelte';
	import {
		activeProjectTab,
		type AITimelineConversationMessage,
		type AITimelineIntent,
		generateAITimeline,
		isProjectNew,
		projectTimeline,
		setProjectTimeline,
		timelineError,
		timelineLoading
	} from '$lib/stores/timeline';
	import { initializeTaskStoreForRoom } from '$lib/stores/tasks';
	import { normalizeRoomIDValue } from '$lib/utils/chat/core';
	import { loadTemplate } from '$lib/utils/timelineTemplates';
	import type { ProjectTimeline } from '$lib/types/timeline';

	export let roomId = '';
	export let aiEnabled = true;

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';

	type OnboardingMode = 'selection' | 'manual' | 'ai';
	type ManualTemplateCard = {
		key: string;
		label: string;
		description: string;
	};
	type PromptStarter = {
		label: string;
		prompt: string;
	};
	type OnboardingAIMessage = {
		id: string;
		role: 'user' | 'assistant';
		text: string;
		timestamp: number;
		intent?: AITimelineIntent;
	};
	type PersistedOnboardingConversation = {
		version: 1;
		messages: OnboardingAIMessage[];
	};

	const MANUAL_TEMPLATE_CARDS: ManualTemplateCard[] = [
		{
			key: 'agile_sprint_planner',
			label: 'Agile Sprint Planner',
			description: 'Backlog, frontend, backend, and QA sprint structure.'
		},
		{
			key: 'waterfall_linear',
			label: 'Waterfall / Linear',
			description: 'Sequential phases with clearly staged delivery.'
		},
		{
			key: 'marketing_blitz',
			label: 'Marketing Blitz',
			description: 'Strategy, asset creation, and launch flow.'
		},
		{
			key: 'time_critical',
			label: 'Time Critical',
			description: 'Day-based execution plan for urgent delivery.'
		},
		{
			key: 'blank_board',
			label: 'Blank Board',
			description: 'Start empty and shape your own workflow.'
		}
	];

	let mode: OnboardingMode = 'selection';
	let aiPrompt = '';
	let localError = '';
	let applyingTemplate = false;
	let aiPartialWarning = '';
	let aiMissingSprints: string[] = [];
	let aiConversation: OnboardingAIMessage[] = [];
	let loadedConversationKey = '';
	let aiThreadElement: HTMLDivElement | null = null;
	let aiComposerTextarea: HTMLTextAreaElement | null = null;

	const ONBOARDING_CHAT_STORAGE_PREFIX = 'tora_ai_chat';
	const ONBOARDING_CHAT_CONTEXT = 'taskboard';
	const ONBOARDING_CHAT_HISTORY_LIMIT = 80;

	$: normalizedOnboardingRoomID = normalizeRoomIDValue(roomId);
	$: onboardingConversationStorageKey = `${ONBOARDING_CHAT_STORAGE_PREFIX}:${normalizedOnboardingRoomID}:${ONBOARDING_CHAT_CONTEXT}`;
	$: if (onboardingConversationStorageKey !== loadedConversationKey) {
		loadedConversationKey = onboardingConversationStorageKey;
		loadOnboardingConversation(onboardingConversationStorageKey);
	}

	$: if (!aiEnabled && mode === 'ai') {
		mode = 'selection';
	}

	const TEMPLATE_KEY_MAP: Record<string, string> = {
		agile_sprint_planner: 'software_agile',
		waterfall_linear: 'waterfall_linear',
		marketing_blitz: 'marketing_blitz',
		time_critical: 'time_critical',
		blank_board: 'blank_board'
	};
	const AI_PROMPT_STARTERS: PromptStarter[] = [
		{
			label: 'Product launch',
			prompt:
				'Build a product launch workspace for 3 sprints with Design, Frontend, Backend, QA, and GTM owners. Include weekly milestones, dependency tasks, and sprint budgets.'
		},
		{
			label: 'Client delivery',
			prompt:
				'Create a 6-week client delivery workspace with discovery, implementation, review, and handoff phases. Add priorities, assignees, and due dates for each phase.'
		},
		{
			label: 'Bug stabilization',
			prompt:
				'Generate a stabilization sprint focused on bug triage, fixes, regression testing, and release prep. Prioritize critical bugs first and include QA checkpoints.'
		},
		{
			label: 'Hiring pipeline',
			prompt:
				'Create a hiring operations workspace for engineering roles with sourcing, screening, interviews, offer, and onboarding tracks. Assign owners and weekly targets.'
		}
	];

	function createMessageID() {
		if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
			return crypto.randomUUID();
		}
		return `onboard-msg-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
	}

	function isBrowser() {
		return typeof window !== 'undefined' && Boolean(window.localStorage);
	}

	function sanitizePersistedConversation(candidate: unknown) {
		if (!Array.isArray(candidate)) {
			return [] as OnboardingAIMessage[];
		}
		const sanitized: OnboardingAIMessage[] = [];
		for (const entry of candidate) {
			if (!entry || typeof entry !== 'object') {
				continue;
			}
			const source = entry as Partial<OnboardingAIMessage>;
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
			const message: OnboardingAIMessage = {
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
		return sanitized.slice(-ONBOARDING_CHAT_HISTORY_LIMIT);
	}

	function loadOnboardingConversation(storageKey: string) {
		if (!isBrowser() || !storageKey) {
			aiConversation = [];
			return;
		}
		try {
			const raw = window.localStorage.getItem(storageKey);
			if (!raw) {
				aiConversation = [];
				return;
			}
			const parsed = JSON.parse(raw) as
				| PersistedOnboardingConversation
				| OnboardingAIMessage[]
				| null;
			aiConversation = Array.isArray(parsed)
				? sanitizePersistedConversation(parsed)
				: sanitizePersistedConversation(
						(parsed as PersistedOnboardingConversation | null)?.messages ?? []
					);
		} catch {
			aiConversation = [];
		}
		scrollOnboardingThreadToBottom();
	}

	function persistOnboardingConversation(storageKey: string) {
		if (!isBrowser() || !storageKey) {
			return;
		}
		try {
			const payload: PersistedOnboardingConversation = {
				version: 1,
				messages: aiConversation.slice(-ONBOARDING_CHAT_HISTORY_LIMIT)
			};
			window.localStorage.setItem(storageKey, JSON.stringify(payload));
		} catch {
			// best effort
		}
	}

	function clearOnboardingConversation(storageKey: string) {
		aiConversation = [];
		aiPartialWarning = '';
		aiMissingSprints = [];
		localError = '';
		if (!isBrowser() || !storageKey) {
			scrollOnboardingThreadToBottom();
			return;
		}
		try {
			window.localStorage.removeItem(storageKey);
		} catch {
			// best effort
		}
		scrollOnboardingThreadToBottom();
	}

	function appendOnboardingMessage(
		role: OnboardingAIMessage['role'],
		text: string,
		intent?: AITimelineIntent
	) {
		const normalizedText = String(text || '').trim();
		if (!normalizedText) {
			return;
		}
		aiConversation = [
			...aiConversation,
			{
				id: createMessageID(),
				role,
				text: normalizedText,
				timestamp: Date.now(),
				intent
			}
		].slice(-ONBOARDING_CHAT_HISTORY_LIMIT);
		persistOnboardingConversation(onboardingConversationStorageKey);
		scrollOnboardingThreadToBottom();
	}

	function scrollOnboardingThreadToBottom() {
		void tick().then(() => {
			if (!aiThreadElement) {
				return;
			}
			aiThreadElement.scrollTop = aiThreadElement.scrollHeight;
		});
	}

	function buildConversationPayload(
		messages: OnboardingAIMessage[]
	): AITimelineConversationMessage[] {
		return messages.map((message) => ({
			role: message.role,
			text: message.text,
			intent: message.intent
		}));
	}

	function formatConversationTime(timestamp: number) {
		return new Date(timestamp).toLocaleTimeString([], {
			hour: '2-digit',
			minute: '2-digit'
		});
	}

	function createBlankTimeline(): ProjectTimeline {
		const today = new Date();
		const dateText = today.toISOString().slice(0, 10);
		return {
			project_name: 'Blank Workspace',
			total_progress: 0,
			sprints: [
				{
					id: 'sprint-backlog',
					name: 'Backlog',
					start_date: dateText,
					end_date: dateText,
					tasks: []
				}
			]
		};
	}

	function goBackToSelection() {
		mode = 'selection';
		localError = '';
		aiPartialWarning = '';
		aiMissingSprints = [];
	}

	function openPartialWorkspace() {
		if (!$projectTimeline) {
			return;
		}
		isProjectNew.set(false);
		activeProjectTab.set('overview');
	}

	function applyPromptStarter(prompt: string) {
		aiPrompt = prompt;
		localError = '';
		void tick().then(() => aiComposerTextarea?.focus());
	}

	function handleAIPromptKeydown(event: KeyboardEvent) {
		if (event.key !== 'Enter' || event.shiftKey) {
			return;
		}
		event.preventDefault();
		void generateWorkspace();
	}

	async function generateWorkspace() {
		const normalizedRoomID = roomId.trim();
		const normalizedPrompt = aiPrompt.trim();
		localError = '';
		aiPartialWarning = '';
		aiMissingSprints = [];
		if (!aiEnabled) {
			localError = 'AI assistant is disabled for this room.';
			return;
		}
		if (!normalizedRoomID) {
			localError = 'Room id is required before generating a workspace.';
			return;
		}
		if (!normalizedPrompt) {
			localError = 'Describe your project before generating.';
			return;
		}

		appendOnboardingMessage('user', normalizedPrompt);
		const conversationPayload = buildConversationPayload(aiConversation);
		aiPrompt = '';

		try {
			const generationResult = await generateAITimeline(
				normalizedRoomID,
				normalizedPrompt,
				conversationPayload
			);
			const generatedTimeline = generationResult.timeline;
			appendOnboardingMessage(
				'assistant',
				generationResult.assistantReply || 'Understood.',
				generationResult.intent
			);
			if (generationResult.intent === 'chat' || generationResult.intent === 'clarify') {
				isProjectNew.set(true);
				return;
			}
			if (!generatedTimeline) {
				throw new Error('AI did not return a timeline. Please provide more detail and try again.');
			}

			await initializeTaskStoreForRoom(normalizeRoomIDValue(normalizedRoomID), {
				apiBase: API_BASE
			});
			if (generatedTimeline.is_partial) {
				aiMissingSprints = generatedTimeline.missing_sprints ?? [];
				aiPartialWarning =
					aiMissingSprints.length > 0
						? 'AI hit request limits and generated only part of the project plan.'
						: 'AI hit request limits and generated a partial project plan.';
				isProjectNew.set(true);
				return;
			}
			isProjectNew.set(false);
			activeProjectTab.set('overview');
		} catch (error) {
			localError = error instanceof Error ? error.message : 'Failed to generate workspace.';
			appendOnboardingMessage('assistant', `Error: ${localError}`, 'chat');
		}
	}

	async function selectManualTemplate(templateKey: string) {
		const normalizedRoomID = roomId.trim();
		localError = '';
		if (!normalizedRoomID) {
			localError = 'Room id is required before applying a template.';
			return;
		}
		if (!templateKey) {
			localError = 'Choose a valid template.';
			return;
		}
		const resolvedTemplateKey = TEMPLATE_KEY_MAP[templateKey] || templateKey;

		if (resolvedTemplateKey === 'blank_board') {
			setProjectTimeline(createBlankTimeline());
			await initializeTaskStoreForRoom(normalizeRoomIDValue(normalizedRoomID), {
				apiBase: API_BASE
			});
			isProjectNew.set(false);
			activeProjectTab.set('overview');
			return;
		}

		applyingTemplate = true;
		try {
			await loadTemplate(normalizedRoomID, resolvedTemplateKey);
			await initializeTaskStoreForRoom(normalizeRoomIDValue(normalizedRoomID), {
				apiBase: API_BASE
			});
			isProjectNew.set(false);
			activeProjectTab.set('overview');
		} catch (error) {
			localError = error instanceof Error ? error.message : 'Failed to apply template.';
		} finally {
			applyingTemplate = false;
		}
	}
</script>

<section class="project-onboarding" aria-label="Project workspace onboarding">
	{#if mode === 'selection'}
		<div class="selection-shell">
			<header class="selection-header">
				<h2>Create Project Workspace</h2>
				<p>Choose your setup path for this room.</p>
			</header>

			<div class="selection-actions" class:single-option={!aiEnabled}>
				<button type="button" class="selection-btn manual" on:click={() => (mode = 'manual')}>
					<span class="selection-icon" aria-hidden="true">
						<svg viewBox="0 0 24 24">
							<rect x="4.5" y="4.5" width="6.5" height="6.5" rx="1.5"></rect>
							<rect x="13" y="4.5" width="6.5" height="6.5" rx="1.5"></rect>
							<rect x="4.5" y="13" width="6.5" height="6.5" rx="1.5"></rect>
							<rect x="13" y="13" width="6.5" height="6.5" rx="1.5"></rect>
						</svg>
					</span>
					<span class="selection-copy">
						<strong>Do it yourself</strong>
						<small>Start from templates or blank and build manually.</small>
					</span>
				</button>

				{#if aiEnabled}
					<button type="button" class="selection-btn ai" on:click={() => (mode = 'ai')}>
						<span class="selection-icon" aria-hidden="true">
							<svg viewBox="0 0 24 24">
								<path d="M12 3.5 13.8 8l4.7 1.8-4.7 1.8L12 16l-1.8-4.4L5.5 9.8 10.2 8 12 3.5Z"
								></path>
								<path d="M18.5 13.5 19.4 15.7l2.1.9-2.1.8-.9 2.2-.8-2.2-2.2-.8 2.2-.9.8-2.2Z"
								></path>
							</svg>
						</span>
						<span class="selection-copy">
							<strong>Let Tora AI do it</strong>
							<small>Describe your project and auto-generate structure.</small>
						</span>
					</button>
				{/if}
			</div>
			{#if !aiEnabled}
				<p class="ai-disabled-note">AI assistant is disabled for this room.</p>
			{/if}
		</div>
	{:else if mode === 'ai' && aiEnabled}
		<section class="tora-chat tora-chat-onboarding" aria-label="Tora AI workspace generator">
			<header class="tora-chat-header">
				<div class="tora-brand">
					<span class="tora-brand-icon" aria-hidden="true">✦</span>
					<div class="tora-brand-copy">
						<h2>Tora AI</h2>
						<p>Workspace generator</p>
					</div>
				</div>
				<div class="tora-meta">
					<button type="button" class="tora-back-btn" on:click={goBackToSelection}>Back</button>
					<button
						type="button"
						class="tora-clear-btn"
						on:click={() => clearOnboardingConversation(onboardingConversationStorageKey)}
						disabled={$timelineLoading || aiConversation.length === 0}
					>
						Clear chat
					</button>
					<span class="tora-meta-badge">Setup mode</span>
				</div>
			</header>

			<div class="tora-thread" bind:this={aiThreadElement}>
				{#if aiConversation.length === 0}
					<div class="empty-state-v2">
						<div class="es-icon" aria-hidden="true">✦</div>
						<h4>Plan your workspace with Tora</h4>
						<p>
							Describe scope, timeline, owners, and constraints. Tora can ask one clarifying
							question before generating.
						</p>
					</div>
				{:else}
					{#each aiConversation as message (message.id)}
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
										<span class="ai-time-chip">{formatConversationTime(message.timestamp)}</span>
									</div>
								</div>
								<div class="ai-response-body">{message.text}</div>
							</article>
						{/if}
					{/each}
				{/if}
				{#if $timelineLoading}
					<div class="ai-loading-row">
						<span class="ai-spinner" aria-hidden="true"></span>
						Generating workspace...
					</div>
				{/if}
			</div>

			<div class="suggestions-panel">
				{#each AI_PROMPT_STARTERS as starter (starter.label)}
					<button
						type="button"
						class="suggestion-item"
						on:click={() => applyPromptStarter(starter.prompt)}
						disabled={$timelineLoading}
					>
						<span class="suggestion-arrow" aria-hidden="true">→</span>
						<span class="suggestion-text">{starter.label}</span>
					</button>
				{/each}
			</div>

			{#if aiPartialWarning}
				<div class="partial-warning-banner">
					<strong>{aiPartialWarning}</strong>
					{#if aiMissingSprints.length > 0}
						<p>Missing sprints: {aiMissingSprints.join(', ')}</p>
					{/if}
					<button type="button" class="warning-cta-btn" on:click={openPartialWorkspace}>
						Open Partial Workspace
					</button>
				</div>
			{/if}

			{#if localError || $timelineError}
				<div class="tora-error" role="status" aria-live="polite">{localError || $timelineError}</div>
			{/if}

			<form class="tora-composer" on:submit|preventDefault={() => void generateWorkspace()}>
				<div class="tora-input-box">
					<textarea
						class="tora-textarea"
						bind:this={aiComposerTextarea}
						bind:value={aiPrompt}
						rows="1"
						placeholder="Describe the workspace you want Tora to generate..."
						on:keydown={handleAIPromptKeydown}
						disabled={$timelineLoading}
					></textarea>
					<div class="tora-toolbar">
						<span class="tora-hint">
							{#if aiPrompt.trim().length === 0}
								Include sprint count, budget, owners, and deadline
							{:else}
								Enter to send
							{/if}
						</span>
						<div class="toolbar-spacer"></div>
						<button
							type="submit"
							class="send-btn"
							disabled={$timelineLoading || !aiPrompt.trim()}
							aria-label="Generate workspace"
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
	{:else}
		<div class="wizard-shell">
			<header class="wizard-head">
				<button type="button" class="back-btn" on:click={goBackToSelection}>Back</button>
				<h3>Manual Setup</h3>
			</header>

			<div class="template-grid">
				{#each MANUAL_TEMPLATE_CARDS as template (template.key)}
					<button
						type="button"
						class="template-card"
						on:click={() => {
							void selectManualTemplate(template.key);
						}}
						disabled={applyingTemplate || $timelineLoading}
					>
						<strong>{template.label}</strong>
						<p>{template.description}</p>
					</button>
				{/each}
			</div>
		</div>
	{/if}

	{#if (localError || $timelineError) && mode !== 'ai'}
		<div class="error-banner">{localError || $timelineError}</div>
	{/if}
</section>

<style>
	:global(:root) {
		--po-bg: #edf3fb;
		--po-text: #13284a;
		--po-muted: #5b739a;
		--po-surface: #ffffff;
		--po-surface-soft: #f8fbff;
		--po-border: #cfdcf0;
		--po-border-strong: #abc2e8;
		--po-accent: #2563eb;
		--po-accent-soft: rgba(37, 99, 235, 0.1);
		--po-danger: #b42318;
		--po-danger-soft: rgba(180, 35, 24, 0.1);
		--po-warning: #b54708;
		--po-warning-soft: rgba(181, 71, 8, 0.12);
		--po-ai-shell-bg: #1e1f24;
		--po-ai-shell-border: rgba(255, 255, 255, 0.08);
		--po-ai-shell-shadow: 0 8px 40px rgba(0, 0, 0, 0.45);
		--po-ai-field-bg: rgba(255, 255, 255, 0.04);
		--po-ai-field-border: rgba(255, 255, 255, 0.1);
		--po-ai-field-focus: rgba(26, 115, 232, 0.5);
		--po-ai-chip-bg: rgba(255, 255, 255, 0.05);
		--po-ai-chip-border: rgba(255, 255, 255, 0.12);
		--po-ai-chip-text: #bdc1c6;
		--po-ai-chip-hover-bg: rgba(255, 255, 255, 0.09);
		--po-ai-hint: #9aa0a6;
	}

	:global(:root[data-theme='dark']),
	:global(.theme-dark) {
		--po-bg: #101113;
		--po-text: #edf0f6;
		--po-muted: #a7adbc;
		--po-surface: #181a1f;
		--po-surface-soft: #21242b;
		--po-border: #353944;
		--po-border-strong: #535b6a;
		--po-accent: #b4becf;
		--po-accent-soft: rgba(180, 190, 207, 0.18);
		--po-danger: #ffb4b4;
		--po-danger-soft: rgba(248, 113, 113, 0.18);
		--po-warning: #ffd89b;
		--po-warning-soft: rgba(251, 191, 36, 0.18);
		--po-ai-shell-bg: #1e1f24;
		--po-ai-shell-border: rgba(255, 255, 255, 0.08);
		--po-ai-shell-shadow: 0 8px 40px rgba(0, 0, 0, 0.45);
		--po-ai-field-bg: rgba(255, 255, 255, 0.04);
		--po-ai-field-border: rgba(255, 255, 255, 0.1);
		--po-ai-field-focus: rgba(26, 115, 232, 0.5);
		--po-ai-chip-bg: rgba(255, 255, 255, 0.05);
		--po-ai-chip-border: rgba(255, 255, 255, 0.12);
		--po-ai-chip-text: #bdc1c6;
		--po-ai-chip-hover-bg: rgba(255, 255, 255, 0.09);
		--po-ai-hint: #9aa0a6;
	}

	.project-onboarding {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: 1fr auto;
		gap: 0.9rem;
		padding: 1rem;
		background: var(--po-bg);
		color: var(--po-text);
	}

	.selection-shell,
	.wizard-shell {
		border: 1px solid var(--po-border);
		border-radius: 18px;
		background: var(--po-surface);
		box-shadow: 0 14px 30px rgba(17, 34, 66, 0.12);
	}

	.selection-shell {
		display: grid;
		align-content: center;
		justify-items: center;
		gap: 1.25rem;
		padding: 1.7rem;
	}

	.selection-header {
		text-align: center;
	}

	.selection-header h2 {
		margin: 0;
		font-size: 1.3rem;
	}

	.selection-header p {
		margin: 0.44rem 0 0;
		color: var(--po-muted);
		font-size: 0.95rem;
	}

	.selection-actions {
		width: min(920px, 100%);
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.95rem;
	}

	.selection-actions.single-option {
		grid-template-columns: minmax(0, 1fr);
		max-width: 460px;
	}

	.ai-disabled-note {
		margin: 0;
		font-size: 0.82rem;
		color: var(--po-muted);
	}

	.selection-btn {
		border: 1px solid var(--po-border);
		background: var(--po-surface);
		border-radius: 16px;
		padding: 1.15rem;
		display: grid;
		grid-template-columns: auto 1fr;
		gap: 0.8rem;
		align-items: center;
		text-align: left;
		cursor: pointer;
		color: var(--po-text);
		transition:
			transform 0.16s ease,
			background 0.16s ease,
			border-color 0.16s ease;
	}

	.selection-btn:hover {
		transform: translateY(-2px);
		border-color: var(--po-border-strong);
		background: color-mix(in srgb, var(--po-accent-soft) 55%, var(--po-surface));
	}

	.selection-icon {
		width: 2.6rem;
		height: 2.6rem;
		border-radius: 12px;
		display: grid;
		place-items: center;
		background: var(--po-accent-soft);
		border: 1px solid color-mix(in srgb, var(--po-accent) 35%, var(--po-border));
	}

	.selection-icon svg {
		width: 1.25rem;
		height: 1.25rem;
		stroke: currentColor;
		stroke-width: 1.8;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.selection-btn.ai .selection-icon svg {
		stroke: var(--po-accent);
	}

	.selection-copy strong {
		display: block;
		font-size: 1rem;
	}

	.selection-copy small {
		display: block;
		margin-top: 0.28rem;
		color: var(--po-muted);
		font-size: 0.82rem;
		line-height: 1.42;
	}

	.wizard-shell {
		display: grid;
		gap: 1rem;
		align-content: start;
		padding: 1rem;
	}

	.wizard-head {
		display: flex;
		align-items: center;
		gap: 0.8rem;
	}

	.wizard-head h3 {
		margin: 0;
		font-size: 1rem;
	}

	.back-btn {
		border-radius: 10px;
		border: 1px solid var(--po-border);
		background: var(--po-surface);
		color: var(--po-text);
		padding: 0.52rem 0.8rem;
		font-size: 0.82rem;
		font-weight: 600;
		cursor: pointer;
	}

	.back-btn:hover {
		border-color: var(--po-border-strong);
		background: color-mix(in srgb, var(--po-accent-soft) 45%, var(--po-surface));
	}

	.tora-chat {
		height: 100%;
		min-height: 0;
		display: flex;
		flex-direction: column;
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
		gap: 0.45rem;
		font-size: 0.66rem;
		color: #9aa0a6;
		white-space: nowrap;
	}

	.tora-back-btn {
		border-radius: 999px;
		border: 1px solid rgba(255, 255, 255, 0.16);
		background: rgba(255, 255, 255, 0.08);
		color: #bdc1c6;
		padding: 0.22rem 0.56rem;
		font-size: 0.66rem;
		font-weight: 600;
		cursor: pointer;
	}

	.tora-back-btn:hover {
		background: rgba(255, 255, 255, 0.14);
	}

	.tora-clear-btn {
		border-radius: 999px;
		border: 1px solid rgba(255, 255, 255, 0.16);
		background: rgba(255, 255, 255, 0.08);
		color: #bdc1c6;
		padding: 0.22rem 0.56rem;
		font-size: 0.66rem;
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
		flex: 1 1 auto;
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
		max-width: 360px;
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

	.template-grid {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.78rem;
	}

	.template-card {
		border: 1px solid var(--po-border);
		border-radius: 14px;
		background: var(--po-surface);
		color: var(--po-text);
		text-align: left;
		padding: 0.9rem;
		cursor: pointer;
		transition:
			border-color 0.16s ease,
			transform 0.16s ease,
			background 0.16s ease;
	}

	.template-card strong {
		display: block;
		font-size: 0.93rem;
	}

	.template-card p {
		margin: 0.38rem 0 0;
		font-size: 0.82rem;
		line-height: 1.38;
		color: var(--po-muted);
	}

	.template-card:hover:not(:disabled) {
		transform: translateY(-1px);
		border-color: var(--po-border-strong);
		background: color-mix(in srgb, var(--po-accent-soft) 45%, var(--po-surface));
	}

	.error-banner {
		border-radius: 12px;
		border: 1px solid color-mix(in srgb, var(--po-danger) 45%, var(--po-border));
		background: var(--po-danger-soft);
		color: var(--po-danger);
		padding: 0.62rem 0.76rem;
		font-size: 0.84rem;
		font-weight: 600;
	}

	.partial-warning-banner {
		margin: 0 0.82rem 0.58rem;
		border-radius: 12px;
		border: 1px solid rgba(255, 185, 64, 0.45);
		background: rgba(181, 112, 18, 0.2);
		color: #ffd89b;
		padding: 0.75rem 0.8rem;
		display: grid;
		gap: 0.52rem;
	}

	.partial-warning-banner strong {
		font-size: 0.85rem;
	}

	.partial-warning-banner p {
		margin: 0;
		font-size: 0.8rem;
		color: #ffe5bb;
	}

	.warning-cta-btn {
		width: fit-content;
		border-radius: 10px;
		border: 1px solid rgba(255, 185, 64, 0.5);
		background: rgba(255, 185, 64, 0.14);
		color: #ffd89b;
		padding: 0.48rem 0.76rem;
		font-size: 0.78rem;
		font-weight: 600;
		cursor: pointer;
	}

	.warning-cta-btn:hover {
		border-color: rgba(255, 185, 64, 0.75);
		background: rgba(255, 185, 64, 0.22);
	}

	@media (max-width: 900px) {
		.selection-actions,
		.template-grid {
			grid-template-columns: minmax(0, 1fr);
		}

		.tora-chat-header {
			flex-wrap: wrap;
			align-items: flex-start;
		}

		.tora-meta {
			width: 100%;
			justify-content: space-between;
		}

		.tora-thread {
			padding: 0.72rem;
		}

		.partial-warning-banner {
			margin: 0 0.72rem 0.54rem;
		}
	}
</style>
