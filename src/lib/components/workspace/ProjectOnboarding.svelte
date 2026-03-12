<script lang="ts">
	import {
		activeProjectTab,
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
	let aiAssistantReply = '';

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
		aiAssistantReply = '';
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
	}

	async function generateWorkspace() {
		const normalizedRoomID = roomId.trim();
		const normalizedPrompt = aiPrompt.trim();
		localError = '';
		aiPartialWarning = '';
		aiMissingSprints = [];
		aiAssistantReply = '';
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

		try {
			const generationResult = await generateAITimeline(normalizedRoomID, normalizedPrompt);
			const generatedTimeline = generationResult.timeline;
			aiAssistantReply = generationResult.assistantReply;
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
								<path d="M12 3.5 13.8 8l4.7 1.8-4.7 1.8L12 16l-1.8-4.4L5.5 9.8 10.2 8 12 3.5Z"></path>
								<path d="M18.5 13.5 19.4 15.7l2.1.9-2.1.8-.9 2.2-.8-2.2-2.2-.8 2.2-.9.8-2.2Z"></path>
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
		<div class="wizard-shell ai-wizard">
			<header class="wizard-head ai-head">
				<button type="button" class="back-btn" on:click={goBackToSelection}>Back</button>
				<div class="ai-head-copy">
					<span class="ai-badge">Tora AI</span>
					<h3>Workspace Generator</h3>
					<p>
						Describe scope, timeline, owners, and constraints. Tora will generate a structured sprint plan.
					</p>
				</div>
			</header>

			<section class="ai-composer-card" aria-label="AI project brief composer">
				<label class="field ai-field">
					<div class="field-head">
						<span>Project brief</span>
						<small>{aiPrompt.trim().length} chars</small>
					</div>
					<textarea
						class="ai-textarea"
						bind:value={aiPrompt}
						placeholder="Example: Build a multi-team launch plan for a mobile app with 3 sprints, clear ownership, budgets, and key dependencies."
						rows="8"
					></textarea>
				</label>

				<div class="prompt-starters" aria-label="Prompt starters">
					{#each AI_PROMPT_STARTERS as starter (starter.label)}
						<button
							type="button"
							class="starter-chip"
							on:click={() => applyPromptStarter(starter.prompt)}
						>
							{starter.label}
						</button>
					{/each}
				</div>
			</section>

			<div class="wizard-actions ai-actions">
				<p class="ai-hint">Tip: include sprint count, budget cap, owners, and fixed deadlines.</p>
				<button
					type="button"
					class="primary-btn generate-btn"
					on:click={() => {
						void generateWorkspace();
					}}
					disabled={$timelineLoading}
				>
					{$timelineLoading ? 'Generating...' : 'Generate Workspace'}
				</button>
			</div>

			{#if aiAssistantReply}
				<div class="ai-assistant-banner" aria-live="polite">{aiAssistantReply}</div>
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
		</div>
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

	{#if localError || $timelineError}
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

	.ai-wizard {
		border-color: var(--po-ai-shell-border);
		background: var(--po-ai-shell-bg);
		box-shadow: var(--po-ai-shell-shadow);
		color: #e8eaed;
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

	.back-btn,
	.primary-btn {
		border-radius: 10px;
		border: 1px solid var(--po-border);
		background: var(--po-surface);
		color: var(--po-text);
		padding: 0.52rem 0.8rem;
		font-size: 0.82rem;
		font-weight: 600;
		cursor: pointer;
	}

	.back-btn:hover,
	.primary-btn:hover:not(:disabled) {
		border-color: var(--po-border-strong);
		background: color-mix(in srgb, var(--po-accent-soft) 45%, var(--po-surface));
	}

	.primary-btn:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}

	.ai-head {
		align-items: flex-start;
	}

	.ai-head-copy {
		display: grid;
		gap: 0.28rem;
	}

	.ai-badge {
		width: fit-content;
		border-radius: 999px;
		padding: 0.24rem 0.56rem;
		font-size: 0.68rem;
		font-weight: 700;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--po-ai-chip-text);
		background: var(--po-ai-chip-bg);
		border: 1px solid var(--po-ai-chip-border);
	}

	.ai-head h3 {
		margin: 0;
		font-size: 1.08rem;
	}

	.ai-head p {
		margin: 0;
		font-size: 0.82rem;
		line-height: 1.45;
		color: #9aa0a6;
		max-width: 640px;
	}

	.field {
		display: grid;
		gap: 0.4rem;
	}

	.field-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.65rem;
	}

	.field span {
		font-size: 0.74rem;
		font-weight: 700;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--po-muted);
	}

	.field textarea {
		width: 100%;
		border-radius: 12px;
		border: 1px solid var(--po-border);
		background: var(--po-surface);
		color: var(--po-text);
		padding: 0.78rem 0.84rem;
		font-size: 0.92rem;
		line-height: 1.45;
		resize: vertical;
	}

	.field textarea::placeholder {
		color: var(--po-muted);
	}

	.ai-composer-card {
		border: 1px solid var(--po-ai-field-border);
		border-radius: 20px;
		background: rgba(255, 255, 255, 0.02);
		padding: 0.95rem;
		display: grid;
		gap: 0.72rem;
	}

	.ai-field {
		gap: 0.52rem;
	}

	.ai-field .field-head small {
		font-size: 0.72rem;
		color: #9aa0a6;
	}

	.ai-textarea {
		min-height: 190px;
		max-height: 340px;
		border-radius: 14px;
		border: 1px solid var(--po-ai-field-border);
		background: var(--po-ai-field-bg);
		color: #e8eaed;
		padding: 0.95rem 1rem;
		font-size: 0.9rem;
		line-height: 1.5;
	}

	.ai-textarea:focus {
		outline: none;
		border-color: var(--po-ai-field-focus);
		box-shadow: 0 0 0 2px rgba(26, 115, 232, 0.22);
	}

	.ai-textarea::placeholder {
		color: #5f6368;
	}

	.prompt-starters {
		display: flex;
		flex-wrap: wrap;
		gap: 0.46rem;
	}

	.starter-chip {
		border: 1px solid var(--po-ai-chip-border);
		background: var(--po-ai-chip-bg);
		color: var(--po-ai-chip-text);
		border-radius: 999px;
		padding: 0.34rem 0.62rem;
		font-size: 0.74rem;
		font-weight: 600;
		cursor: pointer;
		transition:
			border-color 0.14s ease,
			background 0.14s ease,
			transform 0.14s ease;
	}

	.starter-chip:hover {
		background: var(--po-ai-chip-hover-bg);
		border-color: rgba(26, 115, 232, 0.45);
		transform: translateY(-1px);
	}

	.wizard-actions {
		display: flex;
		justify-content: flex-end;
	}

	.ai-actions {
		align-items: center;
		justify-content: space-between;
		gap: 0.8rem;
		flex-wrap: wrap;
	}

	.ai-hint {
		margin: 0;
		font-size: 0.77rem;
		color: var(--po-ai-hint);
	}

	.ai-assistant-banner {
		border-radius: 12px;
		border: 1px solid rgba(26, 115, 232, 0.28);
		background: rgba(26, 115, 232, 0.12);
		padding: 0.72rem 0.8rem;
		font-size: 0.82rem;
		line-height: 1.5;
		color: #dbe8ff;
	}

	.generate-btn {
		border-color: rgba(26, 115, 232, 0.6);
		background: #1a73e8;
		color: #fff;
		padding: 0.6rem 1.02rem;
		font-size: 0.84rem;
	}

	.generate-btn:hover:not(:disabled) {
		background: #1967d2;
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
		border-radius: 12px;
		border: 1px solid color-mix(in srgb, var(--po-warning) 45%, var(--po-border));
		background: var(--po-warning-soft);
		color: var(--po-warning);
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
		color: color-mix(in srgb, var(--po-warning) 80%, var(--po-text));
	}

	.warning-cta-btn {
		width: fit-content;
		border-radius: 10px;
		border: 1px solid color-mix(in srgb, var(--po-warning) 45%, var(--po-border));
		background: color-mix(in srgb, var(--po-warning-soft) 75%, var(--po-surface));
		color: var(--po-warning);
		padding: 0.48rem 0.76rem;
		font-size: 0.78rem;
		font-weight: 600;
		cursor: pointer;
	}

	.warning-cta-btn:hover {
		border-color: color-mix(in srgb, var(--po-warning) 70%, var(--po-border));
	}

	@media (max-width: 900px) {
		.selection-actions,
		.template-grid {
			grid-template-columns: minmax(0, 1fr);
		}

		.ai-head {
			gap: 0.64rem;
		}

		.ai-head h3 {
			font-size: 1rem;
		}

		.ai-actions {
			align-items: stretch;
		}

		.generate-btn {
			width: 100%;
			justify-content: center;
		}
	}
</style>
