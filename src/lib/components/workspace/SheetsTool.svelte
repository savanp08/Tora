<script lang="ts">
	import * as XLSX from 'xlsx';
	import { currentUser } from '$lib/store';
	import { resolveApiBase } from '$lib/config/apiBase';
	import { taskStore, upsertTaskStoreEntry, type Task } from '$lib/stores/tasks';
	import { normalizeRoomIDValue, toStringValue } from '$lib/utils/chat/core';
	import { sendSocketPayload } from '$lib/ws';
	import { buildTaskSocketPayload } from '$lib/ws/client';
	import SpreadsheetGrid from './SpreadsheetGrid.svelte';

	export let canEdit = true;
	export let isAdmin = false;
	export let sessionUserID = '';
	export let sessionUserName = '';
	export let roomId = '';
	import ChangeRequestModal from './ChangeRequestModal.svelte';
	import { type ChangeRequestAction } from '$lib/stores/changeRequests';

	let crModalOpen = false;
	function openImportCR() {
		crModalOpen = true;
	}

	type SheetRecord = {
		task_id: string;
		room_id: string;
		sprint: string;
		task_name: string;
		status: string;
		assignee_id: string;
		budget: number;
		cost: number;
		updated_at: string;
	};

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = resolveApiBase(API_BASE_RAW);
	const STATUS_VALUES = ['todo', 'in_progress', 'done'] as const;

	type SheetsTab = 'spreadsheet' | 'import';
	let activeTab: SheetsTab = 'spreadsheet';

	let importRows: SheetRecord[] = [];
	let importError = '';
	let importSummary = '';
	let selectedFileName = '';
	let importing = false;
	let createMissingTasks = false;

	$: sessionUserID = ($currentUser?.id || '').trim();
	$: sessionUsername = ($currentUser?.username || '').trim();
	$: normalizedWorkspaceRoomID = normalizeRoomIDValue(roomId);
	$: tasks = [...$taskStore];
	$: exportRows = tasks.map((task) => taskToSheetRecord(task));
	$: previewRows = importRows.slice(0, 20);

	function normalizeStatus(value: unknown) {
		const normalized = toStringValue(value).trim().toLowerCase().replace(/\s+/g, '_');
		if (normalized === 'done' || normalized === 'completed') {
			return 'done';
		}
		if (normalized === 'in_progress' || normalized === 'inprogress') {
			return 'in_progress';
		}
		return 'todo';
	}

	function parseNumber(value: unknown) {
		if (typeof value === 'number' && Number.isFinite(value) && value >= 0) {
			return value;
		}
		if (typeof value === 'string') {
			const parsed = Number(value.replace(/[^\d.\-]/g, ''));
			if (Number.isFinite(parsed) && parsed >= 0) {
				return parsed;
			}
		}
		return 0;
	}

	function normalizeColumnKey(key: string) {
		return key.trim().toLowerCase().replace(/[\s_-]+/g, '');
	}

	function readColumnValue(record: Record<string, unknown>, aliases: string[]) {
		const normalizedAliases = new Set(aliases.map((alias) => normalizeColumnKey(alias)));
		for (const [rawKey, rawValue] of Object.entries(record)) {
			if (normalizedAliases.has(normalizeColumnKey(rawKey))) {
				return rawValue;
			}
		}
		return '';
	}

	function toTimestampISO(value: number) {
		if (!Number.isFinite(value) || value <= 0) {
			return '';
		}
		return new Date(value).toISOString();
	}

	function taskToSheetRecord(task: Task): SheetRecord {
		return {
			task_id: task.id,
			room_id: task.roomId,
			sprint: task.sprintName || '',
			task_name: task.title || '',
			status: normalizeStatus(task.status),
			assignee_id: task.assigneeId || '',
			budget: parseNumber(task.budget),
			cost: parseNumber(task.spent),
			updated_at: toTimestampISO(task.updatedAt)
		};
	}

	function parseImportedRow(row: Record<string, unknown>): SheetRecord {
		return {
			task_id: toStringValue(readColumnValue(row, ['task_id', 'task id', 'id'])).trim(),
			room_id: toStringValue(readColumnValue(row, ['room_id', 'room id', 'room'])).trim(),
			sprint: toStringValue(readColumnValue(row, ['sprint', 'sprint_name', 'sprint name'])).trim(),
			task_name: toStringValue(readColumnValue(row, ['task_name', 'task name', 'task', 'title'])).trim(),
			status: normalizeStatus(readColumnValue(row, ['status'])),
			assignee_id: toStringValue(
				readColumnValue(row, ['assignee_id', 'assignee id', 'assignee', 'owner_id', 'owner'])
			).trim(),
			budget: parseNumber(readColumnValue(row, ['budget', 'task_budget', 'task budget'])),
			cost: parseNumber(readColumnValue(row, ['cost', 'spent', 'actual_cost', 'actual cost'])),
			updated_at: toStringValue(
				readColumnValue(row, ['updated_at', 'updated at', 'updated'])
			).trim()
		};
	}

	function withSessionUserHeaders(headers: Record<string, string> = {}) {
		if (!sessionUserID) {
			if (!sessionUsername) {
				return headers;
			}
			return {
				...headers,
				'X-User-Name': sessionUsername
			};
		}
		return {
			...headers,
			'X-User-Id': sessionUserID,
			'X-User-Name': sessionUsername
		};
	}

	async function parseResponseError(response: Response) {
		const payload = (await response.json().catch(() => null)) as
			| {
					error?: string;
					message?: string;
			  }
			| null;
		return payload?.error?.trim() || payload?.message?.trim() || `HTTP ${response.status}`;
	}

	function clearImportPreview() {
		importRows = [];
		selectedFileName = '';
		importError = '';
		importSummary = '';
	}

	function exportAsXlsx() {
		if (typeof window === 'undefined' || exportRows.length === 0) {
			return;
		}
		const worksheet = XLSX.utils.json_to_sheet(exportRows);
		const workbook = XLSX.utils.book_new();
		XLSX.utils.book_append_sheet(workbook, worksheet, 'Tasks');
		XLSX.writeFile(workbook, `taskboard-${Date.now()}.xlsx`);
	}

	async function onImportFileChange(event: Event) {
		const input = event.currentTarget as HTMLInputElement;
		const file = input.files?.[0] ?? null;
		if (!file) {
			return;
		}

		importError = '';
		importSummary = '';
		selectedFileName = file.name;

		try {
			const buffer = await file.arrayBuffer();
			const workbook = XLSX.read(buffer, { type: 'array' });
			const firstSheetName = workbook.SheetNames[0] ?? '';
			if (!firstSheetName) {
				throw new Error('Workbook is empty.');
			}
			const sheet = workbook.Sheets[firstSheetName];
			if (!sheet) {
				throw new Error('Unable to read the first worksheet.');
			}
			const parsedRows = XLSX.utils.sheet_to_json<Record<string, unknown>>(sheet, { defval: '' });
			const normalizedRows = parsedRows
				.map((row) => parseImportedRow(row))
				.filter((row) => row.task_id || row.task_name);
			if (normalizedRows.length === 0) {
				throw new Error(
					'No usable rows found. Include columns like Task ID, Task Name, Sprint, Status, Assignee ID, Budget, and Cost.'
				);
			}
			importRows = normalizedRows;
		} catch (error) {
			importRows = [];
			importError = error instanceof Error ? error.message : 'Failed to parse spreadsheet file.';
		}

		input.value = '';
	}

	function getFallbackImportRoomID() {
		const fromWorkspace = normalizeRoomIDValue(normalizedWorkspaceRoomID);
		if (fromWorkspace) {
			return fromWorkspace;
		}
		return normalizeRoomIDValue(tasks[0]?.roomId || '');
	}

	function getImportRoomID(row: SheetRecord, task?: Task) {
		const fromRow = normalizeRoomIDValue(row.room_id);
		if (fromRow) {
			return fromRow;
		}
		const fromTask = normalizeRoomIDValue(task?.roomId || '');
		if (fromTask) {
			return fromTask;
		}
		return getFallbackImportRoomID();
	}

	function nearlyEqual(left: number, right: number) {
		return Math.abs(left - right) < 0.00001;
	}

	async function applyImportUpdates() {
		if (!canEdit || importing || importRows.length === 0) {
			return;
		}

		importing = true;
		importError = '';
		importSummary = '';

		const taskByID = new Map(tasks.map((task) => [task.id, task] as const));
		let createdCount = 0;
		let updatedCount = 0;
		let skippedCount = 0;
		let failedCount = 0;

		for (const row of importRows) {
			const importedTaskID = row.task_id.trim();
			let latestTask = importedTaskID ? taskByID.get(importedTaskID) ?? null : null;
			const roomID = getImportRoomID(row, latestTask ?? undefined);
			if (!roomID) {
				failedCount += 1;
				if (!importError) {
					importError = 'Room id is missing for one or more rows. Include Room ID column or import from an active room.';
				}
				continue;
			}

			let createdThisRow = false;
			let didUpdate = false;

			try {
				if (!latestTask) {
					if (!createMissingTasks) {
						skippedCount += 1;
						continue;
					}

					const createTitle = row.task_name.trim();
					if (!createTitle) {
						skippedCount += 1;
						continue;
					}

					const createResponse = await fetch(
						`${API_BASE}/api/rooms/${encodeURIComponent(roomID)}/tasks`,
						{
							method: 'POST',
							headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
							credentials: 'include',
							body: JSON.stringify({
								title: createTitle,
								status: normalizeStatus(row.status),
								sprint_name: row.sprint.trim(),
								budget: parseNumber(row.budget),
								actual_cost: parseNumber(row.cost)
							})
						}
					);
					if (!createResponse.ok) {
						throw new Error(await parseResponseError(createResponse));
					}

					const createdPayload = await createResponse.json().catch(() => null);
					const createdTask = upsertTaskStoreEntry(createdPayload, roomID);
					if (!createdTask) {
						throw new Error('Invalid task response while creating spreadsheet row task.');
					}
					latestTask = createdTask;
					taskByID.set(createdTask.id, createdTask);
					if (importedTaskID) {
						taskByID.set(importedTaskID, createdTask);
					}
					sendSocketPayload(buildTaskSocketPayload('task_create', roomID, createdTask));
					createdCount += 1;
					createdThisRow = true;
				}
				if (!latestTask) {
					skippedCount += 1;
					continue;
				}

				const body: Record<string, unknown> = {};
				const nextTitle = row.task_name.trim();
				const nextSprint = row.sprint.trim();
				const nextAssigneeID = row.assignee_id.trim();
				const nextBudget = parseNumber(row.budget);
				const nextCost = parseNumber(row.cost);

				if (nextTitle && nextTitle !== latestTask.title) {
					body.title = nextTitle;
				}
				if (nextSprint !== (latestTask.sprintName || '').trim()) {
					body.sprint_name = nextSprint;
				}
				if (nextAssigneeID !== (latestTask.assigneeId || '').trim()) {
					body.assignee_id = nextAssigneeID;
				}
				if (!nearlyEqual(nextBudget, parseNumber(latestTask.budget))) {
					body.budget = nextBudget;
				}
				if (!nearlyEqual(nextCost, parseNumber(latestTask.spent))) {
					body.actual_cost = nextCost;
				}

				if (Object.keys(body).length > 0) {
					const response = await fetch(
						`${API_BASE}/api/rooms/${encodeURIComponent(roomID)}/tasks/${encodeURIComponent(latestTask.id)}`,
						{
							method: 'PUT',
							headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
							credentials: 'include',
							body: JSON.stringify(body)
						}
					);
					if (!response.ok) {
						throw new Error(await parseResponseError(response));
					}
					const payload = await response.json().catch(() => null);
					const updatedTask = upsertTaskStoreEntry(payload, roomID);
					if (!updatedTask) {
						throw new Error('Invalid task response while applying spreadsheet updates.');
					}
					latestTask = updatedTask;
					taskByID.set(updatedTask.id, updatedTask);
					if (importedTaskID) {
						taskByID.set(importedTaskID, updatedTask);
					}
					sendSocketPayload(buildTaskSocketPayload('task_update', roomID, updatedTask));
					didUpdate = true;
				}

				const nextStatus = normalizeStatus(row.status);
				if (nextStatus !== normalizeStatus(latestTask.status)) {
					const statusResponse = await fetch(
						`${API_BASE}/api/rooms/${encodeURIComponent(roomID)}/tasks/${encodeURIComponent(latestTask.id)}/status`,
						{
							method: 'PUT',
							headers: withSessionUserHeaders({ 'Content-Type': 'application/json' }),
							credentials: 'include',
							body: JSON.stringify({ status: nextStatus })
						}
					);
					if (!statusResponse.ok) {
						throw new Error(await parseResponseError(statusResponse));
					}
					const statusPayload =
						(await statusResponse.json().catch(() => null)) as Record<string, unknown> | null;
					const nowISO = new Date().toISOString();
					const nextTaskSource = {
						...latestTask,
						status: nextStatus,
						updated_at: statusPayload?.updated_at ?? nowISO,
						status_changed_at: statusPayload?.status_changed_at ?? statusPayload?.updated_at ?? nowISO,
						status_actor_id: toStringValue(
							statusPayload?.status_actor_id ?? latestTask.statusActorId
						),
						status_actor_name: toStringValue(
							statusPayload?.status_actor_name ?? latestTask.statusActorName
						),
						room_id: roomID
					};
					const updatedTaskWithStatus = upsertTaskStoreEntry(nextTaskSource, roomID);
					if (!updatedTaskWithStatus) {
						throw new Error('Invalid status update response while applying spreadsheet updates.');
					}
					latestTask = updatedTaskWithStatus;
					taskByID.set(updatedTaskWithStatus.id, updatedTaskWithStatus);
					if (importedTaskID) {
						taskByID.set(importedTaskID, updatedTaskWithStatus);
					}
					sendSocketPayload(buildTaskSocketPayload('task_update', roomID, updatedTaskWithStatus));
					didUpdate = true;
				}

				if (didUpdate) {
					updatedCount += 1;
				} else if (!createdThisRow) {
					skippedCount += 1;
				}
			} catch (error) {
				failedCount += 1;
				importError = error instanceof Error ? error.message : 'Failed while applying spreadsheet rows.';
			}
		}

		importSummary = `Created ${createdCount} task${createdCount === 1 ? '' : 's'}, updated ${updatedCount}, skipped ${skippedCount}, failed ${failedCount}.`;
		importing = false;
	}
</script>

<section class="sheet-tool" aria-label="Spreadsheet tools">
	<div class="sheets-tab-bar" role="tablist">
		<button
			type="button"
			role="tab"
			class="stab-btn"
			class:is-active={activeTab === 'spreadsheet'}
			on:click={() => (activeTab = 'spreadsheet')}
		>
			<svg viewBox="0 0 24 24" aria-hidden="true">
				<path d="M3 3h18v18H3zM3 9h18M3 15h18M9 3v18M15 3v18" />
			</svg>
			Spreadsheet
		</button>
		<button
			type="button"
			role="tab"
			class="stab-btn"
			class:is-active={activeTab === 'import'}
			on:click={() => (activeTab = 'import')}
		>
			<svg viewBox="0 0 24 24" aria-hidden="true">
				<path d="M4 16v1a3 3 0 0 0 3 3h10a3 3 0 0 0 3-3v-1M16 12l-4 4m0 0l-4-4m4 4V4" />
			</svg>
			Import / Export
		</button>
	</div>

	{#if activeTab === 'spreadsheet'}
		<div class="spreadsheet-wrap">
			<SpreadsheetGrid />
		</div>
	{:else}
		<header class="sheet-header">
			<div>
				<h3>Import / Export</h3>
				<p>Import/export taskboard data as `.xlsx`.</p>
			</div>
			<div class="sheet-actions">
				<button type="button" class="sheet-btn" on:click={exportAsXlsx} disabled={exportRows.length === 0}>
					Export XLSX
				</button>
				<label class="sheet-btn sheet-upload-btn" aria-label="Import XLSX">
					Import XLSX
					<input
						type="file"
						accept=".xlsx,.xls,.csv"
						on:change={(event) => void onImportFileChange(event)}
						disabled={importing}
					/>
				</label>
			</div>
		</header>

		<p class="sheet-note">
			Use `Task ID` to update existing tasks. Enable create mode below to add rows whose Task ID is
			missing/not found. `Budget` and `Cost` must be numeric.
		</p>

		{#if selectedFileName}
			<p class="sheet-meta">Loaded file: {selectedFileName}</p>
		{/if}
		{#if importError}
			<p class="sheet-error" role="status">{importError}</p>
		{/if}
		{#if importSummary}
			<p class="sheet-summary" role="status">{importSummary}</p>
		{/if}

		{#if importRows.length > 0}
			<label class="sheet-checkbox">
				<input type="checkbox" bind:checked={createMissingTasks} disabled={importing || !canEdit} />
				<span>Create missing tasks when Task ID is blank/not found</span>
			</label>

			<div class="sheet-import-actions">
				<button
					type="button"
					class="sheet-btn sheet-apply-btn"
					on:click={() => void applyImportUpdates()}
					disabled={!canEdit || importing}
				>
					{importing ? 'Applying…' : 'Apply To Taskboard'}
				</button>
				<button type="button" class="sheet-btn" on:click={clearImportPreview} disabled={importing}>
					Clear File
				</button>
				<span>Previewing {previewRows.length} of {importRows.length} row(s)</span>
			</div>

			<div class="sheet-preview-wrap">
				<table>
					<thead>
						<tr>
							<th>Task ID</th>
							<th>Task</th>
							<th>Sprint</th>
							<th>Status</th>
							<th>Assignee ID</th>
							<th>Budget</th>
							<th>Cost</th>
						</tr>
					</thead>
					<tbody>
						{#each previewRows as row, rowIndex (`${row.task_id}-${rowIndex}`)}
							<tr>
								<td>{row.task_id || '—'}</td>
								<td>{row.task_name || '—'}</td>
								<td>{row.sprint || '—'}</td>
								<td>{row.status || 'todo'}</td>
								<td>{row.assignee_id || '—'}</td>
								<td>{row.budget}</td>
								<td>{row.cost}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	{/if}
</section>

<ChangeRequestModal
	open={crModalOpen}
	{roomId}
	userId={sessionUserID}
	userName={sessionUserName}
	action="import_sheet"
	targetLabel="Import spreadsheet data"
	payload={{}}
	on:submitted={() => (crModalOpen = false)}
	on:cancel={() => (crModalOpen = false)}
/>

<style>
	.sheet-tool {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: auto auto auto auto auto minmax(0, 1fr);
		gap: 0.62rem;
	}

	.sheets-tab-bar {
		display: flex;
		gap: 0.25rem;
		border-bottom: 1px solid color-mix(in srgb, var(--ws-border) 80%, transparent);
		padding-bottom: 0.5rem;
	}

	.stab-btn {
		display: inline-flex;
		align-items: center;
		gap: 0.38rem;
		padding: 0.32rem 0.7rem;
		border-radius: 7px;
		border: 1px solid transparent;
		background: transparent;
		color: var(--ws-muted);
		font-size: 0.72rem;
		font-weight: 600;
		cursor: pointer;
		transition: background 0.13s, color 0.13s, border-color 0.13s;
	}

	.stab-btn svg {
		width: 13px;
		height: 13px;
		stroke: currentColor;
		fill: none;
		stroke-width: 1.8;
		stroke-linecap: round;
		stroke-linejoin: round;
		flex-shrink: 0;
	}

	.stab-btn:hover {
		background: color-mix(in srgb, var(--ws-surface) 80%, var(--ws-border) 20%);
		color: var(--ws-text);
	}

	.stab-btn.is-active {
		background: color-mix(in srgb, var(--ws-surface) 60%, var(--ws-border) 40%);
		color: var(--ws-text);
		border-color: color-mix(in srgb, var(--ws-border) 90%, transparent);
	}

	.spreadsheet-wrap {
		min-height: 0;
		height: 100%;
		overflow: hidden;
		display: flex;
		flex-direction: column;
	}

	.sheet-header {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 0.7rem;
	}

	.sheet-header h3 {
		margin: 0;
		font-size: 0.96rem;
	}

	.sheet-header p {
		margin: 0.2rem 0 0;
		font-size: 0.72rem;
		color: var(--ws-muted);
	}

	.sheet-actions {
		display: inline-flex;
		gap: 0.45rem;
		flex-wrap: wrap;
	}

	.sheet-btn {
		height: 1.92rem;
		padding: 0 0.64rem;
		border-radius: 8px;
		border: 1px solid color-mix(in srgb, var(--ws-border) 88%, transparent);
		background: var(--ws-surface);
		color: var(--ws-text);
		font-size: 0.7rem;
		font-weight: 600;
		cursor: pointer;
		display: inline-flex;
		align-items: center;
		justify-content: center;
	}

	.sheet-btn:disabled {
		opacity: 0.58;
		cursor: not-allowed;
	}

	.sheet-upload-btn {
		position: relative;
		overflow: hidden;
	}

	.sheet-upload-btn input[type='file'] {
		position: absolute;
		inset: 0;
		opacity: 0;
		cursor: pointer;
	}

	.sheet-note,
	.sheet-meta,
	.sheet-summary,
	.sheet-error {
		margin: 0;
		font-size: 0.72rem;
	}

	.sheet-note,
	.sheet-meta {
		color: var(--ws-muted);
	}

	.sheet-summary {
		color: color-mix(in srgb, var(--ws-text) 84%, #10b981 16%);
	}

	.sheet-checkbox {
		display: inline-flex;
		align-items: center;
		gap: 0.45rem;
		font-size: 0.72rem;
		color: var(--ws-text);
	}

	.sheet-checkbox input[type='checkbox'] {
		width: 14px;
		height: 14px;
		cursor: pointer;
	}

	.sheet-checkbox span {
		color: var(--ws-muted);
	}

	.sheet-error {
		padding: 0.42rem 0.5rem;
		border-radius: 8px;
		border: 1px solid color-mix(in srgb, var(--ws-danger) 40%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-danger-soft) 60%, transparent);
		color: var(--ws-danger);
	}

	.sheet-import-actions {
		display: inline-flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.42rem;
	}

	.sheet-import-actions span {
		font-size: 0.68rem;
		color: var(--ws-muted);
	}

	.sheet-apply-btn {
		background: color-mix(in srgb, var(--ws-surface) 76%, #ffffff 24%);
	}

	.sheet-request-btn {
		color: #d97706;
		border-color: color-mix(in srgb, #f59e0b 45%, transparent);
		background: color-mix(in srgb, #f59e0b 10%, transparent);
	}
	.sheet-request-btn:hover {
		background: color-mix(in srgb, #f59e0b 20%, transparent);
	}

	.sheet-no-perm {
		font-size: 0.68rem;
		color: var(--ws-muted);
		font-style: italic;
	}

	.sheet-preview-wrap {
		min-height: 0;
		overflow: auto;
		border: 1px solid color-mix(in srgb, var(--ws-border) 86%, transparent);
		border-radius: 10px;
	}

	table {
		width: 100%;
		border-collapse: collapse;
		min-width: 860px;
	}

	th,
	td {
		padding: 0.48rem 0.54rem;
		font-size: 0.68rem;
		text-align: left;
		border-bottom: 1px solid color-mix(in srgb, var(--ws-border) 84%, transparent);
		white-space: nowrap;
	}

	thead th {
		position: sticky;
		top: 0;
		background: color-mix(in srgb, var(--ws-surface) 97%, transparent);
		z-index: 1;
		font-weight: 700;
	}

	tbody tr:last-child td {
		border-bottom: none;
	}

	@media (max-width: 900px) {
		.sheet-tool {
			grid-template-rows: auto auto auto auto minmax(0, 1fr);
		}

		.sheet-header {
			flex-direction: column;
		}
	}
</style>
