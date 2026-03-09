<script lang="ts">
	import {
		activeProjectTab,
		generateAITimeline,
		isProjectNew,
		setProjectTimeline,
		timelineError,
		timelineLoading
	} from '$lib/stores/timeline';
	import { initializeTaskStoreForRoom } from '$lib/stores/tasks';
	import { normalizeRoomIDValue } from '$lib/utils/chat/core';
	import { loadTemplate } from '$lib/utils/timelineTemplates';
	import type { ProjectTimeline } from '$lib/types/timeline';

	export let roomId = '';

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://localhost:8080';

	type OnboardingMode = 'selection' | 'manual' | 'ai';
	type ManualTemplateCard = {
		key: string;
		label: string;
		description: string;
	};

	const MANUAL_TEMPLATE_CARDS: ManualTemplateCard[] = [
		{
			key: 'software_agile',
			label: 'Software Agile',
			description: 'Backlog, Frontend, Backend, and QA sprint structure.'
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
			key: 'high_volume',
			label: 'High Volume',
			description: 'Bucket workflow for triage, processing, and review.'
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
	}

	async function generateWorkspace() {
		const normalizedRoomID = roomId.trim();
		const normalizedPrompt = aiPrompt.trim();
		localError = '';
		if (!normalizedRoomID) {
			localError = 'Room id is required before generating a workspace.';
			return;
		}
		if (!normalizedPrompt) {
			localError = 'Describe your project before generating.';
			return;
		}

		try {
			await generateAITimeline(normalizedRoomID, normalizedPrompt);
			await initializeTaskStoreForRoom(normalizeRoomIDValue(normalizedRoomID), {
				apiBase: API_BASE
			});
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

		if (templateKey === 'blank_board') {
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
			await loadTemplate(normalizedRoomID, templateKey);
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

			<div class="selection-actions">
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
			</div>
		</div>
	{:else if mode === 'ai'}
		<div class="wizard-shell">
			<header class="wizard-head">
				<button type="button" class="back-btn" on:click={goBackToSelection}>Back</button>
				<h3>Tora AI Workspace Generator</h3>
			</header>

			<label class="field">
				<span>Project description</span>
				<textarea
					bind:value={aiPrompt}
					placeholder="Example: Build a multi-team launch plan for a mobile app with 3 sprints and clear ownership."
					rows="7"
				></textarea>
			</label>

			<div class="wizard-actions">
				<button
					type="button"
					class="primary-btn"
					on:click={() => {
						void generateWorkspace();
					}}
					disabled={$timelineLoading}
				>
					{$timelineLoading ? 'Generating...' : 'Generate Workspace'}
				</button>
			</div>
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
	.project-onboarding {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: 1fr auto;
		gap: 0.85rem;
		padding: 1rem;
		background:
			radial-gradient(circle at 12% -10%, rgba(255, 255, 255, 0.08), transparent 34%),
			#0d0d12;
		color: #f2f6ff;
	}

	.selection-shell,
	.wizard-shell {
		border: 1px solid rgba(255, 255, 255, 0.1);
		border-radius: 18px;
		background: rgba(255, 255, 255, 0.03);
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
		box-shadow: 0 22px 46px rgba(0, 0, 0, 0.36);
	}

	.selection-shell {
		display: grid;
		align-content: center;
		justify-items: center;
		gap: 1.2rem;
		padding: 1.6rem;
	}

	.selection-header {
		text-align: center;
	}

	.selection-header h2 {
		margin: 0;
		font-size: 1.24rem;
	}

	.selection-header p {
		margin: 0.42rem 0 0;
		color: rgba(213, 221, 242, 0.76);
		font-size: 0.9rem;
	}

	.selection-actions {
		width: min(920px, 100%);
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.95rem;
	}

	.selection-btn {
		border: 1px solid rgba(255, 255, 255, 0.18);
		background: rgba(255, 255, 255, 0.04);
		border-radius: 16px;
		padding: 1.1rem;
		display: grid;
		grid-template-columns: auto 1fr;
		gap: 0.78rem;
		align-items: center;
		text-align: left;
		cursor: pointer;
		color: #f3f6ff;
		transition:
			transform 0.18s ease,
			background 0.18s ease,
			border-color 0.18s ease;
	}

	.selection-btn:hover {
		transform: translateY(-2px);
		border-color: rgba(169, 203, 255, 0.72);
		background: rgba(255, 255, 255, 0.1);
	}

	.selection-icon {
		width: 2.5rem;
		height: 2.5rem;
		border-radius: 12px;
		display: grid;
		place-items: center;
		background: rgba(255, 255, 255, 0.08);
		border: 1px solid rgba(255, 255, 255, 0.15);
	}

	.selection-icon svg {
		width: 1.2rem;
		height: 1.2rem;
		stroke: currentColor;
		stroke-width: 1.7;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.selection-btn.ai .selection-icon svg {
		stroke: #b7cdfc;
	}

	.selection-copy strong {
		display: block;
		font-size: 0.95rem;
	}

	.selection-copy small {
		display: block;
		margin-top: 0.24rem;
		color: rgba(205, 214, 236, 0.75);
		font-size: 0.78rem;
		line-height: 1.34;
	}

	.wizard-shell {
		display: grid;
		gap: 0.95rem;
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
		font-size: 0.96rem;
	}

	.back-btn,
	.primary-btn {
		border-radius: 10px;
		border: 1px solid rgba(255, 255, 255, 0.18);
		background: rgba(255, 255, 255, 0.06);
		color: #f2f7ff;
		padding: 0.5rem 0.76rem;
		font-size: 0.79rem;
		cursor: pointer;
	}

	.back-btn:hover,
	.primary-btn:hover:not(:disabled) {
		border-color: rgba(173, 203, 255, 0.66);
		background: rgba(255, 255, 255, 0.12);
	}

	.primary-btn:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}

	.field {
		display: grid;
		gap: 0.38rem;
	}

	.field span {
		font-size: 0.72rem;
		font-weight: 700;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: rgba(188, 199, 224, 0.82);
	}

	.field textarea {
		width: 100%;
		border-radius: 12px;
		border: 1px solid rgba(255, 255, 255, 0.16);
		background: rgba(255, 255, 255, 0.04);
		color: #f3f7ff;
		padding: 0.72rem 0.8rem;
		resize: vertical;
	}

	.field textarea::placeholder {
		color: rgba(191, 200, 223, 0.64);
	}

	.wizard-actions {
		display: flex;
		justify-content: flex-end;
	}

	.template-grid {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.72rem;
	}

	.template-card {
		border: 1px solid rgba(255, 255, 255, 0.14);
		border-radius: 14px;
		background: rgba(255, 255, 255, 0.04);
		color: #f2f7ff;
		text-align: left;
		padding: 0.8rem;
		cursor: pointer;
		transition:
			border-color 0.16s ease,
			transform 0.16s ease,
			background 0.16s ease;
	}

	.template-card strong {
		display: block;
		font-size: 0.87rem;
	}

	.template-card p {
		margin: 0.35rem 0 0;
		font-size: 0.76rem;
		line-height: 1.36;
		color: rgba(205, 214, 236, 0.76);
	}

	.template-card:hover:not(:disabled) {
		transform: translateY(-1px);
		border-color: rgba(174, 205, 255, 0.62);
		background: rgba(255, 255, 255, 0.1);
	}

	.error-banner {
		border-radius: 12px;
		border: 1px solid rgba(248, 113, 113, 0.36);
		background: rgba(220, 38, 38, 0.16);
		color: #ffd7de;
		padding: 0.58rem 0.72rem;
		font-size: 0.8rem;
	}

	@media (max-width: 900px) {
		.selection-actions,
		.template-grid {
			grid-template-columns: minmax(0, 1fr);
		}
	}
</style>
