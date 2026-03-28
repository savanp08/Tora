<script lang="ts">
	import { onMount } from 'svelte';

	type CanvasChange = {
		kind?: string;
		file_path?: string;
		content?: string;
		description?: string;
		lines?: number;
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

	export let text = '';
	export let changesJson = '';
	export let auditTrail: unknown[] = [];
	export let applyChanges:
		| ((payload: { text: string; changes: CanvasChange[] }) => Promise<void>)
		| null = null;

	let changes: CanvasChange[] = [];
	let auditEntries: AuditTrailEntry[] = [];
	let state: CardState = 'pending';
	let errorMsg = '';
	let showAll = false;

	const PREVIEW_LIMIT = 4;
	const AUDIT_PREVIEW_LIMIT = 4;

	function toRecord(value: unknown): Record<string, unknown> | null {
		if (!value || typeof value !== 'object' || Array.isArray(value)) {
			return null;
		}
		return value as Record<string, unknown>;
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

	onMount(() => {
		try {
			const parsed = JSON.parse(changesJson || '[]');
			changes = Array.isArray(parsed) ? parsed : [];
		} catch {
			changes = [];
		}
		if (changes.length > 0 && changes.every((change) => change?.already_applied)) {
			state = 'applied';
		}
	});

	$: auditEntries = Array.isArray(auditTrail)
		? auditTrail
				.map((entry, index) => normalizeAuditTrailEntry(entry, index))
				.filter((entry): entry is AuditTrailEntry => Boolean(entry))
		: [];

	function visibleChanges() {
		return showAll ? changes : changes.slice(0, PREVIEW_LIMIT);
	}

	function auditIcon(entry: AuditTrailEntry) {
		if (entry.error) {
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

	function formatAuditLabel(entry: AuditTrailEntry) {
		const toolLabel = entry.tool.replace(/_/g, ' ').trim() || 'Agent step';
		if (entry.kind === 'text' || entry.kind === 'done' || entry.kind === 'thinking') {
			return entry.text || toolLabel;
		}
		if (entry.kind === 'tool_result' && entry.error) {
			return `${toolLabel} failed`;
		}
		return toolLabel;
	}

	function countLines(change: CanvasChange) {
		if (typeof change.lines === 'number' && Number.isFinite(change.lines)) {
			return Math.max(0, Math.trunc(change.lines));
		}
		const content = typeof change.content === 'string' ? change.content : '';
		return content ? content.split('\n').length : 0;
	}

	async function handleApply() {
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
		try {
			await applyChanges({
				text: text.trim(),
				changes
			});
			state = 'applied';
		} catch (error) {
			state = 'error';
			errorMsg =
				error instanceof Error ? error.message : 'Failed to apply the proposed canvas changes.';
		}
	}
</script>

<div class="tora-canvas-card" data-state={state}>
	<div class="tora-canvas-head">
		<div class="tora-canvas-title">Canvas Changes</div>
		<div class="tora-canvas-count">{changes.length} file{changes.length === 1 ? '' : 's'}</div>
	</div>

	{#if text.trim()}
		<p class="tora-canvas-summary">{text}</p>
	{/if}

	{#if changes.length > 0}
		<div class="tora-canvas-list">
			{#each visibleChanges() as change, index (`${change.file_path || 'file'}-${index}`)}
				<div class="tora-canvas-item">
					<div class="tora-canvas-item-head">
						<span class="tora-canvas-file">{change.file_path || 'Untitled file'}</span>
						<span class="tora-canvas-lines">{countLines(change)} lines</span>
					</div>
					{#if change.description}
						<div class="tora-canvas-desc">{change.description}</div>
					{/if}
				</div>
			{/each}
		</div>

		{#if changes.length > PREVIEW_LIMIT}
			<button
				type="button"
				class="tora-canvas-toggle"
				on:click={() => {
					showAll = !showAll;
				}}
			>
				{showAll ? 'Show less' : `Show all ${changes.length} files`}
			</button>
		{/if}
	{/if}

	{#if auditEntries.length > 0}
		<div class="tora-canvas-audit">
			<div class="tora-canvas-audit-head">
				<span class="tora-canvas-audit-title">Agent audit</span>
				<span class="tora-canvas-audit-count">{auditEntries.length} events</span>
			</div>
			<div class="tora-canvas-audit-list">
				{#each auditEntries.slice(0, AUDIT_PREVIEW_LIMIT) as entry (`canvas-audit-${entry.index}`)}
					<div class="tora-canvas-audit-row" class:is-error={Boolean(entry.error)}>
						<span class="tora-canvas-audit-icon">{auditIcon(entry)}</span>
						<div class="tora-canvas-audit-main">
							<div class="tora-canvas-audit-label">{formatAuditLabel(entry)}</div>
							{#if entry.error}
								<div class="tora-canvas-audit-meta">{entry.error}</div>
							{:else if entry.text && entry.kind !== 'text' && entry.kind !== 'done' && entry.kind !== 'thinking'}
								<div class="tora-canvas-audit-meta">{entry.text}</div>
							{/if}
						</div>
					</div>
				{/each}
			</div>
			{#if auditEntries.length > AUDIT_PREVIEW_LIMIT}
				<div class="tora-canvas-audit-meta">
					+{auditEntries.length - AUDIT_PREVIEW_LIMIT} more audit events recorded
				</div>
			{/if}
		</div>
	{/if}

	{#if state === 'pending'}
		<div class="tora-canvas-actions">
			<button type="button" class="tora-canvas-apply" on:click={handleApply}>Apply to Canvas</button>
			<button type="button" class="tora-canvas-dismiss" on:click={() => (state = 'rejected')}
				>Dismiss</button
			>
		</div>
	{:else if state === 'applying'}
		<div class="tora-canvas-status is-working">Applying changes to the visible canvas...</div>
	{:else if state === 'applied'}
		<div class="tora-canvas-status is-success">Applied to the visible canvas.</div>
	{:else if state === 'rejected'}
		<div class="tora-canvas-status">Dismissed. Changes were not applied.</div>
	{:else if errorMsg}
		<div class="tora-canvas-status is-error">{errorMsg}</div>
	{/if}
</div>

<style>
	.tora-canvas-card {
		display: flex;
		flex-direction: column;
		gap: 10px;
		padding: 12px 14px;
		border-radius: 14px;
		background: rgba(15, 23, 42, 0.05);
		border: 1px solid rgba(99, 102, 241, 0.18);
	}

	.tora-canvas-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
	}

	.tora-canvas-title {
		font-weight: 700;
	}

	.tora-canvas-count {
		font-size: 0.78rem;
		opacity: 0.7;
	}

	.tora-canvas-summary {
		margin: 0;
		font-size: 0.86rem;
		line-height: 1.45;
	}

	.tora-canvas-list {
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.tora-canvas-audit {
		display: flex;
		flex-direction: column;
		gap: 8px;
		padding: 10px;
		border-radius: 10px;
		background: rgba(79, 70, 229, 0.06);
		border: 1px solid rgba(79, 70, 229, 0.14);
	}

	.tora-canvas-audit-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
	}

	.tora-canvas-audit-title {
		font-size: 0.8rem;
		font-weight: 700;
		color: #4338ca;
	}

	.tora-canvas-audit-count {
		font-size: 0.74rem;
		opacity: 0.72;
	}

	.tora-canvas-audit-list {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.tora-canvas-audit-row {
		display: flex;
		align-items: flex-start;
		gap: 8px;
		padding: 7px 8px;
		border-radius: 8px;
		background: rgba(255, 255, 255, 0.58);
		border: 1px solid rgba(15, 23, 42, 0.08);
	}

	.tora-canvas-audit-row.is-error {
		border-color: rgba(220, 38, 38, 0.2);
		background: rgba(254, 226, 226, 0.75);
	}

	.tora-canvas-audit-icon {
		flex-shrink: 0;
		width: 16px;
		text-align: center;
	}

	.tora-canvas-audit-main {
		flex: 1;
		min-width: 0;
	}

	.tora-canvas-audit-label {
		font-size: 0.77rem;
		font-weight: 600;
		line-height: 1.35;
		word-break: break-word;
	}

	.tora-canvas-item {
		padding: 9px 10px;
		border-radius: 10px;
		background: rgba(255, 255, 255, 0.58);
		border: 1px solid rgba(15, 23, 42, 0.08);
	}

	.tora-canvas-item-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 10px;
	}

	.tora-canvas-file {
		font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
		font-size: 0.81rem;
		word-break: break-all;
	}

	.tora-canvas-lines,
	.tora-canvas-desc,
	.tora-canvas-status,
	.tora-canvas-toggle {
		font-size: 0.78rem;
	}

	.tora-canvas-desc {
		margin-top: 5px;
		opacity: 0.78;
		line-height: 1.4;
	}

	.tora-canvas-toggle {
		align-self: flex-start;
		border: none;
		background: transparent;
		color: #4f46e5;
		padding: 0;
		cursor: pointer;
	}

	.tora-canvas-actions {
		display: flex;
		gap: 8px;
		flex-wrap: wrap;
	}

	.tora-canvas-apply,
	.tora-canvas-dismiss {
		border: none;
		border-radius: 999px;
		padding: 8px 12px;
		font-size: 0.8rem;
		cursor: pointer;
	}

	.tora-canvas-apply {
		background: #4f46e5;
		color: white;
		font-weight: 600;
	}

	.tora-canvas-dismiss {
		background: rgba(15, 23, 42, 0.08);
		color: inherit;
	}

	.tora-canvas-status.is-working {
		color: #4f46e5;
	}

	.tora-canvas-status.is-success {
		color: #15803d;
	}

	.tora-canvas-status.is-error {
		color: #b91c1c;
	}
</style>
