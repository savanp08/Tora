<script lang="ts">
	import { createEventDispatcher, tick } from 'svelte';
	import RichTextContent from '$lib/components/chat/RichTextContent.svelte';
	import { resolveApiBase } from '$lib/config/apiBase';
	import {
		activeProjectTab,
		type AITimelineConversationMessage,
		type AITimelineIntent,
		type StreamAIStatusMeta,
		isTimelineRequestStoppedError,
		isProjectNew,
		projectTimeline,
		setProjectTimeline,
		stopActiveTimelineRequest,
		streamAITimeline,
		timelineError,
		timelineLoading
	} from '$lib/stores/timeline';
	import { sessionTier } from '$lib/stores/auth';
	import { fieldSchemaStore } from '$lib/stores/fieldSchema';
	import { initializeTaskStoreForRoom, taskStore } from '$lib/stores/tasks';
	import { normalizeRoomIDValue } from '$lib/utils/chat/core';
	import type { ProjectTimeline } from '$lib/types/timeline';

	export let roomId = '';
	export let aiEnabled = true;
	export let templatePickerOnly = false;
	export let isModal = false;

	const dispatch = createEventDispatcher<{
		close: void;
		templateApplied: {
			templateId: string;
			templateName: string;
			blank: boolean;
			fieldsCreated: number;
			tasksCreated: number;
			automationRulesCreated: number;
		};
	}>();

	const API_BASE = resolveApiBase(import.meta.env.VITE_API_BASE as string | undefined);
	const BLANK_TEMPLATE_ID = 'blank';

	function formatNumberedSprintName(name: string, sprintIndex: number) {
		const trimmed = name.trim() || `Sprint ${sprintIndex + 1}`;
		if (trimmed.toLowerCase() === 'backlog') {
			return 'Backlog';
		}
		return `${sprintIndex + 1}. ${trimmed.replace(/^\d+\.\s*/, '')}`;
	}

	type OnboardingMode = 'selection' | 'manual' | 'ai';
	type ManualStep = 'picker' | 'confirm';
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
	type OnboardingWorkflowEntry = {
		id: string;
		title: string;
		detail?: string;
		progress?: string;
		tone: 'status' | 'success' | 'warning';
		taskTitles?: string[];
		stepKey?: string;
		timing?: {
			startedAt: number;
			endedAt?: number;
			stepBudgetMs?: number;
			promptBudgetMs?: number;
			strategy?: string;
		};
	};
	type PersistedOnboardingConversation = {
		version: 1;
		messages: OnboardingAIMessage[];
	};
	type IndustryTemplateField = {
		name: string;
		fieldType: string;
		options: string[];
		position: number;
	};
	type IndustryTemplateTask = {
		title: string;
		status: string;
		sprintName: string;
		customFields: Record<string, string>;
	};
	type IndustryTemplateRule = {
		name: string;
		triggerType: string;
		triggerConfig: string;
		actionType: string;
		actionConfig: string;
	};
	type IndustryTemplate = {
		id: string;
		name: string;
		description: string;
		industries: string[];
		fieldSchemas: IndustryTemplateField[];
		sampleTasks: IndustryTemplateTask[];
		automationRules: IndustryTemplateRule[];
	};
	type ApplyTemplateResponse = {
		success?: boolean;
		template_id?: string;
		template_name?: string;
		fields_created?: number;
		tasks_created?: number;
		automation_rules_created?: number;
		error?: string;
		message?: string;
	};

	const BLANK_TEMPLATE_CARD: IndustryTemplate = {
		id: BLANK_TEMPLATE_ID,
		name: 'Start Blank',
		description: 'Open a clean workspace with no starter tasks, fields, or automation presets.',
		industries: ['Blank'],
		fieldSchemas: [],
		sampleTasks: [],
		automationRules: []
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

	let mode: OnboardingMode = templatePickerOnly ? 'manual' : 'selection';
	let manualStep: ManualStep = 'picker';
	let aiPrompt = '';
	let fitToTier = false;

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
	let localError = '';
	let applyingTemplate = false;
	let aiPartialWarning = '';
	let aiMissingSprints: string[] = [];
	let aiConversation: OnboardingAIMessage[] = [];
	let aiWorkflowEntries: OnboardingWorkflowEntry[] = [];
	let streamingStep = '';
	let lastWorkflowStatusKey = '';
	let activeWorkflowEntryId = '';
	let activeWorkflowStepKey = '';
	let aiWorkflowRunStartedAt = 0;
	let aiWorkflowRunFinishedAt = 0;
	let aiWorkflowPromptBudgetMs = 15 * 60 * 1000;
	let aiWorkflowClockNow = Date.now();
	let aiWorkflowTimer: number | null = null;
	let loadedConversationKey = '';
	let aiThreadElement: HTMLDivElement | null = null;
	let aiComposerTextarea: HTMLTextAreaElement | null = null;
	let templatesLoading = false;
	let templatesLoadAttempted = false;
	let templateLoadError = '';
	let templates: IndustryTemplate[] = [];
	let selectedTemplateId = '';
	let confirmReplaceExisting = false;

	const ONBOARDING_CHAT_STORAGE_PREFIX = 'tora_ai_chat';
	const ONBOARDING_CHAT_CONTEXT = 'taskboard';
	const ONBOARDING_CHAT_HISTORY_LIMIT = 80;

	$: normalizedOnboardingRoomID = normalizeRoomIDValue(roomId);
	$: onboardingConversationStorageKey = `${ONBOARDING_CHAT_STORAGE_PREFIX}:${normalizedOnboardingRoomID}:${ONBOARDING_CHAT_CONTEXT}`;
	$: if (onboardingConversationStorageKey !== loadedConversationKey) {
		loadedConversationKey = onboardingConversationStorageKey;
		loadOnboardingConversation(onboardingConversationStorageKey);
	}
	$: if (templatePickerOnly && mode !== 'manual') {
		mode = 'manual';
		manualStep = 'picker';
	}
	$: if (!aiEnabled && mode === 'ai') {
		mode = templatePickerOnly ? 'manual' : 'selection';
	}
	$: if ((mode === 'manual' || templatePickerOnly) && !templatesLoadAttempted) {
		templatesLoadAttempted = true;
		void loadTemplates();
	}
	$: roomTaskCount = $taskStore.filter(
		(task) => normalizeRoomIDValue(task.roomId) === normalizedOnboardingRoomID
	).length;
	$: roomFieldCount = $fieldSchemaStore.filter(
		(schema) => normalizeRoomIDValue(schema.roomId) === normalizedOnboardingRoomID
	).length;
	$: roomHasExistingContent = roomTaskCount > 0 || roomFieldCount > 0;
	$: allTemplates = [BLANK_TEMPLATE_CARD, ...templates];
	$: availableTemplates = allTemplates;
	$: selectedTemplate = allTemplates.find((template) => template.id === selectedTemplateId) ?? null;
	$: templatePreviewFields = selectedTemplate?.fieldSchemas ?? [];
	$: templatePreviewTasks = selectedTemplate?.sampleTasks ?? [];

	function createMessageID() {
		if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
			return crypto.randomUUID();
		}
		return `onboard-msg-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
	}

	function isBrowser() {
		return typeof window !== 'undefined' && Boolean(window.localStorage);
	}

	function toRecord(value: unknown): Record<string, unknown> | null {
		if (!value || typeof value !== 'object' || Array.isArray(value)) {
			return null;
		}
		return value as Record<string, unknown>;
	}

	function toStringValue(value: unknown) {
		return typeof value === 'string' ? value.trim() : '';
	}

	function toStringArray(value: unknown) {
		if (!Array.isArray(value)) {
			return [] as string[];
		}
		return value.map((entry) => toStringValue(entry)).filter(Boolean);
	}

	function normalizeTemplateField(raw: unknown): IndustryTemplateField | null {
		const source = toRecord(raw);
		if (!source) {
			return null;
		}
		const name = toStringValue(source.name);
		const fieldType = toStringValue(source.fieldType ?? source.field_type) || 'text';
		if (!name) {
			return null;
		}
		const rawPosition = source.position;
		let position = 0;
		if (typeof rawPosition === 'number' && Number.isFinite(rawPosition)) {
			position = Math.max(0, Math.floor(rawPosition));
		} else if (typeof rawPosition === 'string') {
			const parsed = Number(rawPosition);
			if (Number.isFinite(parsed)) {
				position = Math.max(0, Math.floor(parsed));
			}
		}
		return {
			name,
			fieldType,
			options: toStringArray(source.options),
			position
		};
	}

	function normalizeTemplateTask(raw: unknown): IndustryTemplateTask | null {
		const source = toRecord(raw);
		if (!source) {
			return null;
		}
		const title = toStringValue(source.title);
		if (!title) {
			return null;
		}
		const customFieldsSource = toRecord(source.customFields ?? source.custom_fields) ?? {};
		const customFields: Record<string, string> = {};
		for (const [key, value] of Object.entries(customFieldsSource)) {
			const normalizedKey = key.trim();
			const normalizedValue = toStringValue(value);
			if (!normalizedKey || !normalizedValue) {
				continue;
			}
			customFields[normalizedKey] = normalizedValue;
		}
		return {
			title,
			status: toStringValue(source.status) || 'todo',
			sprintName: toStringValue(source.sprintName ?? source.sprint_name),
			customFields
		};
	}

	function normalizeTemplateRule(raw: unknown): IndustryTemplateRule | null {
		const source = toRecord(raw);
		if (!source) {
			return null;
		}
		const name = toStringValue(source.name);
		const triggerType = toStringValue(source.triggerType ?? source.trigger_type);
		const actionType = toStringValue(source.actionType ?? source.action_type);
		if (!name && !triggerType && !actionType) {
			return null;
		}
		return {
			name,
			triggerType,
			triggerConfig: toStringValue(source.triggerConfig ?? source.trigger_config),
			actionType,
			actionConfig: toStringValue(source.actionConfig ?? source.action_config)
		};
	}

	function normalizeTemplate(raw: unknown): IndustryTemplate | null {
		const source = toRecord(raw);
		if (!source) {
			return null;
		}
		const id = toStringValue(source.id);
		const name = toStringValue(source.name);
		if (!id || !name) {
			return null;
		}
		const rawFieldSchemas: unknown[] = Array.isArray(source.fieldSchemas ?? source.field_schemas)
			? [...((source.fieldSchemas ?? source.field_schemas) as unknown[])]
			: [];
		const rawSampleTasks: unknown[] = Array.isArray(source.sampleTasks ?? source.sample_tasks)
			? [...((source.sampleTasks ?? source.sample_tasks) as unknown[])]
			: [];
		const rawAutomationRules: unknown[] = Array.isArray(
			source.automationRules ?? source.automation_rules
		)
			? [...((source.automationRules ?? source.automation_rules) as unknown[])]
			: [];
		const fieldSchemas = rawFieldSchemas
			.map((entry: unknown) => normalizeTemplateField(entry))
			.filter((entry: IndustryTemplateField | null): entry is IndustryTemplateField =>
				Boolean(entry)
			);
		const sampleTasks = rawSampleTasks
			.map((entry: unknown) => normalizeTemplateTask(entry))
			.filter((entry: IndustryTemplateTask | null): entry is IndustryTemplateTask =>
				Boolean(entry)
			);
		const automationRules = rawAutomationRules
			.map((entry: unknown) => normalizeTemplateRule(entry))
			.filter((entry: IndustryTemplateRule | null): entry is IndustryTemplateRule =>
				Boolean(entry)
			);
		return {
			id,
			name,
			description: toStringValue(source.description),
			industries: toStringArray(source.industries),
			fieldSchemas,
			sampleTasks,
			automationRules
		};
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

	function startOnboardingWorkflowClock(startedAt = Date.now()) {
		stopOnboardingWorkflowClock(startedAt);
		aiWorkflowClockNow = startedAt;
		if (typeof window === 'undefined') {
			return;
		}
		aiWorkflowTimer = window.setInterval(() => {
			aiWorkflowClockNow = Date.now();
		}, 1000);
	}

	function stopOnboardingWorkflowClock(finishedAt = Date.now()) {
		aiWorkflowClockNow = finishedAt;
		if (aiWorkflowTimer && typeof window !== 'undefined') {
			window.clearInterval(aiWorkflowTimer);
		}
		aiWorkflowTimer = null;
	}

	function getOnboardingWorkflowReferenceNow() {
		if ($timelineLoading && aiWorkflowRunStartedAt > 0) {
			return aiWorkflowClockNow || Date.now();
		}
		return aiWorkflowRunFinishedAt || aiWorkflowClockNow || Date.now();
	}

	function getOnboardingWorkflowEntry(entryId: string) {
		return aiWorkflowEntries.find((entry) => entry.id === entryId) ?? null;
	}

	function getOnboardingWorkflowEntryElapsedMs(entry: OnboardingWorkflowEntry) {
		if (!entry.timing) {
			return 0;
		}
		const end = entry.timing.endedAt ?? getOnboardingWorkflowReferenceNow();
		return Math.max(0, end - entry.timing.startedAt);
	}

	function getOnboardingWorkflowEntryTimingChips(entry: OnboardingWorkflowEntry) {
		if (!entry.timing) {
			return [] as string[];
		}
		const chips: string[] = [];
		const elapsedMs = getOnboardingWorkflowEntryElapsedMs(entry);
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

	function getOnboardingTotalElapsedMs() {
		if (!aiWorkflowRunStartedAt) {
			return 0;
		}
		return Math.max(0, getOnboardingWorkflowReferenceNow() - aiWorkflowRunStartedAt);
	}

	function getOnboardingCurrentStepElapsedMs() {
		const entry = activeWorkflowEntryId ? getOnboardingWorkflowEntry(activeWorkflowEntryId) : null;
		return entry ? getOnboardingWorkflowEntryElapsedMs(entry) : 0;
	}

	function getOnboardingWorkflowSummary() {
		if (!aiWorkflowRunStartedAt) {
			return '';
		}
		const parts = [`Elapsed ${formatWorkflowDuration(getOnboardingTotalElapsedMs())}`];
		const currentStepElapsedMs = getOnboardingCurrentStepElapsedMs();
		if (currentStepElapsedMs > 0) {
			parts.push(`Current step ${formatWorkflowDuration(currentStepElapsedMs)}`);
		}
		if (aiWorkflowPromptBudgetMs > 0) {
			parts.push(`Prompt budget ${formatWorkflowDuration(aiWorkflowPromptBudgetMs)}`);
		}
		return parts.join(' • ');
	}

	function startOnboardingWorkflowRun(promptBudgetMs = 15 * 60 * 1000) {
		const startedAt = Date.now();
		aiWorkflowRunStartedAt = startedAt;
		aiWorkflowRunFinishedAt = 0;
		aiWorkflowPromptBudgetMs = promptBudgetMs;
		startOnboardingWorkflowClock(startedAt);
	}

	function finalizeActiveWorkflowEntry(finishedAt = Date.now()) {
		if (!activeWorkflowEntryId) {
			return;
		}
		aiWorkflowEntries = aiWorkflowEntries.map((entry) =>
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

	function finishOnboardingWorkflowRun(finishedAt = Date.now()) {
		finalizeActiveWorkflowEntry(finishedAt);
		aiWorkflowRunFinishedAt = finishedAt;
		stopOnboardingWorkflowClock(finishedAt);
	}

	function resetOnboardingWorkflow() {
		stopOnboardingWorkflowClock();
		aiWorkflowEntries = [];
		lastWorkflowStatusKey = '';
		activeWorkflowEntryId = '';
		activeWorkflowStepKey = '';
		aiWorkflowRunStartedAt = 0;
		aiWorkflowRunFinishedAt = 0;
		aiWorkflowPromptBudgetMs = 15 * 60 * 1000;
	}

	function appendWorkflowEntry(entry: Omit<OnboardingWorkflowEntry, 'id'>) {
		const nextEntry = { id: createMessageID(), ...entry };
		aiWorkflowEntries = [...aiWorkflowEntries, nextEntry].slice(-18);
		scrollOnboardingThreadToBottom();
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
			aiWorkflowPromptBudgetMs = meta.promptTimeoutMs;
		}
		const statusKey = `${normalizedStepKey}::${normalizedLabel}::${progress ?? ''}`;
		if (activeWorkflowEntryId && normalizedStepKey === activeWorkflowStepKey) {
			aiWorkflowEntries = aiWorkflowEntries.map((entry) =>
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

	function summarizeTaskTitles(taskTitles: string[]) {
		if (taskTitles.length <= 4) {
			return taskTitles;
		}
		return [...taskTitles.slice(0, 4), `+${taskTitles.length - 4} more`];
	}

	function stopWorkspaceGeneration() {
		stopActiveTimelineRequest();
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

	function resetTemplateFlow() {
		manualStep = 'picker';
		selectedTemplateId = '';
		confirmReplaceExisting = false;
		localError = '';
	}

	function goBackToSelection() {
		if (templatePickerOnly) {
			dispatch('close');
			return;
		}
		mode = 'selection';
		resetTemplateFlow();
		aiPartialWarning = '';
		aiMissingSprints = [];
	}

	function openTemplateFlow() {
		mode = 'manual';
		manualStep = 'picker';
		selectedTemplateId = '';
		confirmReplaceExisting = false;
		localError = '';
	}

	function openBlankFlow() {
		mode = 'manual';
		manualStep = 'picker';
		selectedTemplateId = BLANK_TEMPLATE_ID;
		confirmReplaceExisting = false;
		localError = '';
	}

	function openAIFlow() {
		if (!aiEnabled) {
			localError = 'Tora AI is unavailable in this room.';
			return;
		}
		localError = '';
		mode = 'ai';
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

	function parseTemplateError(payload: unknown, status: number) {
		const source = toRecord(payload);
		return toStringValue(source?.error) || toStringValue(source?.message) || `HTTP ${status}`;
	}

	async function loadTemplates() {
		templatesLoading = true;
		templateLoadError = '';
		try {
			const response = await fetch(`${API_BASE}/api/templates?include_tasks=true`, {
				method: 'GET',
				credentials: 'include'
			});
			const payload = (await response.json().catch(() => null)) as unknown;
			if (!response.ok) {
				throw new Error(parseTemplateError(payload, response.status));
			}
			const records = Array.isArray(payload) ? payload : [];
			templates = records
				.map((entry) => normalizeTemplate(entry))
				.filter((entry): entry is IndustryTemplate => Boolean(entry));
		} catch (error) {
			templates = [];
			templateLoadError =
				error instanceof Error ? error.message : 'Failed to load starter templates.';
		} finally {
			templatesLoading = false;
		}
	}

	function retryTemplateLoad() {
		templatesLoadAttempted = true;
		void loadTemplates();
	}

	function selectTemplateCard(templateId: string) {
		selectedTemplateId = templateId;
		confirmReplaceExisting = false;
		localError = '';
	}

	function reviewSelectedTemplate() {
		if (!selectedTemplate) {
			localError = 'Choose a template first.';
			return;
		}
		manualStep = 'confirm';
		confirmReplaceExisting = false;
		localError = '';
	}

	function goBackToTemplatePicker() {
		manualStep = 'picker';
		localError = '';
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
		if ([...normalizedPrompt].length > 3000) {
			localError = 'Your prompt is too long (max 3,000 characters). Please shorten it.';
			return;
		}

		appendOnboardingMessage('user', normalizedPrompt);
		const conversationPayload = buildConversationPayload(aiConversation);
		aiPrompt = '';
		streamingStep = '';
		resetOnboardingWorkflow();
		startOnboardingWorkflowRun();
		appendWorkflowEntry({
			title: 'Starting workspace generation',
			detail: 'Tora is preparing the request and opening the live workflow.',
			tone: 'status'
		});

		try {
			const generationResult = await streamAITimeline(
				normalizedRoomID,
				normalizedPrompt,
				conversationPayload,
				{
					onStatus: (step, label, sprintIndex, sprintTotal, meta) => {
						if (meta?.promptTimeoutMs && meta.promptTimeoutMs > 0) {
							aiWorkflowPromptBudgetMs = meta.promptTimeoutMs;
						}
						if (
							typeof sprintIndex === 'number' &&
							typeof sprintTotal === 'number' &&
							sprintTotal > 0
						) {
							streamingStep = `${label} (${Math.min(sprintIndex + 1, sprintTotal)}/${sprintTotal})`;
							addWorkflowStatus(
								step || label,
								label,
								`${Math.min(sprintIndex + 1, sprintTotal)}/${sprintTotal}`,
								meta
							);
							return;
						}
						streamingStep = label;
						addWorkflowStatus(step || label, label, undefined, meta);
					},
					onBlueprint: ({ projectName, assistantReply, sprintNames }) => {
						const numberedSprintNames = sprintNames.map((name, index) =>
							formatNumberedSprintName(name, index)
						);
						appendWorkflowEntry({
							title: `Blueprint ready for ${projectName || 'your workspace'}`,
							detail:
								assistantReply ||
								(numberedSprintNames.length > 0
									? `Planned ${numberedSprintNames.length} sprints: ${numberedSprintNames.join(', ')}.`
									: 'The workspace shell is ready and task creation has started.'),
							tone: 'success',
							taskTitles: summarizeTaskTitles(numberedSprintNames)
						});
					},
					onSprint: ({ sprintName, taskCount, taskTitles, sprintIndex, sprintTotal }) => {
						appendWorkflowEntry({
							title: `Built ${formatNumberedSprintName(sprintName || `Sprint ${sprintIndex + 1}`, sprintIndex)}`,
							detail: `${taskCount} tasks added to the board.`,
							progress:
								sprintTotal > 0
									? `${Math.min(sprintIndex + 1, sprintTotal)}/${sprintTotal}`
									: undefined,
							tone: 'success',
							taskTitles: summarizeTaskTitles(taskTitles)
						});
					},
					onDone: ({ assistantReply, isPartial, missingSprints }) => {
						finishOnboardingWorkflowRun();
						appendWorkflowEntry({
							title: isPartial ? 'Workspace generation paused with partial output' : 'Workspace generation complete',
							detail: isPartial
								? missingSprints.length > 0
									? `Missing sprints: ${missingSprints.join(', ')}.`
									: assistantReply || 'Partial work was kept on the board.'
								: assistantReply || 'The workspace is ready.',
							tone: isPartial ? 'warning' : 'success'
						});
					},
					onError: ({ message, isStopped }) => {
						finishOnboardingWorkflowRun();
						appendWorkflowEntry({
							title: isStopped ? 'Generation stopped' : 'Generation interrupted',
							detail: message,
							tone: 'warning'
						});
					},
					onChat: (intent, assistantReply) => {
						appendOnboardingMessage('assistant', assistantReply || 'Understood.', intent as AITimelineIntent);
					}
				},
				fitToTier
			);
			streamingStep = '';
			const generatedTimeline = generationResult.timeline;
			if (generationResult.intent === 'chat' || generationResult.intent === 'clarify') {
				finishOnboardingWorkflowRun();
				isProjectNew.set(true);
				return;
			}
			appendOnboardingMessage(
				'assistant',
				generationResult.assistantReply || 'Your workspace has been generated.',
				generationResult.intent
			);
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
			finishOnboardingWorkflowRun();
			streamingStep = '';
			if (isTimelineRequestStoppedError(error)) {
				localError = '';
				const stoppedTimeline = error.timeline;
				if (stoppedTimeline?.is_partial) {
					aiMissingSprints = stoppedTimeline.missing_sprints ?? [];
					aiPartialWarning =
						aiMissingSprints.length > 0
							? 'Generation stopped. Partial workspace kept.'
							: 'Generation stopped after creating part of the workspace.';
					isProjectNew.set(true);
				}
				return;
			}
			localError = error instanceof Error ? error.message : 'Failed to generate workspace.';
			const latestWorkflowTitle =
				aiWorkflowEntries[aiWorkflowEntries.length - 1]?.title || 'the current AI step';
			appendOnboardingMessage(
				'assistant',
				`The run stopped during ${latestWorkflowTitle.toLowerCase()}. ${localError}`,
				'chat'
			);
		} finally {
			finishOnboardingWorkflowRun();
		}
	}

	async function applySelectedTemplate() {
		const normalizedRoomID = normalizeRoomIDValue(roomId);
		if (!normalizedRoomID) {
			localError = 'Room id is required before applying a template.';
			return;
		}
		if (!selectedTemplate) {
			localError = 'Choose a starter template before applying it.';
			return;
		}
		if (roomHasExistingContent && !confirmReplaceExisting) {
			localError = 'Confirm that you want to replace the current workspace content first.';
			return;
		}

		applyingTemplate = true;
		localError = '';
		try {
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomID)}/apply-template`,
				{
					method: 'POST',
					credentials: 'include',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({
						template_id: selectedTemplate.id,
						clear_existing: roomHasExistingContent ? confirmReplaceExisting : false
					})
				}
			);
			const payload = (await response.json().catch(() => null)) as ApplyTemplateResponse | null;
			if (!response.ok) {
				throw new Error(parseTemplateError(payload, response.status));
			}

			const blank = selectedTemplate.id === BLANK_TEMPLATE_ID;
			if (blank) {
				setProjectTimeline(createBlankTimeline());
				isProjectNew.set(false);
				activeProjectTab.set('overview');
			}

			dispatch('templateApplied', {
				templateId: selectedTemplate.id,
				templateName: payload?.template_name?.trim() || selectedTemplate.name,
				blank,
				fieldsCreated: payload?.fields_created ?? 0,
				tasksCreated: payload?.tasks_created ?? 0,
				automationRulesCreated: payload?.automation_rules_created ?? 0
			});
		} catch (error) {
			localError = error instanceof Error ? error.message : 'Failed to apply template.';
		} finally {
			applyingTemplate = false;
		}
	}
</script>

<section
	class="project-onboarding"
	class:is-modal={isModal}
	aria-label="Project workspace onboarding"
>
	{#if mode === 'selection'}
		<div class="selection-shell">
			<header class="selection-header">
				<h2>Create Project Workspace</h2>
				<p>Choose Blank, Template, or Tora AI to set up this workspace.</p>
			</header>

			<div class="selection-actions">
				<button type="button" class="selection-btn blank" on:click={openBlankFlow}>
					<span class="selection-icon" aria-hidden="true">
						<svg viewBox="0 0 24 24">
							<path d="M6 6h12v12H6z"></path>
							<path d="M9 9h6M9 12h6M9 15h3"></path>
						</svg>
					</span>
					<span class="selection-copy">
						<strong>Blank</strong>
						<small>Start with an empty board and shape the structure yourself.</small>
					</span>
				</button>

				<button type="button" class="selection-btn manual" on:click={openTemplateFlow}>
					<span class="selection-icon" aria-hidden="true">
						<svg viewBox="0 0 24 24">
							<rect x="4.5" y="4.5" width="6.5" height="6.5" rx="1.5"></rect>
							<rect x="13" y="4.5" width="6.5" height="6.5" rx="1.5"></rect>
							<rect x="4.5" y="13" width="6.5" height="6.5" rx="1.5"></rect>
							<rect x="13" y="13" width="6.5" height="6.5" rx="1.5"></rect>
						</svg>
					</span>
					<span class="selection-copy">
						<strong>Template</strong>
						<small>Browse starter templates, preview the setup, then apply one when ready.</small>
					</span>
				</button>

				<button
					type="button"
					class="selection-btn ai"
					class:is-disabled={!aiEnabled}
					on:click={openAIFlow}
					aria-disabled={!aiEnabled}
					title={aiEnabled
						? 'Generate a workspace with Tora AI'
						: 'Tora AI is unavailable in this room'}
				>
					<span class="selection-icon" aria-hidden="true">
						<svg viewBox="0 0 24 24">
							<path d="M12 3.5 13.8 8l4.7 1.8-4.7 1.8L12 16l-1.8-4.4L5.5 9.8 10.2 8 12 3.5Z"></path>
							<path d="M18.5 13.5 19.4 15.7l2.1.9-2.1.8-.9 2.2-.8-2.2-2.2-.8 2.2-.9.8-2.2Z"></path>
						</svg>
					</span>
					<span class="selection-copy">
						<strong>Tora AI</strong>
						<small>
							{aiEnabled
								? 'Describe your project and auto-generate structure.'
								: 'Unavailable in this room. Blank and Template are still available.'}
						</small>
					</span>
				</button>
			</div>
			{#if !aiEnabled}
				<p class="ai-disabled-note">Tora AI is unavailable in this room.</p>
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
								<div class="ai-response-body">
									<RichTextContent text={message.text} />
								</div>
							</article>
						{/if}
					{/each}
				{/if}
				{#if $timelineLoading || aiWorkflowEntries.length > 0}
					<div class="ai-loading-panel">
						{#if $timelineLoading}
							<div class="ai-loading-row">
								<span class="ai-spinner" aria-hidden="true"></span>
								<div class="ai-loading-copy">
									<strong>{streamingStep || 'Generating workspace...'}</strong>
									<p>Live workflow updates and partial task drops appear here as Tora builds the board.</p>
									{#if getOnboardingWorkflowSummary()}
										<span class="ai-loading-progress">{getOnboardingWorkflowSummary()}</span>
									{/if}
								</div>
							</div>
						{:else}
							<div class="ai-loading-copy">
								<strong>Latest AI workflow</strong>
								<p>The last run has finished. Its step history stays here so interruptions do not erase what Tora already showed you.</p>
								{#if getOnboardingWorkflowSummary()}
									<span class="ai-loading-progress">{getOnboardingWorkflowSummary()}</span>
								{/if}
							</div>
						{/if}
						{#if aiWorkflowEntries.length > 0}
							<div class="ai-workflow-list" role="status" aria-live="polite">
								{#each aiWorkflowEntries as entry (entry.id)}
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
										{#if getOnboardingWorkflowEntryTimingChips(entry).length > 0 || (entry.taskTitles && entry.taskTitles.length > 0)}
											<div class="ai-workflow-tags">
												{#each getOnboardingWorkflowEntryTimingChips(entry) as chip}
													<span>{chip}</span>
												{/each}
												{#each entry.taskTitles ?? [] as item}
													<span>{item}</span>
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

			{#if aiConversation.length === 0}
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
			{/if}

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
				<div class="tora-error" role="status" aria-live="polite">
					{localError || $timelineError}
				</div>
			{/if}

			<form class="tora-composer" on:submit|preventDefault={() => void generateWorkspace()}>
				<div class="tora-input-box">
					<textarea
						class="tora-textarea"
						bind:this={aiComposerTextarea}
						bind:value={aiPrompt}
						use:autoResize={aiPrompt}
						rows="1"
						placeholder="Describe the workspace you want Tora to generate..."
						on:keydown={handleAIPromptKeydown}
						disabled={$timelineLoading}
					></textarea>
					<div class="tora-toolbar">
						<span class="tora-hint" class:tora-hint-warn={[...aiPrompt].length > 2700}>
							{#if aiPrompt.trim().length === 0}
								Include sprint count, budget, owners, and deadline
							{:else if [...aiPrompt].length > 2700}
								{[...aiPrompt].length}/3000
							{:else}
								Enter to send
							{/if}
						</span>
						<button
							type="button"
							class="fit-tier-toggle"
							class:active={fitToTier}
							on:click={() => { fitToTier = !fitToTier; }}
							title={fitToTier
								? `Fit to ${$sessionTier} plan: ON — AI will stay within your tier limits`
								: 'Fit to plan: OFF — AI generates at full quality (may hit limits)'}
						>
							<svg viewBox="0 0 14 14" aria-hidden="true">
								<path d="M1 3h12M3 7h8M5 11h4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
							</svg>
							<span>Fit to {$sessionTier}</span>
						</button>
						<div class="toolbar-spacer"></div>
						{#if $timelineLoading}
							<button
								type="button"
								class="send-btn is-stop"
								on:click={stopWorkspaceGeneration}
								aria-label="Stop workspace generation"
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
								disabled={!aiPrompt.trim()}
								aria-label="Generate workspace"
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
	{:else}
		<div class="wizard-shell">
			<header class="wizard-head">
				<div class="wizard-title-block">
					<h3>{templatePickerOnly ? 'Change Template' : 'Workspace Setup'}</h3>
					<p>
						Choose a starter template, preview the board setup, then apply it when ready.
					</p>
				</div>
				<div class="wizard-head-actions">
					{#if templatePickerOnly}
						<button type="button" class="ghost-btn" on:click={() => dispatch('close')}>Close</button
						>
					{:else}
						<button type="button" class="back-btn" on:click={goBackToSelection}>Back</button>
					{/if}
				</div>
			</header>

			{#if manualStep === 'picker'}
				<div class="picker-stack">
					<section class="picker-section">
						<div class="template-intro-card">
							<div>
								<strong>Starter template</strong>
								<p>
									Choose the board style you want to start from. Each template already defines its
									own structure, starter fields, and sample work.
								</p>
							</div>
							<div class="template-intro-meta">
								<span>{availableTemplates.length} options</span>
								<span>
									{#if templatesLoading && selectedTemplate?.id !== BLANK_TEMPLATE_ID}
										Loading...
									{:else if selectedTemplate}
										{selectedTemplate.name}
									{:else}
										Select one
									{/if}
								</span>
							</div>
						</div>

						{#if templateLoadError}
							<div class="error-banner">
								<span>{templateLoadError}</span>
								<button type="button" class="inline-link-btn" on:click={retryTemplateLoad}
									>Retry</button
								>
							</div>
						{/if}

						<div class="template-grid">
							{#each availableTemplates as template (template.id)}
								<button
									type="button"
									class="template-card"
									class:is-selected={selectedTemplateId === template.id}
									on:click={() => selectTemplateCard(template.id)}
									disabled={applyingTemplate}
								>
									<div class="template-card-top">
										<span class="template-pill">{template.industries[0] || 'Starter'}</span>
										<span class="template-counts">
											{template.fieldSchemas.length} fields · {template.sampleTasks.length} tasks
										</span>
									</div>
									<strong>{template.name}</strong>
									<p>{template.description}</p>
									<div class="template-card-meta">
										{#if template.fieldSchemas.length > 0}
											<span
												>{template.fieldSchemas
													.slice(0, 3)
													.map((field) => field.name)
													.join(' · ')}</span
											>
										{:else}
											<span>No starter data</span>
										{/if}
									</div>
								</button>
							{/each}
						</div>
					</section>
				</div>

				<div class="wizard-actions">
					{#if !templatePickerOnly}
						<button type="button" class="ghost-btn" on:click={goBackToSelection}>Back</button>
					{/if}
					<button
						type="button"
						class="primary-btn"
						on:click={reviewSelectedTemplate}
						disabled={!selectedTemplate ||
							applyingTemplate ||
							(templatesLoading && selectedTemplate?.id !== BLANK_TEMPLATE_ID)}
					>
						{selectedTemplate?.id === BLANK_TEMPLATE_ID ? 'Review blank setup' : 'Review selection'}
					</button>
				</div>
			{:else}
				<section class="template-preview-shell">
					<div class="template-preview-head">
						<div>
							<h4>{selectedTemplate?.name}</h4>
							<p>{selectedTemplate?.description}</p>
						</div>
						<div class="template-preview-badges">
							<span class="template-pill subtle"
								>{selectedTemplate?.industries.join(' · ') || 'Blank'}</span
							>
						</div>
					</div>

					<div class="template-preview-summary">
						<div class="summary-card">
							<span class="summary-label">Starter fields</span>
							<strong>{templatePreviewFields.length}</strong>
						</div>
						<div class="summary-card">
							<span class="summary-label">Starter tasks</span>
							<strong>{templatePreviewTasks.length}</strong>
						</div>
						<div class="summary-card">
							<span class="summary-label">Automation presets</span>
							<strong>{selectedTemplate?.automationRules.length ?? 0}</strong>
						</div>
					</div>

					{#if selectedTemplate?.id === BLANK_TEMPLATE_ID}
						<div class="template-preview-note">
							<strong>Blank workspace</strong>
							<p>This keeps the board clean so you can shape the structure yourself.</p>
						</div>
					{:else}
						<div class="template-preview-grid">
							<div class="preview-list-card">
								<h5>Fields to create</h5>
								<ul>
									{#each templatePreviewFields as field (field.name)}
										<li>
											<span>{field.name}</span>
											<small>{field.fieldType}</small>
										</li>
									{/each}
								</ul>
							</div>
							<div class="preview-list-card">
								<h5>Starter tasks</h5>
								<ul>
									{#each templatePreviewTasks as task (task.title)}
										<li>
											<span>{task.title}</span>
											<small>{task.sprintName || 'Backlog'}</small>
										</li>
									{/each}
								</ul>
							</div>
						</div>
					{/if}

					{#if roomHasExistingContent}
						<label class="replace-warning-card">
							<input type="checkbox" bind:checked={confirmReplaceExisting} />
							<div>
								<strong>Replace existing workspace content</strong>
								<p>
									This will remove the current room tasks, custom fields, and saved automation
									presets before the new template is applied.
								</p>
							</div>
						</label>
					{/if}

					<div class="wizard-actions compact">
						<button type="button" class="ghost-btn" on:click={goBackToTemplatePicker}>Back</button>
						<button
							type="button"
							class="primary-btn"
							on:click={() => void applySelectedTemplate()}
							disabled={applyingTemplate || (roomHasExistingContent && !confirmReplaceExisting)}
						>
							{#if applyingTemplate}
								Applying...
							{:else if selectedTemplate?.id === BLANK_TEMPLATE_ID}
								Start blank
							{:else}
								Apply template
							{/if}
						</button>
					</div>
				</section>
			{/if}
		</div>
	{/if}

	{#if (localError || $timelineError) && mode !== 'ai'}
		<div class="error-banner standalone">{localError || $timelineError}</div>
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
		min-width: 0;
		box-sizing: border-box;
		display: flex;
		flex-direction: column;
		gap: 0.9rem;
		padding: 1rem;
		background: var(--po-bg);
		color: var(--po-text);
		overflow-y: auto;
	}

	.project-onboarding.is-modal {
		padding: 0;
		background: transparent;
	}

	.selection-shell,
	.wizard-shell {
		flex: 1 1 0;
		min-height: 0;
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
		overflow-y: auto;
	}

	.wizard-shell {
		overflow-y: auto;
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
		grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
		gap: 0.95rem;
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

	.selection-btn:disabled,
	.selection-btn.is-disabled {
		cursor: not-allowed;
		opacity: 0.72;
	}

	.selection-btn:disabled:hover,
	.selection-btn.is-disabled:hover {
		transform: none;
		border-color: var(--po-border);
		background: var(--po-surface);
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

	.selection-btn.is-disabled .selection-icon,
	.selection-btn:disabled .selection-icon {
		background: color-mix(in srgb, var(--po-surface-soft) 85%, var(--po-surface));
		border-color: color-mix(in srgb, var(--po-border) 90%, transparent);
	}

	.selection-btn.is-disabled .selection-icon svg,
	.selection-btn:disabled .selection-icon svg {
		stroke: var(--po-muted);
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

	.picker-stack {
		display: grid;
		gap: 1rem;
	}

	.picker-section {
		display: grid;
		gap: 0.78rem;
	}

	.wizard-head {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 1rem;
	}

	.wizard-title-block {
		display: grid;
		gap: 0.24rem;
	}

	.wizard-title-block h3 {
		margin: 0;
		font-size: 1rem;
	}

	.wizard-title-block p {
		margin: 0;
		font-size: 0.78rem;
		line-height: 1.45;
		color: var(--po-muted);
	}

	.wizard-head-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.55rem;
	}

	.back-btn,
	.ghost-btn,
	.primary-btn,
	.inline-link-btn {
		border-radius: 10px;
		padding: 0.56rem 0.9rem;
		font-size: 0.8rem;
		font-weight: 600;
		cursor: pointer;
		transition:
			border-color 0.16s ease,
			background 0.16s ease,
			color 0.16s ease,
			transform 0.16s ease;
	}

	.back-btn,
	.ghost-btn {
		border: 1px solid var(--po-border);
		background: var(--po-surface);
		color: var(--po-text);
	}

	.back-btn:hover,
	.ghost-btn:hover:not(:disabled) {
		border-color: var(--po-border-strong);
		background: color-mix(in srgb, var(--po-accent-soft) 45%, var(--po-surface));
	}

	.primary-btn {
		border: 1px solid color-mix(in srgb, var(--po-accent) 60%, var(--po-border));
		background: color-mix(in srgb, var(--po-accent-soft) 88%, var(--po-surface));
		color: var(--po-text);
	}

	.primary-btn:hover:not(:disabled) {
		transform: translateY(-1px);
		border-color: color-mix(in srgb, var(--po-accent) 72%, var(--po-border));
		background: color-mix(in srgb, var(--po-accent-soft) 100%, var(--po-surface));
	}

	.primary-btn:disabled,
	.ghost-btn:disabled,
	.inline-link-btn:disabled {
		opacity: 0.58;
		cursor: not-allowed;
		transform: none;
	}

	.inline-link-btn {
		border: none;
		background: transparent;
		padding: 0;
		color: inherit;
		text-decoration: underline;
		text-underline-offset: 0.16rem;
	}

	.template-intro-card {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 1rem;
		padding: 0.95rem 1rem;
		border-radius: 16px;
		border: 1px solid color-mix(in srgb, var(--po-border) 86%, transparent);
		background: linear-gradient(135deg, var(--po-surface) 0%, var(--po-surface-soft) 100%);
	}

	.template-intro-card strong {
		display: block;
		font-size: 0.92rem;
	}

	.template-intro-card p {
		margin: 0.36rem 0 0;
		font-size: 0.78rem;
		line-height: 1.48;
		color: var(--po-muted);
		max-width: 48rem;
	}

	.template-intro-meta {
		display: grid;
		gap: 0.42rem;
		justify-items: end;
		font-size: 0.72rem;
		font-weight: 600;
		color: var(--po-muted);
	}

	.template-grid {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.78rem;
	}

	.template-card {
		border: 1px solid var(--po-border);
		border-radius: 16px;
		background: var(--po-surface);
		color: var(--po-text);
		text-align: left;
		padding: 0.95rem;
		cursor: pointer;
		display: grid;
		gap: 0.62rem;
		transition:
			border-color 0.16s ease,
			transform 0.16s ease,
			background 0.16s ease,
			box-shadow 0.16s ease;
	}

	.template-card:hover:not(:disabled) {
		transform: translateY(-1px);
		border-color: var(--po-border-strong);
		background: color-mix(in srgb, var(--po-accent-soft) 45%, var(--po-surface));
	}

	.template-card.is-selected {
		border-color: color-mix(in srgb, var(--po-accent) 72%, var(--po-border));
		background: color-mix(in srgb, var(--po-accent-soft) 78%, var(--po-surface));
		box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--po-accent) 20%, transparent);
	}

	.template-card-top,
	.template-card-meta {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		flex-wrap: wrap;
	}

	.template-card strong {
		display: block;
		font-size: 0.95rem;
	}

	.template-card p {
		margin: 0;
		font-size: 0.82rem;
		line-height: 1.48;
		color: var(--po-muted);
	}

	.template-pill {
		display: inline-flex;
		align-items: center;
		border-radius: 999px;
		padding: 0.18rem 0.52rem;
		font-size: 0.64rem;
		font-weight: 700;
		letter-spacing: 0.03em;
		text-transform: uppercase;
		border: 1px solid color-mix(in srgb, var(--po-accent) 34%, var(--po-border));
		background: color-mix(in srgb, var(--po-accent-soft) 66%, var(--po-surface));
		color: var(--po-text);
	}

	.template-pill.subtle {
		font-size: 0.66rem;
		text-transform: none;
		letter-spacing: 0;
		font-weight: 600;
	}

	.template-counts,
	.template-card-meta {
		font-size: 0.72rem;
		color: var(--po-muted);
	}

	.template-preview-shell {
		display: grid;
		gap: 1rem;
	}

	.template-preview-head {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 1rem;
	}

	.template-preview-badges {
		display: inline-flex;
		align-items: center;
		justify-content: flex-end;
		gap: 0.45rem;
		flex-wrap: wrap;
	}

	.template-preview-head h4 {
		margin: 0;
		font-size: 1rem;
	}

	.template-preview-head p {
		margin: 0.32rem 0 0;
		font-size: 0.8rem;
		line-height: 1.5;
		color: var(--po-muted);
	}

	.template-preview-summary {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
		gap: 0.72rem;
	}

	.summary-card,
	.preview-list-card,
	.replace-warning-card,
	.template-preview-note {
		border-radius: 16px;
		border: 1px solid color-mix(in srgb, var(--po-border) 86%, transparent);
		background: color-mix(in srgb, var(--po-surface-soft) 72%, var(--po-surface));
	}

	.summary-card {
		padding: 0.82rem 0.9rem;
		display: grid;
		gap: 0.24rem;
	}

	.summary-label {
		font-size: 0.7rem;
		font-weight: 700;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--po-muted);
	}

	.summary-card strong {
		font-size: 1.2rem;
	}

	.template-preview-grid {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.8rem;
	}

	.preview-list-card {
		padding: 0.9rem;
	}

	.preview-list-card h5 {
		margin: 0;
		font-size: 0.84rem;
	}

	.preview-list-card ul {
		margin: 0.72rem 0 0;
		padding: 0;
		list-style: none;
		display: grid;
		gap: 0.6rem;
	}

	.preview-list-card li {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.8rem;
		font-size: 0.8rem;
	}

	.preview-list-card small {
		color: var(--po-muted);
		font-size: 0.72rem;
	}

	.template-preview-note {
		padding: 0.95rem 1rem;
	}

	.template-preview-note strong {
		display: block;
		font-size: 0.9rem;
	}

	.template-preview-note p {
		margin: 0.3rem 0 0;
		font-size: 0.78rem;
		line-height: 1.46;
		color: var(--po-muted);
	}

	.replace-warning-card {
		display: grid;
		grid-template-columns: auto 1fr;
		gap: 0.8rem;
		padding: 0.95rem 1rem;
		cursor: pointer;
	}

	.replace-warning-card input {
		margin-top: 0.22rem;
	}

	.replace-warning-card strong {
		display: block;
		font-size: 0.84rem;
	}

	.replace-warning-card p {
		margin: 0.32rem 0 0;
		font-size: 0.76rem;
		line-height: 1.5;
		color: var(--po-muted);
	}

	.wizard-actions {
		display: flex;
		align-items: center;
		justify-content: flex-end;
		gap: 0.62rem;
	}

	.wizard-actions.compact {
		padding-top: 0.2rem;
	}

	.tora-chat {
		flex: 1 1 0;
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

	.tora-back-btn,
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

	.tora-back-btn:hover,
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

	.ai-loading-panel {
		display: grid;
		gap: 0.7rem;
		padding: 0.8rem 0.88rem;
		border-radius: 16px;
		border: 1px solid rgba(255, 255, 255, 0.08);
		background:
			linear-gradient(180deg, rgba(255, 255, 255, 0.05), rgba(255, 255, 255, 0.03)),
			rgba(17, 18, 22, 0.92);
	}

	.ai-loading-row {
		display: flex;
		align-items: flex-start;
		gap: 0.62rem;
		font-size: 0.78rem;
		color: #9aa0a6;
	}

	.ai-loading-copy {
		display: grid;
		gap: 0.18rem;
	}

	.ai-loading-copy strong {
		font-size: 0.82rem;
		color: #f3f6fb;
	}

	.ai-loading-copy p {
		margin: 0;
		font-size: 0.72rem;
		line-height: 1.5;
		color: #9aa0a6;
	}

	.ai-spinner {
		width: 14px;
		height: 14px;
		border: 2px solid rgba(255, 255, 255, 0.12);
		border-top-color: #1a73e8;
		border-radius: 999px;
		animation: tora-spin 0.8s linear infinite;
	}

	.ai-workflow-list {
		display: grid;
		gap: 0.46rem;
	}

	.ai-workflow-entry {
		display: grid;
		gap: 0.28rem;
		padding: 0.56rem 0.62rem;
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
		font-size: 0.77rem;
		color: #eef2f7;
	}

	.ai-workflow-entry p {
		margin: 0;
		font-size: 0.72rem;
		line-height: 1.5;
		color: #b7bec8;
	}

	.ai-workflow-progress {
		border-radius: 999px;
		padding: 0.12rem 0.4rem;
		font-size: 0.62rem;
		font-weight: 700;
		letter-spacing: 0.02em;
		background: rgba(26, 115, 232, 0.14);
		color: #8ab4f8;
	}

	.ai-workflow-tags {
		display: flex;
		flex-wrap: wrap;
		gap: 0.36rem;
	}

	.ai-workflow-tags span {
		border-radius: 999px;
		padding: 0.16rem 0.44rem;
		font-size: 0.64rem;
		background: rgba(255, 255, 255, 0.06);
		color: #d3d9e2;
	}

	.ai-workflow-entry.tone-success {
		border-color: rgba(86, 179, 127, 0.26);
		background: rgba(86, 179, 127, 0.08);
	}

	.ai-workflow-entry.tone-warning {
		border-color: rgba(245, 158, 11, 0.28);
		background: rgba(245, 158, 11, 0.08);
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

	.fit-tier-toggle {
		display: inline-flex;
		align-items: center;
		gap: 0.28rem;
		padding: 0.2rem 0.55rem;
		border-radius: 999px;
		border: 1px solid rgba(255, 255, 255, 0.14);
		background: transparent;
		color: var(--po-text-muted, rgba(200, 210, 230, 0.6));
		font-size: 0.62rem;
		font-weight: 600;
		font-family: 'JetBrains Mono', monospace;
		letter-spacing: 0.04em;
		cursor: pointer;
		transition: color 0.15s, border-color 0.15s, background 0.15s;
		white-space: nowrap;
	}
	.fit-tier-toggle svg {
		width: 12px;
		height: 12px;
		flex-shrink: 0;
		fill: none;
	}
	.fit-tier-toggle:hover {
		color: var(--po-accent, #7eb3f7);
		border-color: rgba(126, 179, 247, 0.3);
	}
	.fit-tier-toggle.active {
		color: #7eb3f7;
		border-color: rgba(126, 179, 247, 0.45);
		background: rgba(126, 179, 247, 0.1);
	}

	.error-banner {
		border-radius: 12px;
		border: 1px solid color-mix(in srgb, var(--po-danger) 45%, var(--po-border));
		background: var(--po-danger-soft);
		color: var(--po-danger);
		padding: 0.68rem 0.8rem;
		font-size: 0.8rem;
		font-weight: 600;
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.8rem;
	}

	.error-banner.standalone {
		padding: 0.62rem 0.76rem;
		font-size: 0.84rem;
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

	@keyframes tora-spin {
		to {
			transform: rotate(360deg);
		}
	}

	@media (max-width: 900px) {
		.selection-actions,
		.template-grid,
		.template-preview-grid,
		.template-preview-summary {
			grid-template-columns: minmax(0, 1fr);
		}

		.tora-chat-header,
		.wizard-head,
		.template-intro-card,
		.template-preview-head {
			flex-wrap: wrap;
		}

		.tora-meta,
		.template-intro-meta {
			width: 100%;
			justify-content: space-between;
			justify-items: start;
		}

		.tora-thread {
			padding: 0.72rem;
		}

		.partial-warning-banner {
			margin: 0 0.72rem 0.54rem;
		}

		.wizard-actions,
		.replace-warning-card,
		.preview-list-card li {
			align-items: flex-start;
		}
	}
</style>
