<script lang="ts">
	import { onMount, tick } from 'svelte';

	// ── Grid dimensions ─────────────────────────────────────────
	const DEFAULT_COLS = 26;   // A–Z
	const DEFAULT_ROWS = 50;
	const EXPAND_BY = 20;

	let numCols = DEFAULT_COLS;
	let numRows = DEFAULT_ROWS;

	// ── Cell storage ─────────────────────────────────────────────
	// raw string values (formulas start with '=')
	let cells = new Map<string, string>();

	// ── Column widths (px) ───────────────────────────────────────
	let colWidths = Array.from({ length: DEFAULT_COLS }, () => 100);

	// ── Selection state ──────────────────────────────────────────
	let anchor: [number, number] | null = null;  // [col, row] 0-indexed
	let focus: [number, number] | null = null;
	let isSelecting = false;

	// ── Editing state ────────────────────────────────────────────
	let editCell: string | null = null;  // "A1"
	let editValue = '';
	let editInputEl: HTMLInputElement | null = null;

	// ── Formula bar ──────────────────────────────────────────────
	let formulaBarValue = '';
	$: {
		if (anchor) {
			const key = cellKey(anchor[0], anchor[1]);
			formulaBarValue = editCell === key ? editValue : (cells.get(key) ?? '');
		}
	}

	// ── Chart state ─────────────────────────────────────────────
	type ChartType = 'bar' | 'line';
	let chartOpen = false;
	let chartType: ChartType = 'bar';
	let chartRangeInput = '';
	let chartTitle = '';
	let chartData: { label: string; value: number }[] = [];
	let chartError = '';

	// ── Column resize ────────────────────────────────────────────
	let resizingCol: number | null = null;
	let resizeStartX = 0;
	let resizeStartW = 0;

	// ── Helpers ──────────────────────────────────────────────────
	function colLetter(c: number): string {
		// support up to ZZ (702 cols)
		if (c < 26) return String.fromCharCode(65 + c);
		return String.fromCharCode(65 + Math.floor(c / 26) - 1) + String.fromCharCode(65 + (c % 26));
	}

	function colIndex(letter: string): number {
		const upper = letter.toUpperCase();
		if (upper.length === 1) return upper.charCodeAt(0) - 65;
		return (upper.charCodeAt(0) - 64) * 26 + (upper.charCodeAt(1) - 65);
	}

	function cellKey(c: number, r: number): string {
		return `${colLetter(c)}${r + 1}`;
	}

	function parseCellKey(key: string): [number, number] | null {
		const m = key.match(/^([A-Z]{1,2})(\d+)$/i);
		if (!m) return null;
		return [colIndex(m[1].toUpperCase()), parseInt(m[2]) - 1];
	}

	function rangeKeys(from: string, to: string): string[] {
		const a = parseCellKey(from);
		const b = parseCellKey(to);
		if (!a || !b) return [];
		const c1 = Math.min(a[0], b[0]);
		const c2 = Math.max(a[0], b[0]);
		const r1 = Math.min(a[1], b[1]);
		const r2 = Math.max(a[1], b[1]);
		const result: string[] = [];
		for (let r = r1; r <= r2; r++) {
			for (let c = c1; c <= c2; c++) {
				result.push(cellKey(c, r));
			}
		}
		return result;
	}

	function getCellValue(key: string): number | string {
		const raw = cells.get(key) ?? '';
		if (!raw) return '';
		if (raw.startsWith('=')) return evalFormula(raw.slice(1));
		const n = Number(raw);
		return isNaN(n) ? raw : n;
	}

	function getNumericValue(key: string): number {
		const v = getCellValue(key);
		if (typeof v === 'number') return v;
		const n = Number(v);
		return isNaN(n) ? 0 : n;
	}

	// ── Formula evaluator ────────────────────────────────────────
	function evalFormula(expr: string): number | string {
		try {
			return evalExpr(expr.trim());
		} catch {
			return '#ERR';
		}
	}

	function evalExpr(expr: string): number | string {
		expr = expr.trim();

		// Functions
		const fnMatch = expr.match(/^([A-Z]+)\((.+)\)$/i);
		if (fnMatch) {
			const fn = fnMatch[1].toUpperCase();
			const args = fnMatch[2];
			return evalFunction(fn, args);
		}

		// Comparison / conditional (simple A1>B1 style)
		const cmpMatch = expr.match(/^(.+?)(>=|<=|<>|>|<|=)(.+)$/);
		if (cmpMatch) {
			const left = evalExpr(cmpMatch[1]);
			const right = evalExpr(cmpMatch[3]);
			const op = cmpMatch[2];
			const l = Number(left), r = Number(right);
			if (op === '>') return l > r ? 1 : 0;
			if (op === '<') return l < r ? 1 : 0;
			if (op === '>=') return l >= r ? 1 : 0;
			if (op === '<=') return l <= r ? 1 : 0;
			if (op === '<>' || op === '!=') return l !== r ? 1 : 0;
			if (op === '=') return l === r ? 1 : 0;
		}

		// Addition / subtraction (right-to-left scan avoids breaking negative numbers)
		for (let i = expr.length - 1; i >= 0; i--) {
			if ((expr[i] === '+' || expr[i] === '-') && i > 0) {
				const left = evalExpr(expr.slice(0, i));
				const right = evalExpr(expr.slice(i + 1));
				if (typeof left === 'number' && typeof right === 'number') {
					return expr[i] === '+' ? left + right : left - right;
				}
				if (expr[i] === '+') return String(left) + String(right);
			}
		}

		// Multiplication / division
		for (let i = expr.length - 1; i >= 0; i--) {
			if (expr[i] === '*' || expr[i] === '/') {
				const left = Number(evalExpr(expr.slice(0, i)));
				const right = Number(evalExpr(expr.slice(i + 1)));
				return expr[i] === '*' ? left * right : left / right;
			}
		}

		// Exponentiation
		if (expr.includes('^')) {
			const [b, e] = expr.split('^');
			return Math.pow(Number(evalExpr(b)), Number(evalExpr(e)));
		}

		// Parentheses
		if (expr.startsWith('(') && expr.endsWith(')')) {
			return evalExpr(expr.slice(1, -1));
		}

		// Cell reference
		if (/^[A-Z]{1,2}\d+$/i.test(expr)) {
			return getCellValue(expr.toUpperCase());
		}

		// String literal
		if (expr.startsWith('"') && expr.endsWith('"')) {
			return expr.slice(1, -1);
		}

		// Number
		const n = Number(expr);
		if (!isNaN(n)) return n;

		return '#REF';
	}

	function evalFunction(fn: string, rawArgs: string): number | string {
		// Parse range or comma-separated args
		const getValues = (a: string): number[] => {
			if (a.includes(':')) {
				return rangeKeys(a.split(':')[0].trim(), a.split(':')[1].trim())
					.map(getNumericValue)
					.filter((v) => !isNaN(v));
			}
			return a.split(',').map((x) => Number(evalExpr(x.trim()))).filter((v) => !isNaN(v));
		};

		switch (fn) {
			case 'SUM': return getValues(rawArgs).reduce((a, b) => a + b, 0);
			case 'AVERAGE': {
				const vals = getValues(rawArgs);
				return vals.length ? vals.reduce((a, b) => a + b, 0) / vals.length : 0;
			}
			case 'COUNT': return getValues(rawArgs).length;
			case 'COUNTA': {
				if (rawArgs.includes(':')) {
					return rangeKeys(rawArgs.split(':')[0].trim(), rawArgs.split(':')[1].trim())
						.filter((k) => (cells.get(k) ?? '').trim() !== '').length;
				}
				return 0;
			}
			case 'MIN': return Math.min(...getValues(rawArgs));
			case 'MAX': return Math.max(...getValues(rawArgs));
			case 'ABS': return Math.abs(Number(evalExpr(rawArgs)));
			case 'ROUND': {
				const p = rawArgs.split(',');
				return parseFloat(Number(evalExpr(p[0])).toFixed(Number(evalExpr(p[1] ?? '0'))));
			}
			case 'SQRT': return Math.sqrt(Number(evalExpr(rawArgs)));
			case 'POWER': {
				const p = rawArgs.split(',');
				return Math.pow(Number(evalExpr(p[0])), Number(evalExpr(p[1])));
			}
			case 'IF': {
				const parts = splitArgs(rawArgs);
				const condition = Number(evalExpr(parts[0]));
				return condition ? evalExpr(parts[1] ?? '0') : evalExpr(parts[2] ?? '0');
			}
			case 'CONCAT':
			case 'CONCATENATE': {
				return splitArgs(rawArgs).map((a) => String(evalExpr(a.trim()))).join('');
			}
			case 'LEN': return String(evalExpr(rawArgs)).length;
			case 'UPPER': return String(evalExpr(rawArgs)).toUpperCase();
			case 'LOWER': return String(evalExpr(rawArgs)).toLowerCase();
			default: return `#NAME(${fn})`;
		}
	}

	function splitArgs(raw: string): string[] {
		// Split on commas not inside parentheses
		const args: string[] = [];
		let depth = 0, cur = '';
		for (const ch of raw) {
			if (ch === '(') depth++;
			else if (ch === ')') depth--;
			if (ch === ',' && depth === 0) { args.push(cur); cur = ''; }
			else cur += ch;
		}
		if (cur) args.push(cur);
		return args;
	}

	// ── Display value ────────────────────────────────────────────
	function displayValue(key: string): string {
		const raw = cells.get(key) ?? '';
		if (!raw) return '';
		if (raw.startsWith('=')) {
			const v = evalFormula(raw.slice(1));
			if (typeof v === 'number') {
				// Nice number formatting
				if (Number.isInteger(v)) return String(v);
				return parseFloat(v.toFixed(8)).toString();
			}
			return String(v);
		}
		return raw;
	}

	// ── Selection helpers ────────────────────────────────────────
	function selectionBounds(): { c1: number; r1: number; c2: number; r2: number } | null {
		if (!anchor || !focus) return anchor ? { c1: anchor[0], r1: anchor[1], c2: anchor[0], r2: anchor[1] } : null;
		return {
			c1: Math.min(anchor[0], focus[0]),
			r1: Math.min(anchor[1], focus[1]),
			c2: Math.max(anchor[0], focus[0]),
			r2: Math.max(anchor[1], focus[1])
		};
	}

	function isCellSelected(c: number, r: number): boolean {
		const b = selectionBounds();
		if (!b) return false;
		return c >= b.c1 && c <= b.c2 && r >= b.r1 && r <= b.r2;
	}

	function isAnchorCell(c: number, r: number): boolean {
		return anchor ? anchor[0] === c && anchor[1] === r : false;
	}

	// ── Cell interaction ─────────────────────────────────────────
	async function selectCell(c: number, r: number, shift = false) {
		if (editCell) await commitEdit();
		if (shift && anchor) {
			focus = [c, r];
		} else {
			anchor = [c, r];
			focus = null;
		}
	}

	function startEdit(c: number, r: number) {
		const key = cellKey(c, r);
		editCell = key;
		editValue = cells.get(key) ?? '';
		anchor = [c, r];
		focus = null;
		tick().then(() => editInputEl?.focus());
	}

	async function commitEdit() {
		if (!editCell) return;
		if (editValue.trim() === '') {
			cells.delete(editCell);
		} else {
			cells.set(editCell, editValue);
		}
		cells = new Map(cells);
		editCell = null;
	}

	function cancelEdit() {
		editCell = null;
		editValue = '';
	}

	async function handleCellKeydown(event: KeyboardEvent, c: number, r: number) {
		if (event.key === 'Escape') {
			cancelEdit();
			return;
		}
		if (event.key === 'Enter' || event.key === 'Tab') {
			event.preventDefault();
			await commitEdit();
			if (event.key === 'Enter') {
				const nextR = r + 1 < numRows ? r + 1 : r;
				anchor = [c, nextR];
				focus = null;
			} else {
				const nextC = c + 1 < numCols ? c + 1 : c;
				anchor = [nextC, r];
				focus = null;
			}
			return;
		}
	}

	async function handleGridKeydown(event: KeyboardEvent) {
		if (!anchor || editCell) return;
		const [c, r] = anchor;
		if (event.key === 'Delete' || event.key === 'Backspace') {
			const b = selectionBounds();
			if (b) {
				for (let row = b.r1; row <= b.r2; row++) {
					for (let col = b.c1; col <= b.c2; col++) {
						cells.delete(cellKey(col, row));
					}
				}
				cells = new Map(cells);
			}
			return;
		}
		if (event.key === 'ArrowUp' && r > 0) { event.preventDefault(); anchor = [c, r - 1]; focus = event.shiftKey ? focus ?? [c, r] : null; if (!event.shiftKey) focus = null; else if (!focus) focus = [c, r]; if (event.shiftKey) focus = [c, Math.max(0, r - 1)]; else { anchor = [c, r - 1]; focus = null; } return; }
		if (event.key === 'ArrowDown' && r < numRows - 1) { event.preventDefault(); if (event.shiftKey) { focus = [c, (focus ?? anchor)[1] + 1]; } else { anchor = [c, r + 1]; focus = null; } return; }
		if (event.key === 'ArrowLeft' && c > 0) { event.preventDefault(); if (event.shiftKey) { focus = [(focus ?? anchor)[0] - 1, r]; } else { anchor = [c - 1, r]; focus = null; } return; }
		if (event.key === 'ArrowRight' && c < numCols - 1) { event.preventDefault(); if (event.shiftKey) { focus = [(focus ?? anchor)[0] + 1, r]; } else { anchor = [c + 1, r]; focus = null; } return; }
		// Start typing to enter edit mode
		if (event.key.length === 1 && !event.ctrlKey && !event.metaKey) {
			startEdit(c, r);
			editValue = event.key;
			return;
		}
		if (event.key === 'F2' || event.key === 'Enter') {
			event.preventDefault();
			startEdit(c, r);
			return;
		}
	}

	// ── Column resize ────────────────────────────────────────────
	function startColResize(event: MouseEvent, colIdx: number) {
		event.preventDefault();
		resizingCol = colIdx;
		resizeStartX = event.clientX;
		resizeStartW = colWidths[colIdx];
	}

	function onMouseMove(event: MouseEvent) {
		if (resizingCol === null) return;
		const delta = event.clientX - resizeStartX;
		colWidths[resizingCol] = Math.max(40, resizeStartW + delta);
		colWidths = [...colWidths];
	}

	function onMouseUp() {
		resizingCol = null;
	}

	// ── Add rows/cols ────────────────────────────────────────────
	function addRows() {
		numRows += EXPAND_BY;
	}

	function addCols() {
		numCols = Math.min(numCols + 26, 702);
		while (colWidths.length < numCols) colWidths.push(100);
		colWidths = [...colWidths];
	}

	// ── Clipboard ────────────────────────────────────────────────
	function handleCopy(event: ClipboardEvent) {
		const b = selectionBounds();
		if (!b) return;
		const rows: string[] = [];
		for (let r = b.r1; r <= b.r2; r++) {
			const row: string[] = [];
			for (let c = b.c1; c <= b.c2; c++) {
				row.push(displayValue(cellKey(c, r)));
			}
			rows.push(row.join('\t'));
		}
		event.clipboardData?.setData('text/plain', rows.join('\n'));
		event.preventDefault();
	}

	function handlePaste(event: ClipboardEvent) {
		if (!anchor) return;
		const text = event.clipboardData?.getData('text/plain') ?? '';
		const rows = text.split('\n');
		rows.forEach((row, ri) => {
			row.split('\t').forEach((val, ci) => {
				const key = cellKey(anchor![0] + ci, anchor![1] + ri);
				if (val.trim()) cells.set(key, val.trim());
			});
		});
		cells = new Map(cells);
		event.preventDefault();
	}

	// ── Formula bar input ────────────────────────────────────────
	function handleFormulaBarInput(event: Event) {
		const v = (event.currentTarget as HTMLInputElement).value;
		formulaBarValue = v;
		if (anchor) {
			const key = cellKey(anchor[0], anchor[1]);
			if (editCell !== key) {
				editCell = key;
			}
			editValue = v;
		}
	}

	async function commitFormulaBar() {
		await commitEdit();
	}

	// ── Chart builder ────────────────────────────────────────────
	function buildChart() {
		chartError = '';
		const input = chartRangeInput.trim().toUpperCase();
		if (!input) { chartError = 'Enter a cell range (e.g. A1:A10)'; return; }

		let keys: string[];
		if (input.includes(':')) {
			const [from, to] = input.split(':');
			keys = rangeKeys(from.trim(), to.trim());
		} else {
			keys = [input];
		}

		chartData = keys
			.map((k, i) => {
				const v = getCellValue(k);
				const num = typeof v === 'number' ? v : Number(v);
				if (isNaN(num)) return null;
				// Try to get label from the column to the left
				const pos = parseCellKey(k);
				let label = k;
				if (pos && pos[0] > 0) {
					const leftKey = cellKey(pos[0] - 1, pos[1]);
					const leftVal = cells.get(leftKey) ?? '';
					if (leftVal && !leftVal.startsWith('=')) label = leftVal;
				}
				return { label, value: num };
			})
			.filter(Boolean) as { label: string; value: number }[];

		if (chartData.length === 0) {
			chartError = 'No numeric data found in range.';
		}
	}

	// ── Chart rendering helpers ──────────────────────────────────
	const CHART_W = 420;
	const CHART_H = 240;
	const CHART_PAD = { top: 24, right: 16, bottom: 48, left: 50 };

	$: chartMax = chartData.length ? Math.max(...chartData.map((d) => d.value)) * 1.1 : 1;
	$: chartMin = chartData.length ? Math.min(0, Math.min(...chartData.map((d) => d.value))) : 0;
	$: chartRange_val = chartMax - chartMin || 1;
	$: innerW = CHART_W - CHART_PAD.left - CHART_PAD.right;
	$: innerH = CHART_H - CHART_PAD.top - CHART_PAD.bottom;

	function chartY(v: number): number {
		return CHART_PAD.top + innerH - ((v - chartMin) / chartRange_val) * innerH;
	}
	function chartX(i: number): number {
		return CHART_PAD.left + (i / Math.max(chartData.length - 1, 1)) * innerW;
	}
	function barX(i: number): number {
		const bw = innerW / chartData.length;
		return CHART_PAD.left + i * bw + bw * 0.1;
	}
	function barW(): number {
		return (innerW / chartData.length) * 0.8;
	}
	function barH(v: number): number {
		return ((v - chartMin) / chartRange_val) * innerH;
	}
	function barY(v: number): number {
		return chartY(Math.max(v, chartMin));
	}

	$: linePoints = chartData.map((d, i) => `${chartX(i)},${chartY(d.value)}`).join(' ');

	// Y-axis ticks
	$: yTicks = (() => {
		const count = 5;
		const step = chartRange_val / count;
		return Array.from({ length: count + 1 }, (_, i) => chartMin + i * step);
	})();

	// ── Grid container ref ───────────────────────────────────────
	let gridContainer: HTMLElement;

	onMount(() => {
		// Pre-fill with sample data for demo
	});
</script>

<!-- svelte-ignore a11y-no-static-element-interactions -->
<div
	class="sg-root"
	on:keydown={handleGridKeydown}
	on:copy={handleCopy}
	on:paste={handlePaste}
	on:mousemove={onMouseMove}
	on:mouseup={onMouseUp}
	tabindex="0"
	aria-label="Spreadsheet"
>
	<!-- ── Formula bar ───────────────────────────────────────── -->
	<div class="sg-formula-bar">
		<div class="sg-cell-ref">
			{#if anchor}
				{cellKey(anchor[0], anchor[1])}
			{:else}
				—
			{/if}
		</div>
		<div class="sg-formula-sep" aria-hidden="true"></div>
		<input
			class="sg-formula-input"
			type="text"
			value={formulaBarValue}
			placeholder="Enter value or formula (=SUM(A1:A5))"
			on:input={handleFormulaBarInput}
			on:keydown={(e) => {
				if (e.key === 'Enter') void commitFormulaBar();
				if (e.key === 'Escape') cancelEdit();
			}}
			aria-label="Formula bar"
		/>
		<div class="sg-toolbar-btns">
			<button
				type="button"
				class="sg-tool-btn"
				class:is-active={chartOpen}
				on:click={() => (chartOpen = !chartOpen)}
				title="Insert chart"
			>
				<svg viewBox="0 0 24 24" aria-hidden="true"
					><path d="M3 3v18h18M9 9l3 3 4-4 3 3"></path></svg
				>
				Chart
			</button>
			<button type="button" class="sg-tool-btn" on:click={addRows} title="Add 20 rows">
				<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14"></path></svg>
				Rows
			</button>
			<button type="button" class="sg-tool-btn" on:click={addCols} title="Add 26 columns">
				<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14"></path></svg>
				Cols
			</button>
		</div>
	</div>

	<!-- ── Chart panel ───────────────────────────────────────── -->
	{#if chartOpen}
		<div class="sg-chart-panel">
			<div class="sg-chart-controls">
				<label class="sg-chart-label">
					Range
					<input
						class="sg-chart-input"
						type="text"
						bind:value={chartRangeInput}
						placeholder="A1:A10"
					/>
				</label>
				<label class="sg-chart-label">
					Title
					<input class="sg-chart-input" type="text" bind:value={chartTitle} placeholder="Chart title" />
				</label>
				<div class="sg-chart-type-btns">
					<button
						type="button"
						class="sg-type-btn"
						class:is-active={chartType === 'bar'}
						on:click={() => (chartType = 'bar')}
					>Bar</button>
					<button
						type="button"
						class="sg-type-btn"
						class:is-active={chartType === 'line'}
						on:click={() => (chartType = 'line')}
					>Line</button>
				</div>
				<button type="button" class="sg-build-btn" on:click={buildChart}>Build</button>
			</div>

			{#if chartError}
				<p class="sg-chart-error">{chartError}</p>
			{/if}

			{#if chartData.length > 0}
				<div class="sg-chart-wrap">
					{#if chartTitle}
						<p class="sg-chart-title">{chartTitle}</p>
					{/if}
					<svg
						class="sg-chart-svg"
						width={CHART_W}
						height={CHART_H}
						viewBox="0 0 {CHART_W} {CHART_H}"
						aria-label="{chartTitle || 'Chart'}"
					>
						<!-- Y-axis -->
						<line
							x1={CHART_PAD.left}
							y1={CHART_PAD.top}
							x2={CHART_PAD.left}
							y2={CHART_PAD.top + innerH}
							class="sg-axis"
						/>
						<!-- X-axis -->
						<line
							x1={CHART_PAD.left}
							y1={chartY(0)}
							x2={CHART_PAD.left + innerW}
							y2={chartY(0)}
							class="sg-axis"
						/>

						<!-- Y-axis ticks + grid lines -->
						{#each yTicks as tick}
							<line
								x1={CHART_PAD.left - 5}
								y1={chartY(tick)}
								x2={CHART_PAD.left + innerW}
								y2={chartY(tick)}
								class="sg-grid-line"
							/>
							<text x={CHART_PAD.left - 8} y={chartY(tick) + 4} class="sg-axis-label" text-anchor="end">
								{parseFloat(tick.toFixed(2))}
							</text>
						{/each}

						{#if chartType === 'bar'}
							{#each chartData as d, i}
								<rect
									x={barX(i)}
									y={barY(d.value)}
									width={barW()}
									height={Math.abs(barH(d.value))}
									class="sg-bar"
								/>
								<!-- Value label on top -->
								<text
									x={barX(i) + barW() / 2}
									y={barY(d.value) - 4}
									class="sg-bar-label"
									text-anchor="middle"
								>{parseFloat(d.value.toFixed(2))}</text>
								<!-- X-axis label -->
								<text
									x={barX(i) + barW() / 2}
									y={CHART_PAD.top + innerH + 18}
									class="sg-axis-label"
									text-anchor="middle"
								>{d.label.length > 8 ? d.label.slice(0, 7) + '…' : d.label}</text>
							{/each}
						{:else}
							<!-- Line chart -->
							{#if chartData.length > 1}
								<polyline points={linePoints} class="sg-line" fill="none" />
							{/if}
							{#each chartData as d, i}
								<circle cx={chartX(i)} cy={chartY(d.value)} r="4" class="sg-dot" />
								<text
									x={chartX(i)}
									y={CHART_PAD.top + innerH + 18}
									class="sg-axis-label"
									text-anchor="middle"
								>{d.label.length > 8 ? d.label.slice(0, 7) + '…' : d.label}</text>
							{/each}
						{/if}
					</svg>
				</div>
			{/if}
		</div>
	{/if}

	<!-- ── Grid ──────────────────────────────────────────────── -->
	<div class="sg-grid-wrap" bind:this={gridContainer}>
		<table class="sg-table">
			<!-- Column headers -->
			<thead>
				<tr>
					<th class="sg-corner" aria-hidden="true"></th>
					{#each Array(numCols) as _, ci}
						<th class="sg-col-header" style="width:{colWidths[ci]}px; min-width:{colWidths[ci]}px">
							{colLetter(ci)}
							<!-- Resize handle -->
							<!-- svelte-ignore a11y-no-static-element-interactions -->
							<span
								class="sg-col-resize"
								on:mousedown={(e) => startColResize(e, ci)}
							></span>
						</th>
					{/each}
				</tr>
			</thead>

			<tbody>
				{#each Array(numRows) as _, ri}
					<tr>
						<td class="sg-row-header">{ri + 1}</td>
						{#each Array(numCols) as _, ci}
							{@const key = cellKey(ci, ri)}
							{@const isEdit = editCell === key}
							{@const isAnchor = isAnchorCell(ci, ri)}
							{@const isSel = isCellSelected(ci, ri)}
							<!-- svelte-ignore a11y-no-noninteractive-element-interactions -->
							<td
								class="sg-cell"
								class:is-selected={isSel}
								class:is-anchor={isAnchor}
								class:is-editing={isEdit}
								style="width:{colWidths[ci]}px; min-width:{colWidths[ci]}px"
								on:mousedown={(e) => {
									if (e.detail === 2) {
										startEdit(ci, ri);
									} else {
										void selectCell(ci, ri, e.shiftKey);
									}
								}}
								on:mouseover={() => {
									if (isSelecting) focus = [ci, ri];
								}}
							>
								{#if isEdit}
									<input
										bind:this={editInputEl}
										class="sg-cell-input"
										bind:value={editValue}
										on:keydown={(e) => void handleCellKeydown(e, ci, ri)}
										on:blur={() => void commitEdit()}
									/>
								{:else}
									<span class="sg-cell-text">{displayValue(key)}</span>
								{/if}
							</td>
						{/each}
					</tr>
				{/each}
			</tbody>
		</table>
	</div>

	<!-- ── Status bar ────────────────────────────────────────── -->
	<div class="sg-status-bar">
		{#if anchor}
			{@const b = selectionBounds()}
			{#if b && (b.c2 > b.c1 || b.r2 > b.r1)}
				{@const selKeys = rangeKeys(cellKey(b.c1, b.r1), cellKey(b.c2, b.r2))}
				{@const numVals = selKeys.map((k) => getCellValue(k)).filter((v) => typeof v === 'number' && !isNaN(Number(v))).map(Number)}
				{@const sum = numVals.reduce((a, b) => a + b, 0)}
				{@const avg = numVals.length ? sum / numVals.length : 0}
				<span>Selection: {cellKey(b.c1, b.r1)}:{cellKey(b.c2, b.r2)}</span>
				<span class="sg-stat-sep"></span>
				<span>Count: {numVals.length}</span>
				<span class="sg-stat-sep"></span>
				<span>Sum: {parseFloat(sum.toFixed(6))}</span>
				<span class="sg-stat-sep"></span>
				<span>Avg: {parseFloat(avg.toFixed(6))}</span>
			{:else}
				<span>Cell: {cellKey(anchor[0], anchor[1])}</span>
				{@const val = getCellValue(cellKey(anchor[0], anchor[1]))}
				{#if typeof val === 'number'}<span class="sg-stat-sep"></span><span>Value: {val}</span>{/if}
			{/if}
		{:else}
			<span>Click a cell to start</span>
		{/if}
		<span class="sg-stat-flex"></span>
		<span class="sg-hint">Shift+Click: select range · F2/Enter: edit · Del: clear · Arrow keys: navigate</span>
	</div>
</div>

<style>
	.sg-root {
		display: flex;
		flex-direction: column;
		height: 100%;
		min-height: 0;
		background: var(--ws-bg, #f2f6fc);
		color: var(--ws-text, #12223f);
		font-family: inherit;
		outline: none;
		overflow: hidden;
		--sg-border: #c8d8ec;
		--sg-sel: rgba(37, 99, 235, 0.12);
		--sg-anchor: rgba(37, 99, 235, 0.22);
		--sg-accent: #2563eb;
		--sg-head-bg: #f0f4fb;
		--sg-head-text: #4a5e78;
		--sg-bar-fill: #3b82f6;
		--sg-line-stroke: #2563eb;
		--sg-dot-fill: #1d4ed8;
	}

	:global([data-theme='dark']) .sg-root,
	:global(.theme-dark) .sg-root {
		--sg-border: #2e3748;
		--sg-sel: rgba(206, 206, 213, 0.14);
		--sg-anchor: rgba(206, 206, 213, 0.24);
		--sg-head-bg: #1e2430;
		--sg-head-text: #8a96a8;
		--sg-bar-fill: #60a5fa;
		--sg-line-stroke: #93c5fd;
		--sg-dot-fill: #bfdbfe;
	}

	/* ── Formula bar ──────────────────────────────────────────── */
	.sg-formula-bar {
		display: flex;
		align-items: center;
		gap: 0;
		padding: 0.28rem 0.5rem;
		border-bottom: 1px solid var(--sg-border);
		background: var(--ws-surface, #fff);
		flex-shrink: 0;
	}

	.sg-cell-ref {
		min-width: 52px;
		font-size: 0.75rem;
		font-weight: 700;
		color: var(--sg-accent);
		text-align: center;
		flex-shrink: 0;
	}

	.sg-formula-sep {
		width: 1px;
		height: 1.2rem;
		background: var(--sg-border);
		margin: 0 0.5rem;
		flex-shrink: 0;
	}

	.sg-formula-input {
		flex: 1;
		min-width: 0;
		border: none;
		outline: none;
		background: transparent;
		color: inherit;
		font-size: 0.8rem;
		font-family: 'Consolas', 'Fira Mono', monospace;
	}

	.sg-toolbar-btns {
		display: inline-flex;
		align-items: center;
		gap: 0.28rem;
		flex-shrink: 0;
		margin-left: 0.5rem;
	}

	.sg-tool-btn {
		display: inline-flex;
		align-items: center;
		gap: 0.25rem;
		padding: 0.22rem 0.5rem;
		border: 1px solid var(--sg-border);
		border-radius: 6px;
		background: transparent;
		color: var(--ws-muted, #5c7196);
		font-size: 0.72rem;
		font-weight: 600;
		cursor: pointer;
		transition:
			background 0.13s ease,
			color 0.13s ease;
	}

	.sg-tool-btn svg {
		width: 0.8rem;
		height: 0.8rem;
		stroke: currentColor;
		fill: none;
		stroke-width: 2;
		stroke-linecap: round;
		stroke-linejoin: round;
		flex-shrink: 0;
	}

	.sg-tool-btn:hover,
	.sg-tool-btn.is-active {
		color: var(--sg-accent);
		background: var(--sg-sel);
		border-color: color-mix(in srgb, var(--sg-accent) 40%, var(--sg-border));
	}

	/* ── Chart panel ──────────────────────────────────────────── */
	.sg-chart-panel {
		border-bottom: 1px solid var(--sg-border);
		padding: 0.6rem 0.8rem;
		background: color-mix(in srgb, var(--ws-surface, #fff) 94%, var(--ws-bg, #f2f6fc));
		flex-shrink: 0;
		display: flex;
		flex-direction: column;
		gap: 0.48rem;
	}

	.sg-chart-controls {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		flex-wrap: wrap;
	}

	.sg-chart-label {
		display: inline-flex;
		align-items: center;
		gap: 0.3rem;
		font-size: 0.73rem;
		color: var(--ws-muted, #5c7196);
		font-weight: 600;
	}

	.sg-chart-input {
		border: 1px solid var(--sg-border);
		border-radius: 6px;
		padding: 0.22rem 0.42rem;
		background: var(--ws-surface, #fff);
		color: inherit;
		font-size: 0.74rem;
		font-family: monospace;
		outline: none;
		width: 90px;
	}

	.sg-chart-input:focus {
		border-color: var(--sg-accent);
	}

	.sg-chart-type-btns {
		display: inline-flex;
		border: 1px solid var(--sg-border);
		border-radius: 6px;
		overflow: hidden;
	}

	.sg-type-btn {
		padding: 0.22rem 0.52rem;
		border: none;
		background: transparent;
		color: var(--ws-muted, #5c7196);
		font-size: 0.72rem;
		font-weight: 600;
		cursor: pointer;
		transition: background 0.12s ease;
	}

	.sg-type-btn.is-active {
		background: var(--sg-accent);
		color: #fff;
	}

	.sg-build-btn {
		padding: 0.26rem 0.68rem;
		border: 1px solid var(--sg-accent);
		border-radius: 6px;
		background: var(--sg-accent);
		color: #fff;
		font-size: 0.73rem;
		font-weight: 700;
		cursor: pointer;
		transition: opacity 0.12s ease;
	}

	.sg-build-btn:hover {
		opacity: 0.85;
	}

	.sg-chart-wrap {
		overflow-x: auto;
	}

	.sg-chart-title {
		margin: 0 0 0.3rem;
		font-size: 0.8rem;
		font-weight: 700;
		text-align: center;
	}

	.sg-chart-svg {
		display: block;
		overflow: visible;
	}

	.sg-axis {
		stroke: var(--sg-border);
		stroke-width: 1.5;
	}

	.sg-grid-line {
		stroke: color-mix(in srgb, var(--sg-border) 50%, transparent);
		stroke-width: 0.5;
		stroke-dasharray: 3 3;
	}

	.sg-axis-label {
		font-size: 9px;
		fill: var(--sg-head-text);
		font-family: inherit;
	}

	.sg-bar {
		fill: var(--sg-bar-fill);
		opacity: 0.85;
	}

	.sg-bar-label {
		font-size: 9px;
		fill: var(--sg-head-text);
		font-family: inherit;
	}

	.sg-line {
		stroke: var(--sg-line-stroke);
		stroke-width: 2;
		stroke-linejoin: round;
	}

	.sg-dot {
		fill: var(--sg-dot-fill);
	}

	.sg-chart-error {
		margin: 0;
		font-size: 0.72rem;
		color: #dc2626;
	}

	/* ── Grid ─────────────────────────────────────────────────── */
	.sg-grid-wrap {
		flex: 1;
		min-height: 0;
		overflow: auto;
		position: relative;
	}

	.sg-table {
		border-collapse: collapse;
		table-layout: fixed;
		font-size: 0.8rem;
	}

	.sg-corner {
		position: sticky;
		top: 0;
		left: 0;
		z-index: 4;
		width: 36px;
		min-width: 36px;
		background: var(--sg-head-bg);
		border-right: 1px solid var(--sg-border);
		border-bottom: 1px solid var(--sg-border);
	}

	.sg-col-header {
		position: sticky;
		top: 0;
		z-index: 3;
		background: var(--sg-head-bg);
		color: var(--sg-head-text);
		font-size: 0.72rem;
		font-weight: 700;
		text-align: center;
		padding: 0.22rem 0;
		border-right: 1px solid var(--sg-border);
		border-bottom: 2px solid var(--sg-border);
		user-select: none;
		white-space: nowrap;
		overflow: hidden;
		position: sticky;
		top: 0;
	}

	.sg-col-resize {
		display: inline-block;
		position: absolute;
		top: 0;
		right: 0;
		width: 5px;
		height: 100%;
		cursor: col-resize;
		opacity: 0;
		background: var(--sg-accent);
		transition: opacity 0.12s;
	}

	.sg-col-header:hover .sg-col-resize {
		opacity: 0.5;
	}

	.sg-row-header {
		position: sticky;
		left: 0;
		z-index: 2;
		width: 36px;
		min-width: 36px;
		background: var(--sg-head-bg);
		color: var(--sg-head-text);
		font-size: 0.7rem;
		font-weight: 600;
		text-align: right;
		padding: 0 0.35rem 0 0;
		border-right: 1px solid var(--sg-border);
		border-bottom: 1px solid var(--sg-border);
		user-select: none;
		white-space: nowrap;
	}

	.sg-cell {
		height: 1.6rem;
		border-right: 1px solid var(--sg-border);
		border-bottom: 1px solid var(--sg-border);
		padding: 0 0.3rem;
		cursor: cell;
		overflow: hidden;
		position: relative;
		white-space: nowrap;
	}

	.sg-cell.is-selected {
		background: var(--sg-sel);
	}

	.sg-cell.is-anchor {
		background: var(--sg-anchor);
		outline: 2px solid var(--sg-accent);
		outline-offset: -1px;
	}

	.sg-cell.is-editing {
		padding: 0;
		overflow: visible;
	}

	.sg-cell-text {
		display: block;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: 0.8rem;
		line-height: 1.6rem;
		pointer-events: none;
	}

	.sg-cell-input {
		position: absolute;
		inset: 0;
		z-index: 5;
		width: 100%;
		height: 100%;
		min-width: 120px;
		border: 2px solid var(--sg-accent);
		border-radius: 0;
		background: var(--ws-surface, #fff);
		color: inherit;
		font-size: 0.8rem;
		font-family: 'Consolas', 'Fira Mono', monospace;
		padding: 0 0.25rem;
		outline: none;
		box-shadow: 0 2px 8px rgba(37, 99, 235, 0.2);
	}

	/* ── Status bar ───────────────────────────────────────────── */
	.sg-status-bar {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		padding: 0.2rem 0.6rem;
		border-top: 1px solid var(--sg-border);
		background: var(--ws-surface, #fff);
		font-size: 0.7rem;
		color: var(--ws-muted, #5c7196);
		flex-shrink: 0;
		overflow: hidden;
	}

	.sg-stat-sep {
		width: 1px;
		height: 0.9rem;
		background: var(--sg-border);
		flex-shrink: 0;
	}

	.sg-stat-flex {
		flex: 1;
	}

	.sg-hint {
		font-size: 0.65rem;
		opacity: 0.7;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
</style>
