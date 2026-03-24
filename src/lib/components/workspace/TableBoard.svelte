<script lang="ts">
	import FieldSchemaEditor from '$lib/components/workspace/FieldSchemaEditor.svelte';
	import { currentUser } from '$lib/store';
	import { fieldSchemaStore, type FieldSchema } from '$lib/stores/fieldSchema';
	import {
		getActiveTaskRoomId,
		taskStore,
		upsertTaskStoreEntry,
		type Task
	} from '$lib/stores/tasks';
	import type { OnlineMember } from '$lib/types/chat';
	import { normalizeRoomIDValue, toStringValue } from '$lib/utils/chat/core';
	import { sendSocketPayload } from '$lib/ws';
	import { buildTaskSocketPayload } from '$lib/ws/client';

	export let onlineMembers: OnlineMember[] = [];

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';
	const INACTIVITY_MS = 3000;

	const STATUS_LABELS: Record<string, string> = {
		todo: 'To Do',
		in_progress: 'Working on it',
		done: 'Done'
	};

	type TaskStatusValue = 'todo' | 'in_progress' | 'done';
	type MetadataEntry = { key: string; label: string; value: string };
	type TableRow = {
		id: string;
		roomId: string;
		title: string;
		status: TaskStatusValue;
		type: string;
		budget: number;
		duration: string;
		effort: number | '';
		description: string;
		assigneeId: string;
		customFields: Record<string, unknown>;
		[key: string]: unknown;
	};

	let tableRows: TableRow[] = [];
	let tableError = '';
	let savingEdits = 0;
	let fieldSchemaEditorOpen = false;

	// Popup edit state
	let editingRowId: string | null = null;
	let editingRow: TableRow | null = null;
	let rowEditValues: Record<string, string> = {};
	let inactivityTimer: ReturnType<typeof setTimeout> | null = null;
	// Popup position (viewport coords)
	let popupTop = 0;
	let popupLeft = 0;
	let popupWidth = 0;

	$: sessionUserID = ($currentUser?.id || '').trim();
	$: sessionUsername = ($currentUser?.username || '').trim();
	$: tableRows = $taskStore.map((task) => mapTaskToRow(task));
	$: taskRows = tableRows.filter((r) => r.type !== 'support');
	$: supportRows = tableRows.filter((r) => r.type === 'support');
	$: activeRoomID = normalizeRoomIDValue(getActiveTaskRoomId() || tableRows[0]?.roomId || '');

	// keep editingRow in sync with store updates
	$: if (editingRowId) {
		const updated = tableRows.find((r) => r.id === editingRowId);
		if (updated) editingRow = updated;
	}

	// ── helpers ─────────────────────────────────────────────────────────────

	function normalizeTaskStatus(value: unknown): TaskStatusValue {
		const n = toStringValue(value).trim().toLowerCase().replace(/\s+/g, '_');
		if (n === 'done' || n === 'completed') return 'done';
		if (n === 'in_progress') return 'in_progress';
		return 'todo';
	}

	function normalizeFieldSchemaType(value: unknown) {
		return toStringValue(value).trim().toLowerCase();
	}

	function customFieldColumnKey(fieldID: string) {
		return `custom_field__${fieldID.trim()}`;
	}

	function resolveCustomFieldIDFromColumn(field: string) {
		const n = field.trim();
		if (!n.startsWith('custom_field__')) return '';
		return n.replace(/^custom_field__/, '').trim();
	}

	function getFieldSchemaByID(fieldID: string) {
		const n = fieldID.trim();
		if (!n) return null;
		return $fieldSchemaStore.find((s) => s.fieldId === n) ?? null;
	}

	function formatCustomFieldValue(schema: FieldSchema, value: unknown): string {
		const ft = normalizeFieldSchemaType(schema.fieldType);
		if (value == null || value === '') return '—';
		if (ft === 'checkbox') return value === true ? '✓' : '—';
		if (ft === 'multi_select') {
			if (Array.isArray(value))
				return value.map((e) => toStringValue(e).trim()).filter(Boolean).join(', ') || '—';
			return toStringValue(value).trim() || '—';
		}
		if (ft === 'date') {
			const d = new Date(toStringValue(value));
			if (!Number.isNaN(d.getTime())) return d.toLocaleDateString();
		}
		return toStringValue(value).trim() || '—';
	}

	function parseDescriptionMetadata(description: string): { base: string; entries: MetadataEntry[] } {
		const trimmed = description.trim();
		if (!trimmed) return { base: '', entries: [] };
		const m = trimmed.match(/\[([^\]]+)\]\s*$/);
		if (!m) return { base: trimmed, entries: [] };
		const base = trimmed.slice(0, m.index).trim();
		const body = (m[1] ?? '').trim();
		if (!body || !body.includes(':')) return { base: trimmed, entries: [] };
		const entries: MetadataEntry[] = [];
		for (const section of body.split('|')) {
			const raw = section.trim();
			if (!raw) continue;
			const [rawLabel, ...rest] = raw.split(':');
			const label = rawLabel.trim();
			const value = rest.join(':').trim();
			if (!label || !value) continue;
			entries.push({ key: label.toLowerCase(), label, value });
		}
		return { base, entries };
	}

	function readMetadataValue(entries: MetadataEntry[], key: string): string {
		return entries.find((e) => e.key === key.trim().toLowerCase())?.value.trim() || '';
	}

	function upsertDescriptionMetadataValue(
		description: string,
		key: string,
		label: string,
		nextValue: string
	) {
		const nk = key.trim().toLowerCase();
		const nl = label.trim() || key.trim();
		const next = nextValue.trim();
		const meta = parseDescriptionMetadata(description);
		const filtered = meta.entries.filter((e) => e.key !== nk);
		if (next) filtered.push({ key: nk, label: nl, value: next });
		if (filtered.length === 0) return meta.base;
		const block = `[${filtered.map((e) => `${e.label}: ${e.value}`).join(' | ')}]`;
		return meta.base ? `${meta.base}\n\n${block}` : block;
	}

	function parseNumericInput(value: unknown): number | null {
		if (typeof value === 'number') return Number.isFinite(value) && value >= 0 ? value : null;
		const t = toStringValue(value).trim();
		if (!t) return null;
		const p = Number(t.replace(/,/g, ''));
		return Number.isFinite(p) && p >= 0 ? p : null;
	}

	function normalizeCustomFieldEditorValue(schema: FieldSchema, value: unknown): unknown {
		const ft = normalizeFieldSchemaType(schema.fieldType);
		if (ft === 'checkbox') {
			if (typeof value === 'boolean') return value;
			const n = toStringValue(value).trim().toLowerCase();
			return n === 'true' || n === '1' || n === 'yes';
		}
		if (ft === 'number') {
			const p = parseNumericInput(value);
			if (p === null && toStringValue(value).trim()) throw new Error(`${schema.name} must be a number.`);
			return p;
		}
		if (ft === 'multi_select') {
			const allowed = new Set((schema.options ?? []).map((o) => o.toLowerCase()));
			const src = Array.isArray(value)
				? value
				: toStringValue(value).split(',').map((e) => e.trim()).filter(Boolean);
			const sel = [...new Set(src.map((e) => toStringValue(e).trim()).filter(Boolean))];
			if (allowed.size > 0) {
				for (const o of sel)
					if (!allowed.has(o.toLowerCase()))
						throw new Error(`"${o}" is not a valid option for ${schema.name}.`);
			}
			return sel.length > 0 ? sel : null;
		}
		const n = toStringValue(value).trim();
		if (!n) return null;
		if (ft === 'select' && Array.isArray(schema.options) && schema.options.length > 0) {
			if (!schema.options.map((o) => o.toLowerCase()).includes(n.toLowerCase()))
				throw new Error(`"${n}" is not a valid option for ${schema.name}.`);
		}
		if (ft === 'url') {
			try {
				const u = new URL(n);
				if (!u.protocol.startsWith('http')) throw new Error('bad protocol');
			} catch { throw new Error(`${schema.name} must be a valid URL.`); }
		}
		if (ft === 'date') {
			if (Number.isNaN(new Date(n).getTime())) throw new Error(`${schema.name} must be a valid date.`);
		}
		return n;
	}

	function formatBudget(value: unknown): string {
		const p = parseNumericInput(value);
		if (p === null) return '$0';
		return p.toLocaleString(undefined, { style: 'currency', currency: 'USD', maximumFractionDigits: 2 });
	}

	function resolveAssigneeName(id: string): string {
		const t = id.trim();
		if (!t) return '—';
		const m = onlineMembers.find((m) => m.id.trim() === t);
		if (m) return m.name;
		return t.length > 8 ? t.slice(0, 8) + '…' : t;
	}

	function withSessionUserHeaders(headers: Record<string, string> = {}) {
		if (!sessionUserID) return sessionUsername ? { ...headers, 'X-User-Name': sessionUsername } : headers;
		return { ...headers, 'X-User-Id': sessionUserID, 'X-User-Name': sessionUsername };
	}

	async function parseResponseError(response: Response) {
		const p = (await response.json().catch(() => null)) as { error?: string; message?: string } | null;
		return p?.error?.trim() || p?.message?.trim() || `HTTP ${response.status}`;
	}

	function mapTaskToRow(task: Task): TableRow {
		const meta = parseDescriptionMetadata(task.description || '');
		const customFields = { ...(task.customFields ?? {}) };
		// Prefer the explicit taskType field; fall back to description metadata for legacy tasks
		const resolvedType = task.taskType?.trim() || readMetadataValue(meta.entries, 'type') || 'sprint';
		const row: TableRow = {
			id: task.id,
			roomId: task.roomId,
			title: task.title,
			status: normalizeTaskStatus(task.status),
			type: resolvedType,
			budget: Number.isFinite(task.budget) ? Number(task.budget) : 0,
			duration: readMetadataValue(meta.entries, 'duration'),
			effort: parseNumericInput(readMetadataValue(meta.entries, 'effort')) ?? '',
			description: task.description || '',
			assigneeId: task.assigneeId || '',
			customFields
		};
		for (const schema of $fieldSchemaStore) {
			row[customFieldColumnKey(schema.fieldId)] = customFields[schema.fieldId];
		}
		return row;
	}

	function buildTaskUpdateBody(row: TableRow, field: string, nextValue: unknown): Record<string, unknown> | null {
		if (field === 'title') {
			const t = toStringValue(nextValue).trim();
			if (!t) throw new Error('Title cannot be empty.');
			return { title: t };
		}
		if (field === 'status') return { status: normalizeTaskStatus(nextValue) };
		if (field === 'budget') {
			const b = parseNumericInput(nextValue);
			if (b === null && toStringValue(nextValue).trim()) throw new Error('Budget must be a non-negative number.');
			return { budget: b ?? 0 };
		}
		if (field === 'effort') {
			const e = toStringValue(nextValue).trim();
			if (e && parseNumericInput(e) === null) throw new Error('Effort must be a non-negative number.');
			const nd = upsertDescriptionMetadataValue(row.description, 'effort', 'Effort', e);
			return nd === row.description ? null : { description: nd };
		}
		if (field === 'duration') {
			const nd = upsertDescriptionMetadataValue(row.description, 'duration', 'Duration', toStringValue(nextValue).trim());
			return nd === row.description ? null : { description: nd };
		}
		if (field === 'type') {
			const nd = upsertDescriptionMetadataValue(row.description, 'type', 'Type', toStringValue(nextValue).trim());
			return nd === row.description ? null : { description: nd };
		}
		if (field === 'assigneeId') {
			const id = toStringValue(nextValue).trim();
			return { assignee_id: id || null };
		}
		const cfID = resolveCustomFieldIDFromColumn(field);
		if (cfID) {
			const schema = getFieldSchemaByID(cfID);
			if (!schema) throw new Error('Custom field definition is missing. Refresh and try again.');
			return { custom_fields: { [cfID]: normalizeCustomFieldEditorValue(schema, nextValue) } };
		}
		return null;
	}

	async function persistTaskUpdate(row: TableRow, field: string, nextValue: unknown) {
		const roomID = normalizeRoomIDValue(row.roomId);
		if (!roomID || !row.id) throw new Error('Task context is missing room information.');
		const body = buildTaskUpdateBody(row, field, nextValue);
		if (!body) return;
		savingEdits += 1;
		try {
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(roomID)}/tasks/${encodeURIComponent(row.id)}`,
				{ method: 'PUT', headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }), credentials: 'include', body: JSON.stringify(body) }
			);
			if (!response.ok) throw new Error(await parseResponseError(response));
			const payload = await response.json().catch(() => null);
			const updated = upsertTaskStoreEntry(payload, roomID);
			if (!updated) throw new Error('Received an invalid task update response.');
			sendSocketPayload(buildTaskSocketPayload('task_update', roomID, updated));
			tableError = '';
		} finally {
			savingEdits = Math.max(0, savingEdits - 1);
		}
	}

	// ── popup edit ───────────────────────────────────────────────────────────

	function openPopup(row: TableRow, e: MouseEvent) {
		if (editingRowId === row.id) return;
		if (editingRowId) void savePopupEdit();

		const rowEl = (e.currentTarget as HTMLElement).closest('tr') ?? (e.currentTarget as HTMLElement);
		const rect = rowEl.getBoundingClientRect();

		editingRowId = row.id;
		editingRow = row;

		// Position popup below the row, clamped to viewport
		const popupH = 420;
		const gap = 6;
		let top = rect.bottom + gap;
		if (top + popupH > window.innerHeight - 12) {
			top = Math.max(12, rect.top - popupH - gap);
		}
		popupTop = top;
		popupLeft = Math.max(8, Math.min(rect.left, window.innerWidth - 440));
		popupWidth = Math.min(rect.width, window.innerWidth - 16);

		rowEditValues = {
			title: String(row.title ?? ''),
			status: String(row.status ?? 'todo'),
			type: String(row.type ?? ''),
			budget: String(row.budget ?? ''),
			duration: String(row.duration ?? ''),
			effort: String(row.effort ?? ''),
			assigneeId: String(row.assigneeId ?? '')
		};
		for (const schema of $fieldSchemaStore) {
			const key = customFieldColumnKey(schema.fieldId);
			rowEditValues[key] = String(row[key] ?? '');
		}
		resetInactivityTimer();
	}

	function resetInactivityTimer() {
		if (inactivityTimer) clearTimeout(inactivityTimer);
		inactivityTimer = setTimeout(() => void savePopupEdit(), INACTIVITY_MS);
	}

	async function savePopupEdit() {
		if (!editingRowId) return;
		const row = editingRow ?? tableRows.find((r) => r.id === editingRowId);
		editingRowId = null;
		editingRow = null;
		if (inactivityTimer) { clearTimeout(inactivityTimer); inactivityTimer = null; }
		if (!row) { rowEditValues = {}; return; }

		const fields = ['title', 'status', 'type', 'budget', 'duration', 'effort', 'assigneeId',
			...$fieldSchemaStore.map((s) => customFieldColumnKey(s.fieldId))];
		for (const field of fields) {
			const next = (rowEditValues[field] ?? '').trim();
			const prev = String(row[field] ?? '').trim();
			if (next === prev) continue;
			try { await persistTaskUpdate(row, field, next); }
			catch (e) { tableError = e instanceof Error ? e.message : 'Failed to update'; }
		}
		rowEditValues = {};
	}

	function closePopup() {
		if (inactivityTimer) { clearTimeout(inactivityTimer); inactivityTimer = null; }
		editingRowId = null;
		editingRow = null;
		rowEditValues = {};
	}

	function handlePopupKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') closePopup();
		else if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) void savePopupEdit();
		else resetInactivityTimer();
	}

	function handleBackdropClick(e: MouseEvent) {
		if ((e.target as HTMLElement).classList.contains('popup-backdrop')) {
			void savePopupEdit();
		}
	}

	function cfInputType(schema: FieldSchema): string {
		const ft = normalizeFieldSchemaType(schema.fieldType);
		if (ft === 'number') return 'number';
		if (ft === 'date') return 'date';
		if (ft === 'url') return 'url';
		return 'text';
	}
</script>

<section class="table-board" aria-label="Task table board">
	<header class="table-header">
		<div>
			<h2>Task Grid</h2>
			<p>{taskRows.length} task{taskRows.length === 1 ? '' : 's'}{supportRows.length > 0 ? ` · ${supportRows.length} support ticket${supportRows.length === 1 ? '' : 's'}` : ''} · click any row to edit</p>
		</div>
		<div class="table-header-actions">
			<button
				type="button"
				class="table-settings-btn"
				on:click={() => (fieldSchemaEditorOpen = true)}
				disabled={!activeRoomID}
				title="Manage custom fields"
			>
				<svg viewBox="0 0 24 24" aria-hidden="true">
					<path d="M9.8 8.2 8.4 5.9l1.4-1.4 2.3 1.4a5.7 5.7 0 0 1 1.8 0l2.3-1.4 1.4 1.4-1.4 2.3c.2.6.3 1.2.3 1.8s-.1 1.2-.3 1.8l1.4 2.3-1.4 1.4-2.3-1.4a5.7 5.7 0 0 1-1.8 0l-2.3 1.4-1.4-1.4 1.4-2.3a5.7 5.7 0 0 1 0-3.6ZM12 14.2a2.2 2.2 0 1 0 0-4.4 2.2 2.2 0 0 0 0 4.4Z"/>
				</svg>
				<span>Fields</span>
			</button>
			{#if savingEdits > 0}
				<span class="table-status">Saving…</span>
			{/if}
		</div>
	</header>

	{#if tableError}
		<p class="table-error" role="status">{tableError}</p>
	{/if}

	<div class="table-surface">
		<table class="grid-table">
			<thead>
				<tr>
					<th class="col-title">Title</th>
					<th class="col-status">Status</th>
					<th class="col-type">Type</th>
					<th class="col-budget">Budget</th>
					<th class="col-dur">Duration</th>
					<th class="col-effort">Effort</th>
					<th class="col-assignee">Assignee</th>
					{#each $fieldSchemaStore as schema (schema.fieldId)}
						<th>{schema.name}</th>
					{/each}
				</tr>
			</thead>
			<tbody>
				{#if taskRows.length === 0}
					<tr><td class="empty-cell" colspan={7 + $fieldSchemaStore.length}>No tasks available yet.</td></tr>
				{/if}
				{#each taskRows as row (row.id)}
					<tr
						class="grid-row"
						class:row-active={editingRowId === row.id}
						on:click={(e) => openPopup(row, e)}
					>
						<td class="col-title"><span class="cell-text">{row.title}</span></td>
						<td class="col-status">
							<span class="status-badge status-{row.status}">{STATUS_LABELS[row.status] ?? row.status}</span>
						</td>
						<td class="col-type"><span class="cell-text">{row.type || '—'}</span></td>
						<td class="col-budget"><span class="cell-text">{formatBudget(row.budget)}</span></td>
						<td class="col-dur"><span class="cell-text">{row.duration || '—'}</span></td>
						<td class="col-effort"><span class="cell-text">{row.effort !== '' ? row.effort : '—'}</span></td>
						<td class="col-assignee"><span class="cell-text">{resolveAssigneeName(row.assigneeId)}</span></td>
						{#each $fieldSchemaStore as schema (schema.fieldId)}
							<td><span class="cell-text">{formatCustomFieldValue(schema, row[customFieldColumnKey(schema.fieldId)])}</span></td>
						{/each}
					</tr>
				{/each}
			</tbody>
		</table>
	</div>

	{#if supportRows.length > 0}
		<div class="support-section">
			<header class="support-section-header">
				<span class="support-section-icon">🎫</span>
				<h3>Support Tickets</h3>
				<span class="support-section-count">{supportRows.length}</span>
			</header>
			<div class="table-surface">
				<table class="grid-table">
					<thead>
						<tr>
							<th class="col-title">Title</th>
							<th class="col-status">Status</th>
							<th class="col-budget">Budget</th>
							<th class="col-dur">Duration</th>
							<th class="col-effort">Effort</th>
							<th class="col-assignee">Assignee</th>
						</tr>
					</thead>
					<tbody>
						{#each supportRows as row (row.id)}
							<tr
								class="grid-row support-row"
								class:row-active={editingRowId === row.id}
								on:click={(e) => openPopup(row, e)}
							>
								<td class="col-title"><span class="cell-text">{row.title}</span></td>
								<td class="col-status">
									<span class="status-badge status-{row.status}">{STATUS_LABELS[row.status] ?? row.status}</span>
								</td>
								<td class="col-budget"><span class="cell-text">{formatBudget(row.budget)}</span></td>
								<td class="col-dur"><span class="cell-text">{row.duration || '—'}</span></td>
								<td class="col-effort"><span class="cell-text">{row.effort !== '' ? row.effort : '—'}</span></td>
								<td class="col-assignee"><span class="cell-text">{resolveAssigneeName(row.assigneeId)}</span></td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		</div>
	{/if}

	<!-- Edit popup -->
	{#if editingRowId && editingRow}
		<div class="popup-backdrop" on:click={handleBackdropClick}>
			<div
				class="edit-popup"
				role="dialog"
				aria-label="Edit task"
				style="top:{popupTop}px; left:{popupLeft}px; min-width:{Math.max(popupWidth, 380)}px;"
				on:keydown={handlePopupKeydown}
			>
				<div class="popup-header">
					<span class="popup-title">Edit Task</span>
					<div class="popup-header-actions">
						<button type="button" class="popup-save-btn" on:click={() => void savePopupEdit()}>Save</button>
						<button type="button" class="popup-close-btn" on:click={closePopup} aria-label="Close">✕</button>
					</div>
				</div>
				<div class="popup-hint">Auto-saves after {INACTIVITY_MS / 1000}s of inactivity · Esc to discard · ⌘Enter to save now</div>

				<div class="popup-fields">
					<!-- Title -->
					<div class="popup-field">
						<label class="popup-label" for="pf-title">Title</label>
						<input id="pf-title" class="popup-input" type="text"
							bind:value={rowEditValues.title}
							on:input={resetInactivityTimer} />
					</div>

					<!-- Status -->
					<div class="popup-field">
						<label class="popup-label" for="pf-status">Status</label>
						<select id="pf-status" class="popup-select" bind:value={rowEditValues.status} on:change={resetInactivityTimer}>
							{#each Object.entries(STATUS_LABELS) as [k, lbl] (k)}
								<option value={k}>{lbl}</option>
							{/each}
						</select>
					</div>

					<!-- Type -->
					<div class="popup-field">
						<label class="popup-label" for="pf-type">Type</label>
						<input id="pf-type" class="popup-input" type="text"
							bind:value={rowEditValues.type}
							on:input={resetInactivityTimer} />
					</div>

					<!-- Budget -->
					<div class="popup-field">
						<label class="popup-label" for="pf-budget">Budget ($)</label>
						<input id="pf-budget" class="popup-input" type="number" min="0"
							bind:value={rowEditValues.budget}
							on:input={resetInactivityTimer} />
					</div>

					<!-- Duration -->
					<div class="popup-field">
						<label class="popup-label" for="pf-duration">Duration</label>
						<input id="pf-duration" class="popup-input" type="text"
							bind:value={rowEditValues.duration}
							on:input={resetInactivityTimer} />
					</div>

					<!-- Effort -->
					<div class="popup-field">
						<label class="popup-label" for="pf-effort">Effort (hrs)</label>
						<input id="pf-effort" class="popup-input" type="number" min="0"
							bind:value={rowEditValues.effort}
							on:input={resetInactivityTimer} />
					</div>

					<!-- Assignee -->
					<div class="popup-field">
						<label class="popup-label" for="pf-assignee">Assignee</label>
						<select id="pf-assignee" class="popup-select" bind:value={rowEditValues.assigneeId} on:change={resetInactivityTimer}>
							<option value="">— unassigned —</option>
							{#each onlineMembers as m (m.id)}
								<option value={m.id}>{m.name}</option>
							{/each}
						</select>
					</div>

					<!-- Custom fields -->
					{#each $fieldSchemaStore as schema (schema.fieldId)}
						{@const cfKey = customFieldColumnKey(schema.fieldId)}
						{@const ft = normalizeFieldSchemaType(schema.fieldType)}
						<div class="popup-field">
							<label class="popup-label" for="pf-{schema.fieldId}">{schema.name}</label>
							{#if ft === 'select'}
								<select id="pf-{schema.fieldId}" class="popup-select"
									bind:value={rowEditValues[cfKey]} on:change={resetInactivityTimer}>
									<option value="">—</option>
									{#each schema.options ?? [] as opt (opt)}
										<option value={opt}>{opt}</option>
									{/each}
								</select>
							{:else if ft === 'checkbox'}
								<input id="pf-{schema.fieldId}" type="checkbox"
									class="popup-checkbox"
									checked={rowEditValues[cfKey] === 'true'}
									on:change={(e) => { rowEditValues[cfKey] = String(e.currentTarget.checked); resetInactivityTimer(); }} />
							{:else}
								<input id="pf-{schema.fieldId}" class="popup-input"
									type={cfInputType(schema)}
									bind:value={rowEditValues[cfKey]}
									on:input={resetInactivityTimer} />
							{/if}
						</div>
					{/each}
				</div>
			</div>
		</div>
	{/if}

	{#if fieldSchemaEditorOpen}
		<div class="field-schema-modal-backdrop" role="dialog" aria-modal="true">
			<div class="field-schema-modal">
				<FieldSchemaEditor roomId={activeRoomID} on:close={() => { fieldSchemaEditorOpen = false; }} />
			</div>
		</div>
	{/if}
</section>

<style>
	:global(:root) {
		--tb-bg: #f6f9ff;
		--tb-surface: #ffffff;
		--tb-border: #d3ddef;
		--tb-text: #14233f;
		--tb-muted: #64748b;
		--tb-danger: #b91c1c;
		--tb-row-hover: #f0f4fb;
		--tb-row-active: #e8f0fe;
		--tb-popup-bg: #ffffff;
		--tb-popup-border: #c5d1e8;
	}

	:global(:root[data-theme='dark']),
	:global(.theme-dark) {
		--tb-bg: #161617;
		--tb-surface: #1f2023;
		--tb-border: #32343a;
		--tb-text: #edf2ff;
		--tb-muted: #a7b0c2;
		--tb-danger: #fca5a5;
		--tb-row-hover: #25262b;
		--tb-row-active: #1a2640;
		--tb-popup-bg: #23252b;
		--tb-popup-border: #3a3d47;
	}

	.table-board {
		height: 100%;
		min-height: 0;
		width: 100%;
		padding: 0.95rem;
		display: grid;
		grid-template-rows: auto auto minmax(0, 1fr);
		gap: 0.75rem;
		background: var(--tb-bg);
		color: var(--tb-text);
		position: relative;
		box-sizing: border-box;
	}

	.table-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.85rem;
	}

	.table-header h2 { margin: 0; font-size: 1rem; font-weight: 700; }
	.table-header p { margin: 0.2rem 0 0; font-size: 0.82rem; color: var(--tb-muted); }

	.table-header-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.56rem;
	}

	.table-settings-btn {
		display: inline-flex;
		align-items: center;
		gap: 0.36rem;
		padding: 0.35rem 0.62rem;
		border-radius: 10px;
		border: 1px solid var(--tb-border);
		background: color-mix(in srgb, var(--tb-surface) 88%, var(--tb-bg));
		color: var(--tb-text);
		font-size: 0.75rem;
		font-weight: 600;
		cursor: pointer;
	}

	.table-settings-btn svg {
		width: 0.82rem; height: 0.82rem;
		stroke: currentColor; stroke-width: 1.8;
		fill: none; stroke-linecap: round; stroke-linejoin: round;
	}

	.table-settings-btn:disabled { opacity: 0.5; cursor: not-allowed; }

	.table-status {
		font-size: 0.78rem;
		padding: 0.32rem 0.6rem;
		border-radius: 999px;
		background: color-mix(in srgb, var(--tb-text) 14%, transparent);
		color: var(--tb-text);
	}

	.table-error { margin: 0; font-size: 0.82rem; color: var(--tb-danger); }

	/* ── table surface ── */
	.table-surface {
		min-height: 0;
		border: 1px solid var(--tb-border);
		border-radius: 14px;
		overflow: auto;
		background: var(--tb-surface);
	}

	.grid-table {
		width: 100%;
		border-collapse: collapse;
		font-size: 0.88rem;
	}

	.grid-table thead {
		position: sticky;
		top: 0;
		z-index: 2;
	}

	.grid-table th {
		padding: 0.55rem 0.75rem;
		text-align: left;
		font-size: 0.78rem;
		font-weight: 700;
		color: var(--tb-muted);
		background: color-mix(in srgb, var(--tb-surface) 95%, var(--tb-bg));
		border-bottom: 1px solid var(--tb-border);
		white-space: nowrap;
		letter-spacing: 0.02em;
		text-transform: uppercase;
	}

	.grid-table td {
		padding: 0;
		border-bottom: 1px solid color-mix(in srgb, var(--tb-border) 55%, transparent);
		vertical-align: middle;
		max-width: 260px;
	}

	/* column widths */
	.col-title { min-width: 220px; }
	.col-status { width: 150px; }
	.col-type { width: 130px; }
	.col-budget { width: 120px; }
	.col-dur { width: 120px; }
	.col-effort { width: 100px; }
	.col-assignee { width: 150px; }

	/* ── row states ── */
	.grid-row { cursor: pointer; transition: background 0.1s; }
	.grid-row:hover { background: var(--tb-row-hover); }
	.grid-row.row-active { background: var(--tb-row-active); outline: 2px solid #4f8ef755; outline-offset: -2px; }

	/* ── cell display ── */
	.cell-text {
		display: block;
		padding: 0.5rem 0.75rem;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.empty-cell {
		padding: 2rem;
		text-align: center;
		color: var(--tb-muted);
		font-size: 0.88rem;
	}

	/* ── status badge ── */
	.status-badge {
		display: inline-block;
		padding: 0.22rem 0.55rem;
		margin: 0.38rem 0.75rem;
		border-radius: 999px;
		font-size: 0.75rem;
		font-weight: 600;
		white-space: nowrap;
	}

	.status-todo { background: color-mix(in srgb, #94a3b8 18%, transparent); color: #475569; }
	.status-in_progress { background: color-mix(in srgb, #3b82f6 18%, transparent); color: #2563eb; }
	.status-done { background: color-mix(in srgb, #22c55e 18%, transparent); color: #16a34a; }

	/* ── popup backdrop ── */
	.popup-backdrop {
		position: fixed;
		inset: 0;
		z-index: 100;
	}

	/* ── edit popup card ── */
	.edit-popup {
		position: fixed;
		z-index: 101;
		max-width: calc(100vw - 16px);
		max-height: 70vh;
		display: flex;
		flex-direction: column;
		background: var(--tb-popup-bg);
		border: 1px solid var(--tb-popup-border);
		border-radius: 14px;
		box-shadow: 0 12px 48px rgba(0, 0, 0, 0.28), 0 2px 8px rgba(0, 0, 0, 0.12);
		overflow: hidden;
	}

	.popup-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 0.7rem 0.9rem 0.5rem;
		border-bottom: 1px solid var(--tb-popup-border);
		flex-shrink: 0;
	}

	.popup-title {
		font-size: 0.9rem;
		font-weight: 700;
		color: var(--tb-text);
	}

	.popup-header-actions {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}

	.popup-save-btn {
		padding: 0.28rem 0.72rem;
		border-radius: 8px;
		border: none;
		background: #4f8ef7;
		color: #fff;
		font-size: 0.78rem;
		font-weight: 600;
		cursor: pointer;
	}
	.popup-save-btn:hover { background: #3a7de0; }

	.popup-close-btn {
		padding: 0.28rem 0.52rem;
		border-radius: 8px;
		border: 1px solid var(--tb-popup-border);
		background: transparent;
		color: var(--tb-muted);
		font-size: 0.78rem;
		cursor: pointer;
		line-height: 1;
	}
	.popup-close-btn:hover { color: var(--tb-text); }

	.popup-hint {
		padding: 0.28rem 0.9rem;
		font-size: 0.72rem;
		color: var(--tb-muted);
		background: color-mix(in srgb, var(--tb-popup-bg) 60%, var(--tb-bg));
		border-bottom: 1px solid var(--tb-popup-border);
		flex-shrink: 0;
	}

	.popup-fields {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 0.6rem 1rem;
		padding: 0.75rem 0.9rem;
		overflow-y: auto;
	}

	.popup-field {
		display: flex;
		flex-direction: column;
		gap: 0.26rem;
	}

	/* Title spans full width */
	.popup-field:first-child {
		grid-column: 1 / -1;
	}

	.popup-label {
		font-size: 0.72rem;
		font-weight: 600;
		color: var(--tb-muted);
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.popup-input,
	.popup-select {
		padding: 0.42rem 0.6rem;
		border: 1px solid var(--tb-popup-border);
		border-radius: 8px;
		background: color-mix(in srgb, var(--tb-popup-bg) 70%, var(--tb-bg));
		color: var(--tb-text);
		font-size: 0.86rem;
		font-family: inherit;
		outline: none;
		width: 100%;
		box-sizing: border-box;
		transition: border-color 0.12s;
	}

	.popup-input:focus,
	.popup-select:focus {
		border-color: #4f8ef7;
		background: color-mix(in srgb, #4f8ef7 6%, var(--tb-popup-bg));
	}

	.popup-checkbox { width: 1.1rem; height: 1.1rem; cursor: pointer; margin-top: 0.3rem; }

	/* ── field-schema modal ── */
	.field-schema-modal-backdrop {
		position: absolute;
		inset: 0;
		background: rgba(8, 8, 9, 0.6);
		display: grid;
		place-items: center;
		padding: 1rem;
		z-index: 200;
	}

	.field-schema-modal {
		width: min(640px, 100%);
		height: min(86vh, 720px);
		border-radius: 16px;
		border: 1px solid var(--tb-border);
		overflow: hidden;
		background: var(--tb-surface);
		box-shadow: 0 30px 80px rgba(0, 0, 0, 0.45);
	}

	@media (max-width: 760px) {
		.table-board {
			padding: 0.68rem;
			gap: 0.6rem;
			height: auto;
			min-height: 100%;
			grid-template-rows: auto auto minmax(400px, 1fr);
		}

		.table-header {
			align-items: flex-start;
			flex-direction: column;
		}

		.table-surface { min-height: 400px; }

		.popup-fields { grid-template-columns: 1fr; }
		.popup-field:first-child { grid-column: 1; }
	}

	/* ── Support tickets section ──────────────────────────────── */
	.support-section {
		margin-top: 28px;
		display: flex;
		flex-direction: column;
		gap: 0;
	}

	.support-section-header {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 10px 16px 8px;
		border-bottom: 2px solid rgba(99,102,241,0.3);
		margin-bottom: 0;
	}

	.support-section-icon {
		font-size: 1.1em;
	}

	.support-section-header h3 {
		margin: 0;
		font-size: 0.9em;
		font-weight: 700;
		letter-spacing: 0.03em;
		color: #818cf8;
	}

	.support-section-count {
		font-size: 0.72em;
		font-weight: 700;
		padding: 1px 7px;
		border-radius: 20px;
		background: rgba(99,102,241,0.15);
		color: #818cf8;
		border: 1px solid rgba(99,102,241,0.3);
	}

	.support-row {
		background: rgba(99,102,241,0.03);
	}
	.support-row:hover {
		background: rgba(99,102,241,0.07);
	}
</style>
