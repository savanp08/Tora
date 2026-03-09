<script lang="ts">
	import type { TimelineTask, TimelineTaskDurationUnit } from '$lib/types/timeline';
	import { projectTimeline, recalculateGanttDates, setProjectTimeline } from '$lib/stores/timeline';

	type GanttRow = {
		id: string;
		title: string;
		type: string;
		startDate: string;
		endDate: string;
		columnStart: number;
		span: number;
		color: string;
		overlap: boolean;
	};

	type GanttModel = {
		rows: GanttRow[];
		scaleLabels: string[];
		totalColumns: number;
		hasOverlap: boolean;
	};

	const TYPE_COLOR_MAP: Record<string, string> = {
		backend: '#4f9cff',
		frontend: '#41c7c7',
		design: '#9b78ff',
		qa: '#5fd18b',
		strategy: '#f7b24f',
		planning: '#8f9cb6',
		general: '#8a9ab3'
	};

	const SMART_INPUT_PATTERN = /^\s*(.+?)\s*:\s*(\d+(?:\.\d+)?)\s*(hour|hours|day|days)\s*$/i;
	const DAY_MS = 24 * 60 * 60 * 1000;

	let smartInput = '';
	let smartInputError = '';
	let selectedSprintID = '';

	$: timeline = $projectTimeline;
	$: sprints = timeline?.sprints ?? [];
	$: if (sprints.length > 0 && !sprints.some((sprint) => sprint.id === selectedSprintID)) {
		selectedSprintID = sprints[0].id;
	}
	$: activeSprint = sprints.find((sprint) => sprint.id === selectedSprintID) ?? null;
	$: activeTasks = activeSprint?.tasks ?? [];
	$: ganttModel = buildGanttModel(activeTasks);

	function parseDate(value: string, fallback: Date) {
		const parsed = Date.parse(value);
		if (Number.isFinite(parsed)) {
			return new Date(parsed);
		}
		return new Date(fallback.getTime());
	}

	function toDayString(value: Date) {
		return value.toISOString().slice(0, 10);
	}

	function formatScaleDate(dayISO: string) {
		const parsed = Date.parse(dayISO);
		if (!Number.isFinite(parsed)) {
			return dayISO;
		}
		return new Date(parsed).toLocaleDateString(undefined, {
			month: 'short',
			day: 'numeric'
		});
	}

	function normalizeDurationUnit(raw: string): TimelineTaskDurationUnit {
		const normalized = raw.trim().toLowerCase();
		if (normalized === 'hour' || normalized === 'hours') {
			return 'hours';
		}
		return 'days';
	}

	function estimateEffortScore(durationUnit: TimelineTaskDurationUnit, durationValue: number) {
		const hours = durationUnit === 'days' ? durationValue * 8 : durationValue;
		if (hours <= 2) {
			return 2;
		}
		if (hours <= 8) {
			return 3;
		}
		if (hours <= 24) {
			return 5;
		}
		if (hours <= 40) {
			return 6;
		}
		if (hours <= 80) {
			return 8;
		}
		return 10;
	}

	function classifyTaskType(title: string) {
		const normalized = title.toLowerCase();
		if (/design|wireframe|ux|ui/.test(normalized)) {
			return 'design';
		}
		if (/api|backend|server|database|schema/.test(normalized)) {
			return 'backend';
		}
		if (/frontend|client|react|svelte|screen|component/.test(normalized)) {
			return 'frontend';
		}
		if (/qa|test|validation|verify/.test(normalized)) {
			return 'qa';
		}
		if (/plan|roadmap|scope/.test(normalized)) {
			return 'planning';
		}
		if (/strategy|growth|go\s*to\s*market|campaign/.test(normalized)) {
			return 'strategy';
		}
		return 'general';
	}

	function createTaskID() {
		if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
			return crypto.randomUUID();
		}
		return `task-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
	}

	function parseSmartInput(value: string) {
		const match = value.match(SMART_INPUT_PATTERN);
		if (!match) {
			return null;
		}
		const [, rawTitle, rawDuration, rawUnit] = match;
		const title = rawTitle.trim();
		if (!title) {
			return null;
		}
		const durationUnit = normalizeDurationUnit(rawUnit);
		const durationValue = Number(rawDuration);
		if (!Number.isFinite(durationValue) || durationValue <= 0) {
			return null;
		}
		return {
			title,
			durationUnit,
			durationValue
		};
	}

	function buildGanttModel(tasks: TimelineTask[]): GanttModel {
		if (tasks.length === 0) {
			return {
				rows: [],
				scaleLabels: [],
				totalColumns: 1,
				hasOverlap: false
			};
		}

		const baseFallback = new Date();
		const parsedRows = tasks.map((task, index) => {
			const fallbackStart = new Date(baseFallback.getTime() + index * DAY_MS);
			const start = parseDate(task.start_date || '', fallbackStart);
			const end = parseDate(task.end_date || '', new Date(start.getTime() + DAY_MS));
			const normalizedEnd = end.getTime() <= start.getTime() ? new Date(start.getTime() + DAY_MS) : end;
			return {
				task,
				start,
				end: normalizedEnd
			};
		});

		const sortedByStart = [...parsedRows].sort((left, right) => left.start.getTime() - right.start.getTime());
		const overlapTaskIDs = new Set<string>();
		let previousEnd = sortedByStart[0]?.end ?? null;
		for (let index = 1; index < sortedByStart.length; index += 1) {
			const current = sortedByStart[index];
			if (previousEnd && current.start.getTime() < previousEnd.getTime()) {
				overlapTaskIDs.add(current.task.id);
			}
			if (!previousEnd || current.end.getTime() > previousEnd.getTime()) {
				previousEnd = current.end;
			}
		}

		const minStart = parsedRows.reduce(
			(min, row) => (row.start.getTime() < min.getTime() ? row.start : min),
			parsedRows[0].start
		);
		const maxEnd = parsedRows.reduce(
			(max, row) => (row.end.getTime() > max.getTime() ? row.end : max),
			parsedRows[0].end
		);
		const totalColumns = Math.max(1, Math.ceil((maxEnd.getTime() - minStart.getTime()) / DAY_MS));
		const scaleLabels = Array.from({ length: totalColumns }, (_, columnIndex) => {
			const date = new Date(minStart.getTime() + columnIndex * DAY_MS);
			return formatScaleDate(toDayString(date));
		});

		const rows: GanttRow[] = parsedRows.map(({ task, start, end }) => {
			const offset = Math.max(0, Math.floor((start.getTime() - minStart.getTime()) / DAY_MS));
			const span = Math.max(1, Math.ceil((end.getTime() - start.getTime()) / DAY_MS));
			const normalizedType = (task.type || 'general').toLowerCase();
			return {
				id: task.id,
				title: task.title,
				type: normalizedType,
				startDate: toDayString(start),
				endDate: toDayString(end),
				columnStart: offset + 1,
				span,
				color: TYPE_COLOR_MAP[normalizedType] || TYPE_COLOR_MAP.general,
				overlap: overlapTaskIDs.has(task.id)
			};
		});

		return {
			rows,
			scaleLabels,
			totalColumns,
			hasOverlap: overlapTaskIDs.size > 0
		};
	}

	function getSprintSeedDate() {
		if (!activeSprint) {
			return new Date();
		}
		return parseDate(
			activeSprint.start_date || activeSprint.tasks[0]?.start_date || sprints[0]?.start_date || '',
			new Date()
		);
	}

	function addSmartTask() {
		smartInputError = '';
		const parsed = parseSmartInput(smartInput);
		if (!parsed) {
			smartInputError = 'Use format: Task title : 3 days or Task title : 4 hours';
			return;
		}
		if (!timeline || !activeSprint) {
			smartInputError = 'Create a project first before adding timeline tasks.';
			return;
		}

		const sprintIndex = timeline.sprints.findIndex((sprint) => sprint.id === activeSprint.id);
		if (sprintIndex < 0) {
			smartInputError = 'Select a valid sprint before adding the task.';
			return;
		}

		const taskType = classifyTaskType(parsed.title);
		const nextTask: TimelineTask = {
			id: createTaskID(),
			title: parsed.title,
			status: 'todo',
			effort_score: estimateEffortScore(parsed.durationUnit, parsed.durationValue),
			type: taskType,
			duration_unit: parsed.durationUnit,
			duration_value: parsed.durationValue,
			description: `Added from Smart Input (${parsed.durationValue} ${parsed.durationUnit}).`
		};

		const nextTasks = recalculateGanttDates([...activeSprint.tasks, nextTask], getSprintSeedDate());
		const nextSprints = timeline.sprints.map((sprint, index) =>
			index === sprintIndex
				? {
						...sprint,
						start_date: nextTasks[0]?.start_date || sprint.start_date,
						end_date: nextTasks[nextTasks.length - 1]?.end_date || sprint.end_date,
						tasks: nextTasks
					}
				: sprint
		);
		setProjectTimeline({
			...timeline,
			sprints: nextSprints
		});

		smartInput = '';
	}
</script>

<section class="gantt-tab" aria-label="Progress Gantt timeline">
	<section class="smart-input-card">
		<div class="smart-copy">
			<h2>Smart Input</h2>
			<p>Type a task in natural format and the timeline resequences instantly.</p>
		</div>
		<form
			class="smart-form"
			on:submit|preventDefault={() => {
				addSmartTask();
			}}
		>
			<input
				type="text"
				bind:value={smartInput}
				placeholder="Design API : 4 hours"
				aria-label="Add timeline task"
			/>
			<button type="submit">Add Task</button>
		</form>
		{#if sprints.length > 1}
			<label class="sprint-picker">
				<span>Target Sprint</span>
				<select bind:value={selectedSprintID}>
					{#each sprints as sprint (sprint.id)}
						<option value={sprint.id}>{sprint.name}</option>
					{/each}
				</select>
			</label>
		{/if}
		{#if smartInputError}
			<p class="smart-error">{smartInputError}</p>
		{/if}
	</section>

	{#if !activeSprint}
		<section class="timeline-empty">No sprint data available yet.</section>
	{:else}
		<section class="timeline-card">
			<header>
				<div>
					<h3>{activeSprint.name}</h3>
					<p>{activeSprint.start_date} -> {activeSprint.end_date}</p>
				</div>
				{#if ganttModel.hasOverlap}
					<span class="overlap-warning">Overlap detected</span>
				{/if}
			</header>

			{#if ganttModel.rows.length === 0}
				<div class="timeline-empty">No tasks in this sprint yet.</div>
			{:else}
				<div class="timeline-canvas">
					<div
						class="scale-row"
						style={`grid-template-columns: repeat(${ganttModel.totalColumns}, minmax(40px, 1fr));`}
					>
						{#each ganttModel.scaleLabels as label, index (`${label}-${index}`)}
							<span>{label}</span>
						{/each}
					</div>

					<div class="task-rows">
						{#each ganttModel.rows as row (row.id)}
							<article class="task-row">
								<div class="task-info">
									<strong>{row.title}</strong>
									<small>{row.type} • {row.startDate} -> {row.endDate}</small>
								</div>
								<div
									class="task-track"
									style={`grid-template-columns: repeat(${ganttModel.totalColumns}, minmax(40px, 1fr));`}
								>
									<div
										class="task-bar"
										class:overlap={row.overlap}
										style={`grid-column:${row.columnStart} / span ${row.span}; --task-color:${row.color};`}
									>
										<span>{row.type}</span>
									</div>
								</div>
							</article>
						{/each}
					</div>
				</div>
			{/if}
		</section>
	{/if}
</section>

<style>
	.gantt-tab {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: auto 1fr;
		gap: 0.8rem;
		padding: 0.95rem;
		background:
			radial-gradient(circle at 16% -12%, rgba(255, 255, 255, 0.08), transparent 34%),
			#0d0d12;
	}

	.smart-input-card,
	.timeline-card,
	.timeline-empty {
		border: 1px solid rgba(255, 255, 255, 0.12);
		border-radius: 14px;
		background: rgba(255, 255, 255, 0.03);
		backdrop-filter: blur(16px);
		-webkit-backdrop-filter: blur(16px);
	}

	.smart-input-card {
		display: grid;
		gap: 0.56rem;
		padding: 0.82rem;
	}

	.smart-copy h2 {
		margin: 0;
		font-size: 0.96rem;
		letter-spacing: 0.03em;
		color: #edf5ff;
	}

	.smart-copy p {
		margin: 0.2rem 0 0;
		font-size: 0.78rem;
		color: rgba(189, 203, 232, 0.84);
	}

	.smart-form {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: 0.52rem;
	}

	.smart-form input,
	.sprint-picker select {
		border: 1px solid rgba(255, 255, 255, 0.14);
		border-radius: 10px;
		background: rgba(255, 255, 255, 0.04);
		color: #eff5ff;
		padding: 0.56rem 0.65rem;
		font-size: 0.8rem;
	}

	.smart-form input::placeholder {
		color: rgba(188, 201, 229, 0.66);
	}

	.smart-form button {
		border: 1px solid rgba(130, 182, 255, 0.56);
		border-radius: 10px;
		background: rgba(101, 159, 251, 0.22);
		color: #edf5ff;
		padding: 0.54rem 0.74rem;
		font-size: 0.77rem;
		cursor: pointer;
	}

	.smart-form button:hover {
		background: rgba(101, 159, 251, 0.32);
	}

	.sprint-picker {
		display: grid;
		gap: 0.32rem;
		max-width: 240px;
	}

	.sprint-picker span {
		font-size: 0.71rem;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: rgba(192, 206, 233, 0.78);
	}

	.smart-error {
		margin: 0;
		font-size: 0.75rem;
		color: rgba(255, 171, 171, 0.92);
	}

	.timeline-empty {
		padding: 0.8rem;
		font-size: 0.8rem;
		color: rgba(188, 202, 232, 0.82);
	}

	.timeline-card {
		min-height: 0;
		display: grid;
		grid-template-rows: auto 1fr;
		gap: 0.7rem;
		padding: 0.78rem;
	}

	.timeline-card header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 0.6rem;
	}

	.timeline-card h3 {
		margin: 0;
		font-size: 0.88rem;
		color: #eff6ff;
	}

	.timeline-card p {
		margin: 0.16rem 0 0;
		font-size: 0.74rem;
		color: rgba(184, 199, 229, 0.78);
	}

	.overlap-warning {
		border-radius: 999px;
		border: 1px solid rgba(255, 126, 126, 0.68);
		background: rgba(255, 95, 95, 0.17);
		padding: 0.2rem 0.44rem;
		font-size: 0.7rem;
		color: rgba(255, 201, 201, 0.95);
	}

	.timeline-canvas {
		min-height: 0;
		overflow: auto;
		display: grid;
		gap: 0.54rem;
	}

	.scale-row {
		display: grid;
		gap: 0.22rem;
	}

	.scale-row span {
		font-size: 0.66rem;
		color: rgba(179, 194, 223, 0.74);
		text-align: center;
	}

	.task-rows {
		display: grid;
		gap: 0.5rem;
	}

	.task-row {
		display: grid;
		gap: 0.34rem;
	}

	.task-info {
		display: grid;
		gap: 0.08rem;
	}

	.task-info strong {
		font-size: 0.78rem;
		color: #edf5ff;
	}

	.task-info small {
		font-size: 0.7rem;
		color: rgba(182, 196, 226, 0.74);
	}

	.task-track {
		display: grid;
		gap: 0.22rem;
		background:
			repeating-linear-gradient(
				90deg,
				rgba(255, 255, 255, 0.04),
				rgba(255, 255, 255, 0.04) 1px,
				transparent 1px,
				transparent 100%
			);
		border-radius: 999px;
		padding: 0.2rem;
		min-height: 1.7rem;
	}

	.task-bar {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		border-radius: 999px;
		border: 1px solid color-mix(in srgb, var(--task-color) 70%, #ffffff 30%);
		background: color-mix(in srgb, var(--task-color) 35%, transparent 65%);
		color: #e9f2ff;
		padding: 0 0.45rem;
		font-size: 0.68rem;
		text-transform: capitalize;
		overflow: hidden;
	}

	.task-bar.overlap {
		border-color: rgba(255, 109, 109, 0.96);
		background:
			linear-gradient(135deg, rgba(255, 88, 88, 0.3), rgba(255, 113, 113, 0.14)),
			color-mix(in srgb, var(--task-color) 24%, transparent 76%);
	}

	@media (max-width: 900px) {
		.smart-form {
			grid-template-columns: minmax(0, 1fr);
		}
	}
</style>
