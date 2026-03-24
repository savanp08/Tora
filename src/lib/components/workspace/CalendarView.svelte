<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import type { FieldSchema } from '$lib/stores/fieldSchema';
	import type { OnlineMember } from '$lib/types/chat';

	type TaskLike = {
		id: string;
		title: string;
		status?: string;
		assigneeId?: string;
		customFields?: Record<string, unknown>;
		dueDate?: number | string | null;
		taskType?: string;
		updatedAt?: number;
	};

	type CalendarTask = {
		id: string;
		title: string;
		status: 'todo' | 'in_progress' | 'done';
		assigneeLabel: string;
		updatedAt: number;
	};

	type CalendarDay = {
		key: string;
		dayNumber: number;
		isCurrentMonth: boolean;
		isToday: boolean;
	};

	const WEEKDAY_LABELS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];

	export let tasks: TaskLike[] = [];
	export let fieldSchemas: FieldSchema[] = [];
	export let onlineMembers: OnlineMember[] = [];

	const dispatch = createEventDispatcher<{
		editTask: { taskId: string };
	}>();

	let activeMonth = startOfMonth(new Date());
	let expandedDateKeys = new Set<string>();

	$: dueDateFieldId = resolveDateFieldId(fieldSchemas, ['duedate']);
	$: memberNameByKey = buildMemberNameByKey(onlineMembers);
	$: taskBuckets = bucketTasksByDueDate(tasks, dueDateFieldId, memberNameByKey);
	$: dueTasksByDate = taskBuckets.byDate;
	$: undatedTasks = taskBuckets.undated;
	$: monthTitle = activeMonth.toLocaleDateString(undefined, { month: 'long', year: 'numeric' });
	$: monthStart = startOfMonth(activeMonth);
	$: monthEnd = endOfMonth(activeMonth);
	$: gridStart = startOfWeekMonday(monthStart);
	$: gridEnd = endOfWeekMonday(monthEnd);
	$: todayKey = toDateKey(new Date());
	$: dayCells = buildCalendarDays(gridStart, gridEnd, monthStart);

	function normalizeSchemaName(value: string) {
		return value.trim().toLowerCase().replace(/[\s_-]+/g, '');
	}

	function resolveDateFieldId(schemas: FieldSchema[], aliases: string[]) {
		const lookup = new Set(aliases.map((alias) => normalizeSchemaName(alias)));
		for (const schema of schemas) {
			if (lookup.has(normalizeSchemaName(schema.name))) {
				return schema.fieldId;
			}
		}
		for (const schema of schemas) {
			if (lookup.has(normalizeSchemaName(schema.fieldId))) {
				return schema.fieldId;
			}
		}
		return '';
	}

	function normalizeStatus(value: unknown): 'todo' | 'in_progress' | 'done' {
		const normalized = String(value ?? '')
			.trim()
			.toLowerCase()
			.replace(/\s+/g, '_');
		if (normalized === 'done' || normalized === 'completed') {
			return 'done';
		}
		if (normalized === 'in_progress') {
			return 'in_progress';
		}
		return 'todo';
	}

	function parseDateValue(value: unknown): Date | null {
		if (value instanceof Date && Number.isFinite(value.getTime())) {
			return new Date(value.getTime());
		}
		if (typeof value === 'number' && Number.isFinite(value)) {
			const date = new Date(value);
			return Number.isFinite(date.getTime()) ? date : null;
		}
		if (typeof value !== 'string') {
			return null;
		}
		const trimmed = value.trim();
		if (!trimmed) {
			return null;
		}
		const dateOnlyMatch = trimmed.match(/^(\d{4})-(\d{2})-(\d{2})$/);
		if (dateOnlyMatch) {
			const year = Number.parseInt(dateOnlyMatch[1], 10);
			const month = Number.parseInt(dateOnlyMatch[2], 10);
			const day = Number.parseInt(dateOnlyMatch[3], 10);
			if (Number.isFinite(year) && Number.isFinite(month) && Number.isFinite(day)) {
				return new Date(year, month - 1, day);
			}
		}
		const parsed = new Date(trimmed);
		if (!Number.isFinite(parsed.getTime())) {
			return null;
		}
		return parsed;
	}

	function pad2(value: number) {
		return String(value).padStart(2, '0');
	}

	function toDateKey(date: Date) {
		return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}`;
	}

	function truncateLabel(value: string, limit = 18) {
		const trimmed = value.trim();
		if (trimmed.length <= limit) {
			return trimmed || 'Untitled task';
		}
		return `${trimmed.slice(0, Math.max(1, limit - 1))}\u2026`;
	}

	function buildMemberNameByKey(members: OnlineMember[]) {
		const map = new Map<string, string>();
		for (const member of members) {
			const key = member.id.trim().toLowerCase();
			if (!key || map.has(key)) {
				continue;
			}
			const normalizedName = member.name.trim().replace(/_/g, ' ');
			map.set(key, normalizedName || member.id.trim());
		}
		return map;
	}

	function ownerLabel(task: TaskLike, memberMap: Map<string, string>) {
		const assigneeId = task.assigneeId?.trim() ?? '';
		if (!assigneeId) {
			return 'Unassigned';
		}
		return memberMap.get(assigneeId.toLowerCase()) ?? assigneeId.replace(/[_-]+/g, ' ');
	}

	function readDueDate(task: TaskLike, dueFieldId: string) {
		if (task.dueDate != null) {
			return parseDateValue(task.dueDate);
		}
		const fields = task.customFields ?? {};
		if (dueFieldId && dueFieldId in fields) {
			return parseDateValue(fields[dueFieldId]);
		}
		for (const fallbackKey of ['due_date', 'dueDate', 'duedate']) {
			if (fallbackKey in fields) {
				return parseDateValue(fields[fallbackKey]);
			}
		}
		return null;
	}

	function compareCalendarTasks(left: CalendarTask, right: CalendarTask) {
		const statusRank: Record<CalendarTask['status'], number> = {
			in_progress: 0,
			todo: 1,
			done: 2
		};
		return (
			statusRank[left.status] - statusRank[right.status] ||
			right.updatedAt - left.updatedAt ||
			left.title.localeCompare(right.title, undefined, { sensitivity: 'base' })
		);
	}

	function bucketTasksByDueDate(
		sourceTasks: TaskLike[],
		dueFieldId: string,
		memberMap: Map<string, string>
	) {
		const byDate = new Map<string, CalendarTask[]>();
		const undated: CalendarTask[] = [];
		for (const task of sourceTasks) {
			const normalizedTask: CalendarTask = {
				id: task.id,
				title: task.title?.trim() || 'Untitled task',
				status: normalizeStatus(task.status),
				assigneeLabel: ownerLabel(task, memberMap),
				updatedAt: Number.isFinite(task.updatedAt) ? Number(task.updatedAt) : 0
			};
			const dueDate = readDueDate(task, dueFieldId);
			if (!dueDate) {
				undated.push(normalizedTask);
				continue;
			}
			const key = toDateKey(dueDate);
			const existing = byDate.get(key);
			if (existing) {
				existing.push(normalizedTask);
			} else {
				byDate.set(key, [normalizedTask]);
			}
		}
		for (const [key, entries] of byDate.entries()) {
			byDate.set(key, [...entries].sort(compareCalendarTasks));
		}
		undated.sort(compareCalendarTasks);
		return { byDate, undated };
	}

	function startOfMonth(date: Date) {
		return new Date(date.getFullYear(), date.getMonth(), 1);
	}

	function endOfMonth(date: Date) {
		return new Date(date.getFullYear(), date.getMonth() + 1, 0);
	}

	function startOfWeekMonday(date: Date) {
		const base = new Date(date.getFullYear(), date.getMonth(), date.getDate());
		const day = base.getDay();
		const distance = (day + 6) % 7;
		base.setDate(base.getDate() - distance);
		return base;
	}

	function endOfWeekMonday(date: Date) {
		const start = startOfWeekMonday(date);
		start.setDate(start.getDate() + 6);
		return start;
	}

	function addDays(date: Date, days: number) {
		const next = new Date(date.getFullYear(), date.getMonth(), date.getDate());
		next.setDate(next.getDate() + days);
		return next;
	}

	function buildCalendarDays(gridStartDate: Date, gridEndDate: Date, currentMonth: Date): CalendarDay[] {
		const days: CalendarDay[] = [];
		for (
			let cursor = new Date(gridStartDate.getTime());
			cursor.getTime() <= gridEndDate.getTime();
			cursor = addDays(cursor, 1)
		) {
			days.push({
				key: toDateKey(cursor),
				dayNumber: cursor.getDate(),
				isCurrentMonth:
					cursor.getMonth() === currentMonth.getMonth() &&
					cursor.getFullYear() === currentMonth.getFullYear(),
				isToday: toDateKey(cursor) === toDateKey(new Date())
			});
		}
		return days;
	}

	function shiftMonth(offset: number) {
		const base = startOfMonth(activeMonth);
		activeMonth = new Date(base.getFullYear(), base.getMonth() + offset, 1);
		expandedDateKeys = new Set<string>();
	}

	function jumpToToday() {
		activeMonth = startOfMonth(new Date());
		expandedDateKeys = new Set<string>();
	}

	function toggleExpandedDay(dateKey: string) {
		const next = new Set(expandedDateKeys);
		if (next.has(dateKey)) {
			next.delete(dateKey);
		} else {
			next.add(dateKey);
		}
		expandedDateKeys = next;
	}

	function openTask(taskId: string) {
		const normalizedTaskID = taskId.trim();
		if (!normalizedTaskID) {
			return;
		}
		dispatch('editTask', { taskId: normalizedTaskID });
	}
</script>

<section class="calendar-view" aria-label="Calendar view">
	<header class="calendar-toolbar">
		<div class="calendar-nav-group">
			<button type="button" class="nav-btn" on:click={() => shiftMonth(-1)} aria-label="Previous month">
				<svg viewBox="0 0 24 24" aria-hidden="true">
					<path d="m14.5 6.5-5 5.5 5 5.5"></path>
				</svg>
			</button>
			<h3>{monthTitle}</h3>
			<button type="button" class="nav-btn" on:click={() => shiftMonth(1)} aria-label="Next month">
				<svg viewBox="0 0 24 24" aria-hidden="true">
					<path d="m9.5 6.5 5 5.5-5 5.5"></path>
				</svg>
			</button>
		</div>
		<button type="button" class="today-btn" on:click={jumpToToday}>Today</button>
	</header>

	<div class="calendar-grid" style={`--day-count:${WEEKDAY_LABELS.length};`}>
		{#each WEEKDAY_LABELS as weekday (weekday)}
			<div class="weekday-header">{weekday}</div>
		{/each}

		{#each dayCells as day (day.key)}
			{@const dayTasks = dueTasksByDate.get(day.key) ?? []}
			{@const isExpanded = expandedDateKeys.has(day.key)}
			{@const visibleTasks = isExpanded || dayTasks.length <= 3 ? dayTasks : dayTasks.slice(0, 2)}
			<article
				class="day-cell"
				class:is-outside-month={!day.isCurrentMonth}
				class:is-today={day.key === todayKey}
				aria-label={`${day.key} due tasks`}
			>
				<header>
					<span>{day.dayNumber}</span>
					{#if dayTasks.length > 0}
						<small>{dayTasks.length}</small>
					{/if}
				</header>
				<div class="day-task-list">
					{#each visibleTasks as task (task.id)}
						<button
							type="button"
							class={`task-chip status-${task.status}`}
							on:click={() => openTask(task.id)}
							title={`${task.title} • ${task.assigneeLabel}`}
						>
							<span class={`task-dot status-${task.status}`} aria-hidden="true"></span>
							<span>{truncateLabel(task.title)}</span>
						</button>
					{/each}
					{#if dayTasks.length > 3}
						<button
							type="button"
							class="more-chip"
							on:click|stopPropagation={() => toggleExpandedDay(day.key)}
						>
							{#if isExpanded}
								Show less
							{:else}
								+{dayTasks.length - 2} more
							{/if}
						</button>
					{/if}
				</div>
			</article>
		{/each}
	</div>

	<section class="undated-section" aria-label="Tasks without due dates">
		<header>
			<h4>No date</h4>
			<span>{undatedTasks.length}</span>
		</header>
		{#if undatedTasks.length === 0}
			<p class="undated-empty">All tasks in view have a due date.</p>
		{:else}
			<div class="undated-list">
				{#each undatedTasks as task (task.id)}
					<button
						type="button"
						class={`task-chip status-${task.status}`}
						on:click={() => openTask(task.id)}
						title={`${task.title} • ${task.assigneeLabel}`}
					>
						<span class={`task-dot status-${task.status}`} aria-hidden="true"></span>
						<span>{truncateLabel(task.title, 28)}</span>
					</button>
				{/each}
			</div>
		{/if}
	</section>
</section>

<style>
	.calendar-view {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: auto minmax(0, 1fr) auto;
		gap: 0.7rem;
	}

	.calendar-toolbar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.55rem;
		padding: 0.6rem 0.7rem;
		border-radius: 14px;
		border: 1px solid var(--tb-grid-border);
		background: var(--tb-panel-bg);
	}

	.calendar-nav-group {
		display: flex;
		align-items: center;
		gap: 0.52rem;
		min-width: 0;
	}

	.calendar-nav-group h3 {
		margin: 0;
		font-size: 0.96rem;
		line-height: 1.2;
		color: var(--tb-cell-text);
	}

	.nav-btn,
	.today-btn {
		height: 2rem;
		padding: 0 0.62rem;
		border-radius: 10px;
		border: 1px solid var(--tb-btn-border);
		background: var(--tb-btn-bg);
		color: var(--tb-btn-text);
		font-size: 0.78rem;
		font-weight: 700;
		cursor: pointer;
	}

	.nav-btn {
		width: 2rem;
		padding: 0;
		display: grid;
		place-items: center;
	}

	.nav-btn svg {
		width: 0.92rem;
		height: 0.92rem;
		stroke: currentColor;
		fill: none;
		stroke-width: 2;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.nav-btn:hover,
	.today-btn:hover {
		background: color-mix(in srgb, var(--tb-btn-bg) 72%, #ffffff 28%);
	}

	.calendar-grid {
		min-height: 0;
		overflow: auto;
		display: grid;
		grid-template-columns: repeat(var(--day-count), minmax(0, 1fr));
		grid-auto-rows: minmax(116px, 1fr);
		border: 1px solid var(--tb-grid-border);
		border-radius: 14px;
		background: var(--tb-grid-bg);
		scrollbar-width: thin;
	}

	.weekday-header {
		position: sticky;
		top: 0;
		z-index: 2;
		height: 2rem;
		display: flex;
		align-items: center;
		justify-content: center;
		font-size: 0.7rem;
		font-weight: 700;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--tb-grid-head-text);
		background: var(--tb-grid-head-bg);
		border-right: 1px solid var(--tb-grid-col-border);
		border-bottom: 1px solid var(--tb-grid-col-border);
	}

	.weekday-header:last-of-type {
		border-right: none;
	}

	.day-cell {
		min-height: 116px;
		padding: 0.42rem;
		display: grid;
		grid-template-rows: auto minmax(0, 1fr);
		gap: 0.36rem;
		border-right: 1px solid var(--tb-grid-col-border);
		border-bottom: 1px solid var(--tb-grid-col-border);
		background: color-mix(in srgb, var(--tb-panel-bg) 70%, transparent);
	}

	.day-cell header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.35rem;
	}

	.day-cell header > span {
		font-size: 0.74rem;
		font-weight: 700;
		color: var(--tb-cell-text);
	}

	.day-cell header > small {
		height: 1.2rem;
		padding: 0 0.36rem;
		border-radius: 999px;
		display: inline-grid;
		place-items: center;
		font-size: 0.64rem;
		font-weight: 700;
		color: var(--tb-cell-muted);
		border: 1px solid var(--tb-grid-col-border);
		background: color-mix(in srgb, var(--tb-btn-bg) 72%, transparent);
	}

	.day-cell.is-outside-month {
		background: color-mix(in srgb, var(--tb-panel-bg) 42%, transparent);
	}

	.day-cell.is-outside-month header > span {
		color: color-mix(in srgb, var(--tb-cell-muted) 88%, transparent);
	}

	.day-cell.is-today {
		box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--tb-grid-head-bg) 32%, transparent);
	}

	.day-task-list {
		min-height: 0;
		display: grid;
		align-content: start;
		gap: 0.26rem;
	}

	.task-chip {
		width: 100%;
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		padding: 0.28rem 0.42rem;
		border-radius: 8px;
		border: 1px solid var(--tb-grid-col-border);
		background: color-mix(in srgb, var(--tb-panel-bg) 75%, transparent);
		color: var(--tb-cell-text);
		font-size: 0.7rem;
		line-height: 1.2;
		text-align: left;
		cursor: pointer;
	}

	.task-chip:hover {
		background: color-mix(in srgb, var(--tb-btn-bg) 68%, transparent);
	}

	.task-chip span:last-child {
		min-width: 0;
		overflow: hidden;
		white-space: nowrap;
		text-overflow: ellipsis;
	}

	.task-dot {
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 999px;
		flex-shrink: 0;
	}

	.status-todo .task-dot,
	.task-dot.status-todo {
		background: #6b7280;
	}

	.status-in_progress .task-dot,
	.task-dot.status-in_progress {
		background: #3b82f6;
	}

	.status-done .task-dot,
	.task-dot.status-done {
		background: #22c55e;
	}

	.more-chip {
		height: 1.64rem;
		border-radius: 8px;
		border: 1px dashed var(--tb-grid-col-border);
		background: transparent;
		color: var(--tb-cell-muted);
		font-size: 0.68rem;
		font-weight: 700;
		cursor: pointer;
	}

	.more-chip:hover {
		border-color: color-mix(in srgb, var(--tb-grid-head-bg) 28%, var(--tb-grid-col-border));
		color: var(--tb-cell-text);
	}

	.undated-section {
		display: grid;
		gap: 0.5rem;
		padding: 0.62rem 0.7rem;
		border-radius: 14px;
		border: 1px solid var(--tb-grid-border);
		background: var(--tb-panel-bg);
	}

	.undated-section > header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}

	.undated-section h4 {
		margin: 0;
		font-size: 0.8rem;
		color: var(--tb-cell-text);
	}

	.undated-section > header span {
		font-size: 0.72rem;
		font-weight: 700;
		color: var(--tb-cell-muted);
	}

	.undated-empty {
		margin: 0;
		font-size: 0.74rem;
		color: var(--tb-cell-muted);
	}

	.undated-list {
		display: flex;
		flex-wrap: wrap;
		gap: 0.35rem;
	}

	.undated-list .task-chip {
		width: auto;
		max-width: 260px;
	}

	@media (max-width: 1080px) {
		.calendar-grid {
			grid-auto-rows: minmax(96px, 1fr);
		}
	}
</style>
