<script lang="ts">
	import type { Sprint, TimelineTask, TimelineTaskStatus } from '$lib/types/timeline';
	import {
		generateAITimeline,
		projectTimeline,
		timelineError,
		timelineLoading
	} from '$lib/stores/timeline';
	import { loadTemplate } from '$lib/utils/timelineTemplates';

	export let roomId = '';

	type TimelineTemplateOption = {
		key: string;
		label: string;
	};

	const TEMPLATE_OPTIONS: TimelineTemplateOption[] = [
		{ key: 'software_mvp', label: 'Software MVP' },
		{ key: 'marketing_campaign', label: 'Marketing Campaign' }
	];

	const KANBAN_COLUMNS: Array<{ key: TimelineTaskStatus; label: string }> = [
		{ key: 'todo', label: 'To Do' },
		{ key: 'in_progress', label: 'In Progress' },
		{ key: 'done', label: 'Done' }
	];

	let aiPrompt = '';
	let selectedSprintID = '';
	let isTemplateMenuOpen = false;

	$: sprints = $projectTimeline?.sprints ?? [];
	$: if (sprints.length > 0 && !sprints.some((sprint) => sprint.id === selectedSprintID)) {
		selectedSprintID = sprints[0].id;
	}
	$: selectedSprint = sprints.find((sprint) => sprint.id === selectedSprintID) ?? null;
	$: progressPercent = $projectTimeline ? Math.max(0, Math.min(100, $projectTimeline.total_progress)) : 0;

	function selectSprint(sprint: Sprint) {
		selectedSprintID = sprint.id;
	}

	function tasksForStatus(status: TimelineTaskStatus) {
		if (!selectedSprint) {
			return [] as TimelineTask[];
		}
		return selectedSprint.tasks.filter((task) => task.status === status);
	}

	function sprintProgress(sprint: Sprint) {
		const total = sprint.tasks.length;
		if (total === 0) {
			return 0;
		}
		const completed = sprint.tasks.filter((task) => task.status === 'done').length;
		return Math.round((completed / total) * 100);
	}

	async function handleGenerate() {
		const normalizedRoomID = roomId.trim();
		const normalizedPrompt = aiPrompt.trim();
		if (!normalizedRoomID) {
			timelineError.set('Room id is required before generating a timeline.');
			return;
		}
		if (!normalizedPrompt) {
			timelineError.set('Describe your project before generating.');
			return;
		}

		isTemplateMenuOpen = false;
		try {
			await generateAITimeline(normalizedRoomID, normalizedPrompt);
		} catch {
			// Error state is already surfaced through the store.
		}
	}

	async function handleTemplateSelection(templateKey: string) {
		const normalizedRoomID = roomId.trim();
		if (!normalizedRoomID) {
			timelineError.set('Room id is required before loading a template.');
			return;
		}
		isTemplateMenuOpen = false;
		try {
			await loadTemplate(normalizedRoomID, templateKey);
		} catch {
			// Error state is already surfaced through the store.
		}
	}
</script>

<section class="timeline-board" aria-label="Project timeline board">
	<div class="control-panel glass-card">
		<div class="template-group">
			<button
				type="button"
				class="template-trigger"
				on:click={() => {
					isTemplateMenuOpen = !isTemplateMenuOpen;
				}}
				aria-expanded={isTemplateMenuOpen}
			>
				1-Click Templates
			</button>
			{#if isTemplateMenuOpen}
				<div class="template-menu" role="menu">
					{#each TEMPLATE_OPTIONS as option}
						<button
							type="button"
							class="template-option"
							on:click={() => {
								void handleTemplateSelection(option.key);
							}}
						>
							{option.label}
						</button>
					{/each}
				</div>
			{/if}
		</div>

		<div class="ai-group">
			<input
				type="text"
				class="ai-input"
				bind:value={aiPrompt}
				placeholder="Describe your project..."
				disabled={$timelineLoading}
			/>
			<button
				type="button"
				class="generate-btn"
				on:click={() => {
					void handleGenerate();
				}}
				disabled={$timelineLoading}
			>
				{$timelineLoading ? 'Generating...' : 'Generate'}
			</button>
		</div>
	</div>

	{#if $timelineError}
		<div class="error-banner">{$timelineError}</div>
	{/if}

	{#if $projectTimeline}
		<section class="timeline-header glass-card">
			<div>
				<h2>{$projectTimeline.project_name}</h2>
				<p>Track progress by sprint and execute with fewer clicks.</p>
			</div>
			<div class="progress-wrap">
				<div class="progress-track" role="progressbar" aria-valuenow={progressPercent} aria-valuemin="0" aria-valuemax="100">
					<div class="progress-fill" style={`width: ${progressPercent}%;`}></div>
				</div>
				<span>{progressPercent.toFixed(1)}% complete</span>
			</div>
		</section>

		<section class="timeline-grid">
			<aside class="sprint-column glass-card">
				<header>
					<h3>Sprint Timeline</h3>
				</header>
				<div class="sprint-list">
					{#each $projectTimeline.sprints as sprint (sprint.id)}
						<button
							type="button"
							class="sprint-card {selectedSprintID === sprint.id ? 'active' : ''}"
							on:click={() => {
								selectSprint(sprint);
							}}
						>
							<strong>{sprint.name}</strong>
							<small>{sprint.start_date} -> {sprint.end_date}</small>
							<div class="sprint-progress">
								<div class="sprint-progress-fill" style={`width: ${sprintProgress(sprint)}%;`}></div>
							</div>
						</button>
					{/each}
				</div>
			</aside>

			<section class="kanban-column glass-card">
				<header>
					<h3>Kanban</h3>
					{#if selectedSprint}
						<span>{selectedSprint.name}</span>
					{/if}
				</header>

				{#if !selectedSprint}
					<div class="empty-state">No sprint selected.</div>
				{:else}
					<div class="kanban-grid">
						{#each KANBAN_COLUMNS as column}
							<div class="kanban-lane">
								<div class="lane-head">
									<h4>{column.label}</h4>
									<span>{tasksForStatus(column.key).length}</span>
								</div>
								<div class="lane-tasks">
									{#if tasksForStatus(column.key).length === 0}
										<div class="lane-empty">No tasks</div>
									{:else}
										{#each tasksForStatus(column.key) as task (task.id)}
											<article class="task-card">
												<strong>{task.title}</strong>
												{#if task.description}
													<p>{task.description}</p>
												{/if}
												<div class="task-meta">
													<span class="badge">{task.type}</span>
													<span class="badge effort">Effort {task.effort_score}</span>
												</div>
											</article>
										{/each}
									{/if}
								</div>
							</div>
						{/each}
					</div>
				{/if}
			</section>
		</section>
	{:else}
		<section class="glass-card empty-state">
			Use a template or AI prompt to generate your first timeline board.
		</section>
	{/if}
</section>

<style>
	:global(:root) {
		--timeline-bg: #0d0d12;
		--timeline-panel-bg: rgba(255, 255, 255, 0.03);
		--timeline-panel-border: rgba(255, 255, 255, 0.1);
		--timeline-text: #f4f7ff;
		--timeline-muted: rgba(205, 213, 235, 0.75);
		--timeline-accent: #8ab4ff;
		--timeline-accent-soft: rgba(138, 180, 255, 0.2);
		--timeline-error-bg: rgba(220, 38, 38, 0.18);
		--timeline-error-border: rgba(248, 113, 113, 0.35);
		--timeline-error-text: #ffd4df;
	}

	.timeline-board {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: auto auto auto 1fr;
		gap: 0.85rem;
		padding: 0.9rem;
		background: radial-gradient(circle at 12% -8%, rgba(255, 255, 255, 0.07), transparent 34%), var(--timeline-bg);
		color: var(--timeline-text);
	}

	.glass-card {
		border-radius: 16px;
		border: 1px solid var(--timeline-panel-border);
		background: var(--timeline-panel-bg);
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
	}

	.control-panel {
		padding: 0.75rem;
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.72rem;
		justify-content: space-between;
		position: relative;
	}

	.template-group {
		position: relative;
	}

	.template-trigger,
	.generate-btn,
	.template-option {
		border: 1px solid rgba(255, 255, 255, 0.18);
		background: rgba(255, 255, 255, 0.06);
		color: var(--timeline-text);
		border-radius: 10px;
		padding: 0.52rem 0.7rem;
		font-size: 0.8rem;
		cursor: pointer;
		transition: border-color 0.2s ease, background 0.2s ease;
	}

	.template-trigger:hover,
	.generate-btn:hover,
	.template-option:hover {
		border-color: rgba(173, 203, 255, 0.62);
		background: rgba(255, 255, 255, 0.11);
	}

	.template-menu {
		position: absolute;
		top: calc(100% + 8px);
		left: 0;
		display: grid;
		gap: 0.3rem;
		min-width: 180px;
		padding: 0.45rem;
		border-radius: 12px;
		border: 1px solid rgba(255, 255, 255, 0.16);
		background: rgba(14, 18, 26, 0.96);
		backdrop-filter: blur(18px);
		-webkit-backdrop-filter: blur(18px);
		z-index: 30;
	}

	.ai-group {
		flex: 1;
		min-width: min(100%, 340px);
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: 0.45rem;
	}

	.ai-input {
		border-radius: 10px;
		border: 1px solid rgba(255, 255, 255, 0.14);
		background: rgba(255, 255, 255, 0.04);
		color: var(--timeline-text);
		padding: 0.54rem 0.65rem;
	}

	.ai-input::placeholder {
		color: var(--timeline-muted);
	}

	.error-banner {
		border-radius: 12px;
		padding: 0.55rem 0.68rem;
		border: 1px solid var(--timeline-error-border);
		background: var(--timeline-error-bg);
		color: var(--timeline-error-text);
		font-size: 0.8rem;
	}

	.timeline-header {
		padding: 0.8rem;
		display: grid;
		gap: 0.7rem;
	}

	.timeline-header h2 {
		margin: 0;
		font-size: 1.02rem;
	}

	.timeline-header p {
		margin: 0.2rem 0 0;
		font-size: 0.8rem;
		color: var(--timeline-muted);
	}

	.progress-wrap {
		display: grid;
		gap: 0.38rem;
	}

	.progress-track {
		height: 10px;
		border-radius: 999px;
		background: rgba(255, 255, 255, 0.08);
		overflow: hidden;
	}

	.progress-fill {
		height: 100%;
		border-radius: inherit;
		background: linear-gradient(90deg, rgba(128, 171, 255, 0.92), rgba(109, 224, 208, 0.86));
	}

	.progress-wrap span {
		font-size: 0.75rem;
		color: var(--timeline-muted);
	}

	.timeline-grid {
		min-height: 0;
		display: grid;
		grid-template-columns: minmax(220px, 320px) minmax(0, 1fr);
		gap: 0.85rem;
	}

	.sprint-column,
	.kanban-column {
		min-height: 0;
		padding: 0.8rem;
		display: grid;
		gap: 0.7rem;
	}

	.sprint-column header h3,
	.kanban-column header h3 {
		margin: 0;
		font-size: 0.8rem;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: rgba(236, 242, 255, 0.9);
	}

	.sprint-list {
		min-height: 0;
		overflow-y: auto;
		display: grid;
		gap: 0.6rem;
		align-content: start;
	}

	.sprint-card {
		display: grid;
		gap: 0.32rem;
		width: 100%;
		text-align: left;
		border-radius: 12px;
		border: 1px solid rgba(255, 255, 255, 0.12);
		background: rgba(255, 255, 255, 0.02);
		color: var(--timeline-text);
		padding: 0.58rem 0.62rem;
		cursor: pointer;
	}

	.sprint-card.active {
		border-color: rgba(143, 187, 255, 0.7);
		background: rgba(122, 166, 241, 0.2);
	}

	.sprint-card strong {
		font-size: 0.84rem;
	}

	.sprint-card small {
		font-size: 0.7rem;
		color: var(--timeline-muted);
	}

	.sprint-progress {
		height: 6px;
		border-radius: 999px;
		background: rgba(255, 255, 255, 0.08);
		overflow: hidden;
	}

	.sprint-progress-fill {
		height: 100%;
		border-radius: inherit;
		background: var(--timeline-accent);
	}

	.kanban-column header {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.65rem;
	}

	.kanban-column header span {
		font-size: 0.72rem;
		color: var(--timeline-muted);
	}

	.kanban-grid {
		min-height: 0;
		display: grid;
		grid-template-columns: repeat(3, minmax(0, 1fr));
		gap: 0.7rem;
	}

	.kanban-lane {
		min-height: 0;
		border-radius: 12px;
		border: 1px solid rgba(255, 255, 255, 0.1);
		background: rgba(255, 255, 255, 0.02);
		display: grid;
		grid-template-rows: auto 1fr;
	}

	.lane-head {
		padding: 0.58rem;
		display: flex;
		justify-content: space-between;
		align-items: center;
		border-bottom: 1px solid rgba(255, 255, 255, 0.08);
	}

	.lane-head h4 {
		margin: 0;
		font-size: 0.74rem;
		letter-spacing: 0.05em;
		text-transform: uppercase;
	}

	.lane-head span {
		font-size: 0.68rem;
		color: var(--timeline-muted);
	}

	.lane-tasks {
		min-height: 0;
		overflow-y: auto;
		padding: 0.58rem;
		display: grid;
		gap: 0.55rem;
		align-content: start;
	}

	.task-card {
		border-radius: 10px;
		border: 1px solid rgba(255, 255, 255, 0.12);
		background: rgba(255, 255, 255, 0.03);
		padding: 0.56rem;
		display: grid;
		gap: 0.36rem;
	}

	.task-card strong {
		font-size: 0.78rem;
		line-height: 1.35;
	}

	.task-card p {
		margin: 0;
		font-size: 0.72rem;
		line-height: 1.38;
		color: var(--timeline-muted);
		white-space: pre-wrap;
	}

	.task-meta {
		display: flex;
		flex-wrap: wrap;
		gap: 0.35rem;
	}

	.badge {
		display: inline-flex;
		align-items: center;
		padding: 0.16rem 0.43rem;
		border-radius: 999px;
		border: 1px solid rgba(130, 177, 255, 0.5);
		background: var(--timeline-accent-soft);
		font-size: 0.62rem;
		color: #d9e8ff;
	}

	.badge.effort {
		border-color: rgba(127, 219, 203, 0.45);
		background: rgba(85, 184, 165, 0.17);
		color: #cdf4ea;
	}

	.empty-state {
		padding: 0.95rem;
		text-align: center;
		font-size: 0.82rem;
		color: var(--timeline-muted);
	}

	.lane-empty {
		padding: 0.65rem;
		border-radius: 10px;
		border: 1px dashed rgba(255, 255, 255, 0.16);
		font-size: 0.72rem;
		text-align: center;
		color: var(--timeline-muted);
	}

	@media (max-width: 1080px) {
		.timeline-grid {
			grid-template-columns: 1fr;
			grid-template-rows: auto 1fr;
		}

		.sprint-column {
			max-height: 34dvh;
		}
	}

	@media (max-width: 860px) {
		.kanban-grid {
			grid-template-columns: 1fr;
		}
	}
</style>
