<script lang="ts">
	import { createEventDispatcher, onMount, tick } from 'svelte';
	import type { FieldSchema } from '$lib/stores/fieldSchema';
	import type { OnlineMember } from '$lib/types/chat';
	import { parseFlexibleDateValue } from '$lib/utils/dateParsing';

	type TaskLike = {
		id: string;
		title: string;
		status?: string;
		assigneeId?: string;
		customFields?: Record<string, unknown>;
		dueDate?: number | string | null;
		startDate?: number | string | null;
		taskType?: string;
	};

	type MemberRow = {
		key: string;
		id: string;
		name: string;
		isOnline: boolean;
	};

	type DatedTask = {
		taskId: string;
		title: string;
		status: 'todo' | 'in_progress' | 'done';
		assigneeKey: string;
		assigneeLabel: string;
		startDate: Date;
		endDate: Date;
	};

	type UndatedTask = {
		taskId: string;
		title: string;
		status: 'todo' | 'in_progress' | 'done';
		assigneeLabel: string;
	};

	type TimelineBar = {
		taskId: string;
		title: string;
		status: 'todo' | 'in_progress' | 'done';
		startIndex: number;
		span: number;
		lane: number;
	};

	type RowMetrics = {
		bars: TimelineBar[];
		overloadedDayIndices: Set<number>;
		rowHeight: number;
	};

	type TimelineDay = {
		key: string;
		label: string;
		dayLabel: string;
		isToday: boolean;
	};

	export let tasks: TaskLike[] = [];
	export let onlineMembers: OnlineMember[] = [];
	export let fieldSchemas: FieldSchema[] = [];

	const dispatch = createEventDispatcher<{
		editTask: { taskId: string };
	}>();

	const UNASSIGNED_KEY = 'unassigned';
	const PREVIOUS_DAYS = 7;
	const VISIBLE_WEEK_DAYS = 7;
	const NEXT_DAYS = 7;
	const TOTAL_TIMELINE_DAYS = PREVIOUS_DAYS + VISIBLE_WEEK_DAYS + NEXT_DAYS;
	const CENTRAL_WEEK_START_INDEX = PREVIOUS_DAYS;
	const DAY_COLUMN_WIDTH = 120;

	let weekStartDate = startOfWeekMonday(new Date());
	let timelineScrollElement: HTMLDivElement | null = null;

	$: todayKey = toDateKey(new Date());
	$: startDateFieldId = resolveDateFieldId(fieldSchemas, ['startdate']);
	$: dueDateFieldId = resolveDateFieldId(fieldSchemas, ['duedate', 'enddate', 'deadline']);
	$: memberNameByKey = buildMemberNameByKey(onlineMembers);
	$: parsedTasks = parseTasksForTimeline(tasks, startDateFieldId, dueDateFieldId, memberNameByKey);
	$: datedTasks = parsedTasks.datedTasks;
	$: undatedTasks = parsedTasks.undatedTasks;
	$: weekLabel = `${formatLongDate(weekStartDate)} - ${formatLongDate(
		addDays(weekStartDate, VISIBLE_WEEK_DAYS - 1)
	)}`;
	$: timelineStartDate = addDays(weekStartDate, -PREVIOUS_DAYS);
	$: timelineDays = buildTimelineDays(timelineStartDate, TOTAL_TIMELINE_DAYS, todayKey);
	$: memberRows = buildMemberRows(onlineMembers, tasks, memberNameByKey);
	$: rowMetricsByKey = buildRowMetrics(
		memberRows,
		datedTasks,
		timelineStartDate,
		timelineDays.length
	);

	onMount(() => {
		void centerVisibleWeek('auto');
	});

	function normalizeSchemaName(value: string) {
		return value
			.trim()
			.toLowerCase()
			.replace(/[\s_-]+/g, '');
	}

	function resolveDateFieldId(schemas: FieldSchema[], aliases: string[]) {
		const aliasLookup = new Set(aliases.map((alias) => normalizeSchemaName(alias)));
		for (const schema of schemas) {
			if (aliasLookup.has(normalizeSchemaName(schema.name))) {
				return schema.fieldId;
			}
		}
		for (const schema of schemas) {
			if (aliasLookup.has(normalizeSchemaName(schema.fieldId))) {
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

	function normalizeMemberKey(value: string) {
		const normalized = value.trim().toLowerCase();
		return normalized || UNASSIGNED_KEY;
	}

	function readableName(value: string) {
		const trimmed = value.trim();
		if (!trimmed) {
			return 'Unassigned';
		}
		return trimmed.replace(/[_-]+/g, ' ');
	}

	function buildMemberNameByKey(members: OnlineMember[]) {
		const map = new Map<string, string>();
		for (const member of members) {
			const key = normalizeMemberKey(member.id);
			if (!key || map.has(key)) {
				continue;
			}
			map.set(key, readableName(member.name || member.id));
		}
		return map;
	}

	function readCustomDate(
		task: TaskLike,
		preferredFieldId: string,
		fallbackKeys: string[],
		directField?: number | string | null
	) {
		if (directField != null) {
			return parseFlexibleDateValue(directField);
		}
		const fields = task.customFields ?? {};
		if (preferredFieldId && preferredFieldId in fields) {
			return parseFlexibleDateValue(fields[preferredFieldId]);
		}
		for (const key of fallbackKeys) {
			if (key in fields) {
				return parseFlexibleDateValue(fields[key]);
			}
		}
		return null;
	}

	function parseTasksForTimeline(
		sourceTasks: TaskLike[],
		startFieldId: string,
		dueFieldId: string,
		memberMap: Map<string, string>
	) {
		const datedTasks: DatedTask[] = [];
		const undatedTasks: UndatedTask[] = [];

		for (const task of sourceTasks) {
			const title = task.title?.trim() || 'Untitled task';
			const status = normalizeStatus(task.status);
			const assigneeRaw = task.assigneeId?.trim() ?? '';
			const assigneeKey = normalizeMemberKey(assigneeRaw);
			const assigneeLabel =
				assigneeKey === UNASSIGNED_KEY
					? 'Unassigned'
					: (memberMap.get(assigneeKey) ?? readableName(assigneeRaw));
			const startDate = readCustomDate(
				task,
				startFieldId,
				['start_date', 'startDate', 'startdate'],
				task.startDate
			);
			const dueDate = readCustomDate(
				task,
				dueFieldId,
				['due_date', 'dueDate', 'duedate', 'end_date', 'endDate', 'enddate', 'deadline'],
				task.dueDate
			);

			if (!startDate && !dueDate) {
				undatedTasks.push({
					taskId: task.id,
					title,
					status,
					assigneeLabel
				});
				continue;
			}

			const resolvedStart = startDate ?? dueDate ?? new Date();
			const resolvedEnd = dueDate ?? startDate ?? resolvedStart;
			const startTime = resolvedStart.getTime();
			const endTime = resolvedEnd.getTime();
			const rangeStart = startTime <= endTime ? resolvedStart : resolvedEnd;
			const rangeEnd = startTime <= endTime ? resolvedEnd : resolvedStart;

			datedTasks.push({
				taskId: task.id,
				title,
				status,
				assigneeKey,
				assigneeLabel,
				startDate: stripTime(rangeStart),
				endDate: stripTime(rangeEnd)
			});
		}

		undatedTasks.sort((left, right) =>
			left.title.localeCompare(right.title, undefined, { sensitivity: 'base' })
		);
		return { datedTasks, undatedTasks };
	}

	function buildMemberRows(
		members: OnlineMember[],
		sourceTasks: TaskLike[],
		memberMap: Map<string, string>
	) {
		const rowsByKey = new Map<string, MemberRow>();
		for (const member of members) {
			const memberId = member.id.trim();
			const key = normalizeMemberKey(memberId);
			if (!key || key === UNASSIGNED_KEY || rowsByKey.has(key)) {
				continue;
			}
			rowsByKey.set(key, {
				key,
				id: memberId,
				name: memberMap.get(key) ?? readableName(member.name || memberId),
				isOnline: Boolean(member.isOnline)
			});
		}

		for (const task of sourceTasks) {
			const assigneeId = task.assigneeId?.trim() ?? '';
			const key = normalizeMemberKey(assigneeId);
			if (!assigneeId || key === UNASSIGNED_KEY || rowsByKey.has(key)) {
				continue;
			}
			rowsByKey.set(key, {
				key,
				id: assigneeId,
				name: memberMap.get(key) ?? readableName(assigneeId),
				isOnline: false
			});
		}

		const rows = [...rowsByKey.values()].sort(
			(left, right) =>
				Number(right.isOnline) - Number(left.isOnline) ||
				left.name.localeCompare(right.name, undefined, { sensitivity: 'base' })
		);

		rows.push({
			key: UNASSIGNED_KEY,
			id: '',
			name: 'Unassigned',
			isOnline: false
		});
		return rows;
	}

	function buildRowMetrics(
		rows: MemberRow[],
		timelineTasks: DatedTask[],
		timelineStart: Date,
		timelineLength: number
	) {
		const maxIndex = Math.max(0, timelineLength - 1);
		const metricsByKey = new Map<string, RowMetrics>();

		for (const row of rows) {
			const rowTasks = timelineTasks
				.filter((task) => task.assigneeKey === row.key)
				.map((task) => {
					const startIndex = diffDays(timelineStart, task.startDate);
					const endIndex = diffDays(timelineStart, task.endDate);
					return {
						...task,
						startIndex,
						endIndex
					};
				})
				.filter((task) => task.endIndex >= 0 && task.startIndex <= maxIndex)
				.sort(
					(left, right) =>
						left.startIndex - right.startIndex ||
						left.endIndex - right.endIndex ||
						left.title.localeCompare(right.title, undefined, { sensitivity: 'base' })
				);

			const dayCounts = new Array<number>(timelineLength).fill(0);
			const clippedTasks = rowTasks.map((task) => {
				const clippedStart = Math.max(0, task.startIndex);
				const clippedEnd = Math.min(maxIndex, task.endIndex);
				for (let dayIndex = clippedStart; dayIndex <= clippedEnd; dayIndex += 1) {
					dayCounts[dayIndex] += 1;
				}
				return {
					...task,
					clippedStart,
					clippedEnd
				};
			});

			const overloadedDayIndices = new Set<number>();
			dayCounts.forEach((count, dayIndex) => {
				if (count > 3) {
					overloadedDayIndices.add(dayIndex);
				}
			});

			const laneEndIndices: number[] = [];
			const bars: TimelineBar[] = [];
			for (const task of clippedTasks) {
				let lane = 0;
				while (lane < laneEndIndices.length && task.clippedStart <= laneEndIndices[lane]) {
					lane += 1;
				}
				if (lane === laneEndIndices.length) {
					laneEndIndices.push(task.clippedEnd);
				} else {
					laneEndIndices[lane] = task.clippedEnd;
				}
				bars.push({
					taskId: task.taskId,
					title: task.title,
					status: task.status,
					startIndex: task.clippedStart,
					span: Math.max(1, task.clippedEnd - task.clippedStart + 1),
					lane
				});
			}

			const laneCount = laneEndIndices.length > 0 ? laneEndIndices.length : 1;
			const rowHeight = Math.max(44, laneCount * 28 + 10);

			metricsByKey.set(row.key, {
				bars,
				overloadedDayIndices,
				rowHeight
			});
		}

		return metricsByKey;
	}

	function startOfWeekMonday(date: Date) {
		const normalized = new Date(date.getFullYear(), date.getMonth(), date.getDate());
		const day = normalized.getDay();
		const offset = (day + 6) % 7;
		normalized.setDate(normalized.getDate() - offset);
		return normalized;
	}

	function addDays(date: Date, amount: number) {
		const next = new Date(date.getFullYear(), date.getMonth(), date.getDate());
		next.setDate(next.getDate() + amount);
		return next;
	}

	function stripTime(date: Date) {
		return new Date(date.getFullYear(), date.getMonth(), date.getDate());
	}

	function diffDays(baseDate: Date, otherDate: Date) {
		const base = stripTime(baseDate).getTime();
		const other = stripTime(otherDate).getTime();
		return Math.floor((other - base) / (24 * 60 * 60 * 1000));
	}

	function pad2(value: number) {
		return String(value).padStart(2, '0');
	}

	function toDateKey(date: Date) {
		return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}`;
	}

	function formatLongDate(date: Date) {
		return date.toLocaleDateString(undefined, {
			month: 'short',
			day: 'numeric'
		});
	}

	function buildTimelineDays(startDate: Date, count: number, today: string): TimelineDay[] {
		const days: TimelineDay[] = [];
		for (let index = 0; index < count; index += 1) {
			const date = addDays(startDate, index);
			days.push({
				key: toDateKey(date),
				label: date.toLocaleDateString(undefined, { weekday: 'short' }),
				dayLabel: `${date.getDate()} ${date.toLocaleDateString(undefined, { month: 'short' })}`,
				isToday: toDateKey(date) === today
			});
		}
		return days;
	}

	function avatarInitials(name: string) {
		const parts = name.trim().split(/\s+/).filter(Boolean).slice(0, 2);
		if (parts.length === 0) {
			return 'U';
		}
		return parts.map((part) => part[0]?.toUpperCase() ?? '').join('');
	}

	function isFocusDay(dayIndex: number) {
		return (
			dayIndex >= CENTRAL_WEEK_START_INDEX &&
			dayIndex < CENTRAL_WEEK_START_INDEX + VISIBLE_WEEK_DAYS
		);
	}

	function truncateLabel(value: string, limit = 26) {
		const trimmed = value.trim();
		if (trimmed.length <= limit) {
			return trimmed || 'Untitled task';
		}
		return `${trimmed.slice(0, Math.max(1, limit - 1))}\u2026`;
	}

	async function centerVisibleWeek(behavior: ScrollBehavior = 'smooth') {
		await tick();
		timelineScrollElement?.scrollTo({
			left: CENTRAL_WEEK_START_INDEX * DAY_COLUMN_WIDTH,
			behavior
		});
	}

	function shiftWeek(offsetWeeks: number) {
		weekStartDate = addDays(weekStartDate, offsetWeeks * VISIBLE_WEEK_DAYS);
		void centerVisibleWeek();
	}

	function jumpToCurrentWeek() {
		weekStartDate = startOfWeekMonday(new Date());
		void centerVisibleWeek();
	}

	function openTask(taskId: string) {
		const normalizedTaskID = taskId.trim();
		if (!normalizedTaskID) {
			return;
		}
		dispatch('editTask', { taskId: normalizedTaskID });
	}
</script>

<section class="workload-view" aria-label="Workload view">
	<header class="workload-toolbar">
		<div class="toolbar-left">
			<button type="button" class="toolbar-btn" on:click={() => shiftWeek(-1)}>
				Previous week
			</button>
			<h3>{weekLabel}</h3>
			<button type="button" class="toolbar-btn" on:click={() => shiftWeek(1)}> Next week </button>
		</div>
		<button type="button" class="toolbar-btn today" on:click={jumpToCurrentWeek}>Today</button>
	</header>

	<div class="timeline-shell" bind:this={timelineScrollElement} aria-label="Workload timeline grid">
		<div class="timeline-header-row" style={`--day-count:${timelineDays.length};`}>
			<div class="member-header">Assignee</div>
			<div class="days-header">
				{#each timelineDays as day, dayIndex (day.key)}
					<div
						class="day-header-cell"
						class:is-focus={isFocusDay(dayIndex)}
						class:is-today={day.isToday}
					>
						<strong>{day.label}</strong>
						<span>{day.dayLabel}</span>
					</div>
				{/each}
			</div>
		</div>

		<div class="timeline-body">
			{#each memberRows as row (row.key)}
				{@const metrics = rowMetricsByKey.get(row.key)}
				{@const safeMetrics = metrics ?? {
					bars: [],
					overloadedDayIndices: new Set<number>(),
					rowHeight: 44
				}}
				<div
					class="timeline-row"
					style={`--day-count:${timelineDays.length}; --row-height:${safeMetrics.rowHeight}px;`}
				>
					<div class="member-cell">
						<span class="member-avatar">{avatarInitials(row.name)}</span>
						<div class="member-copy">
							<strong>{row.name}</strong>
							{#if row.isOnline}
								<span class="member-online">Online</span>
							{:else if row.key !== UNASSIGNED_KEY}
								<span class="member-offline">Offline</span>
							{/if}
						</div>
					</div>

					<div class="timeline-track">
						<div class="day-cells">
							{#each timelineDays as day, dayIndex (day.key)}
								<div
									class="day-cell"
									class:is-focus={isFocusDay(dayIndex)}
									class:is-today={day.isToday}
									class:is-overloaded={safeMetrics.overloadedDayIndices.has(dayIndex)}
								></div>
							{/each}
						</div>

						<div class="bar-layer">
							{#each safeMetrics.bars as bar (`${row.key}-${bar.taskId}`)}
								<button
									type="button"
									class={`task-bar status-${bar.status}`}
									style={`--start:${bar.startIndex}; --span:${bar.span}; --lane:${bar.lane};`}
									title={bar.title}
									on:click={() => openTask(bar.taskId)}
								>
									{truncateLabel(bar.title)}
								</button>
							{/each}
						</div>
					</div>
				</div>
			{/each}
		</div>
	</div>

	<section class="undated-section" aria-label="Undated tasks">
		<header>
			<h4>Undated tasks</h4>
			<span>{undatedTasks.length}</span>
		</header>
		{#if undatedTasks.length === 0}
			<p class="empty-state">All tasks shown here include start or due dates.</p>
		{:else}
			<div class="undated-list">
				{#each undatedTasks as task (task.taskId)}
					<button
						type="button"
						class={`undated-chip status-${task.status}`}
						title={`${task.title} • ${task.assigneeLabel}`}
						on:click={() => openTask(task.taskId)}
					>
						<span class="chip-dot"></span>
						<span>{truncateLabel(task.title, 30)}</span>
						<small>{task.assigneeLabel}</small>
					</button>
				{/each}
			</div>
		{/if}
	</section>
</section>

<style>
	.workload-view {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: auto minmax(0, 1fr) auto;
		gap: 0.68rem;
	}

	.workload-toolbar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.6rem;
		padding: 0.58rem 0.72rem;
		border-radius: 14px;
		border: 1px solid var(--tb-grid-border);
		background: var(--tb-panel-bg);
	}

	.toolbar-left {
		display: flex;
		align-items: center;
		gap: 0.48rem;
		min-width: 0;
	}

	.toolbar-left h3 {
		margin: 0;
		font-size: 0.84rem;
		color: var(--tb-cell-text);
		white-space: nowrap;
	}

	.toolbar-btn {
		height: 1.95rem;
		padding: 0 0.64rem;
		border-radius: 10px;
		border: 1px solid var(--tb-btn-border);
		background: var(--tb-btn-bg);
		color: var(--tb-btn-text);
		font-size: 0.74rem;
		font-weight: 700;
		cursor: pointer;
	}

	.toolbar-btn.today {
		background: color-mix(in srgb, var(--tb-btn-bg) 68%, #ffffff 32%);
	}

	.toolbar-btn:hover {
		background: color-mix(in srgb, var(--tb-btn-bg) 74%, #ffffff 26%);
	}

	.timeline-shell {
		min-height: 0;
		overflow: auto;
		border: 1px solid var(--tb-grid-border);
		border-radius: 14px;
		background: var(--tb-grid-bg);
		scrollbar-width: thin;
	}

	.timeline-header-row,
	.timeline-row {
		display: grid;
		grid-template-columns: 180px minmax(0, 1fr);
		min-width: calc(180px + (var(--day-count, 21) * 120px));
	}

	.timeline-header-row {
		position: sticky;
		top: 0;
		z-index: 4;
	}

	.member-header {
		position: sticky;
		left: 0;
		z-index: 5;
		height: 2.5rem;
		display: flex;
		align-items: center;
		padding: 0 0.64rem;
		background: var(--tb-grid-head-bg);
		color: var(--tb-grid-head-text);
		border-right: 1px solid var(--tb-grid-col-border);
		border-bottom: 1px solid var(--tb-grid-col-border);
		font-size: 0.7rem;
		font-weight: 700;
		letter-spacing: 0.05em;
		text-transform: uppercase;
	}

	.days-header {
		display: grid;
		grid-template-columns: repeat(var(--day-count), 120px);
		background: var(--tb-grid-head-bg);
		border-bottom: 1px solid var(--tb-grid-col-border);
	}

	.day-header-cell {
		height: 2.5rem;
		display: grid;
		align-content: center;
		justify-items: center;
		gap: 0.08rem;
		border-right: 1px solid var(--tb-grid-col-border);
		color: var(--tb-grid-head-muted);
		font-size: 0.64rem;
	}

	.day-header-cell strong {
		font-size: 0.68rem;
		color: var(--tb-grid-head-text);
	}

	.day-header-cell.is-focus {
		background: color-mix(in srgb, var(--tb-btn-bg) 18%, transparent);
	}

	.day-header-cell.is-today {
		box-shadow: inset 0 -2px 0 color-mix(in srgb, var(--tb-grid-head-text) 44%, transparent);
	}

	.timeline-body {
		display: grid;
	}

	.timeline-row {
		border-bottom: 1px solid var(--tb-grid-col-border);
	}

	.member-cell {
		position: sticky;
		left: 0;
		z-index: 3;
		min-height: var(--row-height, 44px);
		padding: 0.42rem 0.58rem;
		display: flex;
		align-items: center;
		gap: 0.48rem;
		background: color-mix(in srgb, var(--tb-panel-bg) 86%, transparent);
		border-right: 1px solid var(--tb-grid-col-border);
	}

	.member-avatar {
		width: 1.58rem;
		height: 1.58rem;
		border-radius: 999px;
		display: grid;
		place-items: center;
		font-size: 0.62rem;
		font-weight: 800;
		background: color-mix(in srgb, var(--tb-grid-head-bg) 22%, var(--tb-btn-bg));
		color: var(--tb-cell-text);
	}

	.member-copy {
		display: grid;
		gap: 0.08rem;
		min-width: 0;
	}

	.member-copy strong {
		font-size: 0.74rem;
		color: var(--tb-cell-text);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.member-online,
	.member-offline {
		font-size: 0.63rem;
		font-weight: 700;
	}

	.member-online {
		color: #22c55e;
	}

	.member-offline {
		color: var(--tb-cell-muted);
	}

	.timeline-track {
		position: relative;
		min-height: var(--row-height, 44px);
	}

	.day-cells {
		display: grid;
		grid-template-columns: repeat(var(--day-count), 120px);
		min-height: var(--row-height, 44px);
	}

	.day-cell {
		border-right: 1px solid var(--tb-grid-col-border);
		background: color-mix(in srgb, var(--tb-panel-bg) 55%, transparent);
	}

	.day-cell.is-focus {
		background: color-mix(in srgb, var(--tb-btn-bg) 50%, transparent);
	}

	.day-cell.is-today {
		box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--tb-grid-head-bg) 30%, transparent);
	}

	.day-cell.is-overloaded {
		background: color-mix(in srgb, #f59e0b 28%, transparent);
	}

	.bar-layer {
		position: absolute;
		inset: 0;
		pointer-events: none;
	}

	.task-bar {
		pointer-events: auto;
		position: absolute;
		left: calc((var(--start) * 120px) + 4px);
		top: calc((var(--lane) * 28px) + 4px);
		width: calc((var(--span) * 120px) - 8px);
		height: 22px;
		padding: 0 0.4rem;
		border-radius: 7px;
		border: 1px solid transparent;
		text-align: left;
		font-size: 0.67rem;
		font-weight: 700;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		cursor: pointer;
	}

	.task-bar.status-todo {
		background: color-mix(in srgb, #6b7280 26%, transparent);
		border-color: color-mix(in srgb, #6b7280 48%, transparent);
		color: var(--tb-cell-text);
	}

	.task-bar.status-in_progress {
		background: color-mix(in srgb, #3b82f6 24%, transparent);
		border-color: color-mix(in srgb, #3b82f6 50%, transparent);
		color: var(--tb-cell-text);
	}

	.task-bar.status-done {
		background: color-mix(in srgb, #22c55e 24%, transparent);
		border-color: color-mix(in srgb, #22c55e 46%, transparent);
		color: var(--tb-cell-text);
	}

	.task-bar:hover {
		filter: brightness(1.03);
	}

	.undated-section {
		display: grid;
		gap: 0.48rem;
		padding: 0.6rem 0.72rem;
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

	.empty-state {
		margin: 0;
		font-size: 0.73rem;
		color: var(--tb-cell-muted);
	}

	.undated-list {
		display: flex;
		flex-wrap: wrap;
		gap: 0.35rem;
	}

	.undated-chip {
		display: inline-flex;
		align-items: center;
		gap: 0.34rem;
		max-width: 320px;
		padding: 0.3rem 0.44rem;
		border-radius: 8px;
		border: 1px solid var(--tb-grid-col-border);
		background: color-mix(in srgb, var(--tb-panel-bg) 75%, transparent);
		color: var(--tb-cell-text);
		font-size: 0.7rem;
		cursor: pointer;
	}

	.undated-chip:hover {
		background: color-mix(in srgb, var(--tb-btn-bg) 66%, transparent);
	}

	.undated-chip > span:nth-child(2) {
		max-width: 200px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.undated-chip small {
		font-size: 0.62rem;
		color: var(--tb-cell-muted);
	}

	.chip-dot {
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 999px;
		flex-shrink: 0;
	}

	.undated-chip.status-todo .chip-dot {
		background: #6b7280;
	}

	.undated-chip.status-in_progress .chip-dot {
		background: #3b82f6;
	}

	.undated-chip.status-done .chip-dot {
		background: #22c55e;
	}

	@media (max-width: 920px) {
		.workload-toolbar {
			flex-direction: column;
			align-items: flex-start;
		}

		.toolbar-left {
			flex-wrap: wrap;
		}

		.timeline-header-row,
		.timeline-row {
			grid-template-columns: 150px minmax(0, 1fr);
			min-width: calc(150px + (var(--day-count, 21) * 120px));
		}
	}
</style>
