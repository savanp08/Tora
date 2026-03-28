<script lang="ts">
	import type { ToraWorkflowEvent as WorkflowEvent } from '$lib/types/chat';

	type TimelineRow = {
		event: WorkflowEvent;
		pairedResult: WorkflowEvent | null;
		label: string;
		icon: string;
		isError: boolean;
		isExpandable: boolean;
		chipLabel: string;
		chipClass: string;
		detailRows: Array<{ key: string; value: string }>;
		resultSummary: string;
		durationMs: number | null;
	};

	export let summary = '';
	export let events: WorkflowEvent[] = [];
	export let workflowKind = '';
	export let isLive = false;

	let isOpen = false;
	let expandedRows: Record<string, boolean> = {};
	let hasUserToggled = false;
	let now = Date.now();

	$: normalizedEvents = normalizeWorkflowEvents(events);
	$: latestEvent = normalizedEvents[normalizedEvents.length - 1] ?? null;
	$: isRunning = Boolean(isLive && latestEvent && latestEvent.kind !== 'done');
	$: hasError = normalizedEvents.some(
		(event) => Boolean((event.error || '').trim()) || Boolean(readResultError(event.result))
	);
	$: progressPercent =
		isRunning && latestEvent
			? Math.min(
					100,
					Math.max(0, (Math.max(1, latestEvent.turn) / Math.max(1, latestEvent.totalTurns)) * 100)
				)
			: 0;
	$: panelTitle = resolvePanelTitle(summary, workflowKind, latestEvent, isRunning);
	$: timelineRows = buildTimelineRows(normalizedEvents);
	$: if (isRunning && normalizedEvents.length > 0 && !hasUserToggled) {
		isOpen = true;
	}
	$: if (!isOpen) {
		now = Date.now();
	}

	function normalizeWorkflowEvents(input: WorkflowEvent[]) {
		return input
			.map((event, index) => {
				const safeTimestamp = Number.isFinite(event.timestamp) && Number(event.timestamp) > 0
					? Number(event.timestamp)
					: Date.now();
				return {
					...event,
					id: event.id || `workflow-${index}-${safeTimestamp}`,
					tool: (event.tool || '').trim(),
					text: (event.text || '').trim(),
					error: (event.error || '').trim(),
					timestamp: safeTimestamp,
					turn: Math.max(1, Math.trunc(Number(event.turn) || 1)),
					totalTurns: Math.max(1, Math.trunc(Number(event.totalTurns) || Number(event.turn) || 1))
				} satisfies WorkflowEvent;
			})
			.sort((left, right) => {
				const leftTime = left.timestamp || 0;
				const rightTime = right.timestamp || 0;
				if (leftTime !== rightTime) {
					return leftTime - rightTime;
				}
				return left.id.localeCompare(right.id);
			});
	}

	function buildTimelineRows(input: WorkflowEvent[]) {
		const rows: TimelineRow[] = [];
		for (let index = 0; index < input.length; index += 1) {
			const event = input[index];
			if (event.kind === 'tool_result' && index > 0 && input[index - 1]?.kind === 'tool_call') {
				continue;
			}
			const pairedResult =
				event.kind === 'tool_call' && input[index + 1]?.kind === 'tool_result'
					? input[index + 1]
					: null;
			rows.push({
				event,
				pairedResult,
				label: formatRowLabel(event, pairedResult),
				icon: resolveIcon(event, pairedResult),
				isError: isWorkflowError(event, pairedResult),
				isExpandable: event.kind === 'tool_call',
				...resolveToolChip(event),
				detailRows: event.kind === 'tool_call' ? buildDetailRows(event.input) : [],
				resultSummary: formatResultSummary(pairedResult ?? event),
				durationMs:
					pairedResult && pairedResult.timestamp && event.timestamp
						? Math.max(0, pairedResult.timestamp - event.timestamp)
						: null
			});
		}
		return rows;
	}

	function toggleOpen() {
		hasUserToggled = true;
		isOpen = !isOpen;
		now = Date.now();
	}

	function toggleRow(id: string) {
		expandedRows = {
			...expandedRows,
			[id]: !expandedRows[id]
		};
	}

	function resolvePanelTitle(
		value: string,
		kind: string,
		event: WorkflowEvent | null,
		running: boolean
	) {
		const trimmed = value.trim();
		if (trimmed) {
			return trimmed;
		}
		const label = resolveWorkflowKindLabel(kind);
		if (running) {
			return label ? `${label} in progress` : 'AI workflow in progress';
		}
		if (event?.kind === 'done') {
			return label ? `${label} complete` : 'AI workflow complete';
		}
		return label || 'AI workflow';
	}

	function resolveWorkflowKindLabel(kind: string) {
		switch (kind.trim().toLowerCase()) {
			case 'task_board':
				return 'Project workflow';
			case 'canvas':
				return 'Canvas workflow';
			case 'chat':
				return 'Chat workflow';
			default:
				return 'AI workflow';
		}
	}

	function resolveIcon(event: WorkflowEvent, pairedResult: WorkflowEvent | null) {
		if (isWorkflowError(event, pairedResult)) {
			return '✕';
		}
		if (event.kind === 'tool_call' && event.tool === 'write_canvas') {
			return '✍️';
		}
		switch (event.kind) {
			case 'thinking':
				return '⏳';
			case 'tool_call':
				return '🔧';
			case 'tool_result':
				return '✓';
			case 'text':
				return '💬';
			case 'done':
				return '✓';
			default:
				return '•';
		}
	}

	function resolveToolChip(event: WorkflowEvent) {
		switch (event.tool) {
			case 'create_task':
				return { chipLabel: 'Create', chipClass: 'chip-create' };
			case 'update_task':
				return { chipLabel: 'Update', chipClass: 'chip-update' };
			case 'delete_task':
				return { chipLabel: 'Delete', chipClass: 'chip-delete' };
			case 'write_canvas':
				return { chipLabel: 'Canvas', chipClass: 'chip-update' };
			default:
				return { chipLabel: '', chipClass: '' };
		}
	}

	function formatRowLabel(event: WorkflowEvent, pairedResult: WorkflowEvent | null) {
		if (event.kind === 'thinking') {
			return truncateText(event.text || 'Thinking…', 100);
		}
		if (event.kind === 'text') {
			return `Tora: ${truncateText(event.text || '…', 100)}`;
		}
		if (event.kind === 'done') {
			return truncateText(event.text || 'Finished', 100);
		}
		if (event.kind === 'tool_result') {
			return formatResultSummary(event);
		}
		if (event.tool === 'write_canvas') {
			const path = readString(event.input, 'file_path');
			const lineCount = readNumber(pairedResult?.result, 'lines');
			if (path && lineCount > 0) {
				return `Writing ${path} (${lineCount} lines)`;
			}
			if (path) {
				return `Writing ${path}`;
			}
		}
		const target =
			readString(event.input, 'title') ||
			readString(event.input, 'task_title') ||
			readString(event.input, 'file_path') ||
			readString(event.input, 'task_id');
		if (target) {
			return `${event.tool || 'tool'}: ${truncateText(target, 72)}`;
		}
		return event.tool || 'Tool call';
	}

	function formatResultSummary(event: WorkflowEvent) {
		const error = (event.error || readResultError(event.result) || '').trim();
		if (error) {
			return error;
		}
		if (event.tool === 'write_canvas') {
			const path = readString(event.result, 'path') || readString(event.input, 'file_path');
			if (path) {
				return `${path} updated`;
			}
		}
		if (event.tool === 'create_task') {
			const title = readString(event.result, 'title') || readString(event.input, 'title');
			return title ? `Created ${title}` : 'Task created';
		}
		if (event.tool === 'update_task') {
			const title =
				readString(event.result, 'title') ||
				readString(event.input, 'task_title') ||
				readString(event.input, 'title');
			return title ? `Updated ${title}` : 'Task updated';
		}
		if (event.tool === 'delete_task') {
			const title =
				readString(event.result, 'task_title') || readString(event.input, 'task_title');
			return title ? `Deleted ${title}` : 'Task deleted';
		}
		if ((event.text || '').trim()) {
			return truncateText(event.text || '', 90);
		}
		return 'Completed';
	}

	function buildDetailRows(input: Record<string, unknown> | undefined) {
		if (!input) {
			return [];
		}
		return Object.entries(input)
			.filter(([key]) => !shouldHideDetailKey(key))
			.map(([key, value]) => ({
				key: formatDetailKey(key),
				value: formatDetailValue(value)
			}));
	}

	function shouldHideDetailKey(key: string) {
		const normalized = key.trim().toLowerCase();
		return (
			normalized === 'id' ||
			normalized.endsWith('_id') ||
			normalized === 'taskid' ||
			normalized === 'originmessageid'
		);
	}

	function formatDetailKey(key: string) {
		return key
			.replace(/_/g, ' ')
			.replace(/\b\w/g, (match) => match.toUpperCase());
	}

	function formatDetailValue(value: unknown): string {
		if (typeof value === 'string') {
			return truncateText(value.trim(), 220);
		}
		if (typeof value === 'number' || typeof value === 'boolean') {
			return String(value);
		}
		if (Array.isArray(value)) {
			return truncateText(
				value
					.map((entry) => formatDetailValue(entry))
					.filter(Boolean)
					.join(', '),
				220
			);
		}
		if (value && typeof value === 'object') {
			try {
				return truncateText(JSON.stringify(value), 220);
			} catch {
				return '[object]';
			}
		}
		return '';
	}

	function readResultError(value: unknown) {
		if (!value || typeof value !== 'object' || Array.isArray(value)) {
			return '';
		}
		const record = value as Record<string, unknown>;
		const candidate = record.error;
		return typeof candidate === 'string' ? candidate.trim() : '';
	}

	function isWorkflowError(event: WorkflowEvent, pairedResult: WorkflowEvent | null) {
		return Boolean(
			(event.error || '').trim() ||
				readResultError(event.result) ||
				(pairedResult ? pairedResult.error || readResultError(pairedResult.result) : '')
		);
	}

	function readString(source: unknown, key: string) {
		if (!source || typeof source !== 'object' || Array.isArray(source)) {
			return '';
		}
		const value = (source as Record<string, unknown>)[key];
		return typeof value === 'string' ? value.trim() : '';
	}

	function readNumber(source: unknown, key: string) {
		if (!source || typeof source !== 'object' || Array.isArray(source)) {
			return 0;
		}
		const value = (source as Record<string, unknown>)[key];
		return typeof value === 'number' && Number.isFinite(value) ? value : 0;
	}

	function truncateText(value: string, maxLen: number) {
		if (value.length <= maxLen) {
			return value;
		}
		return `${value.slice(0, Math.max(0, maxLen - 1))}…`;
	}

	function formatRelativeTime(timestamp: number | undefined, referenceNow: number) {
		const safeTimestamp = Number.isFinite(timestamp) ? Number(timestamp) : referenceNow;
		const diffMs = Math.max(0, referenceNow - safeTimestamp);
		const diffSeconds = Math.round(diffMs / 1000);
		if (diffSeconds < 5) {
			return 'just now';
		}
		if (diffSeconds < 60) {
			return `${diffSeconds}s ago`;
		}
		const diffMinutes = Math.round(diffSeconds / 60);
		if (diffMinutes < 60) {
			return `${diffMinutes}m ago`;
		}
		const diffHours = Math.round(diffMinutes / 60);
		if (diffHours < 24) {
			return `${diffHours}h ago`;
		}
		const diffDays = Math.round(diffHours / 24);
		return `${diffDays}d ago`;
	}

	function formatDuration(durationMs: number | null) {
		if (!Number.isFinite(durationMs) || durationMs == null) {
			return 'n/a';
		}
		if (durationMs < 1000) {
			return `${Math.max(0, Math.round(durationMs))}ms`;
		}
		return `${(durationMs / 1000).toFixed(durationMs >= 10000 ? 0 : 1)}s`;
	}
</script>

<div class="workflow-shell">
	<button
		type="button"
		class="workflow-toggle"
		class:is-running={isRunning}
		class:has-error={hasError}
		aria-expanded={isOpen}
		title={panelTitle}
		on:click={toggleOpen}
	>
		<span class="workflow-toggle-icon" aria-hidden="true">🐼</span>
		<span class="workflow-toggle-text">{isRunning ? 'Tracking' : 'Workflow'}</span>
	</button>

	{#if isOpen}
		<div class="workflow-panel">
			{#if isRunning}
				<div class="workflow-progress">
					<div class="workflow-progress-bar" style={`width: ${progressPercent}%`}></div>
				</div>
			{/if}

			<div class="workflow-panel-head">
				<div class="workflow-panel-title">{panelTitle}</div>
				{#if latestEvent}
					<div class="workflow-panel-meta">
						Turn {latestEvent.turn}/{latestEvent.totalTurns}
					</div>
				{/if}
			</div>

			<div class="workflow-timeline">
				{#if timelineRows.length === 0}
					<div class="workflow-row">
						<span class="workflow-row-icon">⏳</span>
						<div class="workflow-row-main">
							<div class="workflow-row-label">Waiting for activity…</div>
						</div>
					</div>
				{:else}
					{#each timelineRows as row (row.event.id)}
						{#if row.isExpandable}
							<button
								type="button"
								class="workflow-row workflow-row-button"
								class:is-error={row.isError}
								aria-expanded={Boolean(expandedRows[row.event.id])}
								on:click={() => toggleRow(row.event.id)}
							>
								<span class="workflow-row-icon">{row.icon}</span>
								<div class="workflow-row-main">
									<div class="workflow-row-title">
										<span class="workflow-row-label">{row.label}</span>
										{#if row.chipLabel}
											<span class={`workflow-chip ${row.chipClass}`}>{row.chipLabel}</span>
										{/if}
									</div>
									{#if expandedRows[row.event.id]}
										<div class="workflow-drawer">
											{#each row.detailRows as detail}
												<div class="workflow-detail-row">
													<span class="workflow-detail-key">{detail.key}:</span>
													<span>{detail.value}</span>
												</div>
											{/each}
											<div class="workflow-detail-row">
												<span class="workflow-detail-key">Result:</span>
												<span>{row.resultSummary}</span>
											</div>
											<div class="workflow-detail-row">
												<span class="workflow-detail-key">Duration:</span>
												<span>{formatDuration(row.durationMs)}</span>
											</div>
										</div>
									{/if}
								</div>
								<div class="workflow-row-meta">
									<span class="workflow-turn-badge"
										>Turn {row.event.turn}/{row.event.totalTurns}</span
									>
									<span>{formatRelativeTime(row.event.timestamp, now)}</span>
								</div>
							</button>
						{:else}
							<div class="workflow-row" class:is-error={row.isError}>
								<span class="workflow-row-icon">{row.icon}</span>
								<div class="workflow-row-main">
									<div class="workflow-row-title">
										<span class="workflow-row-label">{row.label}</span>
									</div>
								</div>
								<div class="workflow-row-meta">
									<span class="workflow-turn-badge"
										>Turn {row.event.turn}/{row.event.totalTurns}</span
									>
									<span>{formatRelativeTime(row.event.timestamp, now)}</span>
								</div>
							</div>
						{/if}
					{/each}
				{/if}
			</div>
		</div>
	{/if}
</div>

<style>
	.workflow-shell {
		display: inline-flex;
		flex-direction: column;
		align-items: flex-end;
		gap: 8px;
		width: 100%;
	}

	.workflow-toggle {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		padding: 5px 10px;
		border-radius: 999px;
		border: 1px solid rgba(99, 102, 241, 0.18);
		background: rgba(255, 255, 255, 0.84);
		color: #334155;
		font-size: 0.72rem;
		font-weight: 700;
		letter-spacing: 0.01em;
		cursor: pointer;
		box-shadow: 0 8px 18px rgba(15, 23, 42, 0.08);
		backdrop-filter: blur(10px);
	}

	.workflow-toggle.is-running {
		border-color: rgba(99, 102, 241, 0.35);
		background: rgba(238, 242, 255, 0.96);
		color: #4338ca;
	}

	.workflow-toggle.has-error {
		border-color: rgba(220, 38, 38, 0.22);
		color: #b91c1c;
	}

	.workflow-toggle-icon {
		font-size: 0.92rem;
	}

	.workflow-toggle.is-running .workflow-toggle-icon {
		animation: workflowPulse 1.35s ease-in-out infinite;
	}

	.workflow-panel {
		width: min(420px, 92vw);
		border-radius: 18px;
		border: 1px solid rgba(148, 163, 184, 0.2);
		background:
			linear-gradient(180deg, rgba(255, 255, 255, 0.98), rgba(248, 250, 252, 0.96));
		box-shadow: 0 18px 42px rgba(15, 23, 42, 0.12);
		overflow: hidden;
	}

	.workflow-progress {
		height: 2px;
		background: rgba(99, 102, 241, 0.08);
	}

	.workflow-progress-bar {
		height: 100%;
		background: #6366f1;
		transition: width 400ms ease;
	}

	.workflow-panel-head {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 12px;
		padding: 12px 14px 10px;
		border-bottom: 1px solid rgba(148, 163, 184, 0.14);
	}

	.workflow-panel-title {
		font-size: 0.8rem;
		font-weight: 700;
		color: #0f172a;
	}

	.workflow-panel-meta {
		font-size: 0.68rem;
		font-weight: 700;
		color: rgba(71, 85, 105, 0.82);
		text-transform: uppercase;
		letter-spacing: 0.08em;
	}

	.workflow-timeline {
		max-height: 320px;
		overflow: auto;
		padding: 10px 12px 12px;
	}

	.workflow-row {
		display: flex;
		align-items: flex-start;
		gap: 10px;
		width: 100%;
		padding: 8px 0;
		border-bottom: 1px solid rgba(148, 163, 184, 0.12);
		font-size: 0.76rem;
		color: #334155;
		background: transparent;
	}

	.workflow-row:last-child {
		border-bottom: none;
	}

	.workflow-row-button {
		border: none;
		text-align: left;
		cursor: pointer;
		padding-inline: 0;
	}

	.workflow-row.is-error {
		color: #b91c1c;
	}

	.workflow-row-icon {
		flex-shrink: 0;
		width: 18px;
		text-align: center;
		margin-top: 1px;
	}

	.workflow-row-main {
		flex: 1;
		min-width: 0;
	}

	.workflow-row-title {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 6px;
	}

	.workflow-row-label {
		line-height: 1.35;
	}

	.workflow-row-meta {
		display: flex;
		flex-direction: column;
		align-items: flex-end;
		gap: 2px;
		flex-shrink: 0;
		font-size: 0.66rem;
		color: rgba(71, 85, 105, 0.75);
	}

	.workflow-turn-badge {
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.06em;
	}

	.workflow-chip {
		padding: 1px 7px;
		border-radius: 999px;
		font-size: 0.66rem;
		font-weight: 700;
	}

	.chip-create {
		background: rgba(22, 163, 74, 0.12);
		color: #15803d;
	}

	.chip-update {
		background: rgba(59, 130, 246, 0.12);
		color: #2563eb;
	}

	.chip-delete {
		background: rgba(220, 38, 38, 0.12);
		color: #dc2626;
	}

	.workflow-drawer {
		margin-top: 8px;
		padding: 8px 10px;
		border-radius: 12px;
		background: rgba(241, 245, 249, 0.88);
		color: #334155;
	}

	.workflow-detail-row {
		display: flex;
		gap: 6px;
		padding: 2px 0;
		line-height: 1.4;
	}

	.workflow-detail-key {
		flex-shrink: 0;
		font-weight: 700;
		color: #475569;
	}

	@keyframes workflowPulse {
		0%,
		100% {
			transform: translateY(0);
			opacity: 0.95;
		}
		50% {
			transform: translateY(-1px);
			opacity: 0.62;
		}
	}
</style>
