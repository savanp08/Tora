<script lang="ts">
	import { projectTimeline } from '$lib/stores/timeline';
	import type { TimelineTaskPriority } from '$lib/types/timeline';

	type StatTone = 'blue' | 'green' | 'orange' | 'purple' | 'neutral';
	type StatCard = {
		key: string;
		icon: 'done' | 'active' | 'priority' | 'budget';
		label: string;
		value: string;
		sub: string;
		tone: StatTone;
	};

	type SliceItem = {
		label: string;
		count: number;
		color: string;
	};

	type ActiveRow = {
		id: string;
		title: string;
		status: string;
		priority?: TimelineTaskPriority;
		assignee?: string;
		dueLabel: string;
		remainingLabel: string;
		remainingDays: number;
	};

	type SprintSnapshot = {
		id: string;
		name: string;
		startLabel: string;
		endLabel: string;
		total: number;
		done: number;
		inProgress: number;
		todo: number;
		remaining: number;
		sharePct: number;
		color: string;
		isCurrent: boolean;
	};

	type DonutInput = {
		label: string;
		value: number;
		color: string;
	};

	type DonutSegment = DonutInput & {
		pct: number;
		dashArray: string;
		dashOffset: number;
	};

	type TrendPoint = {
		x: number;
		y: number;
		label: string;
		remaining: number;
		done: number;
		total: number;
	};

	type ContributorRow = {
		id: string;
		name: string;
		completed: number;
		inProgress: number;
		total: number;
		barPct: number;
		color: string;
	};

	const ACTIVE_PREVIEW = 10;
	const TREND_W = 460;
	const TREND_H = 220;
	const TREND_PADDING = 26;
	const CHART_COLORS = [
		'var(--tb-blue)',
		'var(--tb-purple)',
		'var(--tb-teal)',
		'var(--tb-orange)',
		'var(--tb-green)'
	];

	$: timeline = $projectTimeline;
	$: sprints = timeline?.sprints ?? [];
	$: activeSprintIndex = (() => {
		const inProgressIndex = sprints.findIndex((sprint) =>
			sprint.tasks.some((task) => task.status === 'in_progress')
		);
		if (inProgressIndex >= 0) return inProgressIndex;
		const openIndex = sprints.findIndex((sprint) =>
			sprint.tasks.some((task) => task.status !== 'done')
		);
		if (openIndex >= 0) return openIndex;
		return sprints.length > 0 ? sprints.length - 1 : -1;
	})();
	$: scopedSprints = activeSprintIndex >= 0 ? sprints.slice(activeSprintIndex) : [];
	$: currentSprint = scopedSprints[0] ?? null;
	$: remainingSprints = scopedSprints.slice(1);

	$: scopedTasks = scopedSprints.flatMap((sprint) => sprint.tasks);
	$: totalTasks = scopedTasks.length;
	$: doneTasks = scopedTasks.filter((task) => task.status === 'done');
	$: inProgTasks = scopedTasks.filter((task) => task.status === 'in_progress');
	$: todoTasks = scopedTasks.filter((task) => task.status === 'todo');
	$: completionRate = totalTasks > 0 ? Math.round((doneTasks.length / totalTasks) * 100) : 0;
	$: highPriorityCount = scopedTasks.filter(
		(task) => task.priority === 'critical' || task.priority === 'high'
	).length;
	$: overdueCount = scopedTasks.filter(
		(task) => task.status !== 'done' && remainingDaysFromNow(task.end_date ?? '') < 0
	).length;

	$: budgetTotal = timeline?.budget_total ?? 0;
	$: budgetSpent = timeline?.budget_spent ?? 0;
	$: estimatedCost = timeline?.estimated_cost ?? '';
	$: scopedBudgetFromTasks = scopedTasks.reduce(
		(sum, task) =>
			sum +
			(typeof task.budget === 'number' && Number.isFinite(task.budget) && task.budget > 0
				? task.budget
				: 0),
		0
	);
	$: scopedSpentFromTasks = scopedTasks.reduce(
		(sum, task) =>
			sum +
			(typeof task.actual_cost === 'number' &&
			Number.isFinite(task.actual_cost) &&
			task.actual_cost >= 0
				? task.actual_cost
				: 0),
		0
	);
	$: hasScopedSpentData = scopedTasks.some(
		(task) =>
			typeof task.actual_cost === 'number' &&
			Number.isFinite(task.actual_cost) &&
			task.actual_cost >= 0
	);
	$: scopedBudgetAllocated = scopedSprints.reduce(
		(sum, sprint) => sum + (sprint.budget_allocated ?? 0),
		0
	);
	$: totalBudgetValue =
		scopedBudgetFromTasks > 0
			? scopedBudgetFromTasks
			: scopedBudgetAllocated > 0
				? scopedBudgetAllocated
				: budgetTotal;
	$: effectiveBudgetSpent = hasScopedSpentData ? scopedSpentFromTasks : budgetSpent;
	$: hasAnyBudget = totalBudgetValue > 0;
	$: budgetPercent = hasAnyBudget
		? Math.min(100, Math.round((effectiveBudgetSpent / totalBudgetValue) * 100))
		: 0;

	$: projectStartDate = firstValidDate(scopedSprints.map((sp) => sp.start_date));
	$: projectEndDate = firstValidDate([
		scopedSprints[scopedSprints.length - 1]?.end_date ?? '',
		...scopedSprints.map((sp) => sp.end_date)
	]);
	$: projectDurationWeeks = computeProjectWeeks(projectStartDate, projectEndDate);
	$: elapsedProjectWeeks = computeElapsedWeeks(projectStartDate, projectEndDate);
	$: remainingProjectWeeks = Math.max(0, projectDurationWeeks - elapsedProjectWeeks);
	$: weeklyBudgetValue = hasAnyBudget ? totalBudgetValue / projectDurationWeeks : 0;
	$: weeklySpendValue = hasAnyBudget ? effectiveBudgetSpent / elapsedProjectWeeks : 0;

	$: activeWorkRows = (currentSprint?.tasks ?? [])
		.map<ActiveRow>((task) => {
			const dueDate = task.end_date || currentSprint?.end_date || '';
			const remDays = remainingDaysFromNow(dueDate);
			return {
				id: task.id,
				title: task.title,
				status: task.status,
				priority: task.priority,
				assignee: task.assignee,
				dueLabel: dueDate ? fmtDate(dueDate) : 'No date',
				remainingLabel: task.status === 'done' ? 'Completed' : remainingLabel(remDays),
				remainingDays: remDays
			};
		})
		.sort((a, b) => {
			if (a.status === 'done' && b.status !== 'done') return 1;
			if (a.status !== 'done' && b.status === 'done') return -1;
			const priorityDelta = priorityRank(a.priority) - priorityRank(b.priority);
			if (priorityDelta !== 0) return priorityDelta;
			const aDays = Number.isFinite(a.remainingDays) ? a.remainingDays : Number.MAX_SAFE_INTEGER;
			const bDays = Number.isFinite(b.remainingDays) ? b.remainingDays : Number.MAX_SAFE_INTEGER;
			return aDays - bDays;
		});
	$: visibleActiveRows = activeWorkRows.slice(0, ACTIVE_PREVIEW);
	$: hiddenActiveRows = Math.max(0, activeWorkRows.length - visibleActiveRows.length);

	$: currentStatusData = [
		{
			label: 'Done',
			count: currentSprint?.tasks.filter((task) => task.status === 'done').length ?? 0,
			color: 'var(--tb-green)'
		},
		{
			label: 'In Progress',
			count: currentSprint?.tasks.filter((task) => task.status === 'in_progress').length ?? 0,
			color: 'var(--tb-blue)'
		},
		{
			label: 'To Do',
			count: currentSprint?.tasks.filter((task) => task.status === 'todo').length ?? 0,
			color: 'var(--tb-muted-chip)'
		}
	] satisfies SliceItem[];
	$: currentSprintTotal = currentStatusData.reduce((sum, item) => sum + item.count, 0);
	$: currentStatusSegments = buildDonutSegments(
		currentStatusData.map((item) => ({
			label: item.label,
			value: item.count,
			color: item.color
		}))
	);

	$: sprintSnapshots = (() => {
		const scopedTotal = Math.max(1, totalTasks);
		return scopedSprints.map<SprintSnapshot>((sprint, index) => {
			const done = sprint.tasks.filter((task) => task.status === 'done').length;
			const inProgress = sprint.tasks.filter((task) => task.status === 'in_progress').length;
			const todo = sprint.tasks.filter((task) => task.status === 'todo').length;
			const total = sprint.tasks.length;
			return {
				id: sprint.id,
				name: sprint.name,
				startLabel: sprint.start_date ? fmtDate(sprint.start_date) : '--',
				endLabel: sprint.end_date ? fmtDate(sprint.end_date) : '--',
				total,
				done,
				inProgress,
				todo,
				remaining: inProgress + todo,
				sharePct: Math.round((total / scopedTotal) * 100),
				color: CHART_COLORS[index % CHART_COLORS.length],
				isCurrent: index === 0
			};
		});
	})();

	$: sprintShareSegments = buildDonutSegments(
		sprintSnapshots.map((snapshot) => ({
			label: snapshot.name,
			value: snapshot.total,
			color: snapshot.color
		}))
	);

	$: trendPoints = buildTrendPoints(sprintSnapshots);
	$: trendLinePath = buildTrendLinePath(trendPoints);
	$: trendAreaPath = buildTrendAreaPath(trendPoints);
	$: maxRemaining = Math.max(1, ...sprintSnapshots.map((snapshot) => snapshot.remaining));
	$: currentCompletedTotal =
		currentSprint?.tasks.filter((task) => task.status === 'done').length ?? 0;

	$: contributorRows = (() => {
		if (!currentSprint) {
			return [] satisfies ContributorRow[];
		}
		const byActor = new Map<string, ContributorRow>();
		for (const task of currentSprint.tasks) {
			if (task.status !== 'done' && task.status !== 'in_progress') {
				continue;
			}
			const actorID = (task.status_actor_id || '').trim();
			const actorName = (task.status_actor_name || task.assignee || '').trim();
			const key = actorID || actorName.toLowerCase() || 'unknown';
			const existing =
				byActor.get(key) ??
				({
					id: actorID || key,
					name: actorName || formatActorFromID(actorID),
					completed: 0,
					inProgress: 0,
					total: 0,
					barPct: 0,
					color: colorFromSeed(key)
				} satisfies ContributorRow);
			if (task.status === 'done') existing.completed += 1;
			if (task.status === 'in_progress') existing.inProgress += 1;
			existing.total = existing.completed + existing.inProgress;
			byActor.set(key, existing);
		}
		const rows = [...byActor.values()]
			.sort(
				(a, b) =>
					b.completed - a.completed || b.inProgress - a.inProgress || a.name.localeCompare(b.name)
			)
			.slice(0, 6);
		const maxCompleted = Math.max(1, ...rows.map((row) => row.completed));
		return rows.map((row) => ({
			...row,
			barPct: Math.max(10, Math.round((row.completed / maxCompleted) * 100))
		}));
	})();

	$: topStats = [
		{
			key: 'done',
			icon: 'done',
			label: 'done',
			value: `${doneTasks.length}`,
			sub: `${completionRate}% completion in visible scope`,
			tone: 'green'
		},
		{
			key: 'active',
			icon: 'active',
			label: 'in progress',
			value: `${inProgTasks.length}`,
			sub: `${todoTasks.length} tasks are waiting in current + remaining`,
			tone: 'blue'
		},
		{
			key: 'priority',
			icon: 'priority',
			label: 'high priority',
			value: `${highPriorityCount}`,
			sub: `${overdueCount} overdue item${overdueCount === 1 ? '' : 's'}`,
			tone: 'orange'
		},
		{
			key: 'budget',
			icon: 'budget',
			label: 'budget used',
			value: hasAnyBudget ? `${budgetPercent}%` : '--',
			sub: hasAnyBudget
				? `${fmtMoney(effectiveBudgetSpent)} / ${fmtMoney(totalBudgetValue)}`
				: estimatedCost || 'Budget not configured',
			tone: hasAnyBudget ? (budgetPercent >= 85 ? 'orange' : 'purple') : 'neutral'
		}
	] satisfies StatCard[];

	function fmtMoney(v: number): string {
		if (v >= 1_000_000) return `$${(v / 1_000_000).toFixed(1)}M`;
		if (v >= 1_000) return `$${(v / 1_000).toFixed(0)}k`;
		return `$${Math.round(v)}`;
	}

	function fmtDate(value: string): string {
		const parsed = Date.parse(value);
		return Number.isFinite(parsed)
			? new Date(parsed).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
			: value;
	}

	function firstValidDate(candidates: string[]): string {
		for (const candidate of candidates) {
			const normalized = candidate?.trim();
			if (!normalized) continue;
			if (Number.isFinite(Date.parse(normalized))) return normalized;
		}
		return '';
	}

	function computeProjectWeeks(startDate: string, endDate: string): number {
		const start = Date.parse(startDate);
		const end = Date.parse(endDate);
		if (!Number.isFinite(start) || !Number.isFinite(end)) return 1;
		const days = Math.max(1, Math.ceil((end - start) / (24 * 60 * 60 * 1000)));
		return Math.max(1, Math.ceil(days / 7));
	}

	function computeElapsedWeeks(startDate: string, endDate: string): number {
		const start = Date.parse(startDate);
		if (!Number.isFinite(start)) return 1;
		const now = Date.now();
		const end = Date.parse(endDate);
		const cap = Number.isFinite(end) ? Math.min(now, end) : now;
		const days = Math.max(1, Math.ceil((cap - start) / (24 * 60 * 60 * 1000)));
		return Math.max(1, Math.ceil(days / 7));
	}

	function remainingDaysFromNow(value: string): number {
		const parsed = Date.parse(value);
		if (!Number.isFinite(parsed)) return Number.POSITIVE_INFINITY;
		const dayMs = 24 * 60 * 60 * 1000;
		return Math.ceil((parsed - Date.now()) / dayMs);
	}

	function remainingLabel(days: number): string {
		if (!Number.isFinite(days)) return 'No deadline';
		if (days < 0) return `Overdue ${Math.abs(days)}d`;
		if (days === 0) return 'Due today';
		return `Due in ${days}d`;
	}

	function statusLabel(status: string): string {
		if (status === 'done') return 'Done';
		if (status === 'in_progress') return 'In Progress';
		return 'To Do';
	}

	function statusColor(status: string): string {
		if (status === 'done') return 'var(--tb-green)';
		if (status === 'in_progress') return 'var(--tb-blue)';
		return 'var(--tb-muted-chip)';
	}

	function priorityLabel(priority?: TimelineTaskPriority): string {
		if (priority === 'critical') return 'Critical';
		if (priority === 'high') return 'High';
		if (priority === 'medium') return 'Medium';
		if (priority === 'low') return 'Low';
		return 'None';
	}

	function priorityColor(priority?: TimelineTaskPriority): string {
		if (priority === 'critical') return 'var(--tb-red)';
		if (priority === 'high') return 'var(--tb-orange)';
		if (priority === 'medium') return 'var(--tb-blue)';
		if (priority === 'low') return 'var(--tb-green)';
		return 'var(--tb-muted-chip)';
	}

	function priorityRank(priority?: TimelineTaskPriority): number {
		const order: Record<TimelineTaskPriority, number> = {
			critical: 0,
			high: 1,
			medium: 2,
			low: 3
		};
		if (!priority) return 4;
		return order[priority];
	}

	function buildDonutSegments(items: DonutInput[], radius = 44): DonutSegment[] {
		const total = items.reduce((sum, item) => sum + item.value, 0);
		if (total <= 0) {
			return [];
		}
		const circumference = 2 * Math.PI * radius;
		let consumed = 0;
		return items
			.filter((item) => item.value > 0)
			.map((item) => {
				const share = item.value / total;
				const arc = share * circumference;
				const segment: DonutSegment = {
					...item,
					pct: Math.round(share * 100),
					dashArray: `${arc.toFixed(2)} ${(circumference - arc).toFixed(2)}`,
					dashOffset: -consumed
				};
				consumed += arc;
				return segment;
			});
	}

	function buildTrendPoints(source: SprintSnapshot[]): TrendPoint[] {
		if (source.length === 0) return [];
		const max = Math.max(1, ...source.map((entry) => entry.remaining));
		const spanX = source.length > 1 ? (TREND_W - TREND_PADDING * 2) / (source.length - 1) : 0;
		return source.map((entry, index) => {
			const x = TREND_PADDING + spanX * index;
			const y = TREND_H - TREND_PADDING - (entry.remaining / max) * (TREND_H - TREND_PADDING * 2);
			return {
				x,
				y,
				label: shortSprintName(entry.name),
				remaining: entry.remaining,
				done: entry.done,
				total: entry.total
			};
		});
	}

	function buildTrendLinePath(points: TrendPoint[]): string {
		if (points.length === 0) return '';
		return points
			.map(
				(point, index) => `${index === 0 ? 'M' : 'L'} ${point.x.toFixed(2)} ${point.y.toFixed(2)}`
			)
			.join(' ');
	}

	function buildTrendAreaPath(points: TrendPoint[]): string {
		if (points.length === 0) return '';
		const floorY = TREND_H - TREND_PADDING;
		const first = points[0];
		const last = points[points.length - 1];
		const line = buildTrendLinePath(points);
		return `${line} L ${last.x.toFixed(2)} ${floorY.toFixed(2)} L ${first.x.toFixed(2)} ${floorY.toFixed(2)} Z`;
	}

	function shortSprintName(value: string): string {
		const parts = value.split(':');
		const trimmed = (parts[parts.length - 1] || value).trim();
		if (trimmed.length <= 12) return trimmed;
		return `${trimmed.slice(0, 11)}…`;
	}

	function formatActorFromID(actorID: string): string {
		const normalized = actorID.trim();
		if (!normalized) return 'Unknown';
		return `User ${normalized.slice(0, 8)}`;
	}

	function colorFromSeed(seed: string): string {
		let hash = 0;
		for (let i = 0; i < seed.length; i += 1) {
			hash = (hash << 5) - hash + seed.charCodeAt(i);
			hash |= 0;
		}
		const palette = CHART_COLORS;
		const index = Math.abs(hash) % palette.length;
		return palette[index];
	}

	function executionFillPct(status: string, remainingDays: number): number {
		if (status === 'done') return 100;
		if (status === 'in_progress') {
			if (!Number.isFinite(remainingDays)) return 55;
			if (remainingDays < 0) return 85;
			if (remainingDays <= 2) return 72;
			return 56;
		}
		if (!Number.isFinite(remainingDays)) return 28;
		if (remainingDays < 0) return 64;
		if (remainingDays <= 2) return 46;
		return 30;
	}

	function sprintCellStatus(
		snapshot: SprintSnapshot,
		cellIndex: number
	): 'done' | 'progress' | 'todo' {
		const slots = Math.max(1, Math.min(24, snapshot.total || 1));
		const doneSlots = Math.round((snapshot.done / Math.max(1, snapshot.total)) * slots);
		const progressSlots = Math.round((snapshot.inProgress / Math.max(1, snapshot.total)) * slots);
		if (cellIndex < doneSlots) return 'done';
		if (cellIndex < doneSlots + progressSlots) return 'progress';
		return 'todo';
	}

	function initials(name: string): string {
		const normalized = name.trim();
		if (!normalized) return '--';
		return normalized
			.split(' ')
			.filter(Boolean)
			.slice(0, 2)
			.map((part) => part[0]?.toUpperCase() ?? '')
			.join('');
	}
</script>

<div class="board" aria-label="Project dashboard overview">
	{#if !timeline}
		<div class="board-empty">
			<svg
				viewBox="0 0 24 24"
				fill="none"
				stroke="currentColor"
				stroke-width="1.6"
				stroke-linecap="round"
			>
				<rect x="3" y="3" width="18" height="18" rx="3" />
				<path d="M9 9h6M9 13h4" />
			</svg>
			<p>No project board yet.</p>
			<p>Use onboarding to generate your first dashboard.</p>
		</div>
	{:else}
		<header class="card project-head">
			<div class="head-main">
				<h2>{timeline.project_name}</h2>
			</div>
			<div class="head-kpi">
				<span>Remaining time</span>
				<strong>{remainingProjectWeeks}w</strong>
				<p>{elapsedProjectWeeks}w elapsed of {projectDurationWeeks}w plan</p>
			</div>
			<div class="head-kpi">
				<span>Budget</span>
				<strong>{hasAnyBudget ? fmtMoney(totalBudgetValue) : '--'}</strong>
				<p>
					{hasAnyBudget
						? `${fmtMoney(weeklyBudgetValue)} / week`
						: estimatedCost || 'Not configured'}
				</p>
			</div>
			<div class="head-kpi">
				<span>Usage</span>
				<strong>{hasAnyBudget ? `${budgetPercent}%` : '--'}</strong>
				<p>
					{hasAnyBudget
						? `${fmtMoney(effectiveBudgetSpent)} spent · ${fmtMoney(weeklySpendValue)} / week`
						: 'No usage data yet'}
				</p>
			</div>
		</header>

		<section class="stats-grid" aria-label="Project stats">
			{#each topStats as stat (stat.key)}
				<article class="card stat-card">
					<div class="stat-icon" data-tone={stat.tone} aria-hidden="true">
						{#if stat.icon === 'done'}
							<svg viewBox="0 0 24 24">
								<polyline points="20 6 9 17 4 12"></polyline>
							</svg>
						{:else if stat.icon === 'active'}
							<svg viewBox="0 0 24 24">
								<polyline points="4 15 9 10 13 14 20 7"></polyline>
								<polyline points="15 7 20 7 20 12"></polyline>
							</svg>
						{:else if stat.icon === 'priority'}
							<svg viewBox="0 0 24 24">
								<circle cx="12" cy="12" r="9"></circle>
								<path d="M12 7v6"></path>
								<path d="M12 17h.01"></path>
							</svg>
						{:else}
							<span class="stat-symbol">$$</span>
						{/if}
					</div>
					<div class="stat-copy">
						<div class="stat-line">
							<span class="stat-value">{stat.value}</span>
							<span class="stat-label">{stat.label}</span>
						</div>
						<p>{stat.sub}</p>
					</div>
				</article>
			{/each}
		</section>

		<section class="visual-grid" aria-label="Sprint visuals">
			<article class="card visual-card">
				<div class="section-head">
					<h4>Current sprint status</h4>
					<span>{currentSprint ? currentSprint.name : 'No current sprint'}</span>
				</div>
				{#if currentSprintTotal === 0}
					<p class="empty-inline">No tasks in the current sprint yet.</p>
				{:else}
					<div class="orbit-wrap">
						<svg class="orbit-chart" viewBox="0 0 120 120" aria-label="Current sprint status">
							<circle class="orbit-base" cx="60" cy="60" r="44"></circle>
							{#each currentStatusSegments as segment (segment.label)}
								<circle
									class="orbit-segment"
									cx="60"
									cy="60"
									r="44"
									stroke={segment.color}
									stroke-dasharray={segment.dashArray}
									stroke-dashoffset={segment.dashOffset}
								></circle>
							{/each}
						</svg>
						<div class="orbit-center">
							<strong>{currentSprintTotal}</strong>
							<span>tasks</span>
						</div>
					</div>
					<div class="legend-list">
						{#each currentStatusData as item (item.label)}
							<div class="legend-row">
								<div class="legend-left">
									<span class="dot" style="background:{item.color}"></span>
									<span>{item.label}</span>
								</div>
								<strong>{item.count}</strong>
							</div>
						{/each}
					</div>
				{/if}
			</article>

			<article class="card visual-card">
				<div class="section-head">
					<h4>Work remaining trend</h4>
					<span>{maxRemaining} max open</span>
				</div>
				{#if trendPoints.length === 0}
					<p class="empty-inline">No sprint trend available yet.</p>
				{:else}
					<svg
						class="trend-chart"
						viewBox={`0 0 ${TREND_W} ${TREND_H}`}
						aria-label="Remaining work trend"
					>
						<line
							x1={TREND_PADDING}
							y1={TREND_H - TREND_PADDING}
							x2={TREND_W - TREND_PADDING}
							y2={TREND_H - TREND_PADDING}
							class="trend-axis"
						></line>
						<line
							x1={TREND_PADDING}
							y1={TREND_PADDING}
							x2={TREND_PADDING}
							y2={TREND_H - TREND_PADDING}
							class="trend-axis"
						></line>
						<path class="trend-area" d={trendAreaPath}></path>
						<path class="trend-line" d={trendLinePath}></path>
						{#each trendPoints as point, i (i)}
							<circle class="trend-point" cx={point.x} cy={point.y} r="4"></circle>
							<text class="trend-point-label" x={point.x} y={point.y - 10}>{point.remaining}</text>
						{/each}
					</svg>
					<div class="trend-labels">
						{#each trendPoints as point, i (i)}
							<span>{point.label}</span>
						{/each}
					</div>
				{/if}
			</article>

			<article class="card visual-card">
				<div class="section-head">
					<h4>Work share by sprint</h4>
					<span>{sprintSnapshots.length} sprint{sprintSnapshots.length === 1 ? '' : 's'}</span>
				</div>
				{#if sprintShareSegments.length === 0}
					<p class="empty-inline">No sprint scope to compare.</p>
				{:else}
					<div class="share-wrap">
						<svg class="share-ring" viewBox="0 0 120 120" aria-label="Sprint work share">
							<circle class="orbit-base" cx="60" cy="60" r="44"></circle>
							{#each sprintShareSegments as segment (segment.label)}
								<circle
									class="orbit-segment"
									cx="60"
									cy="60"
									r="44"
									stroke={segment.color}
									stroke-dasharray={segment.dashArray}
									stroke-dashoffset={segment.dashOffset}
								></circle>
							{/each}
						</svg>
						<div class="orbit-center">
							<strong>{totalTasks}</strong>
							<span>scope</span>
						</div>
					</div>
					<div class="legend-list share-legend">
						{#each sprintSnapshots as snapshot (snapshot.id)}
							<div class="legend-row">
								<div class="legend-left">
									<span class="dot" style="background:{snapshot.color}"></span>
									<span>{snapshot.name}</span>
								</div>
								<strong>{snapshot.sharePct}%</strong>
							</div>
						{/each}
					</div>
				{/if}
			</article>

			<article class="card visual-card">
				<div class="section-head">
					<h4>Who completed tasks</h4>
					<span>{currentCompletedTotal} complete in current sprint</span>
				</div>
				{#if contributorRows.length === 0}
					<p class="empty-inline">
						Status ownership appears when users move tasks to In Progress or Done.
					</p>
				{:else}
					<div class="owner-bars">
						{#each contributorRows as contributor (contributor.id)}
							<div class="owner-col">
								<div class="owner-track">
									<div
										class="owner-fill"
										style="height:{contributor.barPct}%; background:{contributor.color}"
									></div>
								</div>
								<strong>{contributor.completed}</strong>
								<span class="owner-name" title={contributor.name}>{contributor.name}</span>
								<small>{contributor.inProgress} active</small>
							</div>
						{/each}
					</div>
				{/if}
			</article>
		</section>

		<article class="card runway-card" aria-label="Sprint runway">
			<div class="section-head">
				<h4>Sprint runway</h4>
				<span>{remainingSprints.length} remaining after current</span>
			</div>
			{#if sprintSnapshots.length === 0}
				<p class="empty-inline">No sprint data available yet.</p>
			{:else}
				<div class="runway-list">
					{#each sprintSnapshots as snapshot (snapshot.id)}
						<div class="runway-row" class:is-current={snapshot.isCurrent}>
							<div class="runway-main">
								<strong>{snapshot.name}</strong>
								<span>{snapshot.startLabel} - {snapshot.endLabel}</span>
							</div>
							<div class="runway-cells">
								{#each Array.from( { length: Math.max(1, Math.min(24, snapshot.total || 1)) } ) as _, index}
									<span
										class="runway-cell"
										class:is-done={sprintCellStatus(snapshot, index) === 'done'}
										class:is-progress={sprintCellStatus(snapshot, index) === 'progress'}
									></span>
								{/each}
							</div>
							<div class="runway-meta">
								<span>{snapshot.remaining} left</span>
								<strong>{snapshot.total} total</strong>
							</div>
						</div>
					{/each}
				</div>
			{/if}
		</article>

		<article class="card sprint-worklist" aria-label="Current sprint execution lanes">
			<div class="section-head">
				<h4>
					{currentSprint
						? `${currentSprint.name} execution lanes`
						: 'Current sprint execution lanes'}
				</h4>
				<span>{activeWorkRows.length} task{activeWorkRows.length === 1 ? '' : 's'}</span>
			</div>
			{#if !currentSprint || activeWorkRows.length === 0}
				<p class="empty-inline">No active sprint tasks found.</p>
			{:else}
				<div class="lane-list">
					{#each visibleActiveRows as row (row.id)}
						<div class="lane-row">
							<div class="lane-top">
								<div class="lane-title-wrap">
									<strong class="lane-title" title={row.title}>{row.title}</strong>
									<span
										class="status-pill"
										style="color:{priorityColor(row.priority)}; border-color:{priorityColor(
											row.priority
										)}"
									>
										{priorityLabel(row.priority)}
									</span>
								</div>
								<span
									class="lane-due"
									class:is-overdue={row.remainingDays < 0 && row.status !== 'done'}
								>
									{row.dueLabel} · {row.remainingLabel}
								</span>
							</div>
							<div class="lane-track">
								<div
									class="lane-fill"
									style="width:{executionFillPct(
										row.status,
										row.remainingDays
									)}%; background:{statusColor(row.status)}"
								></div>
							</div>
							<div class="lane-meta">
								<span
									class="status-pill"
									style="color:{statusColor(row.status)}; border-color:{statusColor(row.status)}"
								>
									{statusLabel(row.status)}
								</span>
								{#if row.assignee}
									<span class="assignee" title={row.assignee}>{initials(row.assignee)}</span>
								{:else}
									<span class="assignee assignee-empty">--</span>
								{/if}
							</div>
						</div>
					{/each}
				</div>
				{#if hiddenActiveRows > 0}
					<p class="hidden-note">
						+{hiddenActiveRows} more task{hiddenActiveRows === 1 ? '' : 's'} in this sprint
					</p>
				{/if}
			{/if}
		</article>
	{/if}
</div>

<style>
	:global(:root) {
		--tb-bg: #f4f5f7;
		--tb-surface: #ffffff;
		--tb-surface-soft: #fafbfc;
		--tb-border: #dfe1e6;
		--tb-border-hi: #c1c7d0;
		--tb-text: #172b4d;
		--tb-subtext: #42526e;
		--tb-muted: #6b778c;
		--tb-blue: #0052cc;
		--tb-blue-soft: #deebff;
		--tb-green: #36b37e;
		--tb-green-soft: #e3fcef;
		--tb-orange: #ff8b00;
		--tb-orange-soft: #fff4e5;
		--tb-red: #de350b;
		--tb-purple: #6554c0;
		--tb-purple-soft: #eae6ff;
		--tb-teal: #00b8d9;
		--tb-muted-chip: #97a0af;
		--tb-shadow: 0 1px 2px rgba(9, 30, 66, 0.08);
		--tb-shadow-hover: 0 4px 12px rgba(9, 30, 66, 0.16);
	}

	:global(:root[data-theme='dark']),
	:global(.theme-dark) {
		--tb-bg: #161617;
		--tb-surface: #1d1d20;
		--tb-surface-soft: #242428;
		--tb-border: #35353b;
		--tb-border-hi: #4b4b53;
		--tb-text: #f1f1f4;
		--tb-subtext: #c9c9cf;
		--tb-muted: #9e9ea6;
		--tb-blue: #babac2;
		--tb-blue-soft: rgba(186, 186, 194, 0.2);
		--tb-green: #61d59f;
		--tb-green-soft: rgba(97, 213, 159, 0.14);
		--tb-orange: #f0b04a;
		--tb-orange-soft: rgba(240, 176, 74, 0.14);
		--tb-red: #f27f7f;
		--tb-purple: #b8a4d6;
		--tb-purple-soft: rgba(184, 164, 214, 0.14);
		--tb-teal: #66c8bd;
		--tb-muted-chip: #8a919e;
		--tb-shadow: 0 1px 2px rgba(0, 0, 0, 0.45);
		--tb-shadow-hover: 0 10px 24px rgba(0, 0, 0, 0.42);
	}

	.board {
		height: 100%;
		overflow-y: auto;
		padding: 1.4rem;
		display: flex;
		flex-direction: column;
		gap: 1rem;
		background: var(--tb-bg);
		color: var(--tb-text);
		min-width: 0;
		scrollbar-width: thin;
	}

	.card {
		background: var(--tb-surface);
		border: 1px solid var(--tb-border);
		border-radius: 9px;
		box-shadow: var(--tb-shadow);
	}

	.board-empty {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		height: 100%;
		gap: 0.75rem;
		color: var(--tb-muted);
	}

	.board-empty svg {
		width: 2.6rem;
		height: 2.6rem;
		opacity: 0.5;
	}

	.board-empty p {
		margin: 0;
	}

	.project-head {
		padding: 1rem 1.1rem;
		display: grid;
		grid-template-columns: minmax(0, 1.7fr) repeat(3, minmax(0, 1fr));
		gap: 0.85rem;
		align-items: stretch;
	}

	.head-main {
		min-width: 0;
		display: flex;
		align-items: center;
	}

	.head-main h2 {
		margin: 0;
		font-size: clamp(1.2rem, 1.8vw, 1.55rem);
		font-weight: 700;
		line-height: 1.2;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.head-kpi {
		min-width: 0;
		max-width: 250px;
		display: flex;
		flex-direction: column;
		gap: 0.2rem;
		padding: 0.75rem 0.85rem;
		border: 1px solid var(--tb-border);
		border-radius: 8px;
		background: var(--tb-surface-soft);
	}

	.head-kpi span {
		font-size: 0.67rem;
		font-weight: 700;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--tb-muted);
	}

	.head-kpi strong {
		font-size: 1.14rem;
		font-weight: 800;
		color: var(--tb-text);
		line-height: 1.1;
	}

	.head-kpi p {
		margin: 0;
		font-size: 0.76rem;
		color: var(--tb-subtext);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.stats-grid {
		display: flex;
		flex-wrap: wrap;
		justify-content: center;
		align-items: stretch;
		gap: 0.9rem;
		max-width: 1100px;
		margin-inline: auto;
	}

	.stat-card {
		flex: 1 1 220px;
		max-width: 200px;
		min-width: 200px;
		padding: 0.9rem;
		display: flex;
		align-items: center;
		gap: 0.8rem;
		transition:
			box-shadow 0.2s ease,
			border-color 0.2s ease;
	}

	.stat-card:hover {
		border-color: var(--tb-border-hi);
		box-shadow: var(--tb-shadow-hover);
	}

	.stat-icon {
		width: 40px;
		height: 40px;
		border-radius: 50%;
		display: grid;
		place-items: center;
		flex-shrink: 0;
		border: 1px solid transparent;
	}

	.stat-icon svg {
		width: 20px;
		height: 20px;
		display: block;
		fill: none;
		stroke: currentColor;
		stroke-width: 2;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.stat-symbol {
		font-size: 0.9rem;
		font-weight: 800;
		letter-spacing: -0.04em;
		line-height: 1;
	}

	.stat-icon[data-tone='blue'] {
		color: var(--tb-blue);
		background: var(--tb-blue-soft);
		border-color: color-mix(in srgb, var(--tb-blue) 26%, var(--tb-border));
	}

	.stat-icon[data-tone='green'] {
		color: var(--tb-green);
		background: var(--tb-green-soft);
		border-color: color-mix(in srgb, var(--tb-green) 28%, var(--tb-border));
	}

	.stat-icon[data-tone='orange'] {
		color: var(--tb-orange);
		background: var(--tb-orange-soft);
		border-color: color-mix(in srgb, var(--tb-orange) 28%, var(--tb-border));
	}

	.stat-icon[data-tone='purple'] {
		color: var(--tb-purple);
		background: var(--tb-purple-soft);
		border-color: color-mix(in srgb, var(--tb-purple) 28%, var(--tb-border));
	}

	.stat-icon[data-tone='neutral'] {
		color: var(--tb-subtext);
		background: var(--tb-surface-soft);
		border-color: var(--tb-border);
	}

	.stat-copy {
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 0.2rem;
	}

	.stat-line {
		display: flex;
		align-items: baseline;
		gap: 0.4rem;
		min-width: 0;
	}

	.stat-value {
		font-size: 1.45rem;
		font-weight: 800;
		line-height: 1;
		color: var(--tb-text);
	}

	.stat-label {
		font-size: 0.85rem;
		font-weight: 700;
		color: var(--tb-subtext);
	}

	.stat-copy p {
		margin: 0;
		font-size: 0.74rem;
		color: var(--tb-muted);
		line-height: 1.35;
	}

	.section-head {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.7rem;
	}

	.section-head h4 {
		margin: 0;
		font-size: 0.98rem;
		font-weight: 700;
		color: var(--tb-text);
	}

	.section-head span {
		font-size: 0.76rem;
		color: var(--tb-muted);
		font-weight: 600;
	}

	.visual-grid {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.9rem;
	}

	.visual-card {
		padding: 0.95rem;
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
		min-height: 270px;
	}

	.orbit-wrap,
	.share-wrap {
		position: relative;
		width: 150px;
		height: 150px;
		margin-inline: auto;
	}

	.orbit-chart,
	.share-ring {
		width: 100%;
		height: 100%;
		transform: rotate(-90deg);
	}

	.orbit-base {
		fill: none;
		stroke: color-mix(in srgb, var(--tb-border) 74%, transparent);
		stroke-width: 12;
	}

	.orbit-segment {
		fill: none;
		stroke-width: 12;
		stroke-linecap: butt;
		transition:
			stroke-dasharray 0.35s ease,
			stroke-dashoffset 0.35s ease;
	}

	.orbit-center {
		position: absolute;
		inset: 0;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
	}

	.orbit-center strong {
		font-size: 1.3rem;
		font-weight: 800;
		color: var(--tb-text);
		line-height: 1;
	}

	.orbit-center span {
		margin-top: 0.2rem;
		font-size: 0.72rem;
		font-weight: 700;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--tb-muted);
	}

	.legend-list {
		display: flex;
		flex-direction: column;
		gap: 0.3rem;
	}

	.legend-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.8rem;
	}

	.legend-left {
		display: flex;
		align-items: center;
		gap: 0.42rem;
		min-width: 0;
	}

	.legend-left span:last-child {
		font-size: 0.8rem;
		color: var(--tb-subtext);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.dot {
		width: 10px;
		height: 10px;
		border-radius: 3px;
		flex-shrink: 0;
	}

	.legend-row strong {
		font-size: 0.82rem;
		font-weight: 700;
		color: var(--tb-blue);
	}

	.share-legend .legend-left span:last-child {
		max-width: 180px;
	}

	.trend-chart {
		width: 100%;
		height: auto;
		border-radius: 10px;
		background: linear-gradient(
			180deg,
			color-mix(in srgb, var(--tb-blue-soft) 52%, transparent),
			color-mix(in srgb, var(--tb-surface-soft) 82%, transparent)
		);
	}

	.trend-axis {
		stroke: var(--tb-border);
		stroke-width: 1.1;
	}

	.trend-area {
		fill: color-mix(in srgb, var(--tb-blue) 18%, transparent);
	}

	.trend-line {
		fill: none;
		stroke: var(--tb-blue);
		stroke-width: 3;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.trend-point {
		fill: var(--tb-surface);
		stroke: var(--tb-blue);
		stroke-width: 2;
	}

	.trend-point-label {
		font-size: 11px;
		font-weight: 700;
		text-anchor: middle;
		fill: var(--tb-subtext);
	}

	.trend-labels {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(70px, 1fr));
		gap: 0.4rem;
	}

	.trend-labels span {
		font-size: 0.72rem;
		color: var(--tb-muted);
		text-align: center;
	}

	.owner-bars {
		margin-top: auto;
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(74px, 1fr));
		gap: 0.6rem;
		align-items: end;
	}

	.owner-col {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 0.28rem;
		min-width: 0;
	}

	.owner-track {
		width: 34px;
		height: 120px;
		border-radius: 999px;
		background: color-mix(in srgb, var(--tb-border) 74%, transparent);
		display: flex;
		align-items: flex-end;
		padding: 3px;
	}

	.owner-fill {
		width: 100%;
		border-radius: 999px;
		min-height: 8px;
		transition: height 0.3s ease;
	}

	.owner-col strong {
		font-size: 0.9rem;
		font-weight: 800;
		color: var(--tb-text);
	}

	.owner-name {
		font-size: 0.72rem;
		color: var(--tb-subtext);
		max-width: 100%;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.owner-col small {
		font-size: 0.66rem;
		color: var(--tb-muted);
	}

	.runway-card,
	.sprint-worklist {
		padding: 0.95rem;
		display: flex;
		flex-direction: column;
		gap: 0.72rem;
	}

	.runway-list {
		display: flex;
		flex-direction: column;
		gap: 0.34rem;
	}

	.runway-row {
		display: grid;
		grid-template-columns: minmax(150px, 0.8fr) minmax(0, 1fr) auto;
		gap: 0.7rem;
		align-items: center;
		padding: 0.48rem 0.55rem;
		border-radius: 10px;
		border: 1px solid var(--tb-border);
		background: color-mix(in srgb, var(--tb-surface-soft) 72%, transparent);
	}

	.runway-row.is-current {
		border-color: color-mix(in srgb, var(--tb-blue) 32%, var(--tb-border));
		background: color-mix(in srgb, var(--tb-blue-soft) 58%, transparent);
	}

	.runway-main {
		display: flex;
		flex-direction: column;
		gap: 0.12rem;
		min-width: 0;
	}

	.runway-main strong {
		font-size: 0.84rem;
		font-weight: 700;
		color: var(--tb-text);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.runway-main span {
		font-size: 0.72rem;
		color: var(--tb-muted);
	}

	.runway-cells {
		display: grid;
		grid-template-columns: repeat(12, minmax(0, 1fr));
		gap: 4px;
	}

	.runway-cell {
		height: 9px;
		border-radius: 4px;
		background: color-mix(in srgb, var(--tb-muted-chip) 36%, var(--tb-border));
	}

	.runway-cell.is-progress {
		background: color-mix(in srgb, var(--tb-blue) 72%, var(--tb-surface));
	}

	.runway-cell.is-done {
		background: color-mix(in srgb, var(--tb-green) 70%, var(--tb-surface));
	}

	.runway-meta {
		display: flex;
		flex-direction: column;
		align-items: flex-end;
		gap: 0.1rem;
	}

	.runway-meta span {
		font-size: 0.72rem;
		color: var(--tb-muted);
	}

	.runway-meta strong {
		font-size: 0.8rem;
		color: var(--tb-blue);
	}

	.lane-list {
		display: flex;
		flex-direction: column;
		gap: 0.4rem;
	}

	.lane-row {
		border: 1px solid var(--tb-border);
		border-radius: 10px;
		padding: 0.55rem 0.65rem;
		background: color-mix(in srgb, var(--tb-surface-soft) 64%, transparent);
		display: flex;
		flex-direction: column;
		gap: 0.45rem;
	}

	.lane-top {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.7rem;
	}

	.lane-title-wrap {
		min-width: 0;
		display: flex;
		align-items: center;
		gap: 0.45rem;
	}

	.lane-title {
		font-size: 0.84rem;
		font-weight: 700;
		color: var(--tb-text);
		min-width: 0;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.lane-due {
		font-size: 0.72rem;
		color: var(--tb-muted);
		white-space: nowrap;
	}

	.lane-due.is-overdue {
		color: var(--tb-red);
		font-weight: 600;
	}

	.lane-track {
		height: 10px;
		border-radius: 999px;
		background: color-mix(in srgb, var(--tb-border) 78%, transparent);
		overflow: hidden;
	}

	.lane-fill {
		height: 100%;
		border-radius: inherit;
		transition: width 0.3s ease;
	}

	.lane-meta {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 0.7rem;
	}

	.status-pill {
		font-size: 0.66rem;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		padding: 0.14rem 0.4rem;
		border-radius: 999px;
		border: 1px solid;
		background: color-mix(in srgb, var(--tb-surface-soft) 72%, transparent);
		white-space: nowrap;
		justify-self: start;
	}

	.assignee {
		width: 26px;
		height: 26px;
		border-radius: 50%;
		display: grid;
		place-items: center;
		font-size: 0.66rem;
		font-weight: 800;
		color: var(--tb-blue);
		background: var(--tb-blue-soft);
		border: 1px solid color-mix(in srgb, var(--tb-blue) 28%, var(--tb-border));
	}

	.assignee-empty {
		background: transparent;
		color: var(--tb-muted);
		border-style: dashed;
		border-color: var(--tb-border);
	}

	.hidden-note,
	.empty-inline {
		margin: 0;
		font-size: 0.78rem;
		color: var(--tb-muted);
	}

	@media (max-width: 1350px) {
		.project-head {
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}

		.head-main {
			grid-column: 1 / -1;
		}
	}

	@media (max-width: 1180px) {
		.visual-grid {
			grid-template-columns: 1fr;
		}
	}

	@media (max-width: 900px) {
		.project-head {
			grid-template-columns: minmax(0, 1fr);
			gap: 0.75rem;
		}

		.head-main {
			grid-column: 1;
		}

		.head-main h2 {
			white-space: normal;
			overflow: visible;
			text-overflow: clip;
		}

		.head-kpi {
			max-width: none;
		}

		.head-kpi p {
			white-space: normal;
			overflow: visible;
			text-overflow: clip;
		}

		.stats-grid {
			max-width: none;
			justify-content: stretch;
		}

		.stat-card {
			max-width: none;
		}

		.runway-row {
			grid-template-columns: 1fr;
			gap: 0.42rem;
		}

		.runway-meta {
			align-items: flex-start;
		}

		.lane-top {
			flex-direction: column;
			align-items: flex-start;
		}

		.assignee {
			width: auto;
			height: auto;
			border-radius: 999px;
			padding: 0.14rem 0.45rem;
		}
	}

	@media (max-width: 640px) {
		.board {
			padding: 0.9rem;
		}

		.project-head {
			padding: 0.9rem;
		}

		.stat-card {
			max-width: 100%;
			min-width: 100%;
		}

		.head-main h2 {
			font-size: 1.15rem;
		}

		.orbit-wrap,
		.share-wrap {
			width: 126px;
			height: 126px;
		}

		.owner-bars {
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}
	}
</style>
