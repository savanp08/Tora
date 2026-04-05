<script lang="ts">
	import { onMount, createEventDispatcher } from 'svelte';
	import RichTextContent from '$lib/components/chat/RichTextContent.svelte';
	import { projectTypeConfig } from '$lib/stores/projectType';

	type ActionKind = 'task_create' | 'task_update' | 'task_delete';

	type TaskRole = { role: string; responsibilities: string };

	type TaskAction = {
		kind: ActionKind;
		already_applied?: boolean;
		task_id?: string; // internal — used for API calls, never shown
		task_title?: string;
		task_sprint?: string; // sprint the task belongs to (for update/delete context)
		task_parent?: string; // parent task title if subtask (for update/delete context)
		title?: string;
		description?: string;
		status?: string;
		sprint?: string;
		task_type?: string;
		budget?: number;
		start_date?: string;
		due_date?: string;
		roles?: TaskRole[];
		changes?: Record<string, unknown>;
		change_details?: Record<string, unknown>;
		changeDetails?: Record<string, unknown>;
		field_changes?: Record<string, unknown>;
		fieldChanges?: Record<string, unknown>;
		diff?: Record<string, unknown>;
	};

	type CardState = 'pending' | 'applying' | 'applied' | 'rejected' | 'error';

	type AppliedMeta = {
		appliedBy: string;
		appliedAt: string;
		counts: { created: number; updated: number; deleted: number };
	};
	type DismissedMeta = {
		dismissedBy: string;
		dismissedAt: string;
	};
	type PersistedResolution = {
		state: 'applied' | 'rejected';
		appliedMeta?: AppliedMeta;
		dismissedMeta?: DismissedMeta;
	};
	type AuditTrailEntry = {
		index: number;
		kind: string;
		tool: string;
		input: Record<string, unknown>;
		result: unknown;
		text: string;
		error: string;
	};
	type ChangeEntry = {
		field: string;
		label: string;
		beforeText: string;
		afterText: string;
		hasBefore: boolean;
	};

	export let text = '';
	export let actionsJson = '';
	export let auditTrail: unknown[] = [];
	export let roomId = '';
	export let apiBase = '';
	export let authToken = '';
	export let autoApply = false;
	export let currentUserName = '';
	export let canResolve = false;

	const dispatch = createEventDispatcher<{ applied: { roomId: string } }>();

	let actions: TaskAction[] = [];
	let state: CardState = 'pending';
	let errorMsg = '';
	let applyProgress = 0;
	let appliedCounts = { created: 0, updated: 0, deleted: 0 };
	let appliedMeta: AppliedMeta | null = null;
	let dismissedMeta: DismissedMeta | null = null;
	let showModal = false;
	let showAll = false;
	let auditEntries: AuditTrailEntry[] = [];

	const PREVIEW_LIMIT = 5;
	const UPDATE_PREVIEW_LIMIT = 3;
	const AUDIT_PREVIEW_LIMIT = 4;
	const currencyFields = new Set(['budget', 'actual_cost', 'actualcost', 'spent']);
	const dateFormatter = new Intl.DateTimeFormat(undefined, {
		month: 'short',
		day: 'numeric',
		year: 'numeric'
	});
	let fieldLabels: Record<string, string> = {
		title: 'Title',
		description: 'Description',
		status: 'Status',
		sprint: 'Sprint',
		sprint_name: 'Sprint',
		task_type: 'Type',
		budget: 'Budget',
		start_date: 'Start date',
		due_date: 'Due date',
		roles: 'Roles',
		assignee_id: 'Assignee',
		actual_cost: 'Actual cost',
		spent: 'Spent',
		completion_percent: 'Completion',
		blocked_by: 'Blocked by',
		blocks: 'Blocks'
	};
	$: taskTerm = $projectTypeConfig.taskTerm;
	$: groupTerm = $projectTypeConfig.groupTerm;
	$: taskLabel = taskTerm.toLowerCase();
	$: groupLabel = groupTerm.toLowerCase();
	$: fieldLabels = {
		title: 'Title',
		description: 'Description',
		status: 'Status',
		sprint: groupTerm,
		sprint_name: groupTerm,
		task_type: 'Type',
		budget: 'Budget',
		start_date: 'Start date',
		due_date: 'Due date',
		roles: 'Roles',
		assignee_id: 'Assignee',
		actual_cost: 'Actual cost',
		spent: 'Spent',
		completion_percent: 'Completion',
		blocked_by: 'Blocked by',
		blocks: 'Blocks'
	};

	type ActionResult = { ok: boolean; skipped?: boolean; error?: string };
	let actionResults: ActionResult[] = [];

	function toRecord(value: unknown): Record<string, unknown> | null {
		if (!value || typeof value !== 'object' || Array.isArray(value)) {
			return null;
		}
		return value as Record<string, unknown>;
	}

	function normalizeFieldName(field: string) {
		return (field || '').trim().toLowerCase();
	}

	function humanizeFieldLabel(field: string) {
		const normalized = normalizeFieldName(field);
		if (fieldLabels[normalized]) {
			return fieldLabels[normalized];
		}
		return normalized
			.split('_')
			.filter(Boolean)
			.map((part) => part.charAt(0).toUpperCase() + part.slice(1))
			.join(' ');
	}

	function trimPreviewText(value: string, max = 72) {
		const normalized = value.trim();
		if (normalized.length <= max) {
			return normalized;
		}
		return `${normalized.slice(0, max - 1).trimEnd()}…`;
	}

	function formatDateLabel(value: string) {
		const trimmed = value.trim();
		if (!trimmed) {
			return '';
		}
		const dateOnlyMatch = trimmed.match(/^(\d{4})-(\d{2})-(\d{2})$/);
		if (dateOnlyMatch) {
			const [, year, month, day] = dateOnlyMatch;
			return dateFormatter.format(new Date(Number(year), Number(month) - 1, Number(day)));
		}
		const parsed = Date.parse(trimmed);
		if (!Number.isFinite(parsed)) {
			return trimmed;
		}
		return dateFormatter.format(new Date(parsed));
	}

	function formatRoleEntry(value: Record<string, unknown>) {
		const role = typeof value.role === 'string' ? value.role.trim() : '';
		const responsibilities =
			typeof value.responsibilities === 'string' ? value.responsibilities.trim() : '';
		if (role && responsibilities) {
			return `${role} (${responsibilities})`;
		}
		return role || responsibilities || JSON.stringify(value);
	}

	function formatChangeValue(value: unknown, field: string) {
		if (value == null) {
			return 'Empty';
		}
		if (typeof value === 'string') {
			const trimmed = value.trim();
			if (!trimmed) {
				return 'Empty';
			}
			const normalizedField = normalizeFieldName(field);
			if (
				normalizedField.includes('date') ||
				normalizedField.endsWith('_at') ||
				/^\d{4}-\d{2}-\d{2}(?:[tT ].*)?$/.test(trimmed)
			) {
				return formatDateLabel(trimmed);
			}
			return trimmed;
		}
		if (typeof value === 'number') {
			const normalizedField = normalizeFieldName(field);
			if (currencyFields.has(normalizedField)) {
				return `$${value.toLocaleString()}`;
			}
			return value.toLocaleString(undefined, {
				maximumFractionDigits: Number.isInteger(value) ? 0 : 2
			});
		}
		if (typeof value === 'boolean') {
			return value ? 'Yes' : 'No';
		}
		if (Array.isArray(value)) {
			if (value.length === 0) {
				return 'Empty';
			}
			return value
				.map((entry) => {
					if (entry == null) {
						return 'Empty';
					}
					if (typeof entry === 'string') {
						return entry.trim() || 'Empty';
					}
					if (typeof entry === 'number' || typeof entry === 'boolean') {
						return String(entry);
					}
					const record = toRecord(entry);
					if (record) {
						return formatRoleEntry(record);
					}
					return JSON.stringify(entry);
				})
				.join(', ');
		}
		const record = toRecord(value);
		if (record) {
			return formatRoleEntry(record);
		}
		return String(value);
	}

	function formatPreviewValue(value: string, field: string) {
		return trimPreviewText(value, normalizeFieldName(field) === 'description' ? 96 : 54);
	}

	function getDisplayChangeDetails(action: TaskAction): Record<string, unknown> {
		return (toRecord(action.change_details) ??
			toRecord(action.changeDetails) ??
			toRecord(action.field_changes) ??
			toRecord(action.fieldChanges) ??
			toRecord(action.diff) ??
			{}) as Record<string, unknown>;
	}

	function extractDiffValues(value: unknown) {
		const record = toRecord(value);
		if (!record) {
			return { before: undefined, after: undefined, hasBefore: false };
		}
		const before = record.from ?? record.before ?? record.old ?? record.previous ?? record.current;
		const after = record.to ?? record.after ?? record.new ?? record.next ?? record.value;
		return {
			before,
			after,
			hasBefore: before !== undefined
		};
	}

	function getFallbackBeforeValue(action: TaskAction, field: string) {
		const normalizedField = normalizeFieldName(field);
		if (normalizedField === 'title') {
			return action.task_title;
		}
		if (normalizedField === 'sprint' || normalizedField === 'sprint_name') {
			return action.task_sprint;
		}
		if (normalizedField === 'task_parent' || normalizedField === 'parent') {
			return action.task_parent;
		}
		return undefined;
	}

	function getChangeEntries(action: TaskAction): ChangeEntry[] {
		const changes = toRecord(action.changes) ?? {};
		const details = getDisplayChangeDetails(action);
		const orderedKeys: string[] = [];
		const seen = new Set<string>();

		for (const key of Object.keys(changes)) {
			if (seen.has(key)) {
				continue;
			}
			seen.add(key);
			orderedKeys.push(key);
		}
		for (const key of Object.keys(details)) {
			if (seen.has(key)) {
				continue;
			}
			seen.add(key);
			orderedKeys.push(key);
		}

		return orderedKeys
			.map((field) => {
				const detail = extractDiffValues(details[field]);
				const fallbackBefore = getFallbackBeforeValue(action, field);
				const beforeValue = detail.hasBefore ? detail.before : fallbackBefore;
				const afterValue = changes[field] !== undefined ? changes[field] : detail.after;
				if (beforeValue === undefined && afterValue === undefined) {
					return null;
				}
				return {
					field,
					label: humanizeFieldLabel(field),
					beforeText: beforeValue === undefined ? '' : formatChangeValue(beforeValue, field),
					afterText: formatChangeValue(afterValue, field),
					hasBefore: beforeValue !== undefined
				};
			})
			.filter((entry): entry is ChangeEntry => Boolean(entry));
	}

	function getPreviewEntries(action: TaskAction) {
		return getChangeEntries(action).slice(0, UPDATE_PREVIEW_LIMIT);
	}

	function normalizeAuditTrailEntry(value: unknown, fallbackIndex: number): AuditTrailEntry | null {
		const record = toRecord(value);
		if (!record) {
			return null;
		}
		return {
			index:
				typeof record.index === 'number' && Number.isFinite(record.index)
					? Math.max(1, Math.trunc(record.index))
					: fallbackIndex + 1,
			kind: typeof record.kind === 'string' ? record.kind.trim() : '',
			tool: typeof record.tool === 'string' ? record.tool.trim() : '',
			input: toRecord(record.input) ?? {},
			result: record.result,
			text: typeof record.text === 'string' ? record.text.trim() : '',
			error: typeof record.error === 'string' ? record.error.trim() : ''
		};
	}

	function auditEntryHasError(entry: AuditTrailEntry) {
		const resultRecord = toRecord(entry.result);
		return Boolean(entry.error || (resultRecord && typeof resultRecord.error === 'string' && resultRecord.error.trim()));
	}

	function getAuditEntryIcon(entry: AuditTrailEntry) {
		if (auditEntryHasError(entry)) {
			return '✕';
		}
		switch (entry.kind) {
			case 'thinking':
				return '⏳';
			case 'tool_call':
				return entry.tool === 'write_canvas' ? '✍️' : '🔧';
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

	function prettifyToolName(tool: string) {
		return tool
			.trim()
			.replace(/_/g, ' ')
			.replace(/\b\w/g, (letter) => letter.toUpperCase());
	}

	function summarizeAuditTarget(input: Record<string, unknown>) {
		return (
			(typeof input.title === 'string' && input.title.trim()) ||
			(typeof input.task_title === 'string' && input.task_title.trim()) ||
			(typeof input.file_path === 'string' && input.file_path.trim()) ||
			(typeof input.main_file === 'string' && input.main_file.trim()) ||
			(typeof input.task_id === 'string' && input.task_id.trim()) ||
			''
		);
	}

	function summarizeAuditResult(result: unknown) {
		if (Array.isArray(result)) {
			return `${result.length} item${result.length === 1 ? '' : 's'}`;
		}
		const record = toRecord(result);
		if (!record) {
			return '';
		}
		if (typeof record.error === 'string' && record.error.trim()) {
			return record.error.trim();
		}
		if (record.total_tasks !== undefined && typeof record.total_tasks === 'number') {
			const supportTickets =
				typeof record.support_tickets === 'number' ? ` · ${record.support_tickets} support` : '';
			return `${record.total_tasks} tasks${supportTickets}`;
		}
		if (typeof record.task_title === 'string' && record.task_title.trim()) {
			return record.task_title.trim();
		}
		if (typeof record.title === 'string' && record.title.trim()) {
			return record.title.trim();
		}
		if (typeof record.path === 'string' && record.path.trim()) {
			const lines = typeof record.lines === 'number' ? ` · ${Math.max(0, Math.trunc(record.lines))} lines` : '';
			return `${record.path.trim()}${lines}`;
		}
		if (typeof record.deleted === 'boolean' && record.deleted) {
			return 'Deleted';
		}
		if (typeof record.updated === 'boolean' && record.updated) {
			return 'Updated';
		}
		if (typeof record.created === 'boolean' && record.created) {
			return 'Created';
		}
		if (typeof record.written === 'boolean' && record.written) {
			return 'Written';
		}
		return Object.keys(record)
			.slice(0, 3)
			.map((key) => `${humanizeFieldLabel(key)}: ${formatChangeValue(record[key], key)}`)
			.join(' · ');
	}

	function formatAuditEntryLabel(entry: AuditTrailEntry) {
		const toolLabel = prettifyToolName(entry.tool);
		const target = summarizeAuditTarget(entry.input);
		if (entry.kind === 'thinking') {
			return trimPreviewText(entry.text || 'Planning the next step.', 88);
		}
		if (entry.kind === 'text') {
			return `Tora: ${trimPreviewText(entry.text || 'Shared an update.', 88)}`;
		}
		if (entry.kind === 'done') {
			return trimPreviewText(entry.text || 'Completed the agent run.', 96);
		}
		if (entry.kind === 'tool_call') {
			return target ? `${toolLabel}: ${trimPreviewText(target, 68)}` : toolLabel || 'Tool call';
		}
		if (entry.kind === 'tool_result') {
			if (auditEntryHasError(entry)) {
				return `${toolLabel || 'Tool'} failed`;
			}
			const summary = summarizeAuditResult(entry.result);
			if (summary) {
				return `${toolLabel || 'Tool'}: ${trimPreviewText(summary, 76)}`;
			}
			return `${toolLabel || 'Tool'} succeeded`;
		}
		return trimPreviewText(entry.text || toolLabel || 'Agent event', 88);
	}

	function formatAuditEntryMeta(entry: AuditTrailEntry) {
		if (entry.kind === 'tool_call') {
			const params = Object.entries(entry.input)
				.filter(([key, value]) => {
					if (key.toLowerCase().endsWith('id') || value == null) {
						return false;
					}
					if (typeof value === 'string') {
						return value.trim() !== '';
					}
					if (Array.isArray(value)) {
						return value.length > 0;
					}
					return true;
				})
				.slice(0, 3)
				.map(([key, value]) => `${humanizeFieldLabel(key)}: ${trimPreviewText(formatChangeValue(value, key), 48)}`);
			return params.join(' · ');
		}
		if (entry.kind === 'tool_result') {
			const resultSummary = summarizeAuditResult(entry.result);
			return trimPreviewText(resultSummary, 84);
		}
		return '';
	}

	// Stable hash of the full actionsJson string — used for localStorage persistence.
	// Uses djb2 so different action lists always produce different keys.
	function hashActionsJson(s: string): string {
		let h = 5381;
		for (let i = 0; i < s.length; i++) {
			h = (((h << 5) + h) ^ s.charCodeAt(i)) >>> 0;
		}
		return h.toString(36);
	}
	function persistKey(): string {
		return `tora_applied_${roomId}_${hashActionsJson(actionsJson)}`;
	}

	function persistResolutionState(value: PersistedResolution) {
		try {
			localStorage.setItem(persistKey(), JSON.stringify(value));
		} catch {
			/* ignore */
		}
	}

	function resolveActorName() {
		return currentUserName.trim() || 'You';
	}

	$: {
		try {
			const parsed = JSON.parse(actionsJson);
			actions = Array.isArray(parsed) ? parsed : [];
		} catch {
			actions = [];
		}
	}
	$: auditEntries = Array.isArray(auditTrail)
		? auditTrail
				.map((entry, index) => normalizeAuditTrailEntry(entry, index))
				.filter((entry): entry is AuditTrailEntry => Boolean(entry))
		: [];

	$: createCount = actions.filter((a) => a.kind === 'task_create').length;
	$: updateCount = actions.filter((a) => a.kind === 'task_update').length;
	$: deleteCount = actions.filter((a) => a.kind === 'task_delete').length;
	$: visibleActions = showAll ? actions : actions.slice(0, PREVIEW_LIMIT);
	$: hasMore = !showAll && actions.length > PREVIEW_LIMIT;
	$: auditToolCallCount = auditEntries.filter((entry) => entry.kind === 'tool_call').length;
	$: auditErrorCount = auditEntries.filter((entry) => auditEntryHasError(entry)).length;
	$: auditPreviewEntries = auditEntries.slice(0, AUDIT_PREVIEW_LIMIT);
	$: auditSummaryText =
		auditEntries.length === 0
			? ''
			: `${auditEntries.length} event${auditEntries.length === 1 ? '' : 's'} · ${auditToolCallCount} tool call${auditToolCallCount === 1 ? '' : 's'}${auditErrorCount > 0 ? ` · ${auditErrorCount} issue${auditErrorCount === 1 ? '' : 's'}` : ''}`;

	onMount(() => {
		const allAlreadyApplied =
			actions.length > 0 && actions.every((action) => action.already_applied === true);
		if (allAlreadyApplied) {
			appliedCounts = {
				created: createCount,
				updated: updateCount,
				deleted: deleteCount
			};
			actionResults = actions.map(() => ({ ok: true }));
			appliedMeta = {
				appliedBy: 'Tora-Bot',
				appliedAt: new Date().toISOString(),
				counts: { ...appliedCounts }
			};
			state = 'applied';
			return;
		}

		// Restore persisted applied state from localStorage
		try {
			const saved = localStorage.getItem(persistKey());
			if (saved) {
				const parsed = JSON.parse(saved) as PersistedResolution | AppliedMeta;
				if ('state' in parsed) {
					if (parsed.state === 'applied' && parsed.appliedMeta) {
						appliedMeta = parsed.appliedMeta;
						appliedCounts = parsed.appliedMeta.counts;
						state = 'applied';
						return;
					}
					if (parsed.state === 'rejected' && parsed.dismissedMeta) {
						dismissedMeta = parsed.dismissedMeta;
						state = 'rejected';
						return;
					}
				} else {
					appliedMeta = parsed;
					appliedCounts = parsed.counts;
					state = 'applied';
					return;
				}
				return;
			}
		} catch {
			// ignore
		}
		if (autoApply && canResolve && state === 'pending' && actions.length > 0) {
			void applyActions();
		}
	});

	async function applyActions() {
		if (!canResolve) {
			errorMsg = 'Only room admins can accept or dismiss these changes right now.';
			state = 'error';
			return;
		}
		state = 'applying';
		errorMsg = '';
		applyProgress = 0;
		appliedCounts = { created: 0, updated: 0, deleted: 0 };
		dismissedMeta = null;
		actionResults = actions.map(() => ({ ok: false }));
		const errors: string[] = [];

		for (let i = 0; i < actions.length; i++) {
			const action = actions[i];
			applyProgress = i + 1;
			try {
				await applyAction(action);
				if (action.kind === 'task_create') appliedCounts.created++;
				else if (action.kind === 'task_update') appliedCounts.updated++;
				else if (action.kind === 'task_delete') appliedCounts.deleted++;
				actionResults[i] = { ok: true };
			} catch (err: unknown) {
				const msg = err instanceof Error ? err.message : String(err);
				const is404 = msg.includes('(404)');
				if (action.kind === 'task_delete' && is404) {
					appliedCounts.deleted++;
					actionResults[i] = { ok: true, skipped: true };
					continue;
				}
				actionResults[i] = { ok: false, error: msg };
				errors.push(`[${actionTitle(action)}] ${msg}`);
			}
		}

		applyProgress = 0;
		if (errors.length === 0) {
			state = 'applied';
			// Persist applied state so it survives page reload
			const meta: AppliedMeta = {
				appliedBy: resolveActorName(),
				appliedAt: new Date().toISOString(),
				counts: { ...appliedCounts }
			};
			appliedMeta = meta;
			dismissedMeta = null;
			persistResolutionState({ state: 'applied', appliedMeta: meta });
			dispatch('applied', { roomId });
		} else {
			state = 'error';
			errorMsg =
				errors.slice(0, 2).join(' · ') + (errors.length > 2 ? ` (+${errors.length - 2} more)` : '');
		}
	}

	async function applyAction(action: TaskAction) {
		const headers: Record<string, string> = { 'Content-Type': 'application/json' };
		if (authToken) headers['Authorization'] = `Bearer ${authToken}`;

		if (action.kind === 'task_create') {
			const body: Record<string, unknown> = {
				title: action.title ?? `Untitled ${taskTerm}`,
				status: action.status ?? 'Todo',
				task_type: action.task_type ?? 'sprint'
			};
			if (action.description) body.description = action.description;
			if (action.sprint) body.sprint_name = action.sprint;
			if (action.due_date) body.due_date = action.due_date;
			if (action.start_date) body.start_date = action.start_date;
			if (typeof action.budget === 'number') body.budget = action.budget;
			if (action.roles?.length) body.roles = action.roles;
			const res = await fetch(`${apiBase}/api/rooms/${roomId}/tasks`, {
				method: 'POST',
				headers,
				body: JSON.stringify(body)
			});
			if (!res.ok) throw new Error(`Create failed (${res.status})`);
		} else if (action.kind === 'task_update') {
			if (!action.task_id) throw new Error(`Update skipped — no task_id provided`);
			const body = action.changes ?? {};
			const res = await fetch(
				`${apiBase}/api/rooms/${roomId}/tasks/${encodeURIComponent(action.task_id)}`,
				{ method: 'PUT', headers, body: JSON.stringify(body) }
			);
			if (!res.ok) throw new Error(`Update failed (${res.status})`);
		} else if (action.kind === 'task_delete') {
			if (!action.task_id) throw new Error(`Delete skipped — no task_id provided`);
			const res = await fetch(
				`${apiBase}/api/rooms/${roomId}/tasks/${encodeURIComponent(action.task_id)}`,
				{ method: 'DELETE', headers }
			);
			if (!res.ok) throw new Error(`Delete failed (${res.status})`);
		}
	}

	function reject() {
		if (!canResolve) {
			errorMsg = 'Only room admins can accept or dismiss these changes right now.';
			state = 'error';
			return;
		}
		appliedMeta = null;
		const nextDismissedMeta: DismissedMeta = {
			dismissedBy: resolveActorName(),
			dismissedAt: new Date().toISOString()
		};
		dismissedMeta = nextDismissedMeta;
		state = 'rejected';
		persistResolutionState({ state: 'rejected', dismissedMeta: nextDismissedMeta });
	}

	function actionTitle(a: TaskAction): string {
		if (a.kind === 'task_create') return a.title ?? `New ${taskTerm}`;
		return a.task_title ?? a.task_id ?? '(no title)';
	}

	function kindLabel(a: TaskAction): string {
		if (a.kind === 'task_create') return 'Create';
		if (a.kind === 'task_update') return 'Edited';
		if (a.kind === 'task_delete') return 'Delete';
		return 'Change';
	}
</script>

<div class="tora-card" class:applied={state === 'applied'} class:rejected={state === 'rejected'}>
	<!-- AI explanation text -->
	{#if text}
		<div class="tora-text">
			<RichTextContent text={text} />
		</div>
	{/if}

	{#if actions.length > 0 && state !== 'rejected'}
		<!-- Summary chips row -->
		<div class="summary-bar">
			{#if createCount > 0}<span class="chip chip-create">{createCount} create</span>{/if}
			{#if updateCount > 0}<span class="chip chip-update">{updateCount} edit</span>{/if}
			{#if deleteCount > 0}<span class="chip chip-delete">{deleteCount} delete</span>{/if}
			<span class="total-label">{actions.length} total</span>
		</div>

		<!-- Inline change list — capped at 300px -->
		<div class="changes-viewport">
			<div class="changes-list">
				{#each visibleActions as action, i}
					{@const idx = actions.indexOf(action)}
					<div class="change-row change-{action.kind}">
						<span class="badge badge-{action.kind}">{kindLabel(action)}</span>
						<span class="row-title">{actionTitle(action)}</span>

						{#if action.kind === 'task_create'}
							<div class="row-chips">
								{#if action.status}<span class="meta">status: {action.status}</span>{/if}
								{#if action.sprint}<span class="meta">{groupLabel}: {action.sprint}</span>{/if}
								{#if action.task_type}<span class="meta">type: {action.task_type}</span>{/if}
								{#if typeof action.budget === 'number'}<span class="meta meta-budget"
										>${action.budget.toLocaleString()}</span
									>{/if}
								{#if action.start_date}<span class="meta"
										>from: {formatChangeValue(action.start_date, 'start_date')}</span
									>{/if}
								{#if action.due_date}<span class="meta"
										>due: {formatChangeValue(action.due_date, 'due_date')}</span
									>{/if}
								{#if action.roles?.length}<span class="meta meta-roles"
										>{action.roles.map((r) => r.role).join(', ')}</span
									>{/if}
								{#if action.description}<span class="meta meta-desc">{action.description}</span
									>{/if}
							</div>
						{/if}

						{#if action.kind === 'task_update' && action.changes}
							{@const previewEntries = getPreviewEntries(action)}
							{@const totalEntries = getChangeEntries(action).length}
							<div class="row-chips">
								<span class="meta meta-edit-count"
									>{totalEntries} field{totalEntries === 1 ? '' : 's'}</span
								>
								{#each previewEntries as entry}
									<span class="meta meta-diff">
										<strong>{entry.label}:</strong>
										{#if entry.hasBefore}
											<span class="meta-diff-before"
												>{formatPreviewValue(entry.beforeText, entry.field)}</span
											>
											<span class="meta-diff-arrow">→</span>
										{:else}
											<span class="meta-diff-arrow">set to</span>
										{/if}
										<span class="meta-diff-after"
											>{formatPreviewValue(entry.afterText, entry.field)}</span
										>
									</span>
								{/each}
								{#if totalEntries > previewEntries.length}
									<span class="meta">+{totalEntries - previewEntries.length} more</span>
								{/if}
							</div>
						{/if}

						{#if action.kind === 'task_delete' || action.kind === 'task_update'}
							<div class="row-context">
								{#if action.task_sprint}<span class="ctx-tag">{groupLabel}: {action.task_sprint}</span
									>{/if}
								{#if action.task_parent}<span class="ctx-tag ctx-parent"
										>sub-{taskLabel} of: {action.task_parent}</span
									>{/if}
							</div>
						{/if}

						{#if actionResults[idx]?.ok && !actionResults[idx]?.error}
							<span class="result-ok">✓</span>
						{:else if actionResults[idx]?.error}
							<span class="result-err" title={actionResults[idx].error}>✕</span>
						{/if}
					</div>
				{/each}
			</div>
		</div>

		<!-- Show all / Show less + View full details -->
		<div class="expand-row">
			{#if hasMore}
				<button class="expand-btn" on:click={() => (showAll = true)}>
					Show all {actions.length} changes
				</button>
			{:else if showAll && actions.length > PREVIEW_LIMIT}
				<button class="expand-btn" on:click={() => (showAll = false)}>Show less</button>
			{/if}
			<button class="details-btn" on:click={() => (showModal = true)}>Full details</button>
		</div>

		{#if auditEntries.length > 0}
			<div class="audit-summary-card">
				<div class="audit-summary-head">
					<span class="audit-pill">Agent audit</span>
					<span class="audit-summary-text">{auditSummaryText}</span>
				</div>
				<div class="audit-preview-list">
					{#each auditPreviewEntries as entry (`audit-preview-${entry.index}`)}
						{@const auditMeta = formatAuditEntryMeta(entry)}
						<div class="audit-row" class:audit-row-error={auditEntryHasError(entry)}>
							<span class="audit-row-icon">{getAuditEntryIcon(entry)}</span>
							<div class="audit-row-main">
								<div class="audit-row-label">{formatAuditEntryLabel(entry)}</div>
								{#if auditMeta}
									<div class="audit-row-meta">{auditMeta}</div>
								{/if}
							</div>
						</div>
					{/each}
				</div>
				{#if auditEntries.length > auditPreviewEntries.length}
					<button class="expand-btn audit-expand-btn" on:click={() => (showModal = true)}>
						View full audit trail
					</button>
				{/if}
			</div>
		{/if}

		<!-- Action buttons -->
		{#if state === 'pending'}
			{#if !canResolve}
				<div class="resolution-note">Only room admins can accept or dismiss these changes.</div>
			{/if}
			<div class="btn-row">
				<button class="btn btn-apply" disabled={!canResolve} on:click={() => void applyActions()}
					>Apply changes</button
				>
				<button class="btn btn-dismiss" disabled={!canResolve} on:click={reject}>Dismiss</button>
			</div>
		{:else if state === 'applying'}
			<div class="progress-row">
				<span class="progress-label">Applying {applyProgress}/{actions.length}...</span>
				<span class="progress-track">
					<span
						class="progress-fill"
						style="width: {Math.round((applyProgress / actions.length) * 100)}%"
					></span>
				</span>
			</div>
		{:else if state === 'applied'}
			<div class="applied-row">
				<span class="check-icon">✓</span>
				{#if appliedCounts.created > 0}<span class="chip chip-create"
						>{appliedCounts.created} created</span
					>{/if}
				{#if appliedCounts.updated > 0}<span class="chip chip-update"
						>{appliedCounts.updated} edited</span
					>{/if}
				{#if appliedCounts.deleted > 0}<span class="chip chip-delete"
						>{appliedCounts.deleted} deleted</span
					>{/if}
			</div>
			{#if appliedMeta}
				<div class="applied-by">
					Applied by <strong>{appliedMeta.appliedBy}</strong> · {new Date(
						appliedMeta.appliedAt
					).toLocaleString()}
				</div>
			{/if}
		{:else if state === 'error'}
			<div class="error-msg">{errorMsg}</div>
			{#if !canResolve}
				<div class="resolution-note">Only room admins can accept or dismiss these changes.</div>
			{/if}
			<div class="btn-row">
				<button class="btn btn-apply" disabled={!canResolve} on:click={() => void applyActions()}
					>Retry</button
				>
				<button class="btn btn-dismiss" disabled={!canResolve} on:click={reject}>Dismiss</button>
			</div>
		{/if}
	{:else if state === 'rejected'}
		<div class="dismissed-label">Dismissed</div>
		{#if dismissedMeta}
			<div class="applied-by">
				Dismissed by <strong>{dismissedMeta.dismissedBy}</strong> · {new Date(
					dismissedMeta.dismissedAt
				).toLocaleString()}
			</div>
		{/if}
	{/if}
</div>

<!-- ── Full detail modal ──────────────────────────────────── -->
{#if showModal}
	<!-- svelte-ignore a11y-click-events-have-key-events -->
	<!-- svelte-ignore a11y-no-static-element-interactions -->
	<div class="modal-backdrop" on:click|self={() => (showModal = false)}>
		<div class="modal">
			<div class="modal-header">
				<span class="modal-title">Proposed Changes</span>
				<div class="modal-chips">
					{#if createCount > 0}<span class="chip chip-create">{createCount} create</span>{/if}
					{#if updateCount > 0}<span class="chip chip-update">{updateCount} edit</span>{/if}
					{#if deleteCount > 0}<span class="chip chip-delete">{deleteCount} delete</span>{/if}
				</div>
				<button class="modal-close" on:click={() => (showModal = false)}>✕</button>
			</div>

			<div class="modal-body">
				{#if createCount > 0}
					<section>
						<h3 class="section-head head-create">Created ({createCount})</h3>
						{#each actions.filter((a) => a.kind === 'task_create') as a}
							{@const idx = actions.indexOf(a)}
							<div class="detail-item" class:detail-err={actionResults[idx]?.error}>
								<div class="detail-title">{a.title ?? `New ${taskTerm}`}</div>
								<div class="detail-meta">
									{#if a.status}<span class="meta">status: {a.status}</span>{/if}
									{#if a.sprint}<span class="meta">{groupLabel}: {a.sprint}</span>{/if}
									{#if a.task_type}<span class="meta">type: {a.task_type}</span>{/if}
									{#if typeof a.budget === 'number'}<span class="meta meta-budget"
											>${a.budget.toLocaleString()}</span
										>{/if}
									{#if a.start_date}<span class="meta"
											>from: {formatChangeValue(a.start_date, 'start_date')}</span
										>{/if}
									{#if a.due_date}<span class="meta"
											>due: {formatChangeValue(a.due_date, 'due_date')}</span
										>{/if}
								</div>
								{#if a.roles?.length}
									<div class="detail-roles">
										{#each a.roles as r}
											<div class="role-row">
												<span class="role-name">{r.role}</span><span class="role-resp"
													>{r.responsibilities}</span
												>
											</div>
										{/each}
									</div>
								{/if}
								{#if a.description}<p class="detail-desc">{a.description}</p>{/if}
								{#if actionResults[idx]?.error}
									<div class="result-label result-label-err">✕ {actionResults[idx].error}</div>
								{:else if actionResults[idx]?.ok}
									<div class="result-label result-label-ok">✓ Applied</div>
								{/if}
							</div>
						{/each}
					</section>
				{/if}

				{#if updateCount > 0}
					<section>
						<h3 class="section-head head-update">Edited ({updateCount})</h3>
						{#each actions.filter((a) => a.kind === 'task_update') as a}
							{@const idx = actions.indexOf(a)}
							{@const changeEntries = getChangeEntries(a)}
							<div class="detail-item" class:detail-err={actionResults[idx]?.error}>
								<div class="detail-title">{a.task_title ?? '(unknown)'}</div>
								<div class="detail-context">
									{#if a.task_sprint}<span class="ctx-tag">{groupLabel}: {a.task_sprint}</span>{/if}
									{#if a.task_parent}<span class="ctx-tag ctx-parent"
											>sub-{taskLabel} of: {a.task_parent}</span
										>{/if}
								</div>
								{#if changeEntries.length > 0}
									<div class="detail-changes">
										{#each changeEntries as entry}
											<div class="change-field-row">
												<span class="field-name">{entry.label}</span>
												<div class="field-diff">
													{#if entry.hasBefore}
														<span class="field-val field-val-before">{entry.beforeText}</span>
														<span class="field-arrow">→</span>
													{:else}
														<span class="field-pill">Set to</span>
													{/if}
													<span class="field-val field-val-after">{entry.afterText}</span>
												</div>
											</div>
										{/each}
									</div>
								{/if}
								{#if actionResults[idx]?.error}
									<div class="result-label result-label-err">✕ {actionResults[idx].error}</div>
								{:else if actionResults[idx]?.ok}
									<div class="result-label result-label-ok">✓ Applied</div>
								{/if}
							</div>
						{/each}
					</section>
				{/if}

				{#if deleteCount > 0}
					<section>
						<h3 class="section-head head-delete">Deleted ({deleteCount})</h3>
						{#each actions.filter((a) => a.kind === 'task_delete') as a}
							{@const idx = actions.indexOf(a)}
							<div class="detail-item" class:detail-err={actionResults[idx]?.error}>
								<div class="detail-title">{a.task_title ?? '(no title)'}</div>
								<div class="detail-context">
									{#if a.task_sprint}<span class="ctx-tag">{groupLabel}: {a.task_sprint}</span>{/if}
									{#if a.task_parent}<span class="ctx-tag ctx-parent"
											>sub-{taskLabel} of: {a.task_parent}</span
										>{/if}
									{#if !a.task_id}<span class="warn-missing">⚠ missing ID — delete will fail</span
										>{/if}
								</div>
								{#if actionResults[idx]?.skipped}
									<div class="result-label result-label-ok">✓ Already deleted (404)</div>
								{:else if actionResults[idx]?.error}
									<div class="result-label result-label-err">✕ {actionResults[idx].error}</div>
								{:else if actionResults[idx]?.ok}
									<div class="result-label result-label-ok">✓ Deleted</div>
								{/if}
							</div>
						{/each}
					</section>
				{/if}

				{#if auditEntries.length > 0}
					<section>
						<h3 class="section-head head-audit">Agent Audit</h3>
						<div class="audit-detail-list">
							{#each auditEntries as entry (`audit-detail-${entry.index}`)}
								{@const auditMeta = formatAuditEntryMeta(entry)}
								<div class="audit-detail-item" class:audit-detail-error={auditEntryHasError(entry)}>
									<div class="audit-detail-head">
										<span class="audit-detail-index">#{entry.index}</span>
										<span class="audit-row-icon">{getAuditEntryIcon(entry)}</span>
										<span class="audit-detail-label">{formatAuditEntryLabel(entry)}</span>
									</div>
									{#if auditMeta}
										<div class="audit-detail-meta">{auditMeta}</div>
									{/if}
								</div>
							{/each}
						</div>
					</section>
				{/if}
			</div>

			<div class="modal-footer">
				{#if state === 'pending'}
					{#if !canResolve}
						<div class="resolution-note modal-resolution-note">
							Only room admins can accept or dismiss these changes.
						</div>
					{/if}
					<button
						class="btn btn-apply"
						disabled={!canResolve}
						on:click={() => {
							void applyActions();
							showModal = false;
						}}>Apply all</button
					>
					<button
						class="btn btn-dismiss"
						disabled={!canResolve}
						on:click={() => {
							reject();
							showModal = false;
						}}>Dismiss</button
					>
				{:else if state === 'error'}
					<button
						class="btn btn-apply"
						disabled={!canResolve}
						on:click={() => {
							void applyActions();
							showModal = false;
						}}>Retry</button
					>
				{/if}
				<button class="btn btn-dismiss" on:click={() => (showModal = false)}>Close</button>
			</div>
		</div>
	</div>
{/if}

<style>
	/* ── Card shell ─────────────────────────────────────────────── */
	.tora-card {
		display: flex;
		flex-direction: column;
		gap: 8px;
		width: 100%;
	}
	.tora-card.applied {
		opacity: 0.8;
	}
	.tora-card.rejected {
		opacity: 0.45;
	}

	.tora-text {
		margin: 0;
		line-height: 1.5;
		white-space: pre-wrap;
		word-break: break-word;
	}

	/* ── Summary chips ──────────────────────────────────────────── */
	.summary-bar {
		display: flex;
		flex-wrap: wrap;
		gap: 5px;
		align-items: center;
	}
	.total-label {
		font-size: 0.72em;
		opacity: 0.5;
		margin-left: 2px;
	}

	.chip {
		display: inline-flex;
		align-items: center;
		font-size: 0.72em;
		font-weight: 700;
		padding: 2px 8px;
		border-radius: 20px;
		line-height: 1.4;
	}
	.chip-create {
		background: #16a34a28;
		color: #16a34a;
	}
	.chip-update {
		background: #d9770628;
		color: #d97706;
	}
	.chip-delete {
		background: #dc262628;
		color: #dc2626;
	}
	.audit-pill {
		display: inline-flex;
		align-items: center;
		padding: 2px 8px;
		border-radius: 999px;
		background: rgba(99, 102, 241, 0.12);
		color: #818cf8;
		font-size: 0.72em;
		font-weight: 700;
	}

	.audit-summary-card {
		display: flex;
		flex-direction: column;
		gap: 8px;
		padding: 10px 12px;
		border-radius: 10px;
		background: rgba(99, 102, 241, 0.06);
		border: 1px solid rgba(99, 102, 241, 0.16);
	}
	.audit-summary-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
		gap: 8px;
	}
	.audit-summary-text {
		font-size: 0.76em;
		opacity: 0.78;
	}
	.audit-preview-list,
	.audit-detail-list {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}
	.audit-row,
	.audit-detail-item {
		display: flex;
		align-items: flex-start;
		gap: 8px;
		padding: 7px 8px;
		border-radius: 8px;
		background: rgba(15, 23, 42, 0.2);
		border: 1px solid rgba(148, 163, 184, 0.12);
	}
	.audit-row-error,
	.audit-detail-error {
		border-color: rgba(248, 113, 113, 0.3);
		background: rgba(127, 29, 29, 0.16);
	}
	.audit-row-icon {
		flex-shrink: 0;
		width: 18px;
		text-align: center;
	}
	.audit-row-main {
		flex: 1;
		min-width: 0;
	}
	.audit-row-label,
	.audit-detail-label {
		font-size: 0.78em;
		font-weight: 600;
		line-height: 1.35;
	}
	.audit-row-meta,
	.audit-detail-meta {
		font-size: 0.72em;
		margin-top: 2px;
		opacity: 0.74;
		line-height: 1.4;
		word-break: break-word;
	}
	.audit-expand-btn {
		align-self: flex-start;
	}
	.audit-detail-head {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 8px;
	}
	.audit-detail-index {
		font-size: 0.72em;
		color: #94a3b8;
		font-weight: 700;
	}

	/* ── Change list viewport (300px cap) ───────────────────────── */
	.changes-viewport {
		max-height: 300px;
		overflow: hidden;
		position: relative;
	}

	.changes-list {
		display: flex;
		flex-direction: column;
		gap: 5px;
	}

	.change-row {
		display: flex;
		flex-direction: column;
		gap: 3px;
		padding: 7px 10px;
		border-radius: 7px;
		border: 1px solid;
	}
	.change-task_create {
		background: #16a34a0a;
		border-color: #16a34a28;
	}
	.change-task_update {
		background: #d977060a;
		border-color: #d9770628;
	}
	.change-task_delete {
		background: #dc26260a;
		border-color: #dc262628;
	}

	.badge {
		display: inline-block;
		font-size: 0.65em;
		font-weight: 800;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		padding: 1px 6px;
		border-radius: 4px;
		width: fit-content;
	}
	.badge-task_create {
		background: #16a34a28;
		color: #16a34a;
	}
	.badge-task_update {
		background: #d9770628;
		color: #d97706;
	}
	.badge-task_delete {
		background: #dc262628;
		color: #dc2626;
	}

	.row-title {
		font-size: 0.88em;
		font-weight: 500;
		line-height: 1.3;
	}

	.row-chips {
		display: flex;
		flex-wrap: wrap;
		gap: 4px;
		margin-top: 1px;
	}
	.meta {
		font-size: 0.72em;
		padding: 1px 6px;
		border-radius: 20px;
		background: rgba(128, 128, 128, 0.1);
		border: 1px solid rgba(128, 128, 128, 0.18);
		white-space: normal;
		color: inherit;
	}
	.meta-desc {
		font-style: italic;
		white-space: normal;
	}
	.meta-budget {
		color: #16a34a;
		font-weight: 600;
	}
	.meta-roles {
		color: #7c3aed;
	}
	.meta-edit-count {
		background: rgba(217, 119, 6, 0.14);
		border-color: rgba(217, 119, 6, 0.22);
		color: #f59e0b;
	}
	.meta-diff {
		display: inline-flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.22rem;
	}
	.meta-diff-before {
		opacity: 0.72;
		text-decoration: line-through;
	}
	.meta-diff-after {
		font-weight: 700;
	}
	.meta-diff-arrow {
		opacity: 0.7;
	}

	.row-context {
		display: flex;
		flex-wrap: wrap;
		gap: 4px;
		margin-top: 1px;
	}

	.ctx-tag {
		font-size: 0.68em;
		padding: 1px 6px;
		border-radius: 20px;
		background: rgba(128, 128, 128, 0.12);
		border: 1px solid rgba(128, 128, 128, 0.2);
		color: inherit;
		opacity: 0.7;
	}
	.ctx-parent {
		background: rgba(99, 102, 241, 0.1);
		border-color: rgba(99, 102, 241, 0.25);
		color: #818cf8;
		opacity: 1;
	}

	.result-ok {
		font-size: 0.7em;
		color: #16a34a;
	}
	.result-err {
		font-size: 0.7em;
		color: #dc2626;
		cursor: help;
	}

	/* ── Expand / details row ───────────────────────────────────── */
	.expand-row {
		display: flex;
		gap: 8px;
		align-items: center;
		flex-wrap: wrap;
	}

	.expand-btn {
		font-size: 0.76em;
		font-weight: 600;
		padding: 3px 10px;
		border-radius: 5px;
		cursor: pointer;
		border: 1px solid rgba(128, 128, 128, 0.3);
		background: rgba(128, 128, 128, 0.1);
		color: inherit;
		transition: background 0.15s;
	}
	.expand-btn:hover {
		background: rgba(128, 128, 128, 0.2);
	}

	.details-btn {
		font-size: 0.76em;
		font-weight: 600;
		padding: 3px 10px;
		border-radius: 5px;
		cursor: pointer;
		border: 1px solid rgba(99, 102, 241, 0.45);
		background: rgba(99, 102, 241, 0.12);
		color: #818cf8;
		transition: background 0.15s;
		margin-left: auto;
	}
	.details-btn:hover {
		background: rgba(99, 102, 241, 0.22);
		color: #a5b4fc;
	}

	/* ── Action buttons ─────────────────────────────────────────── */
	.btn-row {
		display: flex;
		gap: 8px;
		align-items: center;
		flex-wrap: wrap;
	}

	.btn {
		padding: 5px 16px;
		border-radius: 6px;
		font-size: 0.82em;
		font-weight: 600;
		cursor: pointer;
		border: none;
		transition: opacity 0.15s;
		line-height: 1.5;
	}
	.btn:hover {
		opacity: 0.85;
	}
	.btn:disabled {
		cursor: not-allowed;
		opacity: 0.45;
	}

	.btn-apply {
		background: #2563eb;
		color: #fff;
	}
	.btn-dismiss {
		background: rgba(128, 128, 128, 0.15);
		border: 1px solid rgba(128, 128, 128, 0.25);
		color: inherit;
	}

	/* ── Progress ───────────────────────────────────────────────── */
	.progress-row {
		display: flex;
		align-items: center;
		gap: 8px;
		font-size: 0.8em;
		opacity: 0.8;
	}
	.progress-label {
		white-space: nowrap;
	}
	.progress-track {
		flex: 1;
		max-width: 100px;
		height: 4px;
		background: rgba(128, 128, 128, 0.2);
		border-radius: 2px;
		overflow: hidden;
	}
	.progress-fill {
		display: block;
		height: 100%;
		background: #2563eb;
		border-radius: 2px;
		transition: width 0.2s ease;
	}

	/* ── Roles ───────────────────────────────────────────────────── */
	.detail-roles {
		display: flex;
		flex-direction: column;
		gap: 3px;
		margin: 4px 0;
		padding: 6px 8px;
		border-radius: 6px;
		background: rgba(124, 58, 237, 0.07);
		border-left: 2px solid #7c3aed;
	}
	.role-row {
		display: flex;
		gap: 8px;
		font-size: 0.8em;
		align-items: baseline;
	}
	.role-name {
		font-weight: 600;
		color: #7c3aed;
		white-space: nowrap;
		min-width: 80px;
	}
	.role-resp {
		color: inherit;
		opacity: 0.8;
	}

	/* ── Applied summary ────────────────────────────────────────── */
	.applied-row {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 6px;
		font-size: 0.82em;
		color: #16a34a;
	}
	.check-icon {
		font-weight: 700;
	}
	.applied-by {
		font-size: 0.75em;
		opacity: 0.6;
		margin-top: 2px;
	}

	/* ── Error ──────────────────────────────────────────────────── */
	.error-msg {
		font-size: 0.8em;
		color: #dc2626;
		line-height: 1.4;
	}

	.resolution-note {
		font-size: 0.76em;
		line-height: 1.45;
		color: #94a3b8;
		padding: 8px 10px;
		border-radius: 8px;
		background: rgba(148, 163, 184, 0.1);
		border: 1px solid rgba(148, 163, 184, 0.18);
	}

	.dismissed-label {
		font-size: 0.8em;
		opacity: 0.45;
	}

	/* ── Modal backdrop ─────────────────────────────────────────── */
	.modal-backdrop {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.6);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: 9999;
		padding: 16px;
	}

	/* ── Modal box ──────────────────────────────────────────────── */
	.modal {
		background: #1c1c2e;
		color: #e2e8f0;
		border: 1px solid rgba(255, 255, 255, 0.1);
		border-radius: 12px;
		width: 100%;
		max-width: 560px;
		max-height: 82vh;
		display: flex;
		flex-direction: column;
		overflow: hidden;
		box-shadow: 0 24px 64px rgba(0, 0, 0, 0.5);
	}

	.modal-header {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 14px 16px 12px;
		border-bottom: 1px solid rgba(255, 255, 255, 0.08);
		flex-shrink: 0;
		flex-wrap: wrap;
	}
	.modal-title {
		font-size: 0.92em;
		font-weight: 700;
		color: #f1f5f9;
	}
	.modal-chips {
		display: flex;
		gap: 5px;
		flex-wrap: wrap;
	}
	.modal-close {
		margin-left: auto;
		background: rgba(255, 255, 255, 0.08);
		border: none;
		color: #94a3b8;
		cursor: pointer;
		font-size: 0.8em;
		padding: 3px 8px;
		border-radius: 4px;
	}
	.modal-close:hover {
		background: rgba(255, 255, 255, 0.15);
		color: #f1f5f9;
	}

	.modal-body {
		flex: 1;
		overflow-y: auto;
		padding: 14px 16px;
		display: flex;
		flex-direction: column;
		gap: 18px;
	}

	/* ── Detail sections ────────────────────────────────────────── */
	.section-head {
		font-size: 0.7em;
		font-weight: 800;
		letter-spacing: 0.07em;
		text-transform: uppercase;
		margin: 0 0 6px;
		padding-bottom: 4px;
		border-bottom: 2px solid;
	}
	.head-create {
		color: #4ade80;
		border-color: #16a34a44;
	}
	.head-update {
		color: #fbbf24;
		border-color: #d9770644;
	}
	.head-delete {
		color: #f87171;
		border-color: #dc262644;
	}

	.detail-item {
		padding: 8px 10px;
		border-radius: 8px;
		background: rgba(255, 255, 255, 0.04);
		border: 1px solid rgba(255, 255, 255, 0.08);
		display: flex;
		flex-direction: column;
		gap: 4px;
		margin-bottom: 5px;
	}
	.detail-item.detail-err {
		border-color: rgba(220, 38, 38, 0.35);
		background: rgba(220, 38, 38, 0.06);
	}

	.detail-title {
		font-size: 0.88em;
		font-weight: 600;
		color: #f1f5f9;
		line-height: 1.3;
	}
	.detail-context {
		display: flex;
		flex-wrap: wrap;
		gap: 4px;
		margin-top: 1px;
	}
	.warn-missing {
		font-size: 0.72em;
		color: #f87171;
	}

	.detail-meta {
		display: flex;
		flex-wrap: wrap;
		gap: 4px;
	}

	.detail-desc {
		font-size: 0.78em;
		color: #94a3b8;
		font-style: italic;
		margin: 0;
		line-height: 1.4;
	}

	.detail-changes {
		display: flex;
		flex-direction: column;
		gap: 3px;
		margin-top: 2px;
	}
	.change-field-row {
		display: flex;
		align-items: flex-start;
		gap: 8px;
		font-size: 0.78em;
	}
	.field-name {
		font-weight: 600;
		color: #94a3b8;
		min-width: 72px;
		padding-top: 0.22rem;
	}
	.field-diff {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.38rem;
		flex: 1;
		min-width: 0;
	}
	.field-arrow {
		color: #475569;
	}
	.field-val {
		color: #e2e8f0;
		font-weight: 500;
		padding: 0.2rem 0.5rem;
		border-radius: 0.55rem;
		border: 1px solid rgba(148, 163, 184, 0.16);
		background: rgba(148, 163, 184, 0.08);
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	.field-val-before {
		color: #94a3b8;
		background: rgba(71, 85, 105, 0.18);
		text-decoration: line-through;
	}
	.field-val-after {
		background: rgba(245, 158, 11, 0.12);
		border-color: rgba(245, 158, 11, 0.24);
	}
	.field-pill {
		display: inline-flex;
		align-items: center;
		padding: 0.18rem 0.44rem;
		border-radius: 999px;
		background: rgba(37, 99, 235, 0.12);
		border: 1px solid rgba(37, 99, 235, 0.2);
		color: #93c5fd;
		font-size: 0.74em;
		font-weight: 700;
		letter-spacing: 0.03em;
		text-transform: uppercase;
	}

	.result-label {
		font-size: 0.72em;
		margin-top: 2px;
	}
	.result-label-ok {
		color: #4ade80;
	}
	.result-label-err {
		color: #f87171;
	}
	.head-audit {
		color: #818cf8;
	}

	/* ── Modal footer ───────────────────────────────────────────── */
	.modal-footer {
		display: flex;
		gap: 8px;
		padding: 10px 16px 14px;
		border-top: 1px solid rgba(255, 255, 255, 0.08);
		flex-shrink: 0;
	}

	.modal-resolution-note {
		flex-basis: 100%;
	}
</style>
