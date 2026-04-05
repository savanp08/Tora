<script lang="ts">
	import { fly } from 'svelte/transition';
	import { afterUpdate } from 'svelte';
	import { projectTypeConfig } from '$lib/stores/projectType';
	import type { ToraWorkflowEvent as WorkflowEvent } from '$lib/types/chat';

	type TimelineRow = {
		event: WorkflowEvent;
		pairedResult: WorkflowEvent | null;
		label: string;
		icon: string;
		status: 'ok' | 'error' | 'running';
		isExpandable: boolean;
		chipLabel: string;
		chipClass: string;
		detailRows: Array<{ key: string; value: string }>;
		resultSummary: string;
		durationMs: number | null;
	};

	type WorkflowViewportBounds = {
		top: number;
		right: number;
		bottom: number;
		left: number;
		width: number;
		height: number;
	};

	export let summary = '';
	export let events: WorkflowEvent[] = [];
	export let workflowKind = '';
	export let isLive = false;
	export let viewportBounds: WorkflowViewportBounds | null = null;

	let isOpen = false;
	let expandedRows: Record<string, boolean> = {};
	let hasUserToggled = false;
	let now = Date.now();
	let timelineEl: HTMLElement | null = null;
	let prevRowCount = 0;

	$: taskTerm = $projectTypeConfig.taskTerm;
	$: taskTermPlural = $projectTypeConfig.taskTermPlural;
	$: groupTerm = $projectTypeConfig.groupTerm;
	$: groupTermPlural = $projectTypeConfig.groupTermPlural;
	$: taskLabel = taskTerm.toLowerCase();
	$: taskPluralLabel = taskTermPlural.toLowerCase();
	$: groupLabel = groupTerm.toLowerCase();
	$: groupPluralLabel = groupTermPlural.toLowerCase();
	$: normalizedEvents = normalizeWorkflowEvents(events);
	$: latestEvent = normalizedEvents[normalizedEvents.length - 1] ?? null;
	$: isRunning = Boolean(isLive && latestEvent && latestEvent.kind !== 'done');
	$: isDone = Boolean(latestEvent && latestEvent.kind === 'done');
	$: hasError = normalizedEvents.some(
		(e) => Boolean((e.error || '').trim()) || Boolean(readResultError(e.result))
	);
	$: progressPercent =
		latestEvent
			? Math.min(100, Math.max(0, (Math.max(1, latestEvent.turn) / Math.max(1, latestEvent.totalTurns)) * 100))
			: 0;
	$: panelTitle = resolvePanelTitle(summary, workflowKind, latestEvent, isRunning);
	$: timelineRows = buildTimelineRows(normalizedEvents);
	$: overlayStyle = buildOverlayStyle(viewportBounds);
	$: if (!isOpen) now = Date.now();

	// Auto-scroll to bottom when new rows appear
	afterUpdate(() => {
		if (isOpen && timelineEl && timelineRows.length !== prevRowCount) {
			prevRowCount = timelineRows.length;
			timelineEl.scrollTop = timelineEl.scrollHeight;
		}
	});

	function normalizeWorkflowEvents(input: WorkflowEvent[]) {
		return input
			.map((event, index) => {
				const safeTimestamp =
					Number.isFinite(event.timestamp) && Number(event.timestamp) > 0
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
			.sort((a, b) => {
				if (a.timestamp !== b.timestamp) return a.timestamp - b.timestamp;
				return a.id.localeCompare(b.id);
			});
	}

	function buildTimelineRows(input: WorkflowEvent[]) {
		const rows: TimelineRow[] = [];
		for (let i = 0; i < input.length; i++) {
			const event = input[i];
			// Skip empty pre-call thinking events (emitted to make the button appear early)
			if (event.kind === 'thinking' && !(event.text || '').trim()) continue;
			// Skip raw tool_result rows — they are folded into the preceding tool_call row
			if (event.kind === 'tool_result' && i > 0 && input[i - 1]?.kind === 'tool_call') continue;
			if (shouldCollapseNarrationEvent(rows[rows.length - 1], event)) continue;
			const pairedResult =
				event.kind === 'tool_call' && input[i + 1]?.kind === 'tool_result'
					? input[i + 1]
					: null;
			const isLastTool = event.kind === 'tool_call' && !pairedResult && isRunning;
			rows.push({
				event,
				pairedResult,
				label: formatRowLabel(event, pairedResult),
				icon: resolveIcon(event, pairedResult, isLastTool),
				status: isLastTool ? 'running' : isWorkflowError(event, pairedResult) ? 'error' : 'ok',
				isExpandable: event.kind === 'tool_call' && Boolean(pairedResult),
				chipLabel: resolveToolChip(event).chipLabel,
				chipClass: resolveToolChip(event).chipClass,
				detailRows: event.kind === 'tool_call' ? buildDetailRows(event.input) : [],
				resultSummary: formatResultSummary(pairedResult ?? event),
				durationMs:
					pairedResult?.timestamp && event.timestamp
						? Math.max(0, pairedResult.timestamp - event.timestamp)
						: null
			});
		}
		return rows;
	}

	function shouldCollapseNarrationEvent(previousRow: TimelineRow | undefined, event: WorkflowEvent) {
		if (!previousRow) return false;
		if (!isNarrationEvent(previousRow.event) || !isNarrationEvent(event)) return false;
		const previousText = normalizeNarrationText(previousRow.event.text || '');
		const currentText = normalizeNarrationText(event.text || '');
		return previousText !== '' && previousText === currentText;
	}

	function isNarrationEvent(event: WorkflowEvent) {
		return event.kind === 'thinking' || event.kind === 'text';
	}

	function normalizeNarrationText(value: string) {
		return value.trim().replace(/\s+/g, ' ');
	}

	function toggleOpen() {
		hasUserToggled = true;
		isOpen = !isOpen;
		now = Date.now();
	}

	function closePanel() {
		hasUserToggled = true;
		isOpen = false;
		now = Date.now();
	}

	function toggleRow(id: string) {
		expandedRows = { ...expandedRows, [id]: !expandedRows[id] };
	}

	function handleWindowKeydown(event: KeyboardEvent) {
		if (event.key === 'Escape' && isOpen) closePanel();
	}

	function buildOverlayStyle(bounds: WorkflowViewportBounds | null) {
		if (!bounds || bounds.width <= 0 || bounds.height <= 0) return '';
		return `top:${bounds.top}px;right:${bounds.right}px;bottom:${bounds.bottom}px;left:${bounds.left}px;`;
	}

	function resolvePanelTitle(
		value: string,
		kind: string,
		event: WorkflowEvent | null,
		running: boolean
	) {
		const trimmed = value.trim();
		if (trimmed) return trimmed;
		const label = resolveWorkflowKindLabel(kind);
		if (running) return label ? `${label} in progress` : 'AI workflow in progress';
		if (event?.kind === 'done') return label ? `${label} complete` : 'AI workflow complete';
		return label || 'AI workflow';
	}

	function resolveWorkflowKindLabel(kind: string) {
		switch (kind.trim().toLowerCase()) {
			case 'task_board': return 'Project workflow';
			case 'canvas': return 'Canvas workflow';
			case 'chat': return 'Chat workflow';
			default: return 'AI workflow';
		}
	}

	function resolveIcon(
		event: WorkflowEvent,
		pairedResult: WorkflowEvent | null,
		isActive: boolean
	) {
		if (isActive) return '…';
		if (isWorkflowError(event, pairedResult)) return '✕';
		switch (event.kind) {
			case 'thinking': return '⏳';
			case 'tool_call': return '✓';
			case 'tool_result': return '✓';
			case 'text': return '💬';
			case 'done': return '✓';
			default: return '•';
		}
	}

	function resolveToolChip(event: WorkflowEvent) {
		switch (event.tool) {
			case 'create_task': return { chipLabel: 'create', chipClass: 'chip-create' };
			case 'update_task': return { chipLabel: 'update', chipClass: 'chip-update' };
			case 'delete_task': return { chipLabel: 'delete', chipClass: 'chip-delete' };
			case 'list_tasks': return { chipLabel: 'read', chipClass: 'chip-read' };
			case 'list_sprints': return { chipLabel: 'read', chipClass: 'chip-read' };
			case 'list_groups': return { chipLabel: 'read', chipClass: 'chip-read' };
			case 'search_tasks': return { chipLabel: 'search', chipClass: 'chip-read' };
			case 'delete_group': return { chipLabel: 'delete', chipClass: 'chip-delete' };
			case 'write_canvas': return { chipLabel: 'write', chipClass: 'chip-update' };
			default: return { chipLabel: '', chipClass: '' };
		}
	}

	function formatRowLabel(event: WorkflowEvent, pairedResult: WorkflowEvent | null) {
		if (event.kind === 'thinking') return (event.text || 'Thinking…').trim();
		if (event.kind === 'text') return (event.text || '…').trim();
		if (event.kind === 'done') return (event.text || 'Finished.').trim();
		if (event.kind === 'tool_result') return formatResultSummary(event);
		if (event.kind !== 'tool_call') return '';

		// Human-readable label for each tool call
		const title = readString(event.input, 'title') || readString(event.input, 'task_title');
		const sprint = readString(event.input, 'sprint_name');
		const groupName = readString(event.input, 'group_name');
		const filePath = readString(event.input, 'file_path');
		const query = readString(event.input, 'query');

		switch (event.tool) {
			case 'create_task': {
				const parts = [title ? `"${title}"` : `new ${taskLabel}`];
				if (sprint) parts.push(`→ ${sprint}`);
				return parts.join(' ');
			}
			case 'update_task': {
				const name = title ? `"${title}"` : taskLabel;
				const changes = buildUpdateChangeSummary(event.input);
				return changes ? `${name} — ${changes}` : name;
			}
			case 'delete_task':
				return title ? `"${title}"` : taskLabel;
			case 'list_tasks':
				return `Load ${taskPluralLabel}`;
			case 'list_sprints':
			case 'list_groups':
				return `Load ${groupPluralLabel}`;
			case 'search_tasks':
				return query ? `Search: ${query}` : `Search ${taskPluralLabel}`;
			case 'verify_task_count':
				return `Verify ${taskPluralLabel}`;
			case 'delete_group':
				return groupName ? `Delete ${groupLabel}: ${groupName}` : `Delete ${groupLabel}`;
			case 'write_canvas':
				return filePath ? `Write ${filePath}` : 'Write file';
			default:
				return event.tool || 'tool';
		}
	}

	function buildUpdateChangeSummary(input: Record<string, unknown> | undefined): string {
		if (!input) return '';
		const skip = new Set(['task_id', 'task_title', 'title', 'sprint_name']);
		const parts: string[] = [];
		// Group change is the most useful to surface
		const sprint = readString(input, 'sprint_name');
		if (sprint) parts.push(`${groupLabel} → ${sprint}`);
		for (const [key, val] of Object.entries(input)) {
			if (skip.has(key) || key === 'sprint_name') continue;
			if (val === null || val === undefined || val === '') continue;
			const label = key.replace(/_/g, ' ');
			const valStr =
				typeof val === 'string'
					? val.trim()
					: Array.isArray(val)
					? val.join(', ')
					: String(val);
			if (valStr) parts.push(`${label}: ${valStr}`);
			if (parts.length >= 3) break; // keep it compact
		}
		return parts.join(', ');
	}

	function formatResultSummary(event: WorkflowEvent) {
		const error = (event.error || readResultError(event.result) || '').trim();
		if (error) return error;
		if (event.tool === 'write_canvas') {
			const path = readString(event.result, 'path') || readString(event.input, 'file_path');
			return path ? `${path} updated` : 'Canvas updated';
		}
		if (event.tool === 'create_task') {
			const title = readString(event.result, 'title') || readString(event.input, 'title');
			return title ? `Created "${title}"` : `${taskTerm} created`;
		}
		if (event.tool === 'update_task') {
			const title =
				readString(event.result, 'title') ||
				readString(event.input, 'task_title') ||
				readString(event.input, 'title');
			return title ? `Updated "${title}"` : `${taskTerm} updated`;
		}
		if (event.tool === 'delete_task') {
			const title = readString(event.result, 'task_title') || readString(event.input, 'task_title');
			return title ? `Deleted "${title}"` : `${taskTerm} deleted`;
		}
		if (event.tool === 'list_tasks') {
			const count = readNumber(event.result, 'count') || readArrayLength(event.result, 'tasks');
			return count > 0 ? `${count} ${taskPluralLabel} loaded` : `${taskTermPlural} loaded`;
		}
		if (event.tool === 'list_sprints' || event.tool === 'list_groups') {
			const count =
				readNumber(event.result, 'count') ||
				readArrayLength(event.result, 'groups') ||
				readArrayLength(event.result, 'sprints');
			return count > 0 ? `${count} ${groupPluralLabel} loaded` : `${groupTermPlural} loaded`;
		}
		if (event.tool === 'delete_group') {
			const name = readString(event.result, 'group_name') || readString(event.input, 'group_name');
			return name ? `Deleted ${groupLabel} "${name}"` : `${groupTerm} deleted`;
		}
		if ((event.text || '').trim()) return (event.text || '').trim();
		return 'Done';
	}

	function getToolCallTitle(event: WorkflowEvent): string {
		const target =
			readString(event.input, 'title') ||
			readString(event.input, 'task_title') ||
			readString(event.input, 'file_path');
		return target;
	}

	function buildDetailRows(input: Record<string, unknown> | undefined) {
		if (!input) return [];
		return Object.entries(input)
			.filter(([key]) => !shouldHideDetailKey(key))
			.map(([key, value]) => ({ key: formatDetailKey(key), value: formatDetailValue(value) }));
	}

	function shouldHideDetailKey(key: string) {
		const n = key.trim().toLowerCase();
		return n === 'id' || n.endsWith('_id') || n === 'taskid' || n === 'originmessageid';
	}

	function formatDetailKey(key: string) {
		return key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
	}

	function formatDetailValue(value: unknown): string {
		if (typeof value === 'string') return value.trim();
		if (typeof value === 'number' || typeof value === 'boolean') return String(value);
		if (Array.isArray(value))
			return value.map(formatDetailValue).filter(Boolean).join(', ');
		if (value && typeof value === 'object') {
			try { return JSON.stringify(value); } catch { return '[object]'; }
		}
		return '';
	}

	function readResultError(value: unknown) {
		if (!value || typeof value !== 'object' || Array.isArray(value)) return '';
		const r = value as Record<string, unknown>;
		return typeof r.error === 'string' ? r.error.trim() : '';
	}

	function isWorkflowError(event: WorkflowEvent, pairedResult: WorkflowEvent | null) {
		return Boolean(
			(event.error || '').trim() ||
			readResultError(event.result) ||
			(pairedResult ? pairedResult.error || readResultError(pairedResult.result) : '')
		);
	}

	function readString(source: unknown, key: string) {
		if (!source || typeof source !== 'object' || Array.isArray(source)) return '';
		const v = (source as Record<string, unknown>)[key];
		return typeof v === 'string' ? v.trim() : '';
	}

	function readNumber(source: unknown, key: string) {
		if (!source || typeof source !== 'object' || Array.isArray(source)) return 0;
		const v = (source as Record<string, unknown>)[key];
		return typeof v === 'number' && Number.isFinite(v) ? v : 0;
	}

	function readArrayLength(source: unknown, key: string) {
		if (!source || typeof source !== 'object' || Array.isArray(source)) return 0;
		const v = (source as Record<string, unknown>)[key];
		return Array.isArray(v) ? v.length : 0;
	}

	function truncateText(value: string, maxLen: number) {
		return value.length <= maxLen ? value : `${value.slice(0, maxLen - 1)}…`;
	}

	function formatDuration(ms: number | null) {
		if (!Number.isFinite(ms) || ms == null) return '';
		if (ms < 1000) return `${Math.round(ms)}ms`;
		return `${(ms / 1000).toFixed(ms >= 10000 ? 0 : 1)}s`;
	}

	function resolveStepVerb(tool: string): string {
		switch (tool) {
			case 'create_task': return `Creating ${taskLabel}`;
			case 'update_task': return `Updating ${taskLabel}`;
			case 'delete_task': return `Deleting ${taskLabel}`;
			case 'list_tasks': return `Loading ${taskPluralLabel}`;
			case 'list_sprints':
			case 'list_groups': return `Loading ${groupPluralLabel}`;
			case 'search_tasks': return `Searching ${taskPluralLabel}`;
			case 'delete_group': return `Deleting ${groupLabel}`;
			case 'write_canvas': return 'Writing file';
			case 'verify_task_count': return 'Verifying';
			default: return tool ? `Calling ${tool}` : 'Working';
		}
	}
</script>

<svelte:window on:keydown={handleWindowKeydown} />

<div class="workflow-shell">
	<button
		type="button"
		class="workflow-toggle"
		class:is-running={isRunning}
		class:is-done={isDone && !isRunning}
		class:has-error={hasError}
		aria-expanded={isOpen}
		title={panelTitle}
		on:click={toggleOpen}
	>
		<span class="toggle-icon" aria-hidden="true">🐼</span>
		<span class="toggle-label">
			{#if isRunning}
				<span class="live-dot" aria-hidden="true"></span>
			{/if}
			Workflow
		</span>
		{#if latestEvent}
			<span class="toggle-turns">{latestEvent.turn}/{latestEvent.totalTurns}</span>
		{/if}
	</button>

	{#if isOpen}
		<!-- svelte-ignore a11y_click_events_have_key_events -->
		<div class="wf-overlay" role="presentation" style={overlayStyle} on:click={closePanel}>
			<div
				class="wf-panel"
				role="dialog"
				aria-modal="true"
				aria-label={panelTitle}
				tabindex="-1"
				on:click|stopPropagation
			>
				<!-- Header -->
				<div class="wf-header">
					<div class="wf-header-left">
						<span class="wf-header-icon" aria-hidden="true">🐼</span>
						<div class="wf-header-info">
							<span class="wf-title">{panelTitle}</span>
							{#if latestEvent}
								<span class="wf-subtitle">Turn {latestEvent.turn} of {latestEvent.totalTurns}</span>
							{/if}
						</div>
					</div>
					<div class="wf-header-right">
						{#if isRunning}
							<span class="wf-running-badge">
								<span class="wf-live-dot"></span>Live
							</span>
						{:else if isDone}
							<span class="wf-done-badge">✓ Done</span>
						{/if}
						<button type="button" class="wf-close" aria-label="Close" on:click={closePanel}>✕</button>
					</div>
				</div>

				<!-- Progress bar -->
				{#if isRunning || progressPercent > 0}
					<div class="wf-progress-track">
						<div class="wf-progress-fill" style="width:{progressPercent}%"></div>
					</div>
				{/if}

				<!-- Timeline -->
				<div class="wf-timeline" bind:this={timelineEl}>
					{#if timelineRows.length === 0}
						<div class="wf-empty" in:fly={{ y: 6, duration: 160 }}>
							<span class="wf-spinner" aria-hidden="true"></span>
							Waiting for activity…
						</div>
					{:else}
						{#each timelineRows as row (row.event.id)}
							<div
								class="wf-step"
								class:step-ok={row.status === 'ok'}
								class:step-error={row.status === 'error'}
								class:step-running={row.status === 'running'}
								in:fly={{ y: 8, duration: 200 }}
							>
								<!-- Left accent -->
								<div class="wf-step-accent {row.chipClass}"></div>

								<!-- Icon -->
								<div class="wf-step-icon" class:spin={row.status === 'running'}>
									{#if row.status === 'running'}
										<span class="wf-spinner-icon" aria-hidden="true"></span>
									{:else if row.status === 'error'}
										<span class="icon-error">✕</span>
									{:else if row.event.kind === 'done'}
										<span class="icon-done">✓</span>
									{:else if row.event.kind === 'thinking'}
										<span class="icon-think">…</span>
									{:else if row.event.kind === 'text'}
										<span class="icon-text">💬</span>
									{:else}
										<span class="icon-ok">✓</span>
									{/if}
								</div>

								<!-- Content -->
								<div class="wf-step-body">
									{#if row.event.kind === 'tool_call' || row.event.kind === 'tool_result'}
										<!-- Tool row: chip + human label on one line, result below -->
										<div class="wf-step-head">
											{#if row.chipLabel}
												<span class="wf-chip {row.chipClass}">{row.chipLabel}</span>
											{/if}
											<span class="wf-step-label">{row.label}</span>
											{#if row.durationMs !== null}
												<span class="wf-dur">{formatDuration(row.durationMs)}</span>
											{/if}
										</div>
										{#if row.status === 'running'}
											<div class="wf-step-result running-text">{resolveStepVerb(row.event.tool ?? '')}…</div>
										{:else if row.resultSummary && row.resultSummary !== row.label}
											<div class="wf-step-result" class:result-error={row.status === 'error'}>
												{row.resultSummary}
											</div>
										{/if}
										<!-- Expandable params -->
										{#if row.isExpandable && row.detailRows.length > 0}
											<button
												type="button"
												class="wf-expand-btn"
												on:click={() => toggleRow(row.event.id)}
											>
												{expandedRows[row.event.id] ? '▲ hide' : '▼ details'}
											</button>
											{#if expandedRows[row.event.id]}
												<div class="wf-params" in:fly={{ y: 4, duration: 140 }}>
													{#each row.detailRows as detail}
														<div class="wf-param-row">
															<span class="wf-param-key">{detail.key}</span>
															<span class="wf-param-val">{detail.value}</span>
														</div>
													{/each}
												</div>
											{/if}
										{/if}
									{:else}
										<!-- Thinking / text / done row -->
										<div class="wf-step-prose
											{row.event.kind === 'done' ? 'prose-done' : ''}
											{row.event.kind === 'thinking' ? 'prose-think' : ''}
										">{row.label}</div>
									{/if}
								</div>
							</div>
						{/each}

						<!-- Live tail indicator -->
						{#if isRunning}
							<div class="wf-tail" in:fly={{ y: 6, duration: 180 }}>
								<span class="wf-spinner" aria-hidden="true"></span>
								<span class="tail-text">Processing…</span>
							</div>
						{/if}
					{/if}
				</div>
			</div>
		</div>
	{/if}
</div>

<style>
	/* ── Shell & Toggle ───────────────────────────────────────── */
	.workflow-shell {
		display: inline-flex;
		flex-direction: column;
		align-items: flex-end;
		width: 100%;
	}

	.workflow-toggle {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		padding: 4px 10px 4px 8px;
		border-radius: 999px;
		border: 1px solid rgba(148, 163, 184, 0.22);
		background: rgba(255, 255, 255, 0.86);
		color: #475569;
		font-size: 0.71rem;
		font-weight: 600;
		cursor: pointer;
		box-shadow: 0 2px 6px rgba(15, 23, 42, 0.06);
		backdrop-filter: blur(8px);
		transition: border-color 0.15s, background 0.15s;
	}
	.workflow-toggle:hover {
		border-color: rgba(99, 102, 241, 0.3);
		background: rgba(238, 242, 255, 0.95);
		color: #4338ca;
	}
	.workflow-toggle.is-running {
		border-color: rgba(99, 102, 241, 0.32);
		background: rgba(238, 242, 255, 0.97);
		color: #4338ca;
	}
	.workflow-toggle.is-done {
		border-color: rgba(22, 163, 74, 0.25);
		color: #15803d;
		background: rgba(240, 253, 244, 0.95);
	}
	.workflow-toggle.has-error {
		border-color: rgba(220, 38, 38, 0.25);
		color: #b91c1c;
	}

	.toggle-icon { font-size: 0.88rem; }
	.toggle-label {
		display: flex;
		align-items: center;
		gap: 5px;
		letter-spacing: 0.01em;
	}
	.toggle-turns {
		font-size: 0.65rem;
		color: rgba(100, 116, 139, 0.7);
		font-weight: 500;
	}

	.live-dot {
		display: inline-block;
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: #6366f1;
		animation: pulseDot 1.2s ease-in-out infinite;
	}

	@keyframes pulseDot {
		0%, 100% { opacity: 1; transform: scale(1); }
		50% { opacity: 0.5; transform: scale(0.7); }
	}

	/* ── Overlay & Panel ──────────────────────────────────────── */
	.wf-overlay {
		position: fixed;
		inset: 0;
		z-index: 1200;
		background: rgba(15, 23, 42, 0.14);
		display: flex;
		align-items: flex-start;
		justify-content: flex-end;
		padding: 1rem;
		box-sizing: border-box;
	}

	.wf-panel {
		width: min(36rem, 100%);
		max-height: calc(100vh - 2rem);
		margin-top: clamp(0rem, 5vh, 3rem);
		border-radius: 16px;
		border: 1px solid rgba(148, 163, 184, 0.18);
		background: #ffffff;
		box-shadow:
			0 4px 6px rgba(15, 23, 42, 0.04),
			0 20px 48px rgba(15, 23, 42, 0.12);
		display: flex;
		flex-direction: column;
		overflow: hidden;
	}

	/* ── Header ───────────────────────────────────────────────── */
	.wf-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 10px;
		padding: 12px 14px 11px;
		border-bottom: 1px solid rgba(148, 163, 184, 0.12);
		background: rgba(248, 250, 252, 0.8);
	}

	.wf-header-left {
		display: flex;
		align-items: center;
		gap: 8px;
		min-width: 0;
	}

	.wf-header-icon { font-size: 1rem; flex-shrink: 0; }

	.wf-header-info {
		display: flex;
		flex-direction: column;
		gap: 1px;
		min-width: 0;
	}

	.wf-title {
		font-size: 0.8rem;
		font-weight: 700;
		color: #0f172a;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.wf-subtitle {
		font-size: 0.64rem;
		font-weight: 600;
		color: #94a3b8;
		text-transform: uppercase;
		letter-spacing: 0.07em;
	}

	.wf-header-right {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-shrink: 0;
	}

	.wf-running-badge {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		padding: 2px 8px;
		border-radius: 999px;
		background: rgba(99, 102, 241, 0.1);
		color: #4338ca;
		font-size: 0.65rem;
		font-weight: 700;
		letter-spacing: 0.04em;
		text-transform: uppercase;
	}

	.wf-live-dot {
		display: inline-block;
		width: 5px;
		height: 5px;
		border-radius: 50%;
		background: #6366f1;
		animation: pulseDot 1.2s ease-in-out infinite;
	}

	.wf-done-badge {
		padding: 2px 8px;
		border-radius: 999px;
		background: rgba(22, 163, 74, 0.1);
		color: #15803d;
		font-size: 0.65rem;
		font-weight: 700;
		letter-spacing: 0.04em;
		text-transform: uppercase;
	}

	.wf-close {
		width: 1.75rem;
		height: 1.75rem;
		border: 1px solid rgba(148, 163, 184, 0.22);
		border-radius: 8px;
		background: transparent;
		color: #64748b;
		font-size: 0.78rem;
		cursor: pointer;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		transition: background 0.12s, color 0.12s;
	}
	.wf-close:hover {
		background: rgba(220, 38, 38, 0.08);
		color: #dc2626;
		border-color: rgba(220, 38, 38, 0.2);
	}

	/* ── Progress ─────────────────────────────────────────────── */
	.wf-progress-track {
		height: 2px;
		background: rgba(99, 102, 241, 0.08);
	}
	.wf-progress-fill {
		height: 100%;
		background: linear-gradient(90deg, #6366f1, #818cf8);
		transition: width 350ms ease;
	}

	/* ── Timeline ─────────────────────────────────────────────── */
	.wf-timeline {
		flex: 1 1 auto;
		min-height: 0;
		overflow-y: auto;
		padding: 8px 0 12px;
		scroll-behavior: smooth;
	}

	.wf-empty {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 16px 16px;
		font-size: 0.76rem;
		color: #94a3b8;
		font-style: italic;
	}

	/* ── Step Row ─────────────────────────────────────────────── */
	.wf-step {
		display: flex;
		align-items: flex-start;
		gap: 0;
		padding: 7px 14px 7px 0;
		border-bottom: 1px solid rgba(241, 245, 249, 1);
		position: relative;
	}
	.wf-step:last-child { border-bottom: none; }

	.wf-step-accent {
		flex-shrink: 0;
		width: 3px;
		align-self: stretch;
		border-radius: 0 2px 2px 0;
		margin-right: 10px;
		background: rgba(148, 163, 184, 0.2);
	}
	.wf-step-accent.chip-create { background: rgba(22, 163, 74, 0.45); }
	.wf-step-accent.chip-update { background: rgba(59, 130, 246, 0.45); }
	.wf-step-accent.chip-delete { background: rgba(220, 38, 38, 0.45); }
	.wf-step-accent.chip-read   { background: rgba(148, 163, 184, 0.35); }

	.step-running .wf-step-accent { background: rgba(99, 102, 241, 0.5); }
	.step-error .wf-step-accent   { background: rgba(220, 38, 38, 0.5); }

	.wf-step-icon {
		flex-shrink: 0;
		width: 20px;
		height: 20px;
		display: flex;
		align-items: center;
		justify-content: center;
		margin-right: 8px;
		margin-top: 1px;
		font-size: 0.72rem;
	}

	.icon-ok   { color: #16a34a; font-size: 0.75rem; }
	.icon-done { color: #16a34a; font-weight: 700; }
	.icon-error{ color: #dc2626; font-weight: 700; }
	.icon-think{ color: #94a3b8; font-size: 0.9rem; letter-spacing: -1px; }
	.icon-text { font-size: 0.82rem; }

	.wf-step-body {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	.wf-step-head {
		display: flex;
		align-items: center;
		gap: 6px;
		flex-wrap: wrap;
	}

	.wf-step-label {
		font-size: 0.76rem;
		font-weight: 500;
		color: #1e293b;
		flex: 1;
		min-width: 0;
		word-break: break-word;
	}

	.step-running .wf-step-label { color: #4338ca; }
	.step-error   .wf-step-label { color: #b91c1c; }

	.wf-chip {
		padding: 1px 6px;
		border-radius: 4px;
		font-size: 0.63rem;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.06em;
	}
	.chip-create { background: rgba(22, 163, 74, 0.1);  color: #15803d; }
	.chip-update { background: rgba(59, 130, 246, 0.1); color: #1d4ed8; }
	.chip-delete { background: rgba(220, 38, 38, 0.1);  color: #b91c1c; }
	.chip-read   { background: rgba(148, 163, 184, 0.14); color: #475569; }

	.wf-dur {
		margin-left: auto;
		font-size: 0.62rem;
		color: #94a3b8;
		font-weight: 500;
		flex-shrink: 0;
	}

	.wf-step-arg {
		font-size: 0.73rem;
		color: #475569;
		word-break: break-word;
		padding-left: 2px;
	}

	.wf-step-result {
		font-size: 0.71rem;
		color: #64748b;
		padding-left: 2px;
		word-break: break-word;
	}
	.wf-step-result.result-error { color: #b91c1c; }
	.running-text {
		color: #6366f1;
		font-style: italic;
	}

	.wf-expand-btn {
		margin-top: 3px;
		font-size: 0.63rem;
		color: #94a3b8;
		background: none;
		border: none;
		padding: 0;
		cursor: pointer;
		text-decoration: underline;
		text-underline-offset: 2px;
	}
	.wf-expand-btn:hover { color: #6366f1; }

	.wf-params {
		margin-top: 5px;
		padding: 7px 9px;
		border-radius: 8px;
		background: rgba(248, 250, 252, 0.9);
		border: 1px solid rgba(226, 232, 240, 1);
	}

	.wf-param-row {
		display: flex;
		gap: 6px;
		padding: 2px 0;
		font-size: 0.7rem;
		line-height: 1.4;
	}

	.wf-param-key {
		flex-shrink: 0;
		font-family: 'SF Mono', 'Fira Code', monospace;
		font-weight: 600;
		color: #64748b;
		min-width: 70px;
	}

	.wf-param-val { color: #334155; }

	/* Thinking / text / done prose */
	.wf-step-prose {
		font-size: 0.75rem;
		color: #475569;
		line-height: 1.45;
		padding-left: 2px;
		word-break: break-word;
		white-space: pre-wrap;
	}
	.prose-think { color: #94a3b8; font-style: italic; }
	.prose-done  { color: #15803d; font-weight: 600; }

	/* ── Spinner ──────────────────────────────────────────────── */
	.wf-spinner, .wf-spinner-icon {
		display: inline-block;
		width: 13px;
		height: 13px;
		border: 2px solid rgba(99, 102, 241, 0.18);
		border-top-color: #6366f1;
		border-radius: 50%;
		animation: spinAnim 0.7s linear infinite;
		flex-shrink: 0;
	}

	@keyframes spinAnim {
		to { transform: rotate(360deg); }
	}

	/* ── Tail ─────────────────────────────────────────────────── */
	.wf-tail {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 10px 14px 4px 13px;
		font-size: 0.71rem;
		color: #94a3b8;
	}
	.tail-text { font-style: italic; }

	/* ── Mobile ───────────────────────────────────────────────── */
	@media (max-width: 680px) {
		.wf-overlay {
			padding: 0.5rem;
			align-items: flex-end;
			justify-content: center;
		}
		.wf-panel {
			width: 100%;
			margin-top: 0;
			border-radius: 16px 16px 0 0;
			max-height: 80vh;
		}
	}
</style>
