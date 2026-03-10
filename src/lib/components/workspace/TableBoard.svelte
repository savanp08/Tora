<script lang="ts">
	import type { ProjectTimeline } from '$lib/types/timeline';
	import { projectTimeline } from '$lib/stores/timeline';
	import { tick } from 'svelte';
	import { SvelteMap } from 'svelte/reactivity';

	const MAX_ROWS = 100;
	const MAX_COLS = 100;
	const ROW_HEIGHT = 34;
	const COL_WIDTH = 148;
	const ROW_HEADER_WIDTH = 54;
	const COL_HEADER_HEIGHT = 36;
	const OVERSCAN = 3;

	const TOTAL_WIDTH = MAX_COLS * COL_WIDTH;
	const TOTAL_HEIGHT = MAX_ROWS * ROW_HEIGHT;

	type CellAddress = { row: number; col: number };
	type SelectionRange = { r1: number; c1: number; r2: number; c2: number };
	type ChartType = 'bar' | 'line';

	// ── Scroll / viewport ─────────────────────────────────────────────
	let bodyViewport: HTMLDivElement | null = null;
	let scrollLeft = 0;
	let scrollTop = 0;
	let viewportWidth = 0;
	let viewportHeight = 0;

	// ── Cell data ─────────────────────────────────────────────────────
	let userCellValues = new SvelteMap<string, string>();

	// ── Selection state ───────────────────────────────────────────────
	let anchorCell: CellAddress | null = null;
	let selectionEnd: CellAddress | null = null;
	let isMouseSelecting = false;

	// ── Edit state ────────────────────────────────────────────────────
	let editingCell: CellAddress | null = null;
	let editValue = '';
	let activeEditorRef: HTMLInputElement | null = null;

	// ── Formula bar ───────────────────────────────────────────────────
	let formulaBarValue = '';
	let formulaBarRef: HTMLInputElement | null = null;

	// ── Chart modal ───────────────────────────────────────────────────
	let showChart = false;
	let chartType: ChartType = 'bar';

	// ── Derived visible range ─────────────────────────────────────────
	$: prefilledCellValues = buildPrefilledCells($projectTimeline);
	$: visibleColStart = clamp(Math.floor(scrollLeft / COL_WIDTH) - OVERSCAN, 0, MAX_COLS - 1);
	$: visibleColEnd   = clamp(Math.ceil((scrollLeft + viewportWidth) / COL_WIDTH) + OVERSCAN, 0, MAX_COLS - 1);
	$: visibleRowStart = clamp(Math.floor(scrollTop / ROW_HEIGHT) - OVERSCAN, 0, MAX_ROWS - 1);
	$: visibleRowEnd   = clamp(Math.ceil((scrollTop + viewportHeight) / ROW_HEIGHT) + OVERSCAN, 0, MAX_ROWS - 1);
	$: visibleCols = buildRange(visibleColStart, visibleColEnd);
	$: visibleRows = buildRange(visibleRowStart, visibleRowEnd);
	$: renderedCellCount = visibleCols.length * visibleRows.length;

	// ── Selection helpers ─────────────────────────────────────────────
	$: selRange = computeRange(anchorCell, selectionEnd);
	$: selCellRef = anchorCell ? `${columnLabel(anchorCell.col)}${anchorCell.row + 1}` : '';
	$: selSizeLabel = selRange
		? selRange.r1 === selRange.r2 && selRange.c1 === selRange.c2
			? selCellRef
			: `${columnLabel(selRange.c1)}${selRange.r1 + 1}:${columnLabel(selRange.c2)}${selRange.r2 + 1} (${(selRange.r2 - selRange.r1 + 1) * (selRange.c2 - selRange.c1 + 1)} cells)`
		: '';

	$: isMultiSelect = selRange
		? selRange.r1 !== selRange.r2 || selRange.c1 !== selRange.c2
		: false;

	// Selection overlay (pixel coords in canvas space)
	$: selOverlayStyle = selRange
		? `left:${selRange.c1 * COL_WIDTH}px;top:${selRange.r1 * ROW_HEIGHT}px;width:${(selRange.c2 - selRange.c1 + 1) * COL_WIDTH}px;height:${(selRange.r2 - selRange.r1 + 1) * ROW_HEIGHT}px;`
		: '';

	// Chart data from current selection
	$: chartData = selRange ? extractChartData(selRange) : [];

	function computeRange(a: CellAddress | null, b: CellAddress | null): SelectionRange | null {
		if (!a) return null;
		const other = b ?? a;
		return {
			r1: Math.min(a.row, other.row), c1: Math.min(a.col, other.col),
			r2: Math.max(a.row, other.row), c2: Math.max(a.col, other.col)
		};
	}

	function isCellInSelection(row: number, col: number): boolean {
		if (!selRange) return false;
		return row >= selRange.r1 && row <= selRange.r2 && col >= selRange.c1 && col <= selRange.c2;
	}

	function isAnchor(row: number, col: number): boolean {
		return anchorCell?.row === row && anchorCell?.col === col;
	}

	function isEditingCell(row: number, col: number): boolean {
		return editingCell?.row === row && editingCell?.col === col;
	}

	// ── Data helpers ──────────────────────────────────────────────────
	function cellKey(row: number, col: number) { return `${row}:${col}`; }

	function clamp(v: number, min: number, max: number) {
		return v < min ? min : v > max ? max : v;
	}

	function buildRange(start: number, end: number): number[] {
		const out: number[] = [];
		for (let i = start; i <= end; i++) out.push(i);
		return out;
	}

	function columnLabel(index: number): string {
		let next = index + 1;
		let label = '';
		while (next > 0) {
			const rem = (next - 1) % 26;
			label = String.fromCharCode(65 + rem) + label;
			next = Math.floor((next - 1) / 26);
		}
		return label;
	}

	function colLabelToIndex(label: string): number {
		let index = 0;
		for (const ch of label.toUpperCase()) {
			index = index * 26 + (ch.charCodeAt(0) - 64);
		}
		return index - 1;
	}

	function cellRefToAddr(ref: string): CellAddress | null {
		const m = ref.trim().match(/^([A-Za-z]+)(\d+)$/);
		if (!m) return null;
		return { col: colLabelToIndex(m[1]), row: parseInt(m[2]) - 1 };
	}

	function getCellRaw(row: number, col: number): string {
		const key = cellKey(row, col);
		return userCellValues.has(key) ? (userCellValues.get(key) ?? '') : (prefilledCellValues.get(key) ?? '');
	}

	function getCellDisplay(row: number, col: number): string {
		const raw = getCellRaw(row, col);
		if (raw.startsWith('=')) {
			try { return evalFormula(raw.slice(1)); } catch { return '#ERR'; }
		}
		return raw;
	}

	function hasPrefilledValue(row: number, col: number): boolean {
		return prefilledCellValues.has(cellKey(row, col));
	}

	function setCellValue(row: number, col: number, value: string) {
		userCellValues.set(cellKey(row, col), value);
		userCellValues = userCellValues;
	}

	// ── Formula evaluation ────────────────────────────────────────────
	function getRangeValues(rangeStr: string): string[] {
		const parts = rangeStr.trim().split(':');
		if (parts.length === 1) {
			const addr = cellRefToAddr(parts[0]);
			return addr ? [getCellDisplay(addr.row, addr.col)] : [];
		}
		const start = cellRefToAddr(parts[0]);
		const end   = cellRefToAddr(parts[1]);
		if (!start || !end) return [];
		const vals: string[] = [];
		for (let r = Math.min(start.row, end.row); r <= Math.max(start.row, end.row); r++) {
			for (let c = Math.min(start.col, end.col); c <= Math.max(start.col, end.col); c++) {
				vals.push(getCellDisplay(r, c));
			}
		}
		return vals;
	}

	function evalFormula(expr: string): string {
		const e = expr.trim().toUpperCase();
		const fnMatch = e.match(/^(SUM|AVERAGE|AVG|COUNT|COUNTA|MAX|MIN|PRODUCT)\((.+)\)$/);
		if (fnMatch) {
			const [, fn, arg] = fnMatch;
			const vals = getRangeValues(arg);
			const nums = vals.map(Number).filter((n) => !isNaN(n));
			switch (fn) {
				case 'SUM':     return String(nums.reduce((a, b) => a + b, 0));
				case 'AVERAGE':
				case 'AVG':     return nums.length ? String((nums.reduce((a, b) => a + b, 0) / nums.length).toFixed(4).replace(/\.?0+$/, '')) : '0';
				case 'COUNT':   return String(nums.length);
				case 'COUNTA':  return String(vals.filter((v) => v.trim()).length);
				case 'MAX':     return nums.length ? String(Math.max(...nums)) : '';
				case 'MIN':     return nums.length ? String(Math.min(...nums)) : '';
				case 'PRODUCT': return String(nums.reduce((a, b) => a * b, 1));
			}
		}
		// Simple cell ref
		const addr = cellRefToAddr(e);
		if (addr) return getCellDisplay(addr.row, addr.col);
		// Numeric literal or unknown
		return '#ERR';
	}

	// ── Chart data extraction ─────────────────────────────────────────
	type ChartPoint = { label: string; value: number };

	function extractChartData(range: SelectionRange): ChartPoint[] {
		const points: ChartPoint[] = [];
		for (let r = range.r1; r <= range.r2; r++) {
			for (let c = range.c1; c <= range.c2; c++) {
				const display = getCellDisplay(r, c);
				const num = parseFloat(display);
				if (!isNaN(num)) {
					points.push({ label: `${columnLabel(c)}${r + 1}`, value: num });
				}
			}
		}
		return points;
	}

	// ── Interactions ──────────────────────────────────────────────────
	async function onCellPointerDown(row: number, col: number, e: MouseEvent) {
		if (e.button !== 0) return;
		const selectedCellAlready =
			anchorCell !== null && anchorCell.row === row && anchorCell.col === col && !e.shiftKey;
		if (selectedCellAlready && !editingCell) {
			e.preventDefault();
			await openCellEditor(row, col);
			return;
		}
		if (e.detail >= 2) {
			e.preventDefault();
			await openCellEditor(row, col);
			return;
		}
		if (editingCell) commitEdit();
		if (e.shiftKey && anchorCell) {
			selectionEnd = { row, col };
			return;
		}
		anchorCell = { row, col };
		selectionEnd = null;
		isMouseSelecting = true;
		formulaBarValue = getCellRaw(row, col);
	}

	function onCellPointerEnter(row: number, col: number) {
		if (!isMouseSelecting) return;
		selectionEnd = { row, col };
	}

	async function onCellClick(row: number, col: number, event: MouseEvent) {
		if (event.detail < 2) {
			return;
		}
		event.preventDefault();
		await openCellEditor(row, col);
	}

	function onWindowPointerUp() {
		isMouseSelecting = false;
	}

	async function openCellEditor(
		row: number,
		col: number,
		presetValue: string | null = null,
		selectAll = true
	) {
		anchorCell = { row, col };
		selectionEnd = null;
		editingCell = { row, col };
		editValue = presetValue ?? getCellRaw(row, col);
		formulaBarValue = editValue;
		await tick();
		activeEditorRef?.focus();
		if (selectAll) {
			activeEditorRef?.select();
			return;
		}
		const cursorAt = editValue.length;
		activeEditorRef?.setSelectionRange(cursorAt, cursorAt);
	}

	function isPrintableEditorKey(event: KeyboardEvent) {
		return event.key.length === 1 && !event.metaKey && !event.ctrlKey && !event.altKey;
	}

	async function onCellShellKeyDown(row: number, col: number, event: KeyboardEvent) {
		if (event.key === 'Enter' || event.key === 'F2') {
			event.preventDefault();
			await openCellEditor(row, col);
			return;
		}
		if (event.key === 'Backspace' || event.key === 'Delete') {
			event.preventDefault();
			await openCellEditor(row, col, '', false);
			return;
		}
		if (isPrintableEditorKey(event)) {
			event.preventDefault();
			await openCellEditor(row, col, event.key, false);
		}
	}

	function commitEdit() {
		if (!editingCell) return;
		setCellValue(editingCell.row, editingCell.col, editValue);
		formulaBarValue = editValue;
		editingCell = null;
	}

	function cancelEdit() {
		editingCell = null;
		editValue = '';
	}

	function handleEditorKeyDown(e: KeyboardEvent) {
		if (e.key === 'Enter') { e.preventDefault(); commitEdit(); }
		else if (e.key === 'Escape') { e.preventDefault(); cancelEdit(); }
		else if (e.key === 'Tab') { e.preventDefault(); commitEdit(); }
	}

	function handleFormulaBarKeyDown(e: KeyboardEvent) {
		if (e.key === 'Enter') { e.preventDefault(); applyFormulaBarValue(); }
		else if (e.key === 'Escape') { e.preventDefault(); cancelEdit(); formulaBarRef?.blur(); }
	}

	function onFormulaBarFocus() {
		if (!anchorCell) return;
		editingCell = { ...anchorCell };
		editValue = formulaBarValue;
	}

	function onFormulaBarInput(e: Event) {
		const val = (e.currentTarget as HTMLInputElement).value;
		formulaBarValue = val;
		editValue = val;
	}

	function applyFormulaBarValue() {
		if (!anchorCell) return;
		setCellValue(anchorCell.row, anchorCell.col, formulaBarValue);
		editingCell = null;
		formulaBarRef?.blur();
	}

	function handleViewportScroll(e: Event) {
		const el = e.currentTarget as HTMLDivElement;
		scrollLeft = el.scrollLeft;
		scrollTop = el.scrollTop;
	}

	function observeViewport(node: HTMLDivElement) {
		const sync = () => { viewportWidth = node.clientWidth; viewportHeight = node.clientHeight; };
		sync();
		const obs = new ResizeObserver(sync);
		obs.observe(node);
		return { destroy() { obs.disconnect(); } };
	}

	// ── Prefill builder ───────────────────────────────────────────────
	function titleCase(value: string) {
		return value.toLowerCase().split('_').filter(Boolean)
			.map((p) => p[0].toUpperCase() + p.slice(1)).join(' ');
	}

	function buildPrefilledCells(timeline: ProjectTimeline | null): SvelteMap<string, string> {
		const m = new SvelteMap<string, string>();
		if (!timeline) return m;
		const put = (row: number, col: number, value: string) => {
			if (row >= 0 && col >= 0 && row < MAX_ROWS && col < MAX_COLS && value.trim()) {
				m.set(cellKey(row, col), value.trim());
			}
		};
		put(0, 0, 'Project');      put(0, 1, timeline.project_name || 'Project');
		put(1, 0, 'Est. Cost');    put(1, 1, timeline.estimated_cost || '-');
		put(2, 0, 'Budget Total'); put(2, 1, timeline.budget_total ? String(timeline.budget_total) : '-');
		put(3, 0, 'Budget Spent'); put(3, 1, timeline.budget_spent ? String(timeline.budget_spent) : '-');
		put(4, 0, 'Tech Stack');   put(4, 1, (timeline.tech_stack ?? []).join(', '));

		const hdr = 6;
		['Task','Sprint','Status','Priority','Owner','Type','Effort','Start','End','Description']
			.forEach((h, i) => put(hdr, i, h));

		let r = hdr + 1;
		for (const sprint of timeline.sprints) {
			for (const task of sprint.tasks) {
				if (r >= MAX_ROWS) break;
				put(r, 0, task.title || 'Task');
				put(r, 1, sprint.name || 'Sprint');
				put(r, 2, titleCase(task.status || 'todo'));
				put(r, 3, titleCase(task.priority || 'medium'));
				put(r, 4, task.assignee || task.status_actor_name || 'Unassigned');
				put(r, 5, task.type || 'general');
				put(r, 6, String(task.effort_score ?? ''));
				put(r, 7, task.start_date || sprint.start_date || '');
				put(r, 8, task.end_date   || sprint.end_date   || '');
				put(r, 9, task.description || '');
				r++;
			}
			if (r >= MAX_ROWS) break;
		}
		return m;
	}

	// ── Chart SVG builder ─────────────────────────────────────────────
	const CHART_W = 480;
	const CHART_H = 200;
	const CHART_PAD = { top: 20, right: 16, bottom: 32, left: 40 };

	function buildBarChart(points: ChartPoint[]): string {
		if (!points.length) return '';
		const max = Math.max(...points.map((p) => p.value), 0.001);
		const w = CHART_W - CHART_PAD.left - CHART_PAD.right;
		const h = CHART_H - CHART_PAD.top - CHART_PAD.bottom;
		const bw = Math.max(4, Math.min(48, (w / points.length) * 0.7));
		const gap = w / points.length;
		return points.map((p, i) => {
			const bh = (p.value / max) * h;
			const x = CHART_PAD.left + gap * i + (gap - bw) / 2;
			const y = CHART_PAD.top + h - bh;
			const yl = CHART_PAD.top + h + 16;
			const val = p.value % 1 === 0 ? String(p.value) : p.value.toFixed(1);
			return `<rect x="${x.toFixed(1)}" y="${y.toFixed(1)}" width="${bw.toFixed(1)}" height="${bh.toFixed(1)}" rx="3" class="chart-bar"/>
				<text x="${(x + bw / 2).toFixed(1)}" y="${(y - 4).toFixed(1)}" class="chart-val" text-anchor="middle">${val}</text>
				<text x="${(x + bw / 2).toFixed(1)}" y="${yl}" class="chart-lbl" text-anchor="middle">${p.label}</text>`;
		}).join('\n');
	}

	function buildLineChart(points: ChartPoint[]): string {
		if (points.length < 2) return buildBarChart(points);
		const max = Math.max(...points.map((p) => p.value), 0.001);
		const min = Math.min(...points.map((p) => p.value), 0);
		const range = max - min || 1;
		const w = CHART_W - CHART_PAD.left - CHART_PAD.right;
		const h = CHART_H - CHART_PAD.top - CHART_PAD.bottom;
		const pts = points.map((p, i) => ({
			x: CHART_PAD.left + (i / (points.length - 1)) * w,
			y: CHART_PAD.top + h - ((p.value - min) / range) * h,
			label: p.label,
			value: p.value
		}));
		const path = pts.map((p, i) => `${i === 0 ? 'M' : 'L'} ${p.x.toFixed(1)} ${p.y.toFixed(1)}`).join(' ');
		const area = `${path} L ${pts[pts.length-1].x.toFixed(1)} ${(CHART_PAD.top+h).toFixed(1)} L ${CHART_PAD.left} ${(CHART_PAD.top+h).toFixed(1)} Z`;
		const dots = pts.map((p) => {
			const val = p.value % 1 === 0 ? String(p.value) : p.value.toFixed(1);
			return `<circle cx="${p.x.toFixed(1)}" cy="${p.y.toFixed(1)}" r="4" class="chart-dot"/>
				<text x="${p.x.toFixed(1)}" y="${(p.y - 8).toFixed(1)}" class="chart-val" text-anchor="middle">${val}</text>
				<text x="${p.x.toFixed(1)}" y="${(CHART_PAD.top+h+16).toFixed(1)}" class="chart-lbl" text-anchor="middle">${p.label}</text>`;
		}).join('\n');
		return `<path d="${area}" class="chart-area"/>
			<path d="${path}" class="chart-line"/>
			${dots}`;
	}
</script>

<svelte:window on:pointerup={onWindowPointerUp} />

<section class="sheet-board" aria-label="Taskboard table sheet">

	<!-- ── Toolbar ───────────────────────────────────────────────────── -->
	<header class="sheet-toolbar">
		<div>
			<h2>Table Sheet</h2>
			<p>Click to select · Double-click or type to edit · Shift+click to extend selection</p>
		</div>
		<div class="toolbar-meta">
			<span>{renderedCellCount} cells rendered</span>
			<span>100 × 100</span>
		</div>
	</header>

	<!-- ── Formula bar ───────────────────────────────────────────────── -->
	<div class="formula-bar">
		<div class="formula-ref">{selCellRef || '—'}</div>
		<div class="formula-sep"></div>
		<input
			bind:this={formulaBarRef}
			class="formula-input"
			type="text"
			value={formulaBarValue}
			placeholder="Cell value or =FORMULA(range)…"
			on:focus={onFormulaBarFocus}
			on:input={onFormulaBarInput}
			on:keydown={handleFormulaBarKeyDown}
			spellcheck="false"
		/>
		{#if isMultiSelect}
			<div class="formula-sel-info">{selSizeLabel}</div>
			<button class="formula-chart-btn" type="button" on:click={() => { showChart = true; chartType = 'bar'; }} title="Create bar chart">
				<svg viewBox="0 0 16 16"><rect x="1" y="9" width="3" height="6"/><rect x="6" y="5" width="3" height="10"/><rect x="11" y="2" width="3" height="13"/></svg>
				Bar
			</button>
			<button class="formula-chart-btn" type="button" on:click={() => { showChart = true; chartType = 'line'; }} title="Create line chart">
				<svg viewBox="0 0 16 16"><path d="M1 11l3-3 3 2 4-6 4 3" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>
				Line
			</button>
		{/if}
	</div>

	<!-- ── Grid ──────────────────────────────────────────────────────── -->
	<div class="sheet-grid-shell">
		<div class="sheet-corner" aria-hidden="true">#</div>

		<div class="sheet-col-headers" aria-hidden="true">
			{#each visibleCols as col (col)}
				<div
					class="sheet-col-header-cell"
					class:col-header-active={selRange && col >= selRange.c1 && col <= selRange.c2}
					style={`left:${col * COL_WIDTH - scrollLeft}px; width:${COL_WIDTH}px; height:${COL_HEADER_HEIGHT}px;`}
				>
					{columnLabel(col)}
				</div>
			{/each}
		</div>

		<div class="sheet-row-headers" aria-hidden="true">
			{#each visibleRows as row (row)}
				<div
					class="sheet-row-header-cell"
					class:row-header-active={selRange && row >= selRange.r1 && row <= selRange.r2}
					style={`top:${row * ROW_HEIGHT - scrollTop}px; height:${ROW_HEIGHT}px; width:${ROW_HEADER_WIDTH}px;`}
				>
					{row + 1}
				</div>
			{/each}
		</div>

		<div
			class="sheet-body-viewport"
			bind:this={bodyViewport}
			use:observeViewport
			on:scroll={handleViewportScroll}
		>
			<div class="sheet-body-canvas" style={`width:${TOTAL_WIDTH}px; height:${TOTAL_HEIGHT}px;`}>

				<!-- Selection overlay -->
				{#if selRange && selOverlayStyle}
					<div class="sel-overlay" style={selOverlayStyle} aria-hidden="true"></div>
				{/if}

				{#each visibleRows as row (row)}
					{#each visibleCols as col (col)}
						<div
							role="gridcell" tabindex="0" class="sheet-cell-shell"
							style={`top:${row * ROW_HEIGHT}px; left:${col * COL_WIDTH}px; width:${COL_WIDTH}px; height:${ROW_HEIGHT}px;`}
							on:mousedown={(e) => onCellPointerDown(row, col, e)}
							on:click={(e) => void onCellClick(row, col, e)}
							on:pointerenter={() => onCellPointerEnter(row, col)}
							on:dblclick|preventDefault|stopPropagation={() => void openCellEditor(row, col)}
							on:keydown={(e) => void onCellShellKeyDown(row, col, e)}
						>
							{#if isEditingCell(row, col)}
								<input
									bind:this={activeEditorRef}
									class="sheet-cell-editor"
									value={editValue}
									on:input={(e) => {
										editValue = (e.currentTarget as HTMLInputElement).value;
										formulaBarValue = editValue;
									}}
									on:keydown={handleEditorKeyDown}
									on:blur={commitEdit}
									spellcheck="false"
								/>
							{:else}
								<div
									class="sheet-cell"
									class:is-prefilled={hasPrefilledValue(row, col) && !userCellValues.has(cellKey(row,col))}
									class:is-selected={isCellInSelection(row, col)}
									class:is-anchor={isAnchor(row, col)}
									title={getCellRaw(row, col)}
								>
									<span>{getCellDisplay(row, col)}</span>
								</div>
							{/if}
						</div>
					{/each}
				{/each}
			</div>
		</div>
	</div>

	<!-- ── Supported formulas hint ────────────────────────────────────── -->
	<div class="formula-hint">
		<span>Formulas:</span>
		{#each ['=SUM(A1:B3)', '=AVG(C1:C10)', '=COUNT(A1:A20)', '=MAX(B1:B5)', '=MIN(B1:B5)', '=PRODUCT(A1:A3)'] as f (f)}
			<button type="button" class="hint-chip" on:click={() => { formulaBarValue = f; formulaBarRef?.focus(); }}>{f}</button>
		{/each}
	</div>

</section>

<!-- ── Chart modal ────────────────────────────────────────────────── -->
{#if showChart && chartData.length > 0}
	<!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
	<div class="chart-backdrop" on:click={() => (showChart = false)}>
		<!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
		<div class="chart-modal" on:click|stopPropagation>
			<div class="chart-modal-header">
				<span>Chart — {selSizeLabel}</span>
				<div class="chart-type-btns">
					<button type="button" class:active={chartType === 'bar'}   on:click={() => (chartType = 'bar')}>Bar</button>
					<button type="button" class:active={chartType === 'line'}  on:click={() => (chartType = 'line')}>Line</button>
				</div>
				<button type="button" class="chart-close" on:click={() => (showChart = false)}>✕</button>
			</div>
			{#if chartData.length === 0}
				<p class="chart-empty">No numeric values in selected range.</p>
			{:else}
				<svg
					class="chart-svg"
					viewBox={`0 0 ${CHART_W} ${CHART_H}`}
					aria-label="Chart"
				>
					<!-- Y axis -->
					<line
						x1={CHART_PAD.left} y1={CHART_PAD.top}
						x2={CHART_PAD.left} y2={CHART_PAD.top + CHART_H - CHART_PAD.top - CHART_PAD.bottom}
						class="chart-axis"
					/>
					<!-- X axis -->
					<line
						x1={CHART_PAD.left} y1={CHART_PAD.top + CHART_H - CHART_PAD.top - CHART_PAD.bottom}
						x2={CHART_W - CHART_PAD.right} y2={CHART_PAD.top + CHART_H - CHART_PAD.top - CHART_PAD.bottom}
						class="chart-axis"
					/>
					<!-- eslint-disable-next-line svelte/no-at-html-tags -->
				{@html chartType === 'bar' ? buildBarChart(chartData) : buildLineChart(chartData)}
				</svg>
			{/if}
		</div>
	</div>
{/if}

<style>
/* ── Theme ────────────────────────────────────────────────────────── */
:global(:root) {
	--sheet-bg:         #f2f4f8;
	--sheet-panel:      #ffffff;
	--sheet-border:     #d6dbe6;
	--sheet-text:       #1b2538;
	--sheet-muted:      #5f6b84;
	--sheet-header-bg:  #1b2332;
	--sheet-header-text:#edf3ff;
	--sheet-grid-line:  rgba(26,36,54,0.12);
	--sheet-cell-bg:    #ffffff;
	--sheet-cell-hover: #f5f8ff;
	--sheet-prefill-bg: #f1f6ff;
	--sheet-sel-bg:     rgba(66,133,244,0.1);
	--sheet-sel-border: #4285f4;
	--sheet-anchor-shadow: inset 0 0 0 2px #4285f4;
	--sheet-editor-bg:  #ffffff;
	--sheet-editor-border: #7092c7;
	--sheet-toolbar-bg: #ffffff;
	--sheet-formula-bg: #ffffff;
	--sheet-accent:     #1a73e8;
	--sheet-chart-bar:  #4285f4;
	--sheet-chart-line: #0f9d58;
	--sheet-chart-area: rgba(15,157,88,0.12);
}

:global(:root[data-theme='dark']),
:global(.theme-dark) {
	--sheet-bg:         #18181b;
	--sheet-panel:      #1f2025;
	--sheet-border:     rgba(255,255,255,0.14);
	--sheet-text:       #eef2ff;
	--sheet-muted:      #a8b1c6;
	--sheet-header-bg:  #111318;
	--sheet-header-text:#f3f7ff;
	--sheet-grid-line:  rgba(255,255,255,0.1);
	--sheet-cell-bg:    #1d1f25;
	--sheet-cell-hover: #262934;
	--sheet-prefill-bg: #262d3a;
	--sheet-sel-bg:     rgba(66,133,244,0.18);
	--sheet-sel-border: #7ab5ff;
	--sheet-anchor-shadow: inset 0 0 0 2px #7ab5ff;
	--sheet-editor-bg:  #101319;
	--sheet-editor-border: #86a8dc;
	--sheet-toolbar-bg: #1f2025;
	--sheet-formula-bg: #16181e;
	--sheet-accent:     #7ab5ff;
	--sheet-chart-bar:  #7ab5ff;
	--sheet-chart-line: #4ade80;
	--sheet-chart-area: rgba(74,222,128,0.12);
}

/* ── Board shell ──────────────────────────────────────────────────── */
.sheet-board {
	height: 100%;
	min-height: 0;
	display: grid;
	grid-template-rows: auto auto minmax(0,1fr) auto;
	gap: 0.5rem;
	padding: 0.85rem;
	background: var(--sheet-bg);
}

/* ── Toolbar ──────────────────────────────────────────────────────── */
.sheet-toolbar {
	display: flex;
	justify-content: space-between;
	align-items: center;
	gap: 0.7rem;
	border-radius: 10px;
	border: 1px solid var(--sheet-border);
	background: var(--sheet-toolbar-bg);
	padding: 0.56rem 0.78rem;
}
.sheet-toolbar h2 {
	margin: 0;
	font-size: 0.88rem;
	font-weight: 700;
	color: var(--sheet-text);
}
.sheet-toolbar p {
	margin: 0.14rem 0 0;
	font-size: 0.72rem;
	color: var(--sheet-muted);
}
.toolbar-meta {
	display: flex;
	align-items: center;
	gap: 0.4rem;
}
.toolbar-meta span {
	height: 1.5rem;
	display: inline-flex;
	align-items: center;
	padding: 0 0.52rem;
	border-radius: 999px;
	border: 1px solid var(--sheet-border);
	font-size: 0.68rem;
	font-weight: 700;
	color: var(--sheet-muted);
	background: var(--sheet-panel);
}

/* ── Formula bar ──────────────────────────────────────────────────── */
.formula-bar {
	display: flex;
	align-items: center;
	gap: 0;
	border: 1px solid var(--sheet-border);
	border-radius: 10px;
	background: var(--sheet-formula-bg);
	overflow: hidden;
	height: 2.1rem;
}
.formula-ref {
	flex-shrink: 0;
	width: 64px;
	text-align: center;
	font-size: 0.78rem;
	font-weight: 700;
	color: var(--sheet-text);
	border-right: 1px solid var(--sheet-border);
	padding: 0 0.5rem;
	height: 100%;
	display: flex;
	align-items: center;
	justify-content: center;
	font-family: monospace;
}
.formula-sep {
	width: 1px;
	height: 60%;
	background: var(--sheet-border);
	flex-shrink: 0;
	margin: 0 0.4rem;
}
.formula-input {
	flex: 1;
	min-width: 0;
	border: none;
	background: transparent;
	color: var(--sheet-text);
	font-size: 0.84rem;
	font-family: monospace;
	padding: 0 0.5rem;
	height: 100%;
	outline: none;
}
.formula-sel-info {
	font-size: 0.72rem;
	color: var(--sheet-muted);
	padding: 0 0.6rem;
	white-space: nowrap;
	border-left: 1px solid var(--sheet-border);
	height: 100%;
	display: flex;
	align-items: center;
}
.formula-chart-btn {
	display: flex;
	align-items: center;
	gap: 0.3rem;
	padding: 0 0.75rem;
	height: 100%;
	border: none;
	border-left: 1px solid var(--sheet-border);
	background: var(--sheet-accent);
	color: #fff;
	font-size: 0.76rem;
	font-weight: 600;
	cursor: pointer;
	white-space: nowrap;
}
.formula-chart-btn svg {
	width: 14px;
	height: 14px;
	fill: currentColor;
}
.formula-chart-btn:hover { opacity: 0.88; }

/* ── Grid shell ───────────────────────────────────────────────────── */
.sheet-grid-shell {
	min-height: 0;
	display: grid;
	grid-template-columns: 54px minmax(0,1fr);
	grid-template-rows: 36px minmax(0,1fr);
	border-radius: 10px;
	border: 1px solid var(--sheet-border);
	overflow: hidden;
	background: var(--sheet-panel);
}
.sheet-corner {
	grid-column: 1; grid-row: 1;
	display: grid; place-items: center;
	background: var(--sheet-header-bg);
	color: var(--sheet-header-text);
	font-size: 0.7rem; font-weight: 700;
	border-right: 1px solid var(--sheet-grid-line);
	border-bottom: 1px solid var(--sheet-grid-line);
}
.sheet-col-headers {
	grid-column: 2; grid-row: 1;
	position: relative; overflow: hidden;
	background: var(--sheet-header-bg);
	border-bottom: 1px solid var(--sheet-grid-line);
}
.sheet-col-header-cell {
	position: absolute; top: 0;
	display: grid; place-items: center;
	font-size: 0.68rem; font-weight: 700;
	letter-spacing: 0.04em; text-transform: uppercase;
	color: var(--sheet-header-text);
	border-right: 1px solid var(--sheet-grid-line);
	transition: background 0.1s;
}
.sheet-col-header-cell.col-header-active {
	background: rgba(66,133,244,0.22);
}
.sheet-row-headers {
	grid-column: 1; grid-row: 2;
	position: relative; overflow: hidden;
	background: var(--sheet-header-bg);
	border-right: 1px solid var(--sheet-grid-line);
}
.sheet-row-header-cell {
	position: absolute; left: 0;
	display: grid; place-items: center;
	font-size: 0.66rem; font-weight: 700;
	color: var(--sheet-header-text);
	border-bottom: 1px solid var(--sheet-grid-line);
	transition: background 0.1s;
}
.sheet-row-header-cell.row-header-active {
	background: rgba(66,133,244,0.22);
}
.sheet-body-viewport {
	grid-column: 2; grid-row: 2;
	min-height: 0; overflow: auto;
	scrollbar-width: thin;
	background: var(--sheet-panel);
	cursor: default;
}
.sheet-body-canvas { position: relative; }

/* ── Cell shell ───────────────────────────────────────────────────── */
.sheet-cell-shell {
	position: absolute; padding: 0;
	user-select: none;
}

/* ── Cell display ─────────────────────────────────────────────────── */
.sheet-cell {
	width: 100%; height: 100%;
	display: flex; align-items: center;
	padding: 0 0.5rem;
	font-size: 0.78rem;
	border-right: 1px solid var(--sheet-grid-line);
	border-bottom: 1px solid var(--sheet-grid-line);
	background: var(--sheet-cell-bg);
	color: var(--sheet-text);
	transition: background 0.08s;
	cursor: cell;
}
.sheet-cell span {
	overflow: hidden; text-overflow: ellipsis; white-space: nowrap; width: 100%;
}
.sheet-cell:hover { background: var(--sheet-cell-hover); }
.sheet-cell.is-prefilled { background: var(--sheet-prefill-bg); }
.sheet-cell.is-selected {
	background: var(--sheet-sel-bg);
}
.sheet-cell.is-anchor {
	background: var(--sheet-sel-bg);
	box-shadow: var(--sheet-anchor-shadow);
	z-index: 2;
}

/* ── Selection overlay ────────────────────────────────────────────── */
.sel-overlay {
	position: absolute;
	pointer-events: none;
	border: 2px solid var(--sheet-sel-border);
	border-radius: 1px;
	z-index: 3;
}

/* ── Cell editor ──────────────────────────────────────────────────── */
.sheet-cell-editor {
	width: 100%; height: 100%;
	border: none;
	border-right: 1px solid var(--sheet-grid-line);
	border-bottom: 1px solid var(--sheet-grid-line);
	padding: 0 0.5rem;
	font-size: 0.78rem;
	font-family: inherit;
	background: var(--sheet-editor-bg);
	color: var(--sheet-text);
	outline: none;
	box-shadow: inset 0 0 0 2px var(--sheet-editor-border);
	z-index: 4;
}

/* ── Formula hint bar ─────────────────────────────────────────────── */
.formula-hint {
	display: flex;
	align-items: center;
	gap: 0.35rem;
	flex-wrap: wrap;
	padding: 0 0.1rem;
}
.formula-hint span {
	font-size: 0.72rem;
	color: var(--sheet-muted);
	font-weight: 600;
	flex-shrink: 0;
}
.hint-chip {
	font-size: 0.7rem;
	font-family: monospace;
	padding: 0.18rem 0.5rem;
	border-radius: 6px;
	border: 1px solid var(--sheet-border);
	background: var(--sheet-panel);
	color: var(--sheet-accent);
	cursor: pointer;
}
.hint-chip:hover {
	background: var(--sheet-sel-bg);
	border-color: var(--sheet-sel-border);
}

/* ── Chart modal ──────────────────────────────────────────────────── */
.chart-backdrop {
	position: fixed; inset: 0; z-index: 1000;
	background: rgba(0,0,0,0.45);
	display: grid; place-items: center;
}
.chart-modal {
	background: var(--sheet-panel);
	border: 1px solid var(--sheet-border);
	border-radius: 14px;
	padding: 1.25rem 1.5rem;
	display: flex; flex-direction: column; gap: 1rem;
	min-width: 540px; max-width: 95vw;
}
.chart-modal-header {
	display: flex; align-items: center; gap: 0.75rem;
}
.chart-modal-header span {
	flex: 1; font-size: 0.9rem; font-weight: 700; color: var(--sheet-text);
}
.chart-type-btns {
	display: flex; gap: 0.25rem;
}
.chart-type-btns button {
	padding: 0.25rem 0.7rem; border-radius: 7px;
	border: 1px solid var(--sheet-border);
	background: var(--sheet-panel); color: var(--sheet-muted);
	font-size: 0.78rem; font-weight: 600; cursor: pointer;
}
.chart-type-btns button.active {
	background: var(--sheet-accent); color: #fff; border-color: var(--sheet-accent);
}
.chart-close {
	border: none; background: transparent; color: var(--sheet-muted);
	font-size: 1rem; cursor: pointer; padding: 0.2rem;
}
.chart-close:hover { color: var(--sheet-text); }
.chart-empty {
	margin: 0; color: var(--sheet-muted); font-size: 0.85rem; text-align: center; padding: 1rem 0;
}
.chart-svg {
	width: 100%; height: auto;
	border-radius: 8px;
	background: color-mix(in srgb, var(--sheet-panel) 60%, var(--sheet-prefill-bg) 40%);
}

/* Chart SVG elements (global since they're injected via @html) */
:global(.chart-bar) {
	fill: var(--sheet-chart-bar, #4285f4);
	opacity: 0.85;
}
:global(.chart-line) {
	fill: none;
	stroke: var(--sheet-chart-line, #0f9d58);
	stroke-width: 2.5;
	stroke-linecap: round;
	stroke-linejoin: round;
}
:global(.chart-area) {
	fill: var(--sheet-chart-area, rgba(15,157,88,0.12));
}
:global(.chart-dot) {
	fill: var(--sheet-panel, #fff);
	stroke: var(--sheet-chart-line, #0f9d58);
	stroke-width: 2;
}
:global(.chart-val) {
	font-size: 9px;
	fill: var(--sheet-text, #1b2538);
	font-weight: 600;
}
:global(.chart-lbl) {
	font-size: 9px;
	fill: var(--sheet-muted, #5f6b84);
}
:global(.chart-axis) {
	stroke: var(--sheet-grid-line, rgba(26,36,54,0.12));
	stroke-width: 1;
}

@media (max-width: 640px) {
	.chart-modal { min-width: min(540px, 94vw); padding: 1rem; }
	.formula-bar { flex-wrap: wrap; height: auto; padding: 0.35rem 0; }
}
</style>
