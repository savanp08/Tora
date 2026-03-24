<script lang="ts">
	import { tick } from 'svelte';
	import type { OnlineMember } from '$lib/types/chat';
	import type { TimelineTask, TimelineTaskDurationUnit } from '$lib/types/timeline';
	import { projectTimeline, recalculateGanttDates, setProjectTimeline } from '$lib/stores/timeline';
	import { parseFlexibleDateValue } from '$lib/utils/dateParsing';

	export let onlineMembers: OnlineMember[] = [];

	type EditableRowField = 'title' | 'owner' | 'actualCost';

	type OwnerOption = {
		value: string;
		label: string;
		isOnline: boolean;
	};

	type GanttRow = {
		id: string;
		title: string;
		type: string;
		status: string;
		priority: string;
		owner: string;
		allocatedBudget: number;
		actualCost: number;
		durationLabel: string;
		startDate: string;
		endDate: string;
		columnStart: number;
		span: number;
		color: string;
		overlap: boolean;
	};

	type DayCell = {
		iso: string;
		dayLabel: string;
		weekLabel: string;
		isToday: boolean;
		isWeekend: boolean;
	};

	type GanttModel = {
		rows: GanttRow[];
		dayCells: DayCell[];
		totalColumns: number;
		hasOverlap: boolean;
	};

	const TYPE_COLOR_MAP: Record<string, string> = {
		backend: '#4f9cff',
		frontend: '#23b5d3',
		design: '#9b78ff',
		qa: '#17b37b',
		strategy: '#f59f0b',
		planning: '#7f889c',
		general: '#7a889f'
	};

	const STATUS_LABELS: Record<string, string> = {
		todo: 'To Do',
		in_progress: 'In Progress',
		done: 'Done'
	};

	const PRIORITY_LABELS: Record<string, string> = {
		critical: 'Critical',
		high: 'High',
		medium: 'Medium',
		low: 'Low'
	};

	const SMART_INPUT_PATTERN = /^\s*(.+?)\s*:\s*(\d+(?:\.\d+)?)\s*(hour|hours|day|days)\s*$/i;
	const DAY_MS = 24 * 60 * 60 * 1000;
	const LEFT_COLUMN_WIDTH_PX = 440;
	const DAY_COLUMN_WIDTH_PX = 86;

	let smartInput = '';
	let smartInputError = '';
	let selectedSprintID = '';
	let editingTaskId = '';
	let editingField: EditableRowField | '' = '';
	let editingValue = '';
	let ownerOptions: OwnerOption[] = [];
	let isTimelineFullscreen = false;

	$: timeline = $projectTimeline;
	$: sprints = timeline?.sprints ?? [];
	$: if (sprints.length > 0 && !sprints.some((sprint) => sprint.id === selectedSprintID)) {
		selectedSprintID = sprints[0].id;
	}
	$: activeSprint = sprints.find((sprint) => sprint.id === selectedSprintID) ?? null;
	$: activeTasks = activeSprint?.tasks ?? [];
	$: ownerOptions = buildOwnerOptions(onlineMembers, activeTasks);
	$: ganttModel = buildGanttModel(activeTasks, activeSprint?.budget_allocated ?? 0);
	$: doneCount = activeTasks.filter((task) => task.status === 'done').length;
	$: inProgressCount = activeTasks.filter((task) => task.status === 'in_progress').length;
	$: todoCount = activeTasks.filter((task) => task.status === 'todo').length;
	$: if (editingTaskId && !activeTasks.some((task) => task.id === editingTaskId)) {
		cancelRowEditing();
	}

	function normalizeOwnerKey(value: string) {
		return value.trim().toLowerCase();
	}

	function buildOwnerOptions(members: OnlineMember[], tasks: TimelineTask[]) {
		const next: OwnerOption[] = [];
		const seen = new Set<string>();
		for (const member of members) {
			const rawName = (member.name || '').trim();
			if (!rawName) {
				continue;
			}
			const key = normalizeOwnerKey(rawName);
			if (!key || seen.has(key)) {
				continue;
			}
			seen.add(key);
			next.push({
				value: rawName,
				label: rawName.replace(/_/g, ' '),
				isOnline: Boolean(member.isOnline)
			});
		}
		for (const task of tasks) {
			const owner = (task.assignee || '').trim();
			if (!owner) {
				continue;
			}
			const key = normalizeOwnerKey(owner);
			if (!key || seen.has(key)) {
				continue;
			}
			seen.add(key);
			next.push({
				value: owner,
				label: owner.replace(/_/g, ' '),
				isOnline: false
			});
		}
		return next.sort(
			(left, right) =>
				Number(right.isOnline) - Number(left.isOnline) ||
				left.label.localeCompare(right.label, undefined, { sensitivity: 'base' })
		);
	}

	function ownerOptionsForRow(row: GanttRow) {
		const options = [...ownerOptions];
		const owner = row.owner.trim();
		if (!owner) {
			return options;
		}
		const exists = options.some(
			(option) => normalizeOwnerKey(option.value) === normalizeOwnerKey(owner)
		);
		if (exists) {
			return options;
		}
		return [{ value: owner, label: owner.replace(/_/g, ' '), isOnline: false }, ...options];
	}

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

	function formatReadableDate(value: string | Date, includeYear = false) {
		const parsed = parseFlexibleDateValue(value);
		if (!parsed) {
			return typeof value === 'string' ? value : '';
		}
		return parsed.toLocaleDateString(undefined, {
			month: 'short',
			day: 'numeric',
			...(includeYear ? { year: 'numeric' } : {})
		});
	}

	function formatReadableDateRange(startValue: string, endValue: string, includeYear = false) {
		const start = parseFlexibleDateValue(startValue);
		const end = parseFlexibleDateValue(endValue);
		if (!start || !end) {
			return [startValue, endValue].filter(Boolean).join(' - ');
		}

		const rangeStart = start.getTime() <= end.getTime() ? start : end;
		const rangeEnd = start.getTime() <= end.getTime() ? end : start;
		const sameDay = rangeStart.toDateString() === rangeEnd.toDateString();
		const sameMonth =
			rangeStart.getFullYear() === rangeEnd.getFullYear() &&
			rangeStart.getMonth() === rangeEnd.getMonth();
		const sameYear = rangeStart.getFullYear() === rangeEnd.getFullYear();

		if (sameDay) {
			return formatReadableDate(rangeStart, includeYear);
		}
		if (sameMonth) {
			const monthLabel = rangeStart.toLocaleDateString(undefined, { month: 'short' });
			const startDay = rangeStart.getDate();
			const endDay = rangeEnd.getDate();
			if (includeYear) {
				return `${monthLabel} ${startDay} - ${endDay}, ${rangeStart.getFullYear()}`;
			}
			return `${monthLabel} ${startDay} - ${endDay}`;
		}
		if (sameYear && includeYear) {
			return `${formatReadableDate(rangeStart)} - ${formatReadableDate(rangeEnd)}, ${rangeStart.getFullYear()}`;
		}
		return `${formatReadableDate(rangeStart, !sameYear || includeYear)} - ${formatReadableDate(
			rangeEnd,
			!sameYear || includeYear
		)}`;
	}

	function startOfDay(value: Date) {
		const date = new Date(value.getTime());
		date.setHours(0, 0, 0, 0);
		return date;
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
		if (hours <= 2) return 2;
		if (hours <= 8) return 3;
		if (hours <= 24) return 5;
		if (hours <= 40) return 6;
		if (hours <= 80) return 8;
		return 10;
	}

	function classifyTaskType(title: string) {
		const normalized = title.toLowerCase();
		if (/design|wireframe|ux|ui/.test(normalized)) return 'design';
		if (/api|backend|server|database|schema/.test(normalized)) return 'backend';
		if (/frontend|client|react|svelte|screen|component/.test(normalized)) return 'frontend';
		if (/qa|test|validation|verify/.test(normalized)) return 'qa';
		if (/plan|roadmap|scope/.test(normalized)) return 'planning';
		if (/strategy|growth|go\s*to\s*market|campaign/.test(normalized)) return 'strategy';
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

	function normalizeStatus(raw: string) {
		const normalized = raw.trim().toLowerCase().replace(/\s+/g, '_');
		if (normalized === 'done') return 'done';
		if (normalized === 'in_progress') return 'in_progress';
		return 'todo';
	}

	function formatDurationLabel(task: TimelineTask) {
		const unit = task.duration_unit === 'hours' ? 'hours' : 'days';
		const value = task.duration_value && task.duration_value > 0 ? task.duration_value : 1;
		return `${value} ${unit}`;
	}

	function formatCurrency(value: number) {
		if (!Number.isFinite(value) || value < 0) {
			return '$0';
		}
		return new Intl.NumberFormat(undefined, {
			style: 'currency',
			currency: 'USD',
			maximumFractionDigits: 0
		}).format(value);
	}

	function formatSpentOverBudget(spent: number, allocated: number) {
		return `${formatCurrency(spent)} / ${allocated > 0 ? formatCurrency(allocated) : '--'}`;
	}

	function parseCostInput(value: string) {
		const trimmed = value.trim();
		if (!trimmed) {
			return 0;
		}
		const parsed = Number(trimmed.replace(/[$,]/g, ''));
		if (!Number.isFinite(parsed) || parsed < 0) {
			return null;
		}
		return parsed;
	}

	function isRowEditing(taskId: string, field: EditableRowField) {
		return editingTaskId === taskId && editingField === field;
	}

	function cancelRowEditing() {
		editingTaskId = '';
		editingField = '';
		editingValue = '';
	}

	async function startRowEditing(row: GanttRow, field: EditableRowField) {
		editingTaskId = row.id;
		editingField = field;
		if (field === 'title') {
			editingValue = row.title;
		} else if (field === 'owner') {
			editingValue = row.owner;
		} else {
			editingValue = row.actualCost > 0 ? String(row.actualCost) : '0';
		}
		await tick();
		const editor = document.querySelector<HTMLInputElement | HTMLSelectElement>(
			`[data-gantt-editor=\"${row.id}:${field}\"]`
		);
		editor?.focus();
		if (editor instanceof HTMLInputElement) {
			editor.select();
		}
	}

	function updateTask(taskId: string, updater: (task: TimelineTask) => TimelineTask) {
		if (!timeline || !activeSprint) {
			return false;
		}
		const sprintIndex = timeline.sprints.findIndex((sprint) => sprint.id === activeSprint.id);
		if (sprintIndex < 0) {
			return false;
		}

		let updated = false;
		const nextSprints = timeline.sprints.map((sprint, index) => {
			if (index !== sprintIndex) {
				return sprint;
			}
			const nextTasks = sprint.tasks.map((task) => {
				if (task.id !== taskId) {
					return task;
				}
				updated = true;
				return updater(task);
			});
			return {
				...sprint,
				tasks: nextTasks
			};
		});
		if (!updated) {
			return false;
		}
		setProjectTimeline({
			...timeline,
			sprints: nextSprints
		});
		return true;
	}

	function commitRowEditing(row: GanttRow, field: EditableRowField) {
		if (!isRowEditing(row.id, field)) {
			return;
		}
		const nextRawValue = editingValue.trim();
		if (field === 'title') {
			if (!nextRawValue) {
				return;
			}
			if (nextRawValue === row.title.trim()) {
				cancelRowEditing();
				return;
			}
			updateTask(row.id, (task) => ({ ...task, title: nextRawValue }));
			cancelRowEditing();
			return;
		}
		if (field === 'owner') {
			if (nextRawValue === row.owner.trim()) {
				cancelRowEditing();
				return;
			}
			updateTask(row.id, (task) => ({ ...task, assignee: nextRawValue || undefined }));
			cancelRowEditing();
			return;
		}
		const parsedCost = parseCostInput(nextRawValue);
		if (parsedCost == null) {
			return;
		}
		if (Math.abs(parsedCost - row.actualCost) < 0.000001) {
			cancelRowEditing();
			return;
		}
		updateTask(row.id, (task) => ({ ...task, actual_cost: parsedCost }));
		cancelRowEditing();
	}

	function onEditorKeyDown(event: KeyboardEvent, row: GanttRow, field: EditableRowField) {
		if (event.key === 'Escape') {
			event.preventDefault();
			cancelRowEditing();
			return;
		}
		if (event.key === 'Enter') {
			event.preventDefault();
			commitRowEditing(row, field);
		}
	}

	function buildGanttModel(tasks: TimelineTask[], sprintBudgetAllocated: number): GanttModel {
		if (tasks.length === 0) {
			return {
				rows: [],
				dayCells: [],
				totalColumns: 1,
				hasOverlap: false
			};
		}

		const baseFallback = startOfDay(new Date());
		const parsedRows = tasks.map((task, index) => {
			const fallbackStart = new Date(baseFallback.getTime() + index * DAY_MS);
			const rawStart = parseDate(task.start_date || '', fallbackStart);
			const rawEnd = parseDate(task.end_date || '', new Date(rawStart.getTime() + DAY_MS));
			const start = startOfDay(rawStart);
			const normalizedEnd =
				rawEnd.getTime() <= rawStart.getTime() ? new Date(rawStart.getTime() + DAY_MS) : rawEnd;
			const end = startOfDay(normalizedEnd);
			return {
				task,
				start,
				end: end.getTime() < start.getTime() ? start : end
			};
		});

		const sortedByStart = [...parsedRows].sort(
			(left, right) => left.start.getTime() - right.start.getTime()
		);
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
		const totalColumns = Math.max(
			1,
			Math.ceil((maxEnd.getTime() - minStart.getTime()) / DAY_MS) + 1
		);
		const todayISO = toDayString(startOfDay(new Date()));

		const dayCells: DayCell[] = Array.from({ length: totalColumns }, (_, columnIndex) => {
			const date = new Date(minStart.getTime() + columnIndex * DAY_MS);
			const iso = toDayString(date);
			const isWeekend = date.getDay() === 0 || date.getDay() === 6;
			const dayLabel = date.toLocaleDateString(undefined, { day: 'numeric' });
			const weekLabel = date.toLocaleDateString(undefined, { weekday: 'short' });
			return {
				iso,
				dayLabel,
				weekLabel,
				isToday: iso === todayISO,
				isWeekend
			};
		});

		const perTaskSprintBudget =
			sprintBudgetAllocated > 0 ? sprintBudgetAllocated / Math.max(1, tasks.length) : 0;
		const rows: GanttRow[] = [...parsedRows]
			.sort((left, right) => left.start.getTime() - right.start.getTime())
			.map(({ task, start, end }) => {
				const startOffset = Math.max(
					0,
					Math.floor((start.getTime() - minStart.getTime()) / DAY_MS)
				);
				const endOffset = Math.max(
					startOffset + 1,
					Math.floor((end.getTime() - minStart.getTime()) / DAY_MS) + 1
				);
				const span = Math.max(1, endOffset - startOffset);
				const normalizedType = (task.type || 'general').toLowerCase();
				const status = normalizeStatus(task.status || 'todo');
				const priority = (task.priority || 'medium').toLowerCase();
				const owner = (task.assignee || '').trim();
				const allocatedBudget =
					typeof task.budget === 'number' && Number.isFinite(task.budget) && task.budget > 0
						? task.budget
						: perTaskSprintBudget;
				const actualCost =
					typeof task.actual_cost === 'number' &&
					Number.isFinite(task.actual_cost) &&
					task.actual_cost >= 0
						? task.actual_cost
						: 0;
				return {
					id: task.id,
					title: task.title,
					type: normalizedType,
					status,
					priority,
					owner,
					allocatedBudget,
					actualCost,
					durationLabel: formatDurationLabel(task),
					startDate: toDayString(start),
					endDate: toDayString(end),
					columnStart: startOffset + 1,
					span,
					color: TYPE_COLOR_MAP[normalizedType] || TYPE_COLOR_MAP.general,
					overlap: overlapTaskIDs.has(task.id)
				};
			});

		return {
			rows,
			dayCells,
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
			actual_cost: 0,
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

	function statusLabel(status: string) {
		return STATUS_LABELS[status] || 'To Do';
	}

	function priorityLabel(priority: string) {
		return PRIORITY_LABELS[priority] || 'Medium';
	}

	function toggleTimelineFullscreen() {
		isTimelineFullscreen = !isTimelineFullscreen;
	}

	function closeTimelineFullscreen() {
		isTimelineFullscreen = false;
	}

	function handleWindowKeydown(event: KeyboardEvent) {
		if (event.key === 'Escape' && isTimelineFullscreen) {
			closeTimelineFullscreen();
		}
	}
</script>

<svelte:window on:keydown={handleWindowKeydown} />

<section class="gantt-tab" aria-label="Progress Gantt timeline">
	<section class="toolbar-card">
		<div class="toolbar-copy">
			<h2>Sprint Gantt</h2>
			<p>Jira-style timeline for this sprint with a horizontal date header.</p>
		</div>

		<div class="toolbar-meta">
			{#if sprints.length > 1}
				<label class="sprint-picker">
					<span>Sprint</span>
					<select bind:value={selectedSprintID}>
						{#each sprints as sprint (sprint.id)}
							<option value={sprint.id}>{sprint.name}</option>
						{/each}
					</select>
				</label>
			{/if}
			<div class="summary-chips">
				<span class="chip chip-todo">To Do {todoCount}</span>
				<span class="chip chip-progress">In Progress {inProgressCount}</span>
				<span class="chip chip-done">Done {doneCount}</span>
			</div>
		</div>

		{#if smartInputError}
			<p class="smart-error">{smartInputError}</p>
		{/if}
	</section>

	{#if !activeSprint}
		<section class="timeline-empty">No sprint data available yet.</section>
	{:else}
		{#if isTimelineFullscreen}
			<div
				class="timeline-fullscreen-backdrop"
				aria-hidden="true"
				on:click={closeTimelineFullscreen}
			></div>
		{/if}
		<section class="timeline-card" class:is-fullscreen={isTimelineFullscreen}>
			<header class="timeline-head">
				<div class="timeline-head-copy">
					<h3>{activeSprint.name}</h3>
					<p>{formatReadableDateRange(activeSprint.start_date, activeSprint.end_date, true)}</p>
				</div>
				<div class="timeline-head-actions">
					{#if ganttModel.hasOverlap}
						<span class="overlap-warning">Overlapping dates detected</span>
					{/if}
					<button
						type="button"
						class="timeline-expand-btn"
						aria-pressed={isTimelineFullscreen}
						aria-label={isTimelineFullscreen
							? 'Exit full screen Gantt view'
							: 'Open full screen Gantt view'}
						title={isTimelineFullscreen ? 'Exit full screen' : 'Open full screen'}
						on:click={toggleTimelineFullscreen}
					>
						<svg viewBox="0 0 24 24" aria-hidden="true">
							{#if isTimelineFullscreen}
								<path d="M9 4H4v5M15 4h5v5M4 15v5h5M20 15v5h-5"></path>
							{:else}
								<path d="M9 4H4v5M15 4h5v5M4 15v5h5M20 15v5h-5"></path>
								<path d="M9 9 4 4M15 9l5-5M9 15l-5 5M15 15l5 5"></path>
							{/if}
						</svg>
					</button>
				</div>
			</header>

			{#if ganttModel.rows.length === 0}
				<div class="timeline-empty">No tasks in this sprint yet.</div>
			{:else}
				<div
					class="gantt-scroll"
					style={`--left-col:${LEFT_COLUMN_WIDTH_PX}px; --day-col:${DAY_COLUMN_WIDTH_PX}px;`}
				>
					<div
						class="gantt-grid"
						style={`grid-template-columns: var(--left-col) repeat(${ganttModel.totalColumns}, var(--day-col));`}
					>
						<div class="gantt-top-left">
							<span>Task</span>
						</div>
						<div class="gantt-top-right" style={`--date-cols:${ganttModel.totalColumns};`}>
							<div class="date-strip">
								{#each ganttModel.dayCells as day (day.iso)}
									<span
										class="date-cell"
										class:is-today={day.isToday}
										class:is-weekend={day.isWeekend}
									>
										{day.weekLabel}
										{day.dayLabel}
									</span>
								{/each}
							</div>
						</div>

						{#each ganttModel.rows as row, rowIndex (row.id)}
							<div class="task-meta" class:is-even={rowIndex % 2 === 0}>
								<div class="task-title-wrap">
									{#if isRowEditing(row.id, 'title')}
										<input
											class="inline-editor"
											data-gantt-editor={`${row.id}:title`}
											value={editingValue}
											on:input={(event) => {
												editingValue = (event.currentTarget as HTMLInputElement).value;
											}}
											on:keydown={(event) => onEditorKeyDown(event, row, 'title')}
											on:blur={() => commitRowEditing(row, 'title')}
										/>
									{:else}
										<button
											type="button"
											class="inline-chip-btn title-btn"
											on:click={() => void startRowEditing(row, 'title')}
											on:dblclick|stopPropagation={() => void startRowEditing(row, 'title')}
										>
											{row.title}
										</button>
									{/if}
									<div class="task-date-range" title={`${row.startDate} to ${row.endDate}`}>
										<span class="task-date-chip">
											<span class="task-date-label">Start</span>
											<strong>{formatReadableDate(row.startDate)}</strong>
										</span>
										<span class="task-date-separator" aria-hidden="true">to</span>
										<span class="task-date-chip">
											<span class="task-date-label">End</span>
											<strong>{formatReadableDate(row.endDate)}</strong>
										</span>
									</div>
								</div>
								<div class="task-tags">
									<span class={`tag status-${row.status}`}>{statusLabel(row.status)}</span>
									<span class={`tag priority-${row.priority}`}>{priorityLabel(row.priority)}</span>
									{#if isRowEditing(row.id, 'owner')}
										{#if ownerOptions.length > 0}
											<select
												class="inline-editor owner-select"
												data-gantt-editor={`${row.id}:owner`}
												bind:value={editingValue}
												on:keydown={(event) => onEditorKeyDown(event, row, 'owner')}
												on:change={() => commitRowEditing(row, 'owner')}
												on:blur={() => commitRowEditing(row, 'owner')}
											>
												<option value="">Unassigned</option>
												{#each ownerOptionsForRow(row) as ownerOption (ownerOption.value)}
													<option value={ownerOption.value}>
														{ownerOption.label}{ownerOption.isOnline ? '' : ' (offline)'}
													</option>
												{/each}
											</select>
										{:else}
											<input
												class="inline-editor"
												data-gantt-editor={`${row.id}:owner`}
												value={editingValue}
												on:input={(event) => {
													editingValue = (event.currentTarget as HTMLInputElement).value;
												}}
												on:keydown={(event) => onEditorKeyDown(event, row, 'owner')}
												on:blur={() => commitRowEditing(row, 'owner')}
												placeholder="Assign owner"
											/>
										{/if}
									{:else}
										<button
											type="button"
											class="inline-chip-btn tag owner"
											on:click={() => void startRowEditing(row, 'owner')}
											on:dblclick|stopPropagation={() => void startRowEditing(row, 'owner')}
										>
											{row.owner || 'Unassigned'}
										</button>
									{/if}
									{#if isRowEditing(row.id, 'actualCost')}
										<div class="cost-editor-wrap">
											<span>Cost</span>
											<input
												type="number"
												inputmode="decimal"
												min="0"
												step="0.01"
												class="inline-editor budget-input"
												data-gantt-editor={`${row.id}:actualCost`}
												value={editingValue}
												on:input={(event) => {
													editingValue = (event.currentTarget as HTMLInputElement).value;
												}}
												on:keydown={(event) => onEditorKeyDown(event, row, 'actualCost')}
												on:blur={() => commitRowEditing(row, 'actualCost')}
											/>
										</div>
									{:else}
										<button
											type="button"
											class="inline-chip-btn tag budget"
											on:click={() => void startRowEditing(row, 'actualCost')}
											on:dblclick|stopPropagation={() => void startRowEditing(row, 'actualCost')}
											title="Spent / Allocated"
										>
											{formatSpentOverBudget(row.actualCost, row.allocatedBudget)}
										</button>
									{/if}
									<span class="tag duration">{row.durationLabel}</span>
								</div>
							</div>

							<div
								class="task-track"
								class:is-even={rowIndex % 2 === 0}
								style={`grid-template-columns: repeat(${ganttModel.totalColumns}, var(--day-col));`}
							>
								<div
									class="task-bar"
									class:overlap={row.overlap}
									class:is-done={row.status === 'done'}
									class:is-progress={row.status === 'in_progress'}
									style={`grid-column:${row.columnStart} / span ${row.span}; --task-color:${row.color};`}
								>
									<span>{row.type}</span>
								</div>
							</div>
						{/each}
					</div>
				</div>
			{/if}
		</section>
	{/if}
</section>

<style>
	:global(:root) {
		--gantt-bg: #f2f4f8;
		--gantt-surface: #ffffff;
		--gantt-surface-soft: #f7f9fd;
		--gantt-border: #d8deea;
		--gantt-text: #1e2738;
		--gantt-muted: #687287;
		--gantt-header-bg: #111827;
		--gantt-header-text: #ecf2ff;
		--gantt-grid-line: rgba(17, 24, 39, 0.1);
		--gantt-row-hover: rgba(30, 41, 59, 0.06);
		--gantt-sticky-shadow: rgba(26, 31, 44, 0.1);
		--gantt-chip-bg: #ecf1fb;
		--gantt-chip-border: #c8d3e7;
		--gantt-input-bg: #ffffff;
		--gantt-input-text: #172236;
		--gantt-input-border: #c8d1e2;
		--gantt-btn-bg: #edf2fb;
		--gantt-btn-text: #1f2c42;
		--gantt-btn-border: #c8d4e7;
		--gantt-accent: #5578ff;
		--gantt-accent-soft: rgba(85, 120, 255, 0.16);
	}

	:global(:root[data-theme='dark']),
	:global(.theme-dark) {
		--gantt-bg: #18181b;
		--gantt-surface: #1f2025;
		--gantt-surface-soft: #23252b;
		--gantt-border: rgba(255, 255, 255, 0.12);
		--gantt-text: #f1f4fb;
		--gantt-muted: #a8b0c3;
		--gantt-header-bg: #111318;
		--gantt-header-text: #f4f7ff;
		--gantt-grid-line: rgba(255, 255, 255, 0.08);
		--gantt-row-hover: rgba(255, 255, 255, 0.05);
		--gantt-sticky-shadow: rgba(0, 0, 0, 0.32);
		--gantt-chip-bg: #292d37;
		--gantt-chip-border: rgba(255, 255, 255, 0.14);
		--gantt-input-bg: #17181d;
		--gantt-input-text: #f2f5fb;
		--gantt-input-border: rgba(255, 255, 255, 0.15);
		--gantt-btn-bg: #2a2f3a;
		--gantt-btn-text: #eef2ff;
		--gantt-btn-border: rgba(255, 255, 255, 0.18);
		--gantt-accent: #7b93ff;
		--gantt-accent-soft: rgba(123, 147, 255, 0.18);
	}

	.gantt-tab {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: auto minmax(0, 1fr);
		gap: 0.85rem;
		padding: 1rem;
		background: var(--gantt-bg);
	}

	.toolbar-card,
	.timeline-card,
	.timeline-empty {
		border: 1px solid var(--gantt-border);
		border-radius: 14px;
		background: var(--gantt-surface);
	}

	.toolbar-card {
		display: grid;
		grid-template-columns: minmax(220px, 1fr) minmax(320px, 1.25fr) minmax(240px, 1fr);
		align-items: center;
		gap: 0.72rem 1.05rem;
		padding: 0.95rem 1rem;
	}

	.toolbar-copy {
		min-width: 0;
	}

	.toolbar-copy h2 {
		margin: 0;
		font-size: 1.02rem;
		line-height: 1.2;
		font-weight: 700;
		color: var(--gantt-text);
	}

	.toolbar-copy p {
		margin: 0.2rem 0 0;
		font-size: 0.82rem;
		color: var(--gantt-muted);
	}

	.sprint-picker select {
		width: 100%;
		min-width: 0;
		max-width: 100%;
		height: 2.18rem;
		border: 1px solid var(--gantt-input-border);
		border-radius: 9px;
		background: var(--gantt-input-bg);
		color: var(--gantt-input-text);
		padding: 0 0.62rem;
		font-size: 0.84rem;
	}

	.toolbar-meta {
		min-width: 0;
		display: flex;
		align-items: center;
		justify-content: flex-end;
		flex-wrap: wrap;
		gap: 0.6rem;
		align-self: center;
	}

	.sprint-picker {
		display: grid;
		gap: 0.24rem;
		flex: 1 1 220px;
		min-width: 0;
		max-width: min(100%, 18rem);
	}

	.sprint-picker span {
		font-size: 0.66rem;
		font-weight: 700;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--gantt-muted);
	}

	.summary-chips {
		display: inline-flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.3rem;
	}

	.chip {
		height: 1.66rem;
		display: inline-flex;
		align-items: center;
		padding: 0 0.56rem;
		border-radius: 999px;
		border: 1px solid var(--gantt-chip-border);
		background: var(--gantt-chip-bg);
		font-size: 0.72rem;
		font-weight: 700;
		color: var(--gantt-text);
	}

	.chip-progress {
		border-color: color-mix(in srgb, #f59f0b 45%, var(--gantt-chip-border));
	}

	.chip-done {
		border-color: color-mix(in srgb, #18a777 42%, var(--gantt-chip-border));
	}

	.smart-error {
		grid-column: 1 / -1;
		margin: 0;
		font-size: 0.74rem;
		color: #ff8b8b;
	}

	.timeline-empty {
		padding: 0.95rem;
		font-size: 0.82rem;
		color: var(--gantt-muted);
	}

	.timeline-card {
		min-height: 0;
		display: grid;
		grid-template-rows: auto minmax(0, 1fr);
		padding: 1rem;
		gap: 0.85rem;
		overflow: hidden;
	}

	.timeline-card.is-fullscreen {
		position: fixed;
		inset: 1rem;
		z-index: 61;
		border-radius: 22px;
		padding: 1.1rem;
		box-shadow: 0 28px 80px rgba(9, 14, 28, 0.32);
	}

	.timeline-fullscreen-backdrop {
		position: fixed;
		inset: 0;
		z-index: 60;
		background: rgba(11, 16, 30, 0.5);
		backdrop-filter: blur(10px);
	}

	.timeline-head {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		align-items: start;
		gap: 0.62rem 0.9rem;
	}

	.timeline-head-copy {
		min-width: 0;
		flex: 1 1 240px;
	}

	.timeline-head-actions {
		display: inline-flex;
		flex-wrap: nowrap;
		align-items: center;
		justify-content: flex-end;
		gap: 0.45rem;
		min-width: 0;
		flex-shrink: 0;
		justify-self: end;
	}

	.timeline-head h3 {
		margin: 0;
		font-size: 1rem;
		color: var(--gantt-text);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.timeline-head p {
		margin: 0.18rem 0 0;
		font-size: 0.8rem;
		color: var(--gantt-muted);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.overlap-warning {
		height: 1.5rem;
		display: inline-flex;
		align-items: center;
		padding: 0 0.55rem;
		border-radius: 999px;
		border: 1px solid rgba(255, 103, 103, 0.52);
		background: rgba(255, 103, 103, 0.12);
		color: #ffaaaa;
		font-size: 0.69rem;
		font-weight: 700;
		min-width: 0;
		max-width: clamp(7.5rem, 28vw, 12rem);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.timeline-expand-btn {
		width: 2.2rem;
		height: 2.2rem;
		min-width: 2.2rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		padding: 0;
		border-radius: 12px;
		border: 1px solid color-mix(in srgb, var(--gantt-accent) 42%, var(--gantt-btn-border));
		background: linear-gradient(
			135deg,
			color-mix(in srgb, var(--gantt-accent-soft) 90%, var(--gantt-surface) 10%),
			color-mix(in srgb, var(--gantt-btn-bg) 92%, var(--gantt-surface) 8%)
		);
		color: color-mix(in srgb, var(--gantt-accent) 74%, var(--gantt-text) 26%);
		font-size: 0.74rem;
		font-weight: 700;
		cursor: pointer;
		flex: 0 0 auto;
		transition:
			transform 0.18s ease,
			border-color 0.18s ease,
			box-shadow 0.18s ease;
	}

	.timeline-expand-btn:hover {
		transform: translateY(-1px);
		border-color: color-mix(in srgb, var(--gantt-accent) 58%, var(--gantt-btn-border));
		box-shadow: 0 10px 24px color-mix(in srgb, var(--gantt-accent-soft) 78%, transparent);
	}

	.timeline-expand-btn svg {
		width: 0.98rem;
		height: 0.98rem;
		stroke: currentColor;
		stroke-width: 1.9;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.gantt-scroll {
		min-height: 0;
		height: 100%;
		width: 100%;
		overflow: auto;
		max-height: 100%;
		border-radius: 12px;
		border: 1px solid var(--gantt-border);
		background: var(--gantt-surface-soft);
		scrollbar-width: thin;
		overscroll-behavior: contain;
	}

	.timeline-card.is-fullscreen .gantt-scroll {
		border-radius: 16px;
	}

	.gantt-grid {
		display: grid;
		width: max-content;
		min-width: max(100%, calc(var(--left-col) + (var(--day-col) * 8)));
		grid-auto-rows: minmax(76px, auto);
		position: relative;
	}

	.gantt-top-left {
		grid-column: 1;
		position: relative;
		z-index: 1;
		height: 2.28rem;
		display: flex;
		align-items: center;
		padding: 0 0.78rem;
		font-size: 0.74rem;
		font-weight: 700;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--gantt-header-text);
		background: var(--gantt-header-bg);
		border-right: 1px solid var(--gantt-grid-line);
	}

	.gantt-top-right {
		grid-column: 2 / -1;
		position: relative;
		z-index: 1;
		display: block;
		background: var(--gantt-header-bg);
		border-bottom: 1px solid var(--gantt-grid-line);
	}

	.date-strip {
		display: grid;
		grid-template-columns: repeat(var(--date-cols), var(--day-col));
	}

	.date-cell {
		height: 2.28rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		padding: 0 0.22rem;
		font-size: 0.78rem;
		line-height: 1;
		font-weight: 700;
		color: var(--gantt-header-text);
		white-space: nowrap;
		writing-mode: horizontal-tb;
		text-orientation: mixed;
		transform: none;
		border-right: 1px solid var(--gantt-grid-line);
	}

	.date-cell.is-weekend {
		background: rgba(255, 255, 255, 0.05);
	}

	.date-cell.is-today {
		background: rgba(74, 163, 255, 0.22);
	}

	.task-meta {
		grid-column: 1;
		position: relative;
		z-index: 1;
		display: grid;
		gap: 0.56rem;
		padding: 0.72rem 0.82rem;
		border-right: 1px solid var(--gantt-grid-line);
		border-bottom: 1px solid var(--gantt-grid-line);
		background: var(--gantt-surface);
	}

	.task-meta.is-even,
	.task-track.is-even {
		background: color-mix(in srgb, var(--gantt-surface-soft) 82%, transparent 18%);
	}

	.task-title-wrap {
		display: grid;
		gap: 0.2rem;
	}

	.inline-chip-btn {
		border: none;
		background: transparent;
		padding: 0;
		margin: 0;
		font: inherit;
		color: inherit;
		cursor: pointer;
		text-align: left;
	}

	.inline-chip-btn:disabled {
		cursor: not-allowed;
		opacity: 0.72;
	}

	.title-btn {
		max-width: 100%;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: 0.9rem;
		font-weight: 650;
		color: var(--gantt-text);
		line-height: 1.2;
	}

	.title-btn:hover:not(:disabled) {
		text-decoration: underline;
		text-decoration-color: color-mix(in srgb, var(--gantt-text) 48%, transparent 52%);
		text-underline-offset: 2px;
	}

	.task-title-wrap > .inline-editor {
		width: 100%;
	}

	.task-date-range {
		display: inline-flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.34rem;
	}

	.task-date-chip {
		min-height: 1.45rem;
		display: inline-flex;
		align-items: center;
		gap: 0.34rem;
		padding: 0.18rem 0.5rem;
		border-radius: 999px;
		border: 1px solid color-mix(in srgb, var(--gantt-border) 86%, var(--gantt-accent) 14%);
		background: color-mix(in srgb, var(--gantt-surface-soft) 88%, var(--gantt-accent-soft) 12%);
		color: var(--gantt-text);
		font-size: 0.73rem;
		line-height: 1;
	}

	.task-date-label {
		font-size: 0.65rem;
		font-weight: 700;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--gantt-muted);
	}

	.task-date-chip strong {
		font-size: 0.76rem;
		font-weight: 700;
		color: var(--gantt-text);
	}

	.task-date-separator {
		font-size: 0.72rem;
		font-weight: 600;
		color: var(--gantt-muted);
	}

	.task-tags {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.34rem;
	}

	.tag {
		height: 1.46rem;
		display: inline-flex;
		align-items: center;
		padding: 0 0.5rem;
		border-radius: 999px;
		font-size: 0.68rem;
		font-weight: 700;
		border: 1px solid var(--gantt-chip-border);
		background: var(--gantt-chip-bg);
		color: var(--gantt-text);
	}

	.tag.status-done {
		background: rgba(24, 167, 119, 0.2);
		border-color: rgba(24, 167, 119, 0.45);
	}

	.tag.status-in_progress {
		background: rgba(245, 159, 11, 0.2);
		border-color: rgba(245, 159, 11, 0.42);
	}

	.tag.priority-critical,
	.tag.priority-high {
		background: rgba(255, 101, 101, 0.17);
		border-color: rgba(255, 101, 101, 0.42);
	}

	.tag.owner,
	.tag.duration,
	.tag.budget {
		background: color-mix(in srgb, var(--gantt-chip-bg) 70%, transparent 30%);
	}

	.inline-editor {
		height: 1.7rem;
		min-width: 0;
		border: 1px solid var(--gantt-input-border);
		border-radius: 8px;
		background: var(--gantt-input-bg);
		color: var(--gantt-input-text);
		font-size: 0.74rem;
		padding: 0 0.5rem;
	}

	.inline-editor:focus-visible {
		outline: none;
		box-shadow: 0 0 0 2px color-mix(in srgb, var(--gantt-btn-border) 65%, transparent 35%);
	}

	.owner-select {
		min-width: 160px;
	}

	.cost-editor-wrap {
		height: 1.7rem;
		display: inline-flex;
		align-items: center;
		gap: 0.38rem;
		padding: 0 0.4rem;
		border: 1px solid var(--gantt-chip-border);
		border-radius: 999px;
		background: color-mix(in srgb, var(--gantt-chip-bg) 70%, transparent 30%);
	}

	.cost-editor-wrap span {
		font-size: 0.64rem;
		font-weight: 700;
		color: var(--gantt-muted);
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.budget-input {
		width: 6.2rem;
		height: 1.38rem;
		border-radius: 999px;
	}

	.task-track {
		grid-column: 2 / -1;
		position: relative;
		display: grid;
		align-items: center;
		border-bottom: 1px solid var(--gantt-grid-line);
		padding: 0.56rem 0;
		background: repeating-linear-gradient(
			90deg,
			transparent,
			transparent calc(var(--day-col) - 1px),
			var(--gantt-grid-line) calc(var(--day-col) - 1px),
			var(--gantt-grid-line) var(--day-col)
		);
		min-height: 4.05rem;
	}

	.task-bar {
		height: 1.74rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		border-radius: 999px;
		border: 1px solid color-mix(in srgb, var(--task-color) 70%, #ffffff 30%);
		background: color-mix(in srgb, var(--task-color) 34%, transparent 66%);
		color: color-mix(in srgb, var(--gantt-text) 80%, #ffffff 20%);
		font-size: 0.7rem;
		font-weight: 700;
		text-transform: capitalize;
		overflow: hidden;
		padding: 0 0.46rem;
		margin: 0 2px;
	}

	.task-bar.is-progress {
		box-shadow: inset 0 0 0 1px rgba(245, 159, 11, 0.32);
	}

	.task-bar.is-done {
		box-shadow: inset 0 0 0 1px rgba(24, 167, 119, 0.36);
	}

	.task-bar.overlap {
		border-color: rgba(255, 109, 109, 0.96);
		background:
			linear-gradient(135deg, rgba(255, 88, 88, 0.28), rgba(255, 113, 113, 0.12)),
			color-mix(in srgb, var(--task-color) 24%, transparent 76%);
	}

	@media (max-width: 1340px) {
		.toolbar-card {
			grid-template-columns: minmax(0, 1fr);
			align-items: stretch;
		}

		.toolbar-meta {
			justify-content: flex-start;
		}
	}

	@media (max-width: 1180px) {
		.toolbar-card {
			padding: 0.86rem;
		}

		.toolbar-meta {
			justify-content: flex-start;
		}
	}

	@media (max-width: 800px) {
		.gantt-tab {
			padding: 0.72rem;
		}

		.toolbar-meta {
			width: 100%;
			display: grid;
			grid-template-columns: minmax(0, 1fr);
			justify-content: stretch;
		}

		.sprint-picker {
			width: 100%;
			max-width: none;
		}

		.summary-chips {
			width: 100%;
			flex-wrap: nowrap;
			overflow-x: auto;
			padding-bottom: 0.08rem;
		}

		.timeline-card.is-fullscreen {
			inset: 0.55rem;
			padding: 0.82rem;
			border-radius: 18px;
		}

		.timeline-head-actions {
			gap: 0.32rem;
		}
	}
</style>
