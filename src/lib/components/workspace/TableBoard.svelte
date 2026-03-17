<script lang="ts">
	import { browser } from '$app/environment';
	import { currentUser } from '$lib/store';
	import { taskStore, upsertTaskStoreEntry, type Task } from '$lib/stores/tasks';
	import { normalizeRoomIDValue, toStringValue } from '$lib/utils/chat/core';
	import { sendSocketPayload } from '$lib/ws';
	import { buildTaskSocketPayload } from '$lib/ws/client';
	import 'tabulator-tables/dist/css/tabulator_midnight.min.css';

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = API_BASE_RAW?.trim() ? API_BASE_RAW.trim() : 'http://127.0.0.1:8080';

	const STATUS_LABELS = {
		todo: 'To Do',
		in_progress: 'Working on it',
		done: 'Done'
	} as const;

	type TaskStatusValue = keyof typeof STATUS_LABELS;
	type EditableField = 'title' | 'status' | 'type' | 'budget' | 'duration' | 'effort';
	type MetadataEntry = {
		key: string;
		label: string;
		value: string;
	};
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
	};
	type TabulatorRow = {
		getData: () => TableRow;
	};
	type TabulatorCell = {
		getField: () => string;
		getValue: () => unknown;
		getOldValue?: () => unknown;
		getRow: () => TabulatorRow;
		restoreOldValue?: () => void;
	};
	type TabulatorInstance = {
		updateData: (data: TableRow[]) => Promise<unknown>;
		addData?: (data: TableRow[], addToTop?: boolean) => Promise<unknown>;
		deleteRow?: (id: string) => Promise<unknown> | void;
		destroy: () => void;
		on: (event: string, callback: (cell: TabulatorCell) => void) => void;
	};
	type TabulatorConstructor = new (
		element: HTMLElement,
		options: Record<string, unknown>
	) => TabulatorInstance;

	let TabulatorCtor: TabulatorConstructor | null = null;
	let tableRows: TableRow[] = [];
	let tableError = '';
	let savingEdits = 0;

	$: sessionUserID = ($currentUser?.id || '').trim();
	$: sessionUsername = ($currentUser?.username || '').trim();
	$: tableRows = $taskStore.map((task) => mapTaskToRow(task));

	function normalizeTaskStatus(value: unknown): TaskStatusValue {
		const normalized = toStringValue(value).trim().toLowerCase().replace(/\s+/g, '_');
		if (normalized === 'done' || normalized === 'completed') {
			return 'done';
		}
		if (normalized === 'in_progress') {
			return 'in_progress';
		}
		return 'todo';
	}

	function parseDescriptionMetadata(description: string): {
		base: string;
		entries: MetadataEntry[];
	} {
		const trimmed = description.trim();
		if (!trimmed) {
			return { base: '', entries: [] };
		}
		const metadataMatch = trimmed.match(/\[([^\]]+)\]\s*$/);
		if (!metadataMatch) {
			return { base: trimmed, entries: [] };
		}
		const base = trimmed.slice(0, metadataMatch.index).trim();
		const metadataBody = (metadataMatch[1] ?? '').trim();
		if (!metadataBody || !metadataBody.includes(':')) {
			return { base: trimmed, entries: [] };
		}
		const entries: MetadataEntry[] = [];
		for (const section of metadataBody.split('|')) {
			const raw = section.trim();
			if (!raw) {
				continue;
			}
			const [rawLabel, ...rawValueParts] = raw.split(':');
			const label = rawLabel.trim();
			const value = rawValueParts.join(':').trim();
			if (!label || !value) {
				continue;
			}
			entries.push({
				key: label.toLowerCase(),
				label,
				value
			});
		}
		return { base, entries };
	}

	function readMetadataValue(entries: MetadataEntry[], key: string): string {
		const normalizedKey = key.trim().toLowerCase();
		if (!normalizedKey) {
			return '';
		}
		const found = entries.find((entry) => entry.key === normalizedKey);
		return found?.value.trim() || '';
	}

	function upsertDescriptionMetadataValue(
		description: string,
		key: string,
		label: string,
		nextValue: string
	) {
		const normalizedKey = key.trim().toLowerCase();
		const normalizedLabel = label.trim() || key.trim();
		const next = nextValue.trim();
		const metadata = parseDescriptionMetadata(description);
		const filtered = metadata.entries.filter((entry) => entry.key !== normalizedKey);
		if (next) {
			filtered.push({
				key: normalizedKey,
				label: normalizedLabel,
				value: next
			});
		}
		if (filtered.length === 0) {
			return metadata.base;
		}
		const metadataBlock = `[${filtered.map((entry) => `${entry.label}: ${entry.value}`).join(' | ')}]`;
		if (!metadata.base) {
			return metadataBlock;
		}
		return `${metadata.base}\n\n${metadataBlock}`;
	}

	function parseNumericInput(value: unknown): number | null {
		if (typeof value === 'number') {
			if (Number.isFinite(value) && value >= 0) {
				return value;
			}
			return null;
		}
		const trimmed = toStringValue(value).trim();
		if (!trimmed) {
			return null;
		}
		const parsed = Number(trimmed.replace(/,/g, ''));
		if (!Number.isFinite(parsed) || parsed < 0) {
			return null;
		}
		return parsed;
	}

	function normalizeComparable(field: EditableField, value: unknown): string {
		if (field === 'status') {
			return normalizeTaskStatus(value);
		}
		if (field === 'budget' || field === 'effort') {
			const numeric = parseNumericInput(value);
			return numeric === null ? '' : String(numeric);
		}
		return toStringValue(value).trim();
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

	function mapTaskToRow(task: Task): TableRow {
		const metadata = parseDescriptionMetadata(task.description || '');
		const typeValue = readMetadataValue(metadata.entries, 'type') || 'general';
		const durationValue = readMetadataValue(metadata.entries, 'duration');
		const effortValue = parseNumericInput(readMetadataValue(metadata.entries, 'effort'));

		return {
			id: task.id,
			roomId: task.roomId,
			title: task.title,
			status: normalizeTaskStatus(task.status),
			type: typeValue,
			budget: Number.isFinite(task.budget) ? Number(task.budget) : 0,
			duration: durationValue,
			effort: effortValue ?? '',
			description: task.description || ''
		};
	}

	function buildTaskUpdateBody(
		row: TableRow,
		field: EditableField,
		nextValue: unknown
	): Record<string, unknown> | null {
		if (field === 'title') {
			const title = toStringValue(nextValue).trim();
			if (!title) {
				throw new Error('Title cannot be empty.');
			}
			return { title };
		}

		if (field === 'status') {
			return { status: normalizeTaskStatus(nextValue) };
		}

		if (field === 'budget') {
			const budget = parseNumericInput(nextValue);
			if (budget === null && toStringValue(nextValue).trim()) {
				throw new Error('Budget must be a non-negative number.');
			}
			return { budget: budget ?? 0 };
		}

		if (field === 'effort') {
			const effort = toStringValue(nextValue).trim();
			if (effort && parseNumericInput(effort) === null) {
				throw new Error('Effort must be a non-negative number.');
			}
			const nextDescription = upsertDescriptionMetadataValue(
				row.description,
				'effort',
				'Effort',
				effort
			);
			if (nextDescription === row.description) {
				return null;
			}
			return { description: nextDescription };
		}

		if (field === 'duration') {
			const duration = toStringValue(nextValue).trim();
			const nextDescription = upsertDescriptionMetadataValue(
				row.description,
				'duration',
				'Duration',
				duration
			);
			if (nextDescription === row.description) {
				return null;
			}
			return { description: nextDescription };
		}

		if (field === 'type') {
			const typeValue = toStringValue(nextValue).trim();
			const nextDescription = upsertDescriptionMetadataValue(
				row.description,
				'type',
				'Type',
				typeValue
			);
			if (nextDescription === row.description) {
				return null;
			}
			return { description: nextDescription };
		}

		return null;
	}

	async function persistTaskUpdate(row: TableRow, field: EditableField, nextValue: unknown) {
		const normalizedRoomID = normalizeRoomIDValue(row.roomId);
		if (!normalizedRoomID || !row.id) {
			throw new Error('Task context is missing room information.');
		}

		const body = buildTaskUpdateBody(row, field, nextValue);
		if (!body) {
			return;
		}

		savingEdits += 1;
		try {
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomID)}/tasks/${encodeURIComponent(row.id)}`,
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
			const updatedTask = upsertTaskStoreEntry(payload, normalizedRoomID);
			if (!updatedTask) {
				throw new Error('Received an invalid task update response.');
			}
			sendSocketPayload(buildTaskSocketPayload('task_update', normalizedRoomID, updatedTask));
			tableError = '';
		} finally {
			savingEdits = Math.max(0, savingEdits - 1);
		}
	}

	async function handleCellEdited(cell: TabulatorCell) {
		const field = cell.getField() as EditableField;
		if (!['title', 'status', 'type', 'budget', 'duration', 'effort'].includes(field)) {
			return;
		}

		const row = cell.getRow().getData();
		const nextValue = cell.getValue();
		const previousValue = typeof cell.getOldValue === 'function' ? cell.getOldValue() : undefined;
		if (normalizeComparable(field, nextValue) === normalizeComparable(field, previousValue)) {
			return;
		}

		try {
			await persistTaskUpdate(row, field, nextValue);
		} catch (error) {
			tableError = error instanceof Error ? error.message : 'Failed to update task';
			cell.restoreOldValue?.();
		}
	}

	async function ensureTabulatorCtor() {
		if (TabulatorCtor) {
			return TabulatorCtor;
		}
		const module = await import('tabulator-tables');
		const ctorCandidate = (module.TabulatorFull ?? module.Tabulator) as TabulatorConstructor;
		if (!ctorCandidate) {
			throw new Error('Failed to load tabulator-tables.');
		}
		TabulatorCtor = ctorCandidate;
		return TabulatorCtor;
	}

	function formatBudget(value: unknown) {
		const parsed = parseNumericInput(value);
		if (parsed === null) {
			return '$0';
		}
		return parsed.toLocaleString(undefined, {
			style: 'currency',
			currency: 'USD',
			maximumFractionDigits: 2
		});
	}

	function createRowsByID(rows: TableRow[]) {
		return new Map(rows.map((row) => [row.id, row]));
	}

	function rowChanged(previous: TableRow, next: TableRow) {
		return (
			previous.title !== next.title ||
			previous.status !== next.status ||
			previous.type !== next.type ||
			previous.budget !== next.budget ||
			previous.duration !== next.duration ||
			previous.effort !== next.effort ||
			previous.description !== next.description ||
			previous.roomId !== next.roomId
		);
	}

	function tabulator(node: HTMLDivElement, data: TableRow[]) {
		let table: TabulatorInstance | null = null;
		let disposed = false;
		let queuedData = data;
		let rowsByID = createRowsByID(data);
		let pendingPatch = Promise.resolve();

		async function mount() {
			if (!browser) {
				return;
			}
			const Tabulator = await ensureTabulatorCtor();
			if (disposed) {
				return;
			}
			table = new Tabulator(node, {
				data: queuedData,
				reactiveData: true,
				index: 'id',
				height: '100%',
				layout: 'fitColumns',
				placeholder: 'No tasks available yet.',
				columns: [
					{ title: 'Title', field: 'title', editor: 'input', minWidth: 260 },
					{
						title: 'Status',
						field: 'status',
						editor: 'list',
						editorParams: {
							values: STATUS_LABELS,
							clearable: false,
							autocomplete: true,
							listOnEmpty: true
						},
						formatter: (cell: TabulatorCell) => STATUS_LABELS[normalizeTaskStatus(cell.getValue())],
						width: 160
					},
					{ title: 'Type', field: 'type', editor: 'input', width: 170 },
					{
						title: 'Budget',
						field: 'budget',
						editor: 'number',
						editorParams: { min: 0, step: 1 },
						formatter: (cell: TabulatorCell) => formatBudget(cell.getValue()),
						width: 160
					},
					{ title: 'Duration', field: 'duration', editor: 'input', width: 170 },
					{
						title: 'Effort',
						field: 'effort',
						editor: 'number',
						editorParams: { min: 0, step: 1 },
						width: 130
					}
				]
			});
			table.on('cellEdited', (cell: TabulatorCell) => {
				void handleCellEdited(cell);
			});
		}

		async function applyTablePatches(nextData: TableRow[]) {
			if (!table) {
				rowsByID = createRowsByID(nextData);
				return;
			}

			const nextRowsByID = createRowsByID(nextData);
			const rowsToUpdate: TableRow[] = [];
			const rowsToAdd: TableRow[] = [];
			const rowsToDelete: string[] = [];

			for (const nextRow of nextData) {
				const previousRow = rowsByID.get(nextRow.id);
				if (!previousRow) {
					rowsToAdd.push(nextRow);
					continue;
				}
				if (rowChanged(previousRow, nextRow)) {
					rowsToUpdate.push(nextRow);
				}
			}

			for (const existingID of rowsByID.keys()) {
				if (!nextRowsByID.has(existingID)) {
					rowsToDelete.push(existingID);
				}
			}

			if (rowsToUpdate.length > 0) {
				await table.updateData(rowsToUpdate);
			}
			if (rowsToAdd.length > 0) {
				if (typeof table.addData === 'function') {
					await table.addData(rowsToAdd, false);
				} else {
					await table.updateData(rowsToAdd);
				}
			}
			if (rowsToDelete.length > 0 && typeof table.deleteRow === 'function') {
				for (const rowID of rowsToDelete) {
					await table.deleteRow(rowID);
				}
			}

			rowsByID = nextRowsByID;
		}

		void mount();

		return {
			update(nextData: TableRow[]) {
				queuedData = nextData;
				pendingPatch = pendingPatch
					.then(() => applyTablePatches(nextData))
					.catch(() => applyTablePatches(nextData));
			},
			destroy() {
				disposed = true;
				table?.destroy();
				table = null;
			}
		};
	}
</script>

<section class="table-board" aria-label="Task table board">
	<header class="table-header">
		<div>
			<h2>Task Grid</h2>
			<p>{tableRows.length} task{tableRows.length === 1 ? '' : 's'} synced in real time</p>
		</div>
		{#if savingEdits > 0}
			<span class="table-status">Saving…</span>
		{/if}
	</header>

	{#if tableError}
		<p class="table-error" role="status">{tableError}</p>
	{/if}

	<div class="table-surface">
		<div class="tabulator-host" use:tabulator={tableRows}></div>
	</div>
</section>

<style>
	:global(:root) {
		--tb-bg: #f6f9ff;
		--tb-surface: #ffffff;
		--tb-border: #d3ddef;
		--tb-text: #14233f;
		--tb-muted: #64748b;
		--tb-danger: #b91c1c;
	}

	:global(:root[data-theme='dark']),
	:global(.theme-dark) {
		--tb-bg: #161617;
		--tb-surface: #1f2023;
		--tb-border: #32343a;
		--tb-text: #edf2ff;
		--tb-muted: #a7b0c2;
		--tb-danger: #fca5a5;
	}

	.table-board {
		height: 100%;
		display: grid;
		grid-template-rows: auto auto minmax(0, 1fr);
		gap: 0.75rem;
		padding: 0.95rem;
		background: var(--tb-bg);
		color: var(--tb-text);
		min-height: 0;
	}

	.table-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.85rem;
	}

	.table-header h2 {
		margin: 0;
		font-size: 1rem;
		font-weight: 700;
	}

	.table-header p {
		margin: 0.2rem 0 0;
		font-size: 0.82rem;
		color: var(--tb-muted);
	}

	.table-status {
		font-size: 0.78rem;
		padding: 0.32rem 0.6rem;
		border-radius: 999px;
		background: color-mix(in srgb, var(--tb-text) 14%, transparent);
		color: var(--tb-text);
	}

	.table-error {
		margin: 0;
		font-size: 0.82rem;
		color: var(--tb-danger);
	}

	.table-surface {
		min-height: 0;
		border: 1px solid var(--tb-border);
		border-radius: 14px;
		overflow: hidden;
		background: var(--tb-surface);
	}

	.tabulator-host {
		height: 100%;
		min-height: 320px;
	}

	:global(.tabulator) {
		border: 0;
		font-size: 0.88rem;
	}

	@media (max-width: 760px) {
		.table-board {
			padding: 0.68rem;
			gap: 0.6rem;
		}

		.table-header {
			align-items: flex-start;
			flex-direction: column;
		}
	}
</style>
