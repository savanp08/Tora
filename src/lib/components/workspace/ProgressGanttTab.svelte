<script lang="ts">
	import { tick } from 'svelte';
	import type { OnlineMember } from '$lib/types/chat';
	import type { TimelineTask, TimelineTaskDurationUnit, Sprint } from '$lib/types/timeline';
	import { projectTimeline, recalculateGanttDates, setProjectTimeline } from '$lib/stores/timeline';
	import { parseFlexibleDateValue } from '$lib/utils/dateParsing';

	export let onlineMembers: OnlineMember[] = [];
	export let isAdmin = false;
	export let sessionUserID = "";
	export let sessionUserName = "";
	export let roomId = "";

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
		deadlineColor: string;
		overlap: boolean;
		isSprintHeader?: boolean;
		lastChangedAt?: string;
		lastChangedBy?: string;
	};

	type DayCell = {
		iso: string;
		dayLabel: string;
		weekLabel: string;
		isToday: boolean;
		isWeekend: boolean;
	};

	type MonthGroup = {
		label: string;
		colStart: number;
		colSpan: number;
	};

	type GanttModel = {
		rows: GanttRow[];
		dayCells: DayCell[];
		totalColumns: number;
		hasOverlap: boolean;
		monthGroups: MonthGroup[];
	};

	// ── Colors ─────────────────────────────────────────────────────────────────
	const TYPE_COLOR_MAP: Record<string, string> = {
		backend: '#4f9cff',
		frontend: '#23b5d3',
		design: '#9b78ff',
		qa: '#17b37b',
		strategy: '#f59f0b',
		planning: '#7f889c',
		general: '#7a889f'
	};

	// Deadline color constants (computed per sprint duration)
	const DEADLINE_WARN_FACTOR = 0.25; // 25% of sprint duration = warn threshold
	const DEADLINE_URGENT_FACTOR = 0.1; // 10% of sprint duration = urgent threshold
	const DEADLINE_WARN_MIN = 3;
	const DEADLINE_WARN_MAX = 14;
	const DEADLINE_URGENT_MIN = 1;
	const DEADLINE_URGENT_MAX = 5;

	// Deadline status colors
	const COLOR_DONE_LATE = '#18a777'; // completed even if past due → green
	const COLOR_OVERDUE = '#ef4444'; // past due, not done → red
	const COLOR_URGENT = '#c2410c'; // due very soon, not done → dark orange
	const COLOR_WARNING = '#f59f0b'; // approaching deadline → amber

	// Sprint header row accent
	const SPRINT_HEADER_COLOR = '#5578ff';

	// Sentinel for project-wide view
	const PROJECT_GANTT_ID = '__all__';

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

	function formatSprintDisplayName(name: string, sprintIndex: number) {
		const trimmed = name.trim() || `Sprint ${sprintIndex + 1}`;
		if (trimmed.toLowerCase() === 'backlog') {
			return 'Backlog';
		}
		return `${sprintIndex + 1}. ${trimmed.replace(/^\d+\.\s*/, '')}`;
	}

	// ── State ──────────────────────────────────────────────────────────────────
	let smartInput = '';
	let smartInputError = '';
	let selectedSprintID = '';
	let editingTaskId = '';
	let editingField: EditableRowField | '' = '';
	let editingValue = '';
	let ownerOptions: OwnerOption[] = [];
	let isTimelineFullscreen = false;

	// ── Derived ────────────────────────────────────────────────────────────────
	$: timeline = $projectTimeline;
	$: sprints = timeline?.sprints ?? [];
	$: if (sprints.length > 0 && !selectedSprintID) {
		selectedSprintID = sprints[0].id;
	}
	$: isProjectView = selectedSprintID === PROJECT_GANTT_ID;
	$: activeSprint = isProjectView
		? null
		: (sprints.find((s) => s.id === selectedSprintID) ?? null);
	$: activeTasks = activeSprint?.tasks ?? [];
	$: allTasksFlat = sprints.flatMap((s) => s.tasks);
	$: ownerOptions = buildOwnerOptions(onlineMembers, isProjectView ? allTasksFlat : activeTasks);
	$: ganttModel = isProjectView
		? buildProjectGanttModel(sprints)
		: buildGanttModel(activeTasks, activeSprint?.budget_allocated ?? 0, activeSprint);
	$: doneCount = (isProjectView ? allTasksFlat : activeTasks).filter(
		(t) => t.status === 'done'
	).length;
	$: inProgressCount = (isProjectView ? allTasksFlat : activeTasks).filter(
		(t) => t.status === 'in_progress'
	).length;
	$: todoCount = (isProjectView ? allTasksFlat : activeTasks).filter(
		(t) => t.status === 'todo'
	).length;
	$: if (editingTaskId && !activeTasks.some((t) => t.id === editingTaskId)) {
		cancelRowEditing();
	}

	// ── Utility ────────────────────────────────────────────────────────────────
	function normalizeOwnerKey(value: string) {
		return value.trim().toLowerCase();
	}

	function buildOwnerOptions(members: OnlineMember[], tasks: TimelineTask[]) {
		const next: OwnerOption[] = [];
		const seen = new Set<string>();
		for (const member of members) {
			const rawName = (member.name || '').trim();
			if (!rawName) continue;
			const key = normalizeOwnerKey(rawName);
			if (!key || seen.has(key)) continue;
			seen.add(key);
			next.push({ value: rawName, label: rawName.replace(/_/g, ' '), isOnline: Boolean(member.isOnline) });
		}
		for (const task of tasks) {
			const owner = (task.assignee || '').trim();
			if (!owner) continue;
			const key = normalizeOwnerKey(owner);
			if (!key || seen.has(key)) continue;
			seen.add(key);
			next.push({ value: owner, label: owner.replace(/_/g, ' '), isOnline: false });
		}
		return next.sort(
			(a, b) =>
				Number(b.isOnline) - Number(a.isOnline) ||
				a.label.localeCompare(b.label, undefined, { sensitivity: 'base' })
		);
	}

	function ownerOptionsForRow(row: GanttRow) {
		const options = [...ownerOptions];
		const owner = row.owner.trim();
		if (!owner) return options;
		const exists = options.some((o) => normalizeOwnerKey(o.value) === normalizeOwnerKey(owner));
		if (exists) return options;
		return [{ value: owner, label: owner.replace(/_/g, ' '), isOnline: false }, ...options];
	}

	function parseDate(value: string, fallback: Date) {
		const parsed = Date.parse(value);
		if (Number.isFinite(parsed)) return new Date(parsed);
		return new Date(fallback.getTime());
	}

	function toDayString(value: Date) {
		return value.toISOString().slice(0, 10);
	}

	function formatReadableDate(value: string | Date, includeYear = false) {
		const parsed = parseFlexibleDateValue(value);
		if (!parsed) return typeof value === 'string' ? value : '';
		return parsed.toLocaleDateString(undefined, {
			month: 'short',
			day: 'numeric',
			...(includeYear ? { year: 'numeric' } : {})
		});
	}

	function formatReadableDateRange(startValue: string, endValue: string, includeYear = false) {
		const start = parseFlexibleDateValue(startValue);
		const end = parseFlexibleDateValue(endValue);
		if (!start || !end) return [startValue, endValue].filter(Boolean).join(' – ');

		const rangeStart = start.getTime() <= end.getTime() ? start : end;
		const rangeEnd = start.getTime() <= end.getTime() ? end : start;
		const sameDay = rangeStart.toDateString() === rangeEnd.toDateString();
		const sameMonth =
			rangeStart.getFullYear() === rangeEnd.getFullYear() &&
			rangeStart.getMonth() === rangeEnd.getMonth();
		const sameYear = rangeStart.getFullYear() === rangeEnd.getFullYear();

		if (sameDay) return formatReadableDate(rangeStart, includeYear);
		if (sameMonth) {
			const monthLabel = rangeStart.toLocaleDateString(undefined, { month: 'short' });
			const s = rangeStart.getDate();
			const e = rangeEnd.getDate();
			if (includeYear) return `${monthLabel} ${s} – ${e}, ${rangeStart.getFullYear()}`;
			return `${monthLabel} ${s} – ${e}`;
		}
		if (sameYear && includeYear) {
			return `${formatReadableDate(rangeStart)} – ${formatReadableDate(rangeEnd)}, ${rangeStart.getFullYear()}`;
		}
		return `${formatReadableDate(rangeStart, !sameYear || includeYear)} – ${formatReadableDate(rangeEnd, !sameYear || includeYear)}`;
	}

	function formatRelativeDate(iso: string): string {
		const date = new Date(iso);
		if (isNaN(date.getTime())) return '';
		const diffMs = Date.now() - date.getTime();
		const diffDays = Math.floor(diffMs / DAY_MS);
		if (diffDays === 0) return 'today';
		if (diffDays === 1) return 'yesterday';
		if (diffDays < 7) return `${diffDays}d ago`;
		if (diffDays < 30) return `${Math.floor(diffDays / 7)}w ago`;
		return formatReadableDate(iso);
	}

	function startOfDay(value: Date) {
		const date = new Date(value.getTime());
		date.setHours(0, 0, 0, 0);
		return date;
	}

	function normalizeDurationUnit(raw: string): TimelineTaskDurationUnit {
		const normalized = raw.trim().toLowerCase();
		if (normalized === 'hour' || normalized === 'hours') return 'hours';
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
		if (!match) return null;
		const [, rawTitle, rawDuration, rawUnit] = match;
		const title = rawTitle.trim();
		if (!title) return null;
		const durationUnit = normalizeDurationUnit(rawUnit);
		const durationValue = Number(rawDuration);
		if (!Number.isFinite(durationValue) || durationValue <= 0) return null;
		return { title, durationUnit, durationValue };
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
		if (!Number.isFinite(value) || value < 0) return '$0';
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
		if (!trimmed) return 0;
		const parsed = Number(trimmed.replace(/[$,]/g, ''));
		if (!Number.isFinite(parsed) || parsed < 0) return null;
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
		if (row.isSprintHeader) return;
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
			`[data-gantt-editor="${row.id}:${field}"]`
		);
		editor?.focus();
		if (editor instanceof HTMLInputElement) editor.select();
	}

	function updateTask(taskId: string, updater: (task: TimelineTask) => TimelineTask) {
		if (!timeline || !activeSprint) return false;
		const sprintIndex = timeline.sprints.findIndex((s) => s.id === activeSprint!.id);
		if (sprintIndex < 0) return false;

		let updated = false;
		const nextSprints = timeline.sprints.map((sprint, index) => {
			if (index !== sprintIndex) return sprint;
			const nextTasks = sprint.tasks.map((task) => {
				if (task.id !== taskId) return task;
				updated = true;
				return updater(task);
			});
			return { ...sprint, tasks: nextTasks };
		});
		if (!updated) return false;
		setProjectTimeline({ ...timeline, sprints: nextSprints });
		return true;
	}

	function commitRowEditing(row: GanttRow, field: EditableRowField) {
		if (!isRowEditing(row.id, field)) return;
		const nextRawValue = editingValue.trim();
		if (field === 'title') {
			if (!nextRawValue) return;
			if (nextRawValue === row.title.trim()) { cancelRowEditing(); return; }
			updateTask(row.id, (task) => ({ ...task, title: nextRawValue }));
			cancelRowEditing();
			return;
		}
		if (field === 'owner') {
			if (nextRawValue === row.owner.trim()) { cancelRowEditing(); return; }
			updateTask(row.id, (task) => ({ ...task, assignee: nextRawValue || undefined }));
			cancelRowEditing();
			return;
		}
		const parsedCost = parseCostInput(nextRawValue);
		if (parsedCost == null) return;
		if (Math.abs(parsedCost - row.actualCost) < 0.000001) { cancelRowEditing(); return; }
		updateTask(row.id, (task) => ({ ...task, actual_cost: parsedCost }));
		cancelRowEditing();
	}

	function onEditorKeyDown(event: KeyboardEvent, row: GanttRow, field: EditableRowField) {
		if (event.key === 'Escape') { event.preventDefault(); cancelRowEditing(); return; }
		if (event.key === 'Enter') { event.preventDefault(); commitRowEditing(row, field); }
	}

	// ── Deadline coloring ──────────────────────────────────────────────────────
	function computeDeadlineThresholds(sprintStart: Date | null, sprintEnd: Date | null): { warnDays: number; urgentDays: number } {
		const durationDays = sprintStart && sprintEnd
			? Math.max(1, (sprintEnd.getTime() - sprintStart.getTime()) / DAY_MS)
			: 14;
		const warnDays = Math.min(DEADLINE_WARN_MAX, Math.max(DEADLINE_WARN_MIN, Math.round(durationDays * DEADLINE_WARN_FACTOR)));
		const urgentDays = Math.min(DEADLINE_URGENT_MAX, Math.max(DEADLINE_URGENT_MIN, Math.round(durationDays * DEADLINE_URGENT_FACTOR)));
		return { warnDays, urgentDays };
	}

	function computeDeadlineColor(
		status: string,
		endDate: Date,
		today: Date,
		urgentDays: number,
		warnDays: number,
		typeColor: string
	): string {
		const daysUntilEnd = (endDate.getTime() - today.getTime()) / DAY_MS;
		if (status === 'done') {
			return daysUntilEnd < 0 ? COLOR_DONE_LATE : typeColor;
		}
		if (daysUntilEnd < 0) return COLOR_OVERDUE;
		if (daysUntilEnd <= urgentDays) return COLOR_URGENT;
		if (daysUntilEnd <= warnDays) return COLOR_WARNING;
		return typeColor;
	}

	// ── Month grouping ─────────────────────────────────────────────────────────
	function buildMonthGroups(dayCells: DayCell[]): MonthGroup[] {
		const groups: MonthGroup[] = [];
		let current: MonthGroup | null = null;
		dayCells.forEach((cell, idx) => {
			const date = new Date(cell.iso + 'T00:00:00');
			const label = date.toLocaleDateString(undefined, { month: 'short', year: 'numeric' });
			if (!current || current.label !== label) {
				if (current) groups.push(current);
				current = { label, colStart: idx + 1, colSpan: 1 };
			} else {
				current.colSpan++;
			}
		});
		if (current) groups.push(current);
		return groups;
	}

	// ── Core Gantt model builder ───────────────────────────────────────────────
	function buildGanttModel(
		tasks: TimelineTask[],
		sprintBudgetAllocated: number,
		sprint: Sprint | null
	): GanttModel {
		if (tasks.length === 0) {
			return { rows: [], dayCells: [], totalColumns: 1, hasOverlap: false, monthGroups: [] };
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
			return { task, start, end: end.getTime() < start.getTime() ? start : end };
		});

		const sortedByStart = [...parsedRows].sort((a, b) => a.start.getTime() - b.start.getTime());
		const overlapTaskIDs = new Set<string>();
		let previousEnd = sortedByStart[0]?.end ?? null;
		for (let i = 1; i < sortedByStart.length; i++) {
			const cur = sortedByStart[i];
			if (previousEnd && cur.start.getTime() < previousEnd.getTime()) {
				overlapTaskIDs.add(cur.task.id);
			}
			if (!previousEnd || cur.end.getTime() > previousEnd.getTime()) {
				previousEnd = cur.end;
			}
		}

		const minStart = parsedRows.reduce(
			(min, r) => (r.start.getTime() < min.getTime() ? r.start : min),
			parsedRows[0].start
		);
		const maxEnd = parsedRows.reduce(
			(max, r) => (r.end.getTime() > max.getTime() ? r.end : max),
			parsedRows[0].end
		);
		const totalColumns = Math.max(1, Math.ceil((maxEnd.getTime() - minStart.getTime()) / DAY_MS) + 1);
		const todayISO = toDayString(startOfDay(new Date()));
		const today = startOfDay(new Date());

		const dayCells: DayCell[] = Array.from({ length: totalColumns }, (_, ci) => {
			const date = new Date(minStart.getTime() + ci * DAY_MS);
			const iso = toDayString(date);
			return {
				iso,
				dayLabel: date.toLocaleDateString(undefined, { day: 'numeric' }),
				weekLabel: date.toLocaleDateString(undefined, { weekday: 'short' }),
				isToday: iso === todayISO,
				isWeekend: date.getDay() === 0 || date.getDay() === 6
			};
		});

		// Deadline thresholds from sprint date range
		const sprintStart = sprint?.start_date ? parseDate(sprint.start_date, today) : null;
		const sprintEnd = sprint?.end_date ? parseDate(sprint.end_date, today) : null;
		const { warnDays, urgentDays } = computeDeadlineThresholds(sprintStart, sprintEnd);

		const perTaskSprintBudget =
			sprintBudgetAllocated > 0 ? sprintBudgetAllocated / Math.max(1, tasks.length) : 0;

		const rows: GanttRow[] = [...parsedRows]
			.sort((a, b) => a.start.getTime() - b.start.getTime())
			.map(({ task, start, end }) => {
				const startOffset = Math.max(0, Math.floor((start.getTime() - minStart.getTime()) / DAY_MS));
				const endOffset = Math.max(startOffset + 1, Math.floor((end.getTime() - minStart.getTime()) / DAY_MS) + 1);
				const span = Math.max(1, endOffset - startOffset);
				const normalizedType = (task.type || 'general').toLowerCase();
				const status = normalizeStatus(task.status || 'todo');
				const priority = (task.priority || 'medium').toLowerCase();
				const owner = (task.assignee || '').trim();
				const allocatedBudget =
					typeof task.budget === 'number' && task.budget > 0
						? task.budget
						: perTaskSprintBudget;
				const actualCost =
					typeof task.actual_cost === 'number' && task.actual_cost >= 0
						? task.actual_cost
						: 0;
				const typeColor = TYPE_COLOR_MAP[normalizedType] || TYPE_COLOR_MAP.general;
				const deadlineColor = computeDeadlineColor(status, end, today, urgentDays, warnDays, typeColor);

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
					color: typeColor,
					deadlineColor,
					overlap: overlapTaskIDs.has(task.id),
					lastChangedAt: task.status_changed_at,
					lastChangedBy: task.status_actor_name
				};
			});

		return { rows, dayCells, totalColumns, hasOverlap: overlapTaskIDs.size > 0, monthGroups: buildMonthGroups(dayCells) };
	}

	// ── Project-wide Gantt (all sprints) ───────────────────────────────────────
	function buildProjectGanttModel(sprintList: Sprint[]): GanttModel {
		const allTaskParsed: { task: TimelineTask; start: Date; end: Date; sprint: Sprint }[] = [];

		for (const sprint of sprintList) {
			const baseFallback = startOfDay(new Date());
			sprint.tasks.forEach((task, index) => {
				const fallbackStart = new Date(baseFallback.getTime() + index * DAY_MS);
				const rawStart = parseDate(task.start_date || '', fallbackStart);
				const rawEnd = parseDate(task.end_date || '', new Date(rawStart.getTime() + DAY_MS));
				const start = startOfDay(rawStart);
				const normalizedEnd =
					rawEnd.getTime() <= rawStart.getTime() ? new Date(rawStart.getTime() + DAY_MS) : rawEnd;
				const end = startOfDay(normalizedEnd);
				allTaskParsed.push({ task, start, end: end.getTime() < start.getTime() ? start : end, sprint });
			});
		}

		if (allTaskParsed.length === 0 && sprintList.length === 0) {
			return { rows: [], dayCells: [], totalColumns: 1, hasOverlap: false, monthGroups: [] };
		}

		// Determine global date range
		const allDates = allTaskParsed.flatMap((r) => [r.start, r.end]);
		// Also include sprint declared start/end dates
		for (const sprint of sprintList) {
			const sd = sprint.start_date ? parseDate(sprint.start_date, new Date()) : null;
			const ed = sprint.end_date ? parseDate(sprint.end_date, new Date()) : null;
			if (sd) allDates.push(startOfDay(sd));
			if (ed) allDates.push(startOfDay(ed));
		}

		if (allDates.length === 0) {
			return { rows: [], dayCells: [], totalColumns: 1, hasOverlap: false, monthGroups: [] };
		}

		const minStart = allDates.reduce((min, d) => (d.getTime() < min.getTime() ? d : min), allDates[0]);
		const maxEnd = allDates.reduce((max, d) => (d.getTime() > max.getTime() ? d : max), allDates[0]);
		const totalColumns = Math.max(1, Math.ceil((maxEnd.getTime() - minStart.getTime()) / DAY_MS) + 1);
		const todayISO = toDayString(startOfDay(new Date()));
		const today = startOfDay(new Date());

		const dayCells: DayCell[] = Array.from({ length: totalColumns }, (_, ci) => {
			const date = new Date(minStart.getTime() + ci * DAY_MS);
			const iso = toDayString(date);
			return {
				iso,
				dayLabel: date.toLocaleDateString(undefined, { day: 'numeric' }),
				weekLabel: date.toLocaleDateString(undefined, { weekday: 'short' }),
				isToday: iso === todayISO,
				isWeekend: date.getDay() === 0 || date.getDay() === 6
			};
		});

		const rows: GanttRow[] = [];
		const sortedSprints = [...sprintList].sort((a, b) => {
			const aStart = a.start_date ? parseDate(a.start_date, new Date()).getTime() : 0;
			const bStart = b.start_date ? parseDate(b.start_date, new Date()).getTime() : 0;
			return aStart - bStart;
		});

		for (const [sortedIndex, sprint] of sortedSprints.entries()) {
			// Sprint header row
			const sprintStartDate = sprint.start_date
				? startOfDay(parseDate(sprint.start_date, today))
				: (allTaskParsed.filter((r) => r.sprint.id === sprint.id)[0]?.start ?? today);
			const sprintEndDate = sprint.end_date
				? startOfDay(parseDate(sprint.end_date, today))
				: (allTaskParsed.filter((r) => r.sprint.id === sprint.id).slice(-1)[0]?.end ?? sprintStartDate);

			const sprintColStart = Math.max(0, Math.floor((sprintStartDate.getTime() - minStart.getTime()) / DAY_MS));
			const sprintColEnd = Math.max(sprintColStart + 1, Math.floor((sprintEndDate.getTime() - minStart.getTime()) / DAY_MS) + 1);
			const sprintSpan = Math.max(1, sprintColEnd - sprintColStart);

			rows.push({
				id: sprint.id,
				title: formatSprintDisplayName(sprint.name, sortedIndex),
				type: 'sprint',
				status: 'todo',
				priority: 'medium',
				owner: '',
				allocatedBudget: sprint.budget_allocated ?? 0,
				actualCost: 0,
				durationLabel: '',
				startDate: toDayString(sprintStartDate),
				endDate: toDayString(sprintEndDate),
				columnStart: sprintColStart + 1,
				span: sprintSpan,
				color: SPRINT_HEADER_COLOR,
				deadlineColor: SPRINT_HEADER_COLOR,
				overlap: false,
				isSprintHeader: true
			});

			// Deadline thresholds for this sprint's tasks
			const { warnDays, urgentDays } = computeDeadlineThresholds(sprintStartDate, sprintEndDate);

			// Task rows for this sprint
			const sprintTasks = allTaskParsed.filter((r) => r.sprint.id === sprint.id);
			const perTaskBudget = (sprint.budget_allocated ?? 0) > 0
				? (sprint.budget_allocated ?? 0) / Math.max(1, sprint.tasks.length)
				: 0;

			for (const { task, start, end } of sprintTasks.sort((a, b) => a.start.getTime() - b.start.getTime())) {
				const startOffset = Math.max(0, Math.floor((start.getTime() - minStart.getTime()) / DAY_MS));
				const endOffset = Math.max(startOffset + 1, Math.floor((end.getTime() - minStart.getTime()) / DAY_MS) + 1);
				const span = Math.max(1, endOffset - startOffset);
				const normalizedType = (task.type || 'general').toLowerCase();
				const status = normalizeStatus(task.status || 'todo');
				const priority = (task.priority || 'medium').toLowerCase();
				const allocatedBudget =
					typeof task.budget === 'number' && task.budget > 0 ? task.budget : perTaskBudget;
				const actualCost =
					typeof task.actual_cost === 'number' && task.actual_cost >= 0 ? task.actual_cost : 0;
				const typeColor = TYPE_COLOR_MAP[normalizedType] || TYPE_COLOR_MAP.general;
				const deadlineColor = computeDeadlineColor(status, end, today, urgentDays, warnDays, typeColor);

				rows.push({
					id: task.id,
					title: task.title,
					type: normalizedType,
					status,
					priority,
					owner: (task.assignee || '').trim(),
					allocatedBudget,
					actualCost,
					durationLabel: formatDurationLabel(task),
					startDate: toDayString(start),
					endDate: toDayString(end),
					columnStart: startOffset + 1,
					span,
					color: typeColor,
					deadlineColor,
					overlap: false,
					isSprintHeader: false,
					lastChangedAt: task.status_changed_at,
					lastChangedBy: task.status_actor_name
				});
			}
		}

		return {
			rows,
			dayCells,
			totalColumns,
			hasOverlap: false,
			monthGroups: buildMonthGroups(dayCells)
		};
	}

	// ── Sprint seed / smart input ──────────────────────────────────────────────
	function getSprintSeedDate() {
		if (!activeSprint) return new Date();
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
			smartInputError = 'Select a sprint before adding timeline tasks.';
			return;
		}
		const sprintIndex = timeline.sprints.findIndex((s) => s.id === activeSprint!.id);
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
		setProjectTimeline({ ...timeline, sprints: nextSprints });
		smartInput = '';
	}

	function statusLabel(status: string) { return STATUS_LABELS[status] || 'To Do'; }
	function priorityLabel(priority: string) { return PRIORITY_LABELS[priority] || 'Medium'; }

	function toggleTimelineFullscreen() { isTimelineFullscreen = !isTimelineFullscreen; }
	function closeTimelineFullscreen() { isTimelineFullscreen = false; }

	function handleWindowKeydown(event: KeyboardEvent) {
		if (event.key === 'Escape' && isTimelineFullscreen) closeTimelineFullscreen();
	}
</script>

<svelte:window on:keydown={handleWindowKeydown} />

<section class="gantt-tab" aria-label="Progress Gantt timeline">
	<!-- ── Toolbar ─────────────────────────────────────────────────────────── -->
	<section class="toolbar-card">
		<div class="toolbar-copy">
			<h2>{isProjectView ? 'Project Timeline' : 'Sprint Gantt'}</h2>
			<p>
				{isProjectView
					? 'All sprints, tasks, and subtasks across the project.'
					: 'Jira-style timeline for this sprint.'}
			</p>
		</div>

		<div class="toolbar-right">
			<div class="toolbar-meta">
				<label class="sprint-picker">
					<span>View</span>
					<select bind:value={selectedSprintID}>
						{#if sprints.length > 0}
							<option value={PROJECT_GANTT_ID}>All Sprints</option>
						{/if}
						{#each sprints as sprint, sprintIndex (sprint.id)}
							<option value={sprint.id}>{formatSprintDisplayName(sprint.name, sprintIndex)}</option>
						{/each}
					</select>
				</label>
				<div class="summary-chips">
					<span class="chip chip-todo">
						<span class="chip-dot dot-todo"></span>To Do {todoCount}
					</span>
					<span class="chip chip-progress">
						<span class="chip-dot dot-progress"></span>In Progress {inProgressCount}
					</span>
					<span class="chip chip-done">
						<span class="chip-dot dot-done"></span>Done {doneCount}
					</span>
				</div>
			</div>
		</div>

	</section>

	<!-- ── Deadline legend ────────────────────────────────────────────────── -->
	<div class="deadline-legend" aria-label="Bar color legend">
		<span class="legend-item">
			<span class="legend-swatch" style="background:{COLOR_DONE_LATE}"></span>Completed
		</span>
		<span class="legend-item">
			<span class="legend-swatch" style="background:var(--gantt-accent)"></span>On track
		</span>
		<span class="legend-item">
			<span class="legend-swatch" style="background:{COLOR_WARNING}"></span>Approaching deadline
		</span>
		<span class="legend-item">
			<span class="legend-swatch" style="background:{COLOR_URGENT}"></span>Due soon
		</span>
		<span class="legend-item">
			<span class="legend-swatch" style="background:{COLOR_OVERDUE}"></span>Overdue
		</span>
	</div>

	{#if !activeSprint && !isProjectView}
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
			<!-- Header (always visible, outside scroll) -->
			<header class="timeline-head">
				<div class="timeline-head-copy">
					{#if isProjectView}
						<h3>{timeline?.project_name || 'Project Timeline'}</h3>
						<p>
							{sprints.length} sprint{sprints.length !== 1 ? 's' : ''} ·
							{allTasksFlat.length} task{allTasksFlat.length !== 1 ? 's' : ''}
						</p>
					{:else}
						<h3>{activeSprint?.name ?? ''}</h3>
						<p>{formatReadableDateRange(activeSprint?.start_date ?? '', activeSprint?.end_date ?? '', true)}</p>
					{/if}
				</div>
				<div class="timeline-head-actions">
					{#if ganttModel.hasOverlap}
						<span class="overlap-warning">Overlapping dates</span>
					{/if}
					<button
						type="button"
						class="timeline-expand-btn"
						aria-pressed={isTimelineFullscreen}
						aria-label={isTimelineFullscreen ? 'Exit full screen' : 'Open full screen'}
						title={isTimelineFullscreen ? 'Exit full screen' : 'Full screen'}
						on:click={toggleTimelineFullscreen}
					>
						{#if isTimelineFullscreen}
							<svg viewBox="0 0 24 24" aria-hidden="true">
								<path d="M8 3v3a2 2 0 01-2 2H3M21 8h-3a2 2 0 01-2-2V3M3 16h3a2 2 0 012 2v3M16 21v-3a2 2 0 012-2h3"></path>
							</svg>
						{:else}
							<svg viewBox="0 0 24 24" aria-hidden="true">
								<path d="M8 3H5a2 2 0 00-2 2v3M21 8V5a2 2 0 00-2-2h-3M3 16v3a2 2 0 002 2h3M16 21h3a2 2 0 002-2v-3"></path>
							</svg>
						{/if}
					</button>
				</div>
			</header>

			{#if ganttModel.rows.length === 0}
				<div class="timeline-empty-inner">No tasks in this sprint yet.</div>
			{:else}
				<!-- Scrollable Gantt area -->
				<div
					class="gantt-scroll"
					style="--left-col:{LEFT_COLUMN_WIDTH_PX}px; --day-col:{DAY_COLUMN_WIDTH_PX}px;"
				>
					<div
						class="gantt-grid"
						style="grid-template-columns: var(--left-col) repeat({ganttModel.totalColumns}, var(--day-col));"
					>
						<!-- ── Sticky top-left corner ────────────────────────────── -->
						<div class="gantt-top-left">
							{#if ganttModel.monthGroups.length > 1}
								<span class="tl-month-label">Month</span>
							{/if}
							<span class="tl-task-label">Task</span>
						</div>

						<!-- ── Sticky date header ────────────────────────────────── -->
						<div class="gantt-top-right" style="--date-cols:{ganttModel.totalColumns};">
							<!-- Month strip -->
							{#if ganttModel.monthGroups.length > 0}
								<div
									class="month-strip"
									style="grid-template-columns: repeat({ganttModel.totalColumns}, var(--day-col));"
								>
									{#each ganttModel.monthGroups as month (month.colStart)}
										<div
											class="month-cell"
											style="grid-column: {month.colStart} / span {month.colSpan};"
										>
											{month.label}
										</div>
									{/each}
								</div>
							{/if}
							<!-- Day strip -->
							<div class="date-strip">
								{#each ganttModel.dayCells as day (day.iso)}
									<span
										class="date-cell"
										class:is-today={day.isToday}
										class:is-weekend={day.isWeekend}
									>
										<span class="date-weekday">{day.weekLabel}</span>
										<span class="date-day">{day.dayLabel}</span>
									</span>
								{/each}
							</div>
						</div>

						<!-- ── Task rows ─────────────────────────────────────────── -->
						{#each ganttModel.rows as row, rowIndex (row.id)}
							{#if row.isSprintHeader}
								<!-- Sprint group header row -->
								<div class="sprint-row-meta">
									<div class="sprint-row-info">
										<span class="sprint-row-icon" aria-hidden="true">
											<svg viewBox="0 0 24 24"><path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01"/></svg>
										</span>
										<div>
											<span class="sprint-row-name">{row.title}</span>
											<span class="sprint-row-dates">
												{formatReadableDateRange(row.startDate, row.endDate, true)}
											</span>
										</div>
									</div>
									{#if row.allocatedBudget > 0}
										<span class="sprint-row-budget">{formatCurrency(row.allocatedBudget)}</span>
									{/if}
								</div>
								<div
									class="sprint-row-track"
									style="grid-template-columns: repeat({ganttModel.totalColumns}, var(--day-col));"
								>
									<div
										class="sprint-row-bar"
										style="grid-column: {row.columnStart} / span {row.span};"
									>
										<span>{row.title}</span>
									</div>
								</div>
							{:else}
								<!-- Task row -->
								<div class="task-meta" class:is-even={rowIndex % 2 === 0}>
									<div class="task-title-wrap">
										{#if isRowEditing(row.id, 'title')}
											<input
												class="inline-editor"
												data-gantt-editor="{row.id}:title"
												value={editingValue}
												on:input={(e) => { editingValue = (e.currentTarget as HTMLInputElement).value; }}
												on:keydown={(e) => onEditorKeyDown(e, row, 'title')}
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
										<div class="task-date-range" title="{row.startDate} to {row.endDate}">
											<span class="task-date-chip">
												<span class="task-date-label">Start</span>
												<strong>{formatReadableDate(row.startDate)}</strong>
											</span>
											<span class="task-date-separator" aria-hidden="true">→</span>
											<span class="task-date-chip">
												<span class="task-date-label">End</span>
												<strong>{formatReadableDate(row.endDate)}</strong>
											</span>
										</div>
									</div>
									<div class="task-tags">
										<span class="tag status-{row.status}">{statusLabel(row.status)}</span>
										<span class="tag priority-{row.priority}">{priorityLabel(row.priority)}</span>
										{#if isRowEditing(row.id, 'owner')}
											{#if ownerOptions.length > 0}
												<select
													class="inline-editor owner-select"
													data-gantt-editor="{row.id}:owner"
													bind:value={editingValue}
													on:keydown={(e) => onEditorKeyDown(e, row, 'owner')}
													on:change={() => commitRowEditing(row, 'owner')}
													on:blur={() => commitRowEditing(row, 'owner')}
												>
													<option value="">Unassigned</option>
													{#each ownerOptionsForRow(row) as opt (opt.value)}
														<option value={opt.value}>{opt.label}{opt.isOnline ? '' : ' (offline)'}</option>
													{/each}
												</select>
											{:else}
												<input
													class="inline-editor"
													data-gantt-editor="{row.id}:owner"
													value={editingValue}
													on:input={(e) => { editingValue = (e.currentTarget as HTMLInputElement).value; }}
													on:keydown={(e) => onEditorKeyDown(e, row, 'owner')}
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
													data-gantt-editor="{row.id}:actualCost"
													value={editingValue}
													on:input={(e) => { editingValue = (e.currentTarget as HTMLInputElement).value; }}
													on:keydown={(e) => onEditorKeyDown(e, row, 'actualCost')}
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
									{#if row.lastChangedAt}
										<div class="task-history-hint">
											<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z"/></svg>
											Status changed {formatRelativeDate(row.lastChangedAt)}{row.lastChangedBy ? ` by ${row.lastChangedBy}` : ''}
										</div>
									{/if}
								</div>

								<div
									class="task-track"
									class:is-even={rowIndex % 2 === 0}
									style="grid-template-columns: repeat({ganttModel.totalColumns}, var(--day-col));"
								>
									<div
										class="task-bar"
										class:overlap={row.overlap}
										class:is-done={row.status === 'done'}
										class:is-progress={row.status === 'in_progress'}
										class:is-overdue={row.deadlineColor === COLOR_OVERDUE}
										class:is-urgent={row.deadlineColor === COLOR_URGENT}
										class:is-warn={row.deadlineColor === COLOR_WARNING}
										class:is-done-late={row.deadlineColor === COLOR_DONE_LATE}
										style="grid-column: {row.columnStart} / span {row.span}; --task-color: {row.deadlineColor};"
									>
										<span class="bar-type-label">{row.type}</span>
									</div>
								</div>
							{/if}
						{/each}
					</div>
				</div>
			{/if}
		</section>
	{/if}
</section>

<style>
	/* ── CSS Variables ──────────────────────────────────────────────────────── */
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
		--gantt-sticky-shadow: rgba(26, 31, 44, 0.12);
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
		--gantt-sprint-header-bg: rgba(85, 120, 255, 0.07);
		--gantt-sprint-header-border: rgba(85, 120, 255, 0.22);
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
		--gantt-sticky-shadow: rgba(0, 0, 0, 0.36);
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
		--gantt-sprint-header-bg: rgba(123, 147, 255, 0.08);
		--gantt-sprint-header-border: rgba(123, 147, 255, 0.26);
	}

	/* ── Layout ─────────────────────────────────────────────────────────────── */
	.gantt-tab {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: auto auto minmax(0, 1fr);
		gap: 0.6rem;
		padding: 1rem;
		background: var(--gantt-bg);
		overflow-x: clip; /* prevent the fullscreen button from escaping viewport */
	}

	/* ── Toolbar ─────────────────────────────────────────────────────────────── */
	.toolbar-card {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.6rem 1rem;
		padding: 0.82rem 1rem;
		border: 1px solid var(--gantt-border);
		border-radius: 14px;
		background: var(--gantt-surface);
	}

	.toolbar-copy {
		flex: 1 1 180px;
		min-width: 0;
	}

	.toolbar-copy h2 {
		margin: 0;
		font-size: 1rem;
		line-height: 1.2;
		font-weight: 700;
		color: var(--gantt-text);
	}

	.toolbar-copy p {
		margin: 0.18rem 0 0;
		font-size: 0.8rem;
		color: var(--gantt-muted);
	}

	.toolbar-right {
		flex: 0 0 auto;
		display: flex;
		align-items: center;
		gap: 0.6rem;
	}

	.toolbar-meta {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.5rem;
	}

	.sprint-picker {
		display: grid;
		gap: 0.2rem;
		min-width: 0;
	}

	.sprint-picker span {
		font-size: 0.64rem;
		font-weight: 700;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--gantt-muted);
	}

	.sprint-picker select {
		height: 2.1rem;
		min-width: 9rem;
		max-width: 16rem;
		border: 1px solid var(--gantt-input-border);
		border-radius: 9px;
		background: var(--gantt-input-bg);
		color: var(--gantt-input-text);
		padding: 0 0.62rem;
		font-size: 0.84rem;
	}

	.summary-chips {
		display: inline-flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.28rem;
	}

	.chip {
		height: 1.7rem;
		display: inline-flex;
		align-items: center;
		gap: 0.32rem;
		padding: 0 0.56rem;
		border-radius: 999px;
		border: 1px solid var(--gantt-chip-border);
		background: var(--gantt-chip-bg);
		font-size: 0.72rem;
		font-weight: 700;
		color: var(--gantt-text);
	}

	.chip-dot {
		width: 0.42rem;
		height: 0.42rem;
		border-radius: 50%;
		flex-shrink: 0;
	}
	.dot-todo { background: var(--gantt-muted); }
	.dot-progress { background: #f59f0b; }
	.dot-done { background: #18a777; }

	.chip-progress { border-color: color-mix(in srgb, #f59f0b 40%, var(--gantt-chip-border)); }
	.chip-done { border-color: color-mix(in srgb, #18a777 40%, var(--gantt-chip-border)); }

	.smart-input-row {
		width: 100%;
		display: flex;
		align-items: center;
		gap: 0.46rem;
		margin-top: 0.28rem;
	}

	.smart-input {
		flex: 1 1 0;
		min-width: 0;
		height: 2rem;
		border: 1px solid var(--gantt-input-border);
		border-radius: 9px;
		background: var(--gantt-input-bg);
		color: var(--gantt-input-text);
		padding: 0 0.65rem;
		font-size: 0.82rem;
	}

	.smart-input::placeholder {
		color: var(--gantt-muted);
		opacity: 0.7;
	}

	.smart-submit {
		height: 2rem;
		padding: 0 0.88rem;
		border-radius: 9px;
		border: 1px solid color-mix(in srgb, var(--gantt-accent) 40%, var(--gantt-btn-border));
		background: var(--gantt-accent-soft);
		color: var(--gantt-accent);
		font-size: 0.8rem;
		font-weight: 700;
		cursor: pointer;
		flex-shrink: 0;
		transition: background 0.15s ease;
	}

	.smart-submit:hover {
		background: color-mix(in srgb, var(--gantt-accent-soft) 180%, transparent);
	}

	.smart-error {
		width: 100%;
		margin: 0;
		font-size: 0.74rem;
		color: #ff8b8b;
	}

	/* ── Deadline legend ─────────────────────────────────────────────────────── */
	.deadline-legend {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.4rem 1rem;
		padding: 0.46rem 0.9rem;
		border: 1px solid var(--gantt-border);
		border-radius: 10px;
		background: var(--gantt-surface);
	}

	.legend-item {
		display: inline-flex;
		align-items: center;
		gap: 0.38rem;
		font-size: 0.71rem;
		font-weight: 600;
		color: var(--gantt-muted);
	}

	.legend-swatch {
		width: 0.82rem;
		height: 0.82rem;
		border-radius: 3px;
		flex-shrink: 0;
	}

	/* ── Timeline card ──────────────────────────────────────────────────────── */
	.timeline-card {
		min-height: 0;
		display: grid;
		grid-template-rows: auto minmax(0, 1fr);
		padding: 0.9rem;
		gap: 0.72rem;
		overflow: hidden;
		border: 1px solid var(--gantt-border);
		border-radius: 14px;
		background: var(--gantt-surface);
	}

	.timeline-card.is-fullscreen {
		position: fixed;
		inset: 0.75rem;
		z-index: 1600; /* keep the fullscreen gantt above chat side panes like the online list */
		border-radius: 20px;
		padding: 1rem;
		box-shadow: 0 24px 72px rgba(9, 14, 28, 0.38);
	}

	.timeline-fullscreen-backdrop {
		position: fixed;
		inset: 0;
		z-index: 1599;
		background: rgba(11, 16, 30, 0.55);
		backdrop-filter: blur(12px);
	}

	/* ── Timeline header ────────────────────────────────────────────────────── */
	.timeline-head {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 0.6rem;
		flex-wrap: nowrap;
	}

	.timeline-head-copy {
		min-width: 0;
		flex: 1 1 0;
	}

	.timeline-head h3 {
		margin: 0;
		font-size: 1rem;
		font-weight: 700;
		color: var(--gantt-text);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.timeline-head p {
		margin: 0.16rem 0 0;
		font-size: 0.79rem;
		color: var(--gantt-muted);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.timeline-head-actions {
		display: flex;
		flex-wrap: nowrap;
		align-items: center;
		gap: 0.4rem;
		flex-shrink: 0;
	}

	.timeline-card.is-fullscreen .timeline-head-actions {
		position: relative;
		z-index: 2;
	}

	.overlap-warning {
		height: 1.5rem;
		display: inline-flex;
		align-items: center;
		padding: 0 0.52rem;
		border-radius: 999px;
		border: 1px solid rgba(255, 103, 103, 0.5);
		background: rgba(255, 103, 103, 0.1);
		color: #ffaaaa;
		font-size: 0.68rem;
		font-weight: 700;
		white-space: nowrap;
	}

	.timeline-expand-btn {
		width: 2.15rem;
		height: 2.15rem;
		min-width: 2.15rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		padding: 0;
		border-radius: 11px;
		border: 1px solid color-mix(in srgb, var(--gantt-accent) 42%, var(--gantt-btn-border));
		background: color-mix(in srgb, var(--gantt-accent-soft) 80%, var(--gantt-surface) 20%);
		color: color-mix(in srgb, var(--gantt-accent) 80%, var(--gantt-text) 20%);
		cursor: pointer;
		flex-shrink: 0;
		position: relative;
		z-index: 1;
		transition: box-shadow 0.18s ease, border-color 0.18s ease, transform 0.18s ease;
	}

	.timeline-expand-btn:hover {
		transform: translateY(-1px);
		border-color: var(--gantt-accent);
		box-shadow: 0 6px 20px color-mix(in srgb, var(--gantt-accent) 28%, transparent);
	}

	.timeline-expand-btn svg {
		width: 1rem;
		height: 1rem;
		stroke: currentColor;
		stroke-width: 1.9;
		fill: none;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.timeline-empty,
	.timeline-empty-inner {
		padding: 0.95rem;
		font-size: 0.82rem;
		color: var(--gantt-muted);
		border: 1px solid var(--gantt-border);
		border-radius: 14px;
		background: var(--gantt-surface);
	}

	.timeline-empty-inner {
		border: none;
		padding: 1.5rem;
		text-align: center;
	}

	/* ── Gantt scroll container ─────────────────────────────────────────────── */
	.gantt-scroll {
		min-height: 0;
		height: 100%;
		width: 100%;
		overflow: auto;
		border-radius: 12px;
		border: 1px solid var(--gantt-border);
		background: var(--gantt-surface-soft);
		scrollbar-width: thin;
		scrollbar-color: var(--gantt-border) transparent;
		overscroll-behavior: contain;
	}

	.timeline-card.is-fullscreen .gantt-scroll {
		border-radius: 14px;
	}

	/* ── Gantt grid ─────────────────────────────────────────────────────────── */
	.gantt-grid {
		display: grid;
		width: max-content;
		min-width: max(100%, calc(var(--left-col) + (var(--day-col) * 8)));
		grid-auto-rows: minmax(72px, auto);
		position: relative;
	}

	/* ── Sticky top-left corner ─────────────────────────────────────────────── */
	.gantt-top-left {
		grid-column: 1;
		position: sticky;
		top: 0;
		left: 0;
		z-index: 5;
		display: flex;
		flex-direction: column;
		justify-content: flex-end;
		padding: 0.4rem 0.78rem 0.38rem;
		background: var(--gantt-header-bg);
		border-right: 1px solid var(--gantt-grid-line);
		box-shadow: 2px 0 8px var(--gantt-sticky-shadow);
	}

	.tl-month-label {
		font-size: 0.6rem;
		font-weight: 700;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: color-mix(in srgb, var(--gantt-header-text) 45%, transparent);
		line-height: 1.6rem; /* match month row height */
	}

	.tl-task-label {
		font-size: 0.72rem;
		font-weight: 700;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--gantt-header-text);
		line-height: 1.8rem; /* match day row height */
	}

	/* ── Sticky date header ─────────────────────────────────────────────────── */
	.gantt-top-right {
		grid-column: 2 / -1;
		position: sticky;
		top: 0;
		z-index: 4;
		display: flex;
		flex-direction: column;
		background: var(--gantt-header-bg);
		border-bottom: 1px solid var(--gantt-grid-line);
	}

	/* Month strip */
	.month-strip {
		display: grid;
		border-bottom: 1px solid color-mix(in srgb, var(--gantt-grid-line) 80%, transparent);
	}

	.month-cell {
		height: 1.6rem;
		display: flex;
		align-items: center;
		padding: 0 0.56rem;
		font-size: 0.67rem;
		font-weight: 700;
		color: color-mix(in srgb, var(--gantt-header-text) 65%, transparent);
		letter-spacing: 0.04em;
		border-right: 1px solid var(--gantt-grid-line);
		white-space: nowrap;
		overflow: hidden;
	}

	/* Day strip */
	.date-strip {
		display: grid;
		grid-template-columns: repeat(var(--date-cols), var(--day-col));
	}

	.date-cell {
		height: 1.8rem;
		display: inline-flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 0;
		padding: 0 0.14rem;
		border-right: 1px solid var(--gantt-grid-line);
	}

	.date-weekday {
		font-size: 0.58rem;
		font-weight: 700;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: color-mix(in srgb, var(--gantt-header-text) 55%, transparent);
		line-height: 1;
	}

	.date-day {
		font-size: 0.76rem;
		font-weight: 700;
		color: var(--gantt-header-text);
		line-height: 1.1;
	}

	.date-cell.is-weekend {
		background: rgba(255, 255, 255, 0.04);
	}

	.date-cell.is-today {
		background: rgba(74, 163, 255, 0.2);
	}

	.date-cell.is-today .date-day {
		color: #6ebaff;
	}

	/* ── Sticky task-meta column ────────────────────────────────────────────── */
	.task-meta {
		grid-column: 1;
		position: sticky;
		left: 0;
		z-index: 3;
		display: grid;
		gap: 0.48rem;
		padding: 0.68rem 0.82rem;
		border-right: 1px solid var(--gantt-grid-line);
		border-bottom: 1px solid var(--gantt-grid-line);
		background: var(--gantt-surface);
		box-shadow: 2px 0 8px var(--gantt-sticky-shadow);
		align-content: start;
	}

	.task-meta.is-even {
		background: color-mix(in srgb, var(--gantt-surface-soft) 85%, transparent);
	}

	/* ── Sprint header row (project view) ───────────────────────────────────── */
	.sprint-row-meta {
		grid-column: 1;
		position: sticky;
		left: 0;
		z-index: 3;
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		padding: 0.6rem 0.82rem;
		border-right: 1px solid var(--gantt-sprint-header-border);
		border-bottom: 1px solid var(--gantt-sprint-header-border);
		border-top: 1px solid var(--gantt-sprint-header-border);
		background: var(--gantt-sprint-header-bg);
		box-shadow: 2px 0 8px var(--gantt-sticky-shadow);
	}

	.sprint-row-info {
		display: flex;
		align-items: center;
		gap: 0.52rem;
		min-width: 0;
	}

	.sprint-row-icon {
		width: 1.5rem;
		height: 1.5rem;
		display: flex;
		align-items: center;
		justify-content: center;
		border-radius: 6px;
		background: color-mix(in srgb, var(--gantt-accent) 18%, transparent);
		color: var(--gantt-accent);
		flex-shrink: 0;
	}

	.sprint-row-icon svg {
		width: 0.88rem;
		height: 0.88rem;
		stroke: currentColor;
		stroke-width: 2;
		fill: none;
		stroke-linecap: round;
	}

	.sprint-row-name {
		display: block;
		font-size: 0.88rem;
		font-weight: 700;
		color: var(--gantt-text);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.sprint-row-dates {
		display: block;
		font-size: 0.72rem;
		color: var(--gantt-muted);
		white-space: nowrap;
	}

	.sprint-row-budget {
		font-size: 0.72rem;
		font-weight: 700;
		color: var(--gantt-accent);
		flex-shrink: 0;
	}

	.sprint-row-track {
		grid-column: 2 / -1;
		position: relative;
		display: grid;
		align-items: center;
		border-bottom: 1px solid var(--gantt-sprint-header-border);
		border-top: 1px solid var(--gantt-sprint-header-border);
		padding: 0.52rem 0;
		background: var(--gantt-sprint-header-bg);
		background-image: repeating-linear-gradient(
			90deg,
			transparent,
			transparent calc(var(--day-col) - 1px),
			color-mix(in srgb, var(--gantt-accent) 10%, transparent) calc(var(--day-col) - 1px),
			color-mix(in srgb, var(--gantt-accent) 10%, transparent) var(--day-col)
		);
		min-height: 3rem;
	}

	.sprint-row-bar {
		height: 1.5rem;
		display: flex;
		align-items: center;
		padding: 0 0.6rem;
		border-radius: 999px;
		border: 1px solid color-mix(in srgb, var(--gantt-accent) 55%, transparent);
		background: color-mix(in srgb, var(--gantt-accent) 20%, transparent);
		color: var(--gantt-accent);
		font-size: 0.7rem;
		font-weight: 700;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		margin: 0 2px;
	}

	/* ── Task row internals ─────────────────────────────────────────────────── */
	.task-title-wrap {
		display: grid;
		gap: 0.18rem;
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
		font-size: 0.88rem;
		font-weight: 650;
		color: var(--gantt-text);
		line-height: 1.25;
	}

	.title-btn:hover:not(:disabled) {
		text-decoration: underline;
		text-decoration-color: color-mix(in srgb, var(--gantt-text) 45%, transparent);
		text-underline-offset: 2px;
	}

	.task-title-wrap > .inline-editor {
		width: 100%;
	}

	.task-date-range {
		display: inline-flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.26rem;
	}

	.task-date-chip {
		min-height: 1.38rem;
		display: inline-flex;
		align-items: center;
		gap: 0.28rem;
		padding: 0.14rem 0.44rem;
		border-radius: 999px;
		border: 1px solid color-mix(in srgb, var(--gantt-border) 82%, var(--gantt-accent) 18%);
		background: color-mix(in srgb, var(--gantt-surface-soft) 85%, var(--gantt-accent-soft) 15%);
		color: var(--gantt-text);
		font-size: 0.7rem;
	}

	.task-date-label {
		font-size: 0.62rem;
		font-weight: 700;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--gantt-muted);
	}

	.task-date-chip strong {
		font-size: 0.73rem;
		font-weight: 700;
		color: var(--gantt-text);
	}

	.task-date-separator {
		font-size: 0.7rem;
		color: var(--gantt-muted);
	}

	.task-tags {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.3rem;
	}

	.tag {
		height: 1.4rem;
		display: inline-flex;
		align-items: center;
		padding: 0 0.46rem;
		border-radius: 999px;
		font-size: 0.67rem;
		font-weight: 700;
		border: 1px solid var(--gantt-chip-border);
		background: var(--gantt-chip-bg);
		color: var(--gantt-text);
	}

	.tag.status-done {
		background: rgba(24, 167, 119, 0.18);
		border-color: rgba(24, 167, 119, 0.42);
		color: #18a777;
	}

	.tag.status-in_progress {
		background: rgba(245, 159, 11, 0.18);
		border-color: rgba(245, 159, 11, 0.4);
		color: #d48709;
	}

	.tag.priority-critical,
	.tag.priority-high {
		background: rgba(255, 101, 101, 0.15);
		border-color: rgba(255, 101, 101, 0.38);
	}

	.tag.owner,
	.tag.duration,
	.tag.budget {
		background: color-mix(in srgb, var(--gantt-chip-bg) 65%, transparent);
	}

	/* Task history hint */
	.task-history-hint {
		display: inline-flex;
		align-items: center;
		gap: 0.3rem;
		font-size: 0.67rem;
		color: var(--gantt-muted);
	}

	.task-history-hint svg {
		width: 0.72rem;
		height: 0.72rem;
		stroke: currentColor;
		stroke-width: 1.8;
		fill: none;
		stroke-linecap: round;
		flex-shrink: 0;
	}

	/* ── Inline editors ─────────────────────────────────────────────────────── */
	.inline-editor {
		height: 1.68rem;
		min-width: 0;
		border: 1px solid var(--gantt-input-border);
		border-radius: 8px;
		background: var(--gantt-input-bg);
		color: var(--gantt-input-text);
		font-size: 0.74rem;
		padding: 0 0.46rem;
	}

	.inline-editor:focus-visible {
		outline: none;
		box-shadow: 0 0 0 2px color-mix(in srgb, var(--gantt-accent) 38%, transparent);
	}

	.owner-select { min-width: 160px; }

	.cost-editor-wrap {
		height: 1.68rem;
		display: inline-flex;
		align-items: center;
		gap: 0.36rem;
		padding: 0 0.38rem;
		border: 1px solid var(--gantt-chip-border);
		border-radius: 999px;
		background: color-mix(in srgb, var(--gantt-chip-bg) 65%, transparent);
	}

	.cost-editor-wrap span {
		font-size: 0.62rem;
		font-weight: 700;
		color: var(--gantt-muted);
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.budget-input {
		width: 6rem;
		height: 1.3rem;
		border-radius: 999px;
	}

	/* ── Task track (chart area) ─────────────────────────────────────────────── */
	.task-track {
		grid-column: 2 / -1;
		position: relative;
		display: grid;
		align-items: center;
		border-bottom: 1px solid var(--gantt-grid-line);
		padding: 0.48rem 0;
		background: repeating-linear-gradient(
			90deg,
			transparent,
			transparent calc(var(--day-col) - 1px),
			var(--gantt-grid-line) calc(var(--day-col) - 1px),
			var(--gantt-grid-line) var(--day-col)
		);
		min-height: 3.8rem;
	}

	.task-track.is-even {
		background: repeating-linear-gradient(
			90deg,
			color-mix(in srgb, var(--gantt-surface-soft) 82%, transparent),
			color-mix(in srgb, var(--gantt-surface-soft) 82%, transparent) calc(var(--day-col) - 1px),
			var(--gantt-grid-line) calc(var(--day-col) - 1px),
			var(--gantt-grid-line) var(--day-col)
		);
	}

	/* ── Task bars ──────────────────────────────────────────────────────────── */
	.task-bar {
		height: 1.68rem;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		border-radius: 999px;
		border: 1px solid color-mix(in srgb, var(--task-color) 65%, #ffffff 35%);
		background: color-mix(in srgb, var(--task-color) 30%, transparent 70%);
		color: color-mix(in srgb, var(--gantt-text) 82%, var(--task-color) 18%);
		font-size: 0.69rem;
		font-weight: 700;
		text-transform: capitalize;
		overflow: hidden;
		padding: 0 0.52rem;
		margin: 0 2px;
		transition: filter 0.15s ease, box-shadow 0.15s ease;
	}

	.task-bar:hover {
		filter: brightness(1.08);
	}

	/* Done early / on track → subtle glow */
	.task-bar.is-done {
		border-color: color-mix(in srgb, var(--task-color) 75%, #ffffff 25%);
		background: color-mix(in srgb, var(--task-color) 40%, transparent 60%);
	}

	/* In progress */
	.task-bar.is-progress {
		box-shadow: inset 0 0 0 1px rgba(245, 159, 11, 0.28);
	}

	/* Completed and past due → solid green */
	.task-bar.is-done-late {
		border-color: rgba(24, 167, 119, 0.7);
		background: rgba(24, 167, 119, 0.28);
	}

	/* Approaching deadline → amber */
	.task-bar.is-warn {
		border-color: rgba(245, 159, 11, 0.7);
		background: rgba(245, 159, 11, 0.22);
	}

	/* Due very soon → dark orange */
	.task-bar.is-urgent {
		border-color: rgba(194, 65, 12, 0.75);
		background: rgba(194, 65, 12, 0.22);
		animation: pulse-urgent 2s ease-in-out infinite;
	}

	/* Overdue → red */
	.task-bar.is-overdue {
		border-color: rgba(239, 68, 68, 0.8);
		background: rgba(239, 68, 68, 0.22);
		animation: pulse-urgent 1.6s ease-in-out infinite;
	}

	/* Overlap → striped red */
	.task-bar.overlap {
		border-color: rgba(255, 109, 109, 0.92);
		background:
			repeating-linear-gradient(
				-45deg,
				rgba(255, 88, 88, 0.18),
				rgba(255, 88, 88, 0.18) 4px,
				transparent 4px,
				transparent 8px
			),
			color-mix(in srgb, var(--task-color) 18%, transparent 82%);
	}

	@keyframes pulse-urgent {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.78; }
	}

	.bar-type-label {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	/* ── Responsive ─────────────────────────────────────────────────────────── */
	@media (max-width: 900px) {
		.gantt-tab {
			padding: 0.7rem;
			gap: 0.5rem;
		}

		.toolbar-card {
			flex-direction: column;
			align-items: stretch;
		}

		.toolbar-right {
			justify-content: flex-start;
		}

		.deadline-legend {
			gap: 0.32rem 0.7rem;
		}

		.timeline-card.is-fullscreen {
			inset: 0.4rem;
			border-radius: 16px;
		}

		/* Gantt sticky stub: shrink the left column to ~44px so the chart is visible */
		.gantt-scroll {
			--left-col: 44px !important;
		}

		.gantt-top-left {
			padding: 0 4px;
			overflow: hidden;
		}

		.tl-month-label,
		.tl-task-label {
			display: none;
		}

		/* Task meta: hide details, show only truncated title */
		.task-meta {
			padding: 0.5rem 4px;
			gap: 0;
			overflow: hidden;
		}

		.task-title-wrap {
			width: 36px;
			overflow: hidden;
		}

		.inline-chip-btn.title-btn {
			max-width: 36px;
			overflow: hidden;
			text-overflow: ellipsis;
			white-space: nowrap;
			font-size: 0.65rem;
			padding: 0 2px;
		}

		.task-date-range,
		.task-tags {
			display: none;
		}

		/* Sprint row stub */
		.sprint-row-meta {
			padding: 0.5rem 4px;
			overflow: hidden;
		}

		.sprint-row-icon,
		.sprint-row-dates {
			display: none;
		}

		.sprint-row-name {
			max-width: 36px;
			overflow: hidden;
			text-overflow: ellipsis;
			white-space: nowrap;
			font-size: 0.65rem;
		}

		.sprint-row-budget {
			display: none;
		}
	}

	@media (max-width: 600px) {
		.summary-chips {
			flex-wrap: nowrap;
			overflow-x: auto;
		}
		.toolbar-copy{
			flex:none;
		}

		.deadline-legend {
			display: none;
		}
	}
</style>
