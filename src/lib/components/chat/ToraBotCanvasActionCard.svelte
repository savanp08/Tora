<script lang="ts">
	import { browser } from '$app/environment';
	import { afterUpdate, onDestroy, onMount } from 'svelte';
	import {
		loadPersistedRoomCanvasFileContents,
		type CanvasAgenticApplyResult,
		type CanvasAgenticChange
	} from '$lib/utils/canvasAgentApply';
	import RichTextContent from '$lib/components/chat/RichTextContent.svelte';

	type CanvasChange = CanvasAgenticChange & {
		already_applied?: boolean;
	};

	type CardState = 'pending' | 'applying' | 'applied' | 'rejected' | 'error';

	type AuditTrailEntry = {
		index: number;
		kind: string;
		tool: string;
		text: string;
		error: string;
	};

	type AppliedMeta = {
		appliedBy: string;
		appliedAt: string;
		result: CanvasAgenticApplyResult;
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

	type DiffRow = {
		path: string;
		operation: 'create' | 'update' | 'change';
		additions: number;
		deletions: number;
	};

	export let roomId = '';
	export let apiBase = '';
	export let text = '';
	export let changesJson = '';
	export let auditTrail: unknown[] = [];
	export let currentUserName = '';
	export let canResolve = false;
	export let applyChanges:
		| ((payload: { text: string; changes: CanvasChange[] }) => Promise<CanvasAgenticApplyResult>)
		| null = null;

	let changes: CanvasChange[] = [];
	let auditEntries: AuditTrailEntry[] = [];
	let diffRows: DiffRow[] = [];
	let state: CardState = 'pending';
	let errorMsg = '';
	let showNotes = false;
	let appliedMeta: AppliedMeta | null = null;
	let dismissedMeta: DismissedMeta | null = null;
	let lastPreviewSeed = '';
	let cardBody: HTMLDivElement | null = null;
	let bodyMeasureFrame: number | null = null;
	let visibleBodyHeight = 320;
	let canExpandBody = false;

	const AUDIT_PREVIEW_LIMIT = 3;
	const CARD_BODY_EXPAND_STEP = 320;

	function toRecord(value: unknown): Record<string, unknown> | null {
		if (!value || typeof value !== 'object' || Array.isArray(value)) {
			return null;
		}
		return value as Record<string, unknown>;
	}

	function hashChangesJson(value: string) {
		let hash = 5381;
		for (let index = 0; index < value.length; index += 1) {
			hash = (((hash << 5) + hash) ^ value.charCodeAt(index)) >>> 0;
		}
		return hash.toString(36);
	}

	function persistKey() {
		return `tora_canvas_resolution_${roomId}_${hashChangesJson(changesJson)}`;
	}

	function persistResolutionState(value: PersistedResolution) {
		try {
			localStorage.setItem(persistKey(), JSON.stringify(value));
		} catch {
			/* ignore */
		}
	}

	function normalizeActorName() {
		return currentUserName.trim() || 'You';
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
			text: typeof record.text === 'string' ? record.text.trim() : '',
			error: typeof record.error === 'string' ? record.error.trim() : ''
		};
	}

	function normalizeCanvasPath(value: string) {
		return (value || '')
			.trim()
			.replace(/\\/g, '/')
			.replace(/^\/+/, '')
			.split('/')
			.filter(Boolean)
			.join('/');
	}

	function splitContentIntoLines(value: string) {
		const normalized = String(value || '').replace(/\r\n/g, '\n').replace(/\r/g, '\n');
		return normalized ? normalized.split('\n') : [];
	}

	function countLines(change: CanvasChange) {
		if (typeof change.lines === 'number' && Number.isFinite(change.lines)) {
			return Math.max(0, Math.trunc(change.lines));
		}
		return splitContentIntoLines(typeof change.content === 'string' ? change.content : '').length;
	}

	function formatAuditLabel(entry: AuditTrailEntry) {
		if (entry.text) {
			return entry.text;
		}
		if (entry.tool) {
			return entry.tool.replace(/_/g, ' ');
		}
		return entry.kind || 'Agent step';
	}

	function normalizeOperation(value: string) {
		const normalized = value.trim().toLowerCase();
		if (normalized === 'create') {
			return 'create' as const;
		}
		if (normalized === 'update') {
			return 'update' as const;
		}
		return 'change' as const;
	}

	function longestCommonSubsequenceLength(left: string[], right: string[]) {
		const row = new Array<number>(right.length + 1).fill(0);
		for (let leftIndex = 1; leftIndex <= left.length; leftIndex += 1) {
			let diagonal = 0;
			for (let rightIndex = 1; rightIndex <= right.length; rightIndex += 1) {
				const previousTop = row[rightIndex];
				if (left[leftIndex - 1] === right[rightIndex - 1]) {
					row[rightIndex] = diagonal + 1;
				} else {
					row[rightIndex] = Math.max(row[rightIndex], row[rightIndex - 1]);
				}
				diagonal = previousTop;
			}
		}
		return row[right.length];
	}

	function estimateSharedLineCount(left: string[], right: string[]) {
		const counts = new Map<string, number>();
		for (const line of left) {
			counts.set(line, (counts.get(line) ?? 0) + 1);
		}
		let shared = 0;
		for (const line of right) {
			const available = counts.get(line) ?? 0;
			if (available <= 0) {
				continue;
			}
			shared += 1;
			counts.set(line, available - 1);
		}
		return shared;
	}

	function computeLineDiffStats(beforeContent: string, afterContent: string) {
		const beforeLines = splitContentIntoLines(beforeContent);
		const afterLines = splitContentIntoLines(afterContent);
		let startIndex = 0;
		while (
			startIndex < beforeLines.length &&
			startIndex < afterLines.length &&
			beforeLines[startIndex] === afterLines[startIndex]
		) {
			startIndex += 1;
		}

		let beforeEnd = beforeLines.length - 1;
		let afterEnd = afterLines.length - 1;
		while (
			beforeEnd >= startIndex &&
			afterEnd >= startIndex &&
			beforeLines[beforeEnd] === afterLines[afterEnd]
		) {
			beforeEnd -= 1;
			afterEnd -= 1;
		}

		const beforeCore = beforeLines.slice(startIndex, beforeEnd + 1);
		const afterCore = afterLines.slice(startIndex, afterEnd + 1);

		if (beforeCore.length === 0) {
			return { additions: afterCore.length, deletions: 0 };
		}
		if (afterCore.length === 0) {
			return { additions: 0, deletions: beforeCore.length };
		}

		const workSize = beforeCore.length * afterCore.length;
		const sharedLines =
			workSize > 180000
				? estimateSharedLineCount(beforeCore, afterCore)
				: longestCommonSubsequenceLength(beforeCore, afterCore);

		return {
			additions: Math.max(0, afterCore.length - sharedLines),
			deletions: Math.max(0, beforeCore.length - sharedLines)
		};
	}

	function buildFallbackRow(change: CanvasChange): DiffRow {
		return {
			path: normalizeCanvasPath(change.file_path || ''),
			operation: normalizeOperation(String(change.operation || '')),
			additions: countLines(change),
			deletions: 0
		};
	}

	function buildDiffRow(change: CanvasChange, currentContent: string | null): DiffRow {
		const operation = normalizeOperation(String(change.operation || ''));
		const nextContent = typeof change.content === 'string' ? change.content : '';
		if (operation === 'create' || currentContent == null) {
			return {
				path: normalizeCanvasPath(change.file_path || ''),
				operation,
				additions: splitContentIntoLines(nextContent).length,
				deletions: 0
			};
		}
		const stats = computeLineDiffStats(currentContent, nextContent);
		return {
			path: normalizeCanvasPath(change.file_path || ''),
			operation,
			additions: stats.additions,
			deletions: stats.deletions
		};
	}

	async function hydrateDiffRows() {
		const fallbackRows = changes.map((change) => buildFallbackRow(change));
		diffRows = fallbackRows;
		if (!browser || !roomId || !apiBase || changes.length === 0) {
			return;
		}
		try {
			const currentFiles = await loadPersistedRoomCanvasFileContents({
				apiBase,
				roomId
			});
			diffRows = changes.map((change) =>
				buildDiffRow(change, currentFiles[normalizeCanvasPath(change.file_path || '')] ?? null)
			);
		} catch {
			diffRows = fallbackRows;
		}
	}

	function measureBodyExpansionState() {
		if (!cardBody) {
			canExpandBody = false;
			return;
		}
		canExpandBody = cardBody.scrollHeight > visibleBodyHeight + 6;
	}

	function scheduleBodyMeasurement() {
		if (!browser || bodyMeasureFrame !== null) {
			return;
		}
		bodyMeasureFrame = window.requestAnimationFrame(() => {
			bodyMeasureFrame = null;
			measureBodyExpansionState();
		});
	}

	function expandBody() {
		visibleBodyHeight += CARD_BODY_EXPAND_STEP;
		scheduleBodyMeasurement();
	}

	async function handleApply() {
		if (!canResolve) {
			state = 'error';
			errorMsg = 'Only room admins can accept or dismiss these changes right now.';
			return;
		}
		if (!applyChanges) {
			state = 'error';
			errorMsg = 'Canvas apply handler is unavailable.';
			return;
		}
		if (changes.length === 0) {
			state = 'error';
			errorMsg = 'No canvas changes were included in this proposal.';
			return;
		}
		state = 'applying';
		errorMsg = '';
		dismissedMeta = null;
		try {
			const result = await applyChanges({
				text: text.trim(),
				changes
			});
			if (result.failed > 0) {
				state = 'error';
				errorMsg =
					result.applied > 0
						? `Applied ${result.applied} change(s). ${result.failed} failed.`
						: `Unable to apply ${result.failed} change(s).`;
				return;
			}
			const nextAppliedMeta: AppliedMeta = {
				appliedBy: normalizeActorName(),
				appliedAt: new Date().toISOString(),
				result
			};
			appliedMeta = nextAppliedMeta;
			state = 'applied';
			persistResolutionState({ state: 'applied', appliedMeta: nextAppliedMeta });
		} catch (error) {
			state = 'error';
			errorMsg =
				error instanceof Error ? error.message : 'Failed to apply the proposed canvas changes.';
		}
	}

	function handleDismiss() {
		if (!canResolve) {
			state = 'error';
			errorMsg = 'Only room admins can accept or dismiss these changes right now.';
			return;
		}
		appliedMeta = null;
		const nextDismissedMeta: DismissedMeta = {
			dismissedBy: normalizeActorName(),
			dismissedAt: new Date().toISOString()
		};
		dismissedMeta = nextDismissedMeta;
		state = 'rejected';
		persistResolutionState({ state: 'rejected', dismissedMeta: nextDismissedMeta });
	}

	$: {
		try {
			const parsed = JSON.parse(changesJson || '[]');
			changes = Array.isArray(parsed) ? parsed : [];
		} catch {
			changes = [];
		}
	}

	$: auditEntries = Array.isArray(auditTrail)
		? auditTrail
				.map((entry, index) => normalizeAuditTrailEntry(entry, index))
				.filter((entry): entry is AuditTrailEntry => Boolean(entry))
		: [];

	$: totalFilesChanged = diffRows.length || changes.length;
	$: totalAdditions = diffRows.reduce((sum, row) => sum + row.additions, 0);
	$: totalDeletions = diffRows.reduce((sum, row) => sum + row.deletions, 0);
	$: hasAuditNotes = auditEntries.length > 0;

	$: {
		const previewSeed = `${roomId}::${apiBase}::${changesJson}`;
		if (browser && previewSeed !== lastPreviewSeed) {
			lastPreviewSeed = previewSeed;
			visibleBodyHeight = CARD_BODY_EXPAND_STEP;
			void hydrateDiffRows();
		}
	}

	afterUpdate(() => {
		scheduleBodyMeasurement();
	});

	onMount(() => {
		if (changes.length > 0 && changes.every((change) => change?.already_applied)) {
			appliedMeta = {
				appliedBy: 'Tora-Bot',
				appliedAt: new Date().toISOString(),
				result: {
					applied: changes.length,
					failed: 0,
					changesApplied: changes.length,
					foldersCreated: 0,
					filesCreated: changes.filter((change) => normalizeOperation(String(change.operation || '')) === 'create')
						.length,
					filesUpdated: changes.filter((change) => normalizeOperation(String(change.operation || '')) === 'update')
						.length
				}
			};
			state = 'applied';
			return;
		}

		try {
			const saved = localStorage.getItem(persistKey());
			if (!saved) {
				return;
			}
			const parsed = JSON.parse(saved) as PersistedResolution;
			if (parsed.state === 'applied' && parsed.appliedMeta) {
				appliedMeta = parsed.appliedMeta;
				state = 'applied';
				return;
			}
			if (parsed.state === 'rejected' && parsed.dismissedMeta) {
				dismissedMeta = parsed.dismissedMeta;
				state = 'rejected';
			}
		} catch {
			/* ignore */
		}

		scheduleBodyMeasurement();
	});

	onDestroy(() => {
		if (!browser || bodyMeasureFrame === null) {
			return;
		}
		window.cancelAnimationFrame(bodyMeasureFrame);
		bodyMeasureFrame = null;
	});
</script>

<div class="tora-canvas-card" data-state={state}>
	<div
		class="canvas-diff-body"
		bind:this={cardBody}
		style:max-height={canExpandBody ? `${visibleBodyHeight}px` : 'none'}
	>
		{#if text.trim()}
			<div class="canvas-diff-text canvas-diff-text-primary">
				<RichTextContent text={text} />
			</div>
		{/if}

		<div class="canvas-diff-head">
			<div class="canvas-diff-summary">
				<span class="canvas-diff-count">
					{totalFilesChanged} file{totalFilesChanged === 1 ? '' : 's'} changed
				</span>
				<span class="canvas-diff-add">+{totalAdditions}</span>
				<span class="canvas-diff-del">-{totalDeletions}</span>
			</div>

			{#if state === 'applying'}
				<div class="canvas-diff-state is-working">Applying…</div>
			{:else if state === 'applied'}
				<div class="canvas-diff-state is-success">Accepted</div>
			{:else if state === 'rejected'}
				<div class="canvas-diff-state">Dismissed</div>
			{:else if errorMsg}
				<div class="canvas-diff-state is-error">Retry</div>
			{/if}
		</div>

		{#if diffRows.length > 0}
			<div class="canvas-diff-list">
				{#each diffRows as row, index (`${row.path}-${index}`)}
					<div class="canvas-diff-row">
						<div class="canvas-diff-path-wrap">
							<span class="canvas-diff-path" title={row.path || 'Untitled file'}>
								{row.path || 'Untitled file'}
							</span>
							{#if row.operation === 'create'}
								<span class="canvas-diff-dot is-create" aria-hidden="true"></span>
							{:else if row.operation === 'update'}
								<span class="canvas-diff-dot is-update" aria-hidden="true"></span>
							{/if}
						</div>
						<div class="canvas-diff-row-stats">
							<span class="canvas-diff-add">+{row.additions}</span>
							<span class="canvas-diff-del">-{row.deletions}</span>
						</div>
					</div>
				{/each}
			</div>
		{/if}

		{#if state === 'error'}
			<div class="canvas-diff-feedback is-error">{errorMsg}</div>
		{:else if state === 'applying'}
			<div class="canvas-diff-feedback is-working">Applying changes to the room canvas…</div>
		{:else if state === 'applied' && appliedMeta}
			<div class="canvas-diff-feedback is-success">
				Saved to the room canvas · Accepted by <strong>{appliedMeta.appliedBy}</strong> · {new Date(
					appliedMeta.appliedAt
				).toLocaleString()}
			</div>
		{:else if state === 'rejected' && dismissedMeta}
			<div class="canvas-diff-feedback">
				Dismissed by <strong>{dismissedMeta.dismissedBy}</strong> · {new Date(
					dismissedMeta.dismissedAt
				).toLocaleString()}
			</div>
		{/if}

		{#if state === 'pending' && !canResolve}
			<div class="canvas-diff-feedback">Only room admins can accept or dismiss these changes right now.</div>
		{/if}

		{#if hasAuditNotes}
			<button
				type="button"
				class="canvas-diff-notes-toggle"
				on:click={() => {
					showNotes = !showNotes;
				}}
			>
				{showNotes ? 'Hide agent notes' : 'Show agent notes'}
			</button>

			{#if showNotes}
				<div class="canvas-diff-notes">
					{#if auditEntries.length > 0}
						<div class="canvas-diff-audit">
							{#each auditEntries.slice(0, AUDIT_PREVIEW_LIMIT) as entry (`canvas-audit-${entry.index}`)}
								<div class="canvas-diff-audit-row" class:is-error={Boolean(entry.error)}>
									<div class="canvas-diff-audit-label">{formatAuditLabel(entry)}</div>
									{#if entry.error}
										<div class="canvas-diff-audit-meta">{entry.error}</div>
									{/if}
								</div>
							{/each}
							{#if auditEntries.length > AUDIT_PREVIEW_LIMIT}
								<div class="canvas-diff-audit-meta">
									+{auditEntries.length - AUDIT_PREVIEW_LIMIT} more audit events recorded
								</div>
							{/if}
						</div>
					{/if}
				</div>
			{/if}
		{/if}
	</div>

	{#if canExpandBody}
		<button type="button" class="canvas-diff-read-more" on:click={expandBody}>Read more</button>
	{/if}

	{#if state === 'pending' || state === 'error'}
		<div class="canvas-diff-actions canvas-diff-actions-footer">
			<button
				type="button"
				class="canvas-diff-btn is-primary"
				disabled={!canResolve}
				on:click={handleApply}
			>
				{state === 'error' ? 'Retry' : 'Accept'}
			</button>
			<button
				type="button"
				class="canvas-diff-btn"
				disabled={!canResolve}
				on:click={handleDismiss}
			>
				Dismiss
			</button>
		</div>
	{/if}
</div>

<style>
	.tora-canvas-card {
		--canvas-card-bg:
			radial-gradient(circle at top left, rgba(255, 255, 255, 0.92), transparent 42%),
			linear-gradient(180deg, rgba(248, 251, 255, 0.98), rgba(239, 245, 252, 0.98));
		--canvas-card-border: rgba(188, 202, 220, 0.88);
		--canvas-card-shadow: 0 16px 34px rgba(15, 23, 42, 0.12);
		--canvas-card-text: rgba(24, 37, 57, 0.94);
		--canvas-card-text-strong: rgba(15, 23, 42, 0.96);
		--canvas-card-text-muted: rgba(84, 100, 123, 0.88);
		--canvas-card-divider: rgba(198, 209, 223, 0.88);
		--canvas-card-button-bg: rgba(255, 255, 255, 0.74);
		--canvas-card-button-bg-hover: rgba(255, 255, 255, 0.94);
		--canvas-card-button-border: rgba(176, 191, 210, 0.9);
		--canvas-card-button-text: rgba(35, 50, 74, 0.94);
		--canvas-card-button-primary-bg: rgba(34, 197, 94, 0.12);
		--canvas-card-button-primary-border: rgba(34, 197, 94, 0.26);
		--canvas-card-button-primary-text: #156a3e;
		--canvas-card-pill-bg: rgba(255, 255, 255, 0.74);
		--canvas-card-pill-text: rgba(55, 65, 81, 0.8);
		--canvas-card-working-bg: rgba(99, 102, 241, 0.12);
		--canvas-card-working-text: #4858c6;
		--canvas-card-success-bg: rgba(34, 197, 94, 0.13);
		--canvas-card-success-text: #17603d;
		--canvas-card-error-bg: rgba(239, 68, 68, 0.12);
		--canvas-card-error-text: #b5473a;
		--canvas-card-path-text: rgba(29, 41, 62, 0.94);
		--canvas-card-link-text: rgba(70, 90, 118, 0.9);
		--canvas-card-link-text-hover: rgba(22, 35, 55, 0.98);
		--canvas-card-audit-bg: rgba(255, 255, 255, 0.6);
		--canvas-card-audit-border: rgba(203, 214, 228, 0.92);
		--canvas-card-audit-error-bg: rgba(254, 242, 242, 0.96);
		--canvas-card-audit-error-border: rgba(252, 165, 165, 0.5);
		--canvas-card-audit-label: rgba(21, 34, 54, 0.9);
		display: flex;
		flex-direction: column;
		gap: 12px;
		padding: 14px 16px;
		border-radius: 18px;
		background: var(--canvas-card-bg);
		border: 1px solid var(--canvas-card-border);
		box-shadow: var(--canvas-card-shadow);
		color: var(--canvas-card-text);
	}

	:global(.messages-shell.theme-dark) .tora-canvas-card {
		--canvas-card-bg:
			radial-gradient(circle at top left, rgba(255, 255, 255, 0.04), transparent 42%),
			linear-gradient(180deg, rgba(34, 34, 34, 0.98), rgba(24, 24, 24, 0.98));
		--canvas-card-border: rgba(255, 255, 255, 0.08);
		--canvas-card-shadow: 0 18px 40px rgba(15, 23, 42, 0.18);
		--canvas-card-text: rgba(245, 245, 245, 0.96);
		--canvas-card-text-strong: rgba(250, 250, 250, 0.92);
		--canvas-card-text-muted: rgba(226, 232, 240, 0.72);
		--canvas-card-divider: rgba(255, 255, 255, 0.08);
		--canvas-card-button-bg: rgba(255, 255, 255, 0.04);
		--canvas-card-button-bg-hover: rgba(255, 255, 255, 0.08);
		--canvas-card-button-border: rgba(255, 255, 255, 0.1);
		--canvas-card-button-text: rgba(248, 248, 248, 0.92);
		--canvas-card-button-primary-bg: rgba(120, 219, 169, 0.14);
		--canvas-card-button-primary-border: rgba(120, 219, 169, 0.26);
		--canvas-card-button-primary-text: #aaf1c8;
		--canvas-card-pill-bg: rgba(255, 255, 255, 0.06);
		--canvas-card-pill-text: rgba(248, 248, 248, 0.8);
		--canvas-card-working-bg: rgba(99, 102, 241, 0.16);
		--canvas-card-working-text: #a5b4fc;
		--canvas-card-success-bg: rgba(120, 219, 169, 0.16);
		--canvas-card-success-text: #aaf1c8;
		--canvas-card-error-bg: rgba(239, 68, 68, 0.16);
		--canvas-card-error-text: #ffb4a3;
		--canvas-card-path-text: rgba(244, 244, 245, 0.92);
		--canvas-card-link-text: rgba(226, 232, 240, 0.76);
		--canvas-card-link-text-hover: rgba(248, 250, 252, 0.94);
		--canvas-card-audit-bg: rgba(255, 255, 255, 0.04);
		--canvas-card-audit-border: rgba(255, 255, 255, 0.06);
		--canvas-card-audit-error-bg: rgba(127, 29, 29, 0.22);
		--canvas-card-audit-error-border: rgba(248, 113, 113, 0.18);
		--canvas-card-audit-label: rgba(248, 250, 252, 0.88);
	}

	.canvas-diff-body {
		overflow: hidden;
		transition: max-height 180ms ease;
	}

	.canvas-diff-head,
	.canvas-diff-row,
	.canvas-diff-actions {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
	}

	.canvas-diff-summary {
		display: flex;
		align-items: baseline;
		flex-wrap: wrap;
		gap: 12px;
		min-width: 0;
	}

	.canvas-diff-count {
		font-size: 1.05rem;
		font-weight: 650;
		color: var(--canvas-card-text-strong);
	}

	.canvas-diff-add,
	.canvas-diff-del {
		font-size: 0.96rem;
		font-weight: 700;
		letter-spacing: 0.01em;
	}

	.canvas-diff-add {
		color: #78dba9;
	}

	.canvas-diff-del {
		color: #ff8f73;
	}

	.canvas-diff-actions {
		flex-shrink: 0;
	}

	.canvas-diff-btn {
		border: 1px solid var(--canvas-card-button-border);
		background: var(--canvas-card-button-bg);
		color: var(--canvas-card-button-text);
		border-radius: 999px;
		padding: 7px 12px;
		font-size: 0.76rem;
		font-weight: 650;
		cursor: pointer;
		transition:
			background 120ms ease,
			border-color 120ms ease,
			opacity 120ms ease;
	}

	.canvas-diff-btn:hover {
		background: var(--canvas-card-button-bg-hover);
	}

	.canvas-diff-btn.is-primary {
		background: var(--canvas-card-button-primary-bg);
		border-color: var(--canvas-card-button-primary-border);
		color: var(--canvas-card-button-primary-text);
	}

	.canvas-diff-btn:disabled {
		opacity: 0.45;
		cursor: not-allowed;
	}

	.canvas-diff-state {
		flex-shrink: 0;
		font-size: 0.76rem;
		font-weight: 700;
		padding: 6px 10px;
		border-radius: 999px;
		background: var(--canvas-card-pill-bg);
		color: var(--canvas-card-pill-text);
	}

	.canvas-diff-state.is-working {
		color: var(--canvas-card-working-text);
		background: var(--canvas-card-working-bg);
	}

	.canvas-diff-state.is-success {
		color: var(--canvas-card-success-text);
		background: var(--canvas-card-success-bg);
	}

	.canvas-diff-state.is-error {
		color: var(--canvas-card-error-text);
		background: var(--canvas-card-error-bg);
	}

	.canvas-diff-list {
		display: flex;
		flex-direction: column;
		border-top: 1px solid var(--canvas-card-divider);
		border-bottom: 1px solid var(--canvas-card-divider);
	}

	.canvas-diff-row {
		padding: 13px 0;
		border-top: 1px solid var(--canvas-card-divider);
	}

	.canvas-diff-row:first-child {
		border-top: none;
	}

	.canvas-diff-path-wrap {
		display: flex;
		align-items: center;
		gap: 10px;
		min-width: 0;
		flex: 1;
	}

	.canvas-diff-path {
		min-width: 0;
		font-size: 0.94rem;
		font-weight: 520;
		color: var(--canvas-card-path-text);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.canvas-diff-dot {
		width: 8px;
		height: 8px;
		border-radius: 999px;
		flex-shrink: 0;
		background: rgba(148, 163, 184, 0.7);
	}

	.canvas-diff-dot.is-create {
		background: #52b3ff;
	}

	.canvas-diff-dot.is-update {
		background: rgba(255, 255, 255, 0.5);
	}

	.canvas-diff-row-stats {
		display: flex;
		align-items: center;
		gap: 12px;
		flex-shrink: 0;
	}

	.canvas-diff-read-more,
	.canvas-diff-notes-toggle {
		align-self: flex-start;
		border: none;
		background: transparent;
		padding: 0;
		font-size: 0.75rem;
		font-weight: 650;
		color: var(--canvas-card-link-text);
		cursor: pointer;
	}

	.canvas-diff-read-more:hover,
	.canvas-diff-notes-toggle:hover {
		color: var(--canvas-card-link-text-hover);
	}

	.canvas-diff-feedback,
	.canvas-diff-text,
	.canvas-diff-audit-meta {
		font-size: 0.78rem;
		line-height: 1.5;
		color: var(--canvas-card-text-muted);
	}

	.canvas-diff-text-primary {
		margin: 0 0 2px;
		color: var(--canvas-card-text-strong);
	}

	.canvas-diff-feedback strong {
		color: var(--canvas-card-text-strong);
	}

	.canvas-diff-feedback.is-success {
		color: var(--canvas-card-success-text);
	}

	.canvas-diff-feedback.is-working {
		color: var(--canvas-card-working-text);
	}

	.canvas-diff-feedback.is-error {
		color: var(--canvas-card-error-text);
	}

	.canvas-diff-actions-footer {
		padding-top: 4px;
	}

	.canvas-diff-notes {
		display: flex;
		flex-direction: column;
		gap: 10px;
		padding-top: 2px;
	}

	.canvas-diff-text {
		margin: 0;
	}

	.canvas-diff-audit {
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.canvas-diff-audit-row {
		padding: 9px 10px;
		border-radius: 10px;
		background: var(--canvas-card-audit-bg);
		border: 1px solid var(--canvas-card-audit-border);
	}

	.canvas-diff-audit-row.is-error {
		border-color: var(--canvas-card-audit-error-border);
		background: var(--canvas-card-audit-error-bg);
	}

	.canvas-diff-audit-label {
		font-size: 0.77rem;
		font-weight: 600;
		line-height: 1.45;
		color: var(--canvas-card-audit-label);
	}

	@media (max-width: 640px) {
		.canvas-diff-head {
			flex-direction: column;
			align-items: flex-start;
		}

		.canvas-diff-actions,
		.canvas-diff-actions-footer {
			width: 100%;
			flex-wrap: wrap;
		}

		.canvas-diff-row {
			align-items: flex-start;
		}

		.canvas-diff-row-stats {
			padding-top: 1px;
		}
	}
</style>
