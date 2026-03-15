<script lang="ts">
	import { onDestroy, onMount } from 'svelte';

	export let isDarkMode = false;

	const BRUSH_WIDTH_PRESETS = [1.5, 3, 5, 8] as const;
	const COLOR_SWATCHES = [
		'#111827',
		'#2563eb',
		'#0ea5e9',
		'#16a34a',
		'#d97706',
		'#dc2626',
		'#7c3aed',
		'#f8fafc'
	] as const;
	const POINTER_SAMPLE_MIN_DISTANCE = 0.0015;

	type ToolMode = 'draw' | 'eraser';

	type DrawPoint = {
		x: number;
		y: number;
	};

	type DrawStroke = {
		points: DrawPoint[];
		color: string;
		width: number;
		isEraser: boolean;
	};

	let canvasEl: HTMLCanvasElement | null = null;
	let boardWrapEl: HTMLDivElement | null = null;
	let context: CanvasRenderingContext2D | null = null;
	let resizeObserver: ResizeObserver | null = null;
	let activePointerId = -1;
	let boardWidth = 1;
	let boardHeight = 1;
	let activeTool: ToolMode = 'draw';
	let brushColor = '#111827';
	let brushWidth = 3;
	let brushColorCustomized = false;
	let strokes: DrawStroke[] = [];
	let redoStack: DrawStroke[] = [];
	let liveStroke: DrawStroke | null = null;

	$: if (!brushColorCustomized) {
		brushColor = isDarkMode ? '#f8fafc' : '#111827';
	}

	$: if (context) {
		redrawBoard();
	}

	function resolveBoardBackgroundColor() {
		return isDarkMode ? '#0b1220' : '#ffffff';
	}

	function resolveGridMinorColor() {
		return isDarkMode ? 'rgba(148, 163, 184, 0.08)' : 'rgba(30, 41, 59, 0.06)';
	}

	function resolveGridMajorColor() {
		return isDarkMode ? 'rgba(148, 163, 184, 0.14)' : 'rgba(30, 41, 59, 0.1)';
	}

	function clamp(value: number, min: number, max: number) {
		return Math.min(max, Math.max(min, value));
	}

	function clearCanvasToBackground() {
		if (!context) {
			return;
		}
		context.clearRect(0, 0, boardWidth, boardHeight);
		context.fillStyle = resolveBoardBackgroundColor();
		context.fillRect(0, 0, boardWidth, boardHeight);
	}

	function drawGrid() {
		if (!context) {
			return;
		}
		const minorStep = 32;
		const majorStep = minorStep * 4;
		context.save();
		context.lineWidth = 1;
		context.strokeStyle = resolveGridMinorColor();
		for (let x = minorStep; x < boardWidth; x += minorStep) {
			context.beginPath();
			context.moveTo(x, 0);
			context.lineTo(x, boardHeight);
			context.stroke();
		}
		for (let y = minorStep; y < boardHeight; y += minorStep) {
			context.beginPath();
			context.moveTo(0, y);
			context.lineTo(boardWidth, y);
			context.stroke();
		}
		context.strokeStyle = resolveGridMajorColor();
		for (let x = majorStep; x < boardWidth; x += majorStep) {
			context.beginPath();
			context.moveTo(x, 0);
			context.lineTo(x, boardHeight);
			context.stroke();
		}
		for (let y = majorStep; y < boardHeight; y += majorStep) {
			context.beginPath();
			context.moveTo(0, y);
			context.lineTo(boardWidth, y);
			context.stroke();
		}
		context.restore();
	}

	function toCanvasPoint(point: DrawPoint) {
		return {
			x: point.x * boardWidth,
			y: point.y * boardHeight
		};
	}

	function drawStroke(stroke: DrawStroke) {
		if (!context || stroke.points.length === 0) {
			return;
		}
		const strokeColor = stroke.isEraser ? resolveBoardBackgroundColor() : stroke.color;
		context.save();
		context.lineCap = 'round';
		context.lineJoin = 'round';
		context.strokeStyle = strokeColor;
		context.fillStyle = strokeColor;
		context.lineWidth = stroke.width;
		const firstPoint = toCanvasPoint(stroke.points[0]);
		if (stroke.points.length === 1) {
			context.beginPath();
			context.arc(firstPoint.x, firstPoint.y, Math.max(1, stroke.width * 0.5), 0, Math.PI * 2);
			context.fill();
			context.restore();
			return;
		}
		context.beginPath();
		context.moveTo(firstPoint.x, firstPoint.y);
		for (let index = 1; index < stroke.points.length; index += 1) {
			const point = toCanvasPoint(stroke.points[index]);
			context.lineTo(point.x, point.y);
		}
		context.stroke();
		context.restore();
	}

	function redrawBoard() {
		if (!context) {
			return;
		}
		clearCanvasToBackground();
		drawGrid();
		for (const stroke of strokes) {
			drawStroke(stroke);
		}
		if (liveStroke) {
			drawStroke(liveStroke);
		}
	}

	function resizeBoard() {
		if (!canvasEl || !boardWrapEl || typeof window === 'undefined') {
			return;
		}
		const rect = boardWrapEl.getBoundingClientRect();
		const nextWidth = Math.max(1, Math.floor(rect.width));
		const nextHeight = Math.max(1, Math.floor(rect.height));
		const dpr = Math.max(1, window.devicePixelRatio || 1);
		canvasEl.width = Math.max(1, Math.floor(nextWidth * dpr));
		canvasEl.height = Math.max(1, Math.floor(nextHeight * dpr));
		canvasEl.style.width = `${nextWidth}px`;
		canvasEl.style.height = `${nextHeight}px`;
		boardWidth = nextWidth;
		boardHeight = nextHeight;
		context = canvasEl.getContext('2d');
		if (!context) {
			return;
		}
		context.setTransform(dpr, 0, 0, dpr, 0, 0);
		redrawBoard();
	}

	function pointFromPointer(event: PointerEvent): DrawPoint {
		if (!canvasEl) {
			return { x: 0, y: 0 };
		}
		const rect = canvasEl.getBoundingClientRect();
		const x = clamp((event.clientX - rect.left) / Math.max(1, rect.width), 0, 1);
		const y = clamp((event.clientY - rect.top) / Math.max(1, rect.height), 0, 1);
		return { x, y };
	}

	function beginStroke(point: DrawPoint) {
		liveStroke = {
			points: [point],
			color: brushColor,
			width: brushWidth,
			isEraser: activeTool === 'eraser'
		};
		redrawBoard();
	}

	function appendStrokePoint(point: DrawPoint) {
		if (!liveStroke) {
			return;
		}
		const lastPoint = liveStroke.points[liveStroke.points.length - 1];
		if (!lastPoint) {
			liveStroke.points = [point];
			redrawBoard();
			return;
		}
		const distance = Math.hypot(point.x - lastPoint.x, point.y - lastPoint.y);
		if (distance < POINTER_SAMPLE_MIN_DISTANCE) {
			return;
		}
		liveStroke.points = [...liveStroke.points, point];
		redrawBoard();
	}

	function commitStroke() {
		if (!liveStroke || liveStroke.points.length === 0) {
			liveStroke = null;
			return;
		}
		strokes = [...strokes, liveStroke];
		redoStack = [];
		liveStroke = null;
		redrawBoard();
	}

	function cancelStroke() {
		liveStroke = null;
		redrawBoard();
	}

	function handlePointerDown(event: PointerEvent) {
		if (!canvasEl || activePointerId !== -1) {
			return;
		}
		canvasEl.focus();
		activePointerId = event.pointerId;
		beginStroke(pointFromPointer(event));
		canvasEl.setPointerCapture(event.pointerId);
	}

	function handlePointerMove(event: PointerEvent) {
		if (event.pointerId !== activePointerId) {
			return;
		}
		appendStrokePoint(pointFromPointer(event));
	}

	function handlePointerEnd(event: PointerEvent) {
		if (event.pointerId !== activePointerId || !canvasEl) {
			return;
		}
		if (canvasEl.hasPointerCapture(event.pointerId)) {
			canvasEl.releasePointerCapture(event.pointerId);
		}
		activePointerId = -1;
		commitStroke();
	}

	function handlePointerCancel(event: PointerEvent) {
		if (event.pointerId !== activePointerId || !canvasEl) {
			return;
		}
		if (canvasEl.hasPointerCapture(event.pointerId)) {
			canvasEl.releasePointerCapture(event.pointerId);
		}
		activePointerId = -1;
		cancelStroke();
	}

	function selectColor(color: string) {
		brushColor = color;
		brushColorCustomized = true;
		activeTool = 'draw';
	}

	function useAdaptiveInk() {
		brushColorCustomized = false;
		activeTool = 'draw';
	}

	function setTool(tool: ToolMode) {
		activeTool = tool;
	}

	function setBrushWidth(width: number) {
		brushWidth = width;
	}

	function undoStroke() {
		if (strokes.length === 0) {
			return;
		}
		const lastStroke = strokes[strokes.length - 1];
		if (!lastStroke) {
			return;
		}
		strokes = strokes.slice(0, -1);
		redoStack = [...redoStack, lastStroke];
		redrawBoard();
	}

	function redoStroke() {
		if (redoStack.length === 0) {
			return;
		}
		const nextStroke = redoStack[redoStack.length - 1];
		if (!nextStroke) {
			return;
		}
		redoStack = redoStack.slice(0, -1);
		strokes = [...strokes, nextStroke];
		redrawBoard();
	}

	function clearBoard() {
		if (strokes.length === 0 && !liveStroke) {
			return;
		}
		strokes = [];
		redoStack = [];
		liveStroke = null;
		redrawBoard();
	}

	function downloadBoardAsImage() {
		if (!canvasEl) {
			return;
		}
		const link = document.createElement('a');
		link.href = canvasEl.toDataURL('image/png');
		link.download = `tora-draw-${Date.now()}.png`;
		link.click();
	}

	function handleCanvasKeydown(event: KeyboardEvent) {
		if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'z') {
			event.preventDefault();
			if (event.shiftKey) {
				redoStroke();
				return;
			}
			undoStroke();
			return;
		}
		if (event.key.toLowerCase() === 'e') {
			event.preventDefault();
			setTool('eraser');
			return;
		}
		if (event.key.toLowerCase() === 'b') {
			event.preventDefault();
			setTool('draw');
		}
	}

	onMount(() => {
		resizeBoard();
		if (boardWrapEl) {
			resizeObserver = new ResizeObserver(() => {
				resizeBoard();
			});
			resizeObserver.observe(boardWrapEl);
		}
		window.addEventListener('resize', resizeBoard);
	});

	onDestroy(() => {
		if (resizeObserver) {
			resizeObserver.disconnect();
			resizeObserver = null;
		}
		window.removeEventListener('resize', resizeBoard);
	});
</script>

<section class="draw-shell" data-theme={isDarkMode ? 'dark' : 'light'}>
	<header class="draw-toolbar">
		<div class="toolbar-group">
			<span class="group-label">Tool</span>
			<div class="tool-toggle" role="group" aria-label="Drawing tool">
				<button
					type="button"
					class="tool-btn"
					class:is-active={activeTool === 'draw'}
					on:click={() => setTool('draw')}
				>
					Brush
				</button>
				<button
					type="button"
					class="tool-btn"
					class:is-active={activeTool === 'eraser'}
					on:click={() => setTool('eraser')}
				>
					Eraser
				</button>
			</div>
		</div>

		<div class="toolbar-group">
			<span class="group-label">Width</span>
			<div class="width-presets" role="group" aria-label="Brush width presets">
				{#each BRUSH_WIDTH_PRESETS as width}
					<button
						type="button"
						class="width-btn"
						class:is-active={Math.abs(brushWidth - width) < 0.001}
						on:click={() => setBrushWidth(width)}
					>
						{width}px
					</button>
				{/each}
			</div>
		</div>

		<div class="toolbar-group swatches-group">
			<span class="group-label">Ink</span>
			<div class="swatches" aria-label="Brush colors">
				<button
					type="button"
					class="swatch adaptive"
					class:is-active={!brushColorCustomized}
					on:click={useAdaptiveInk}
					aria-label="Use adaptive theme ink"
				>
					Auto
				</button>
				{#each COLOR_SWATCHES as color}
					<button
						type="button"
						class="swatch"
						class:is-active={brushColorCustomized && brushColor === color}
						style={`--swatch-color:${color};`}
						on:click={() => selectColor(color)}
						aria-label={`Use ${color} brush`}
					></button>
				{/each}
			</div>
		</div>

		<div class="toolbar-actions">
			<button type="button" class="action-btn" on:click={undoStroke} disabled={strokes.length === 0}>
				Undo
			</button>
			<button type="button" class="action-btn" on:click={redoStroke} disabled={redoStack.length === 0}>
				Redo
			</button>
			<button type="button" class="action-btn" on:click={clearBoard}>Clear</button>
			<button type="button" class="action-btn primary" on:click={downloadBoardAsImage}>Download PNG</button>
		</div>
	</header>

	<div class="draw-meta">
		<span>Strokes: {strokes.length}</span>
		<span>Shortcuts: `B` brush, `E` eraser, `Ctrl/Cmd + Z` undo</span>
		<span>Local board only. No backend save or sync.</span>
	</div>

	<div class="draw-board-wrap" bind:this={boardWrapEl}>
		<canvas
			bind:this={canvasEl}
			class="draw-board"
			tabindex="0"
			on:keydown={handleCanvasKeydown}
			on:pointerdown={handlePointerDown}
			on:pointermove={handlePointerMove}
			on:pointerup={handlePointerEnd}
			on:pointercancel={handlePointerCancel}
			on:pointerleave={handlePointerEnd}
		></canvas>
	</div>
</section>

<style>
	.draw-shell {
		height: 100%;
		display: grid;
		grid-template-rows: auto auto minmax(0, 1fr);
		border-radius: 0.95rem;
		overflow: hidden;
		border: 1px solid rgba(131, 158, 196, 0.4);
		background: linear-gradient(180deg, rgba(249, 252, 255, 0.96), rgba(241, 247, 255, 0.98));
	}

	.draw-toolbar {
		display: flex;
		align-items: center;
		gap: 0.72rem;
		padding: 0.64rem 0.76rem;
		background: rgba(255, 255, 255, 0.86);
		border-bottom: 1px solid rgba(148, 173, 207, 0.36);
		flex-wrap: wrap;
	}

	.toolbar-group {
		display: inline-flex;
		align-items: center;
		gap: 0.44rem;
	}

	.group-label {
		font-size: 0.64rem;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: rgba(39, 61, 99, 0.84);
		font-weight: 700;
	}

	.tool-toggle,
	.width-presets {
		display: inline-flex;
		align-items: center;
		gap: 0.34rem;
	}

	.tool-btn,
	.width-btn,
	.action-btn {
		border: 1px solid rgba(131, 163, 209, 0.56);
		background: rgba(238, 246, 255, 0.92);
		color: #163155;
		border-radius: 0.42rem;
		padding: 0.28rem 0.54rem;
		font-size: 0.69rem;
		font-weight: 600;
		cursor: pointer;
	}

	.tool-btn:hover,
	.width-btn:hover,
	.action-btn:hover {
		border-color: rgba(108, 158, 233, 0.86);
		background: rgba(219, 234, 255, 0.96);
	}

	.tool-btn.is-active,
	.width-btn.is-active {
		border-color: rgba(61, 181, 118, 0.84);
		background: rgba(25, 115, 74, 0.42);
		color: #effff7;
	}

	.swatches-group {
		flex-wrap: wrap;
	}

	.swatches {
		display: inline-flex;
		align-items: center;
		gap: 0.3rem;
		flex-wrap: wrap;
	}

	.swatch {
		width: 1.15rem;
		height: 1.15rem;
		border-radius: 999px;
		border: 2px solid rgba(255, 255, 255, 0.14);
		padding: 0;
		cursor: pointer;
		background: var(--swatch-color, #111827);
	}

	.swatch:hover {
		border-color: rgba(194, 220, 255, 0.8);
	}

	.swatch.is-active {
		border-color: rgba(56, 189, 248, 0.92);
		box-shadow: 0 0 0 1px rgba(56, 189, 248, 0.92);
	}

	.swatch.adaptive {
		width: auto;
		min-width: 2.1rem;
		padding: 0 0.5rem;
		border-radius: 999px;
		background: rgba(227, 238, 255, 0.92);
		color: #14315a;
		font-size: 0.65rem;
		font-weight: 700;
	}

	.toolbar-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.34rem;
		margin-left: auto;
		flex-wrap: wrap;
	}

	.action-btn:disabled {
		opacity: 0.45;
		cursor: not-allowed;
	}

	.action-btn.primary {
		border-color: rgba(56, 189, 248, 0.76);
		background: rgba(3, 105, 161, 0.52);
		color: #e0f7ff;
	}

	.draw-meta {
		display: flex;
		align-items: center;
		gap: 0.9rem;
		flex-wrap: wrap;
		padding: 0.42rem 0.76rem;
		font-size: 0.66rem;
		color: rgba(34, 56, 94, 0.84);
		border-bottom: 1px solid rgba(151, 176, 213, 0.36);
		background: rgba(243, 248, 255, 0.76);
	}

	.draw-board-wrap {
		position: relative;
		min-width: 0;
		min-height: 0;
	}

	.draw-board {
		display: block;
		width: 100%;
		height: 100%;
		touch-action: none;
		cursor: crosshair;
		outline: none;
	}

	.draw-board:focus-visible {
		box-shadow: inset 0 0 0 2px rgba(56, 189, 248, 0.4);
	}

	:global(body.theme-dark) .draw-shell,
	:global(:root[data-theme='dark']) .draw-shell,
	.draw-shell[data-theme='dark'] {
		border-color: rgba(100, 126, 163, 0.44);
		background: linear-gradient(180deg, rgba(7, 12, 22, 0.95), rgba(5, 9, 17, 0.96));
	}

	:global(body.theme-dark) .draw-toolbar,
	:global(:root[data-theme='dark']) .draw-toolbar,
	.draw-shell[data-theme='dark'] .draw-toolbar {
		background: rgba(8, 14, 25, 0.78);
		border-bottom-color: rgba(120, 144, 184, 0.35);
	}

	:global(body.theme-dark) .group-label,
	:global(:root[data-theme='dark']) .group-label,
	.draw-shell[data-theme='dark'] .group-label {
		color: rgba(194, 210, 238, 0.86);
	}

	:global(body.theme-dark) .tool-btn,
	:global(body.theme-dark) .width-btn,
	:global(body.theme-dark) .action-btn,
	:global(:root[data-theme='dark']) .tool-btn,
	:global(:root[data-theme='dark']) .width-btn,
	:global(:root[data-theme='dark']) .action-btn,
	.draw-shell[data-theme='dark'] .tool-btn,
	.draw-shell[data-theme='dark'] .width-btn,
	.draw-shell[data-theme='dark'] .action-btn {
		background: rgba(18, 30, 49, 0.88);
		border-color: rgba(109, 135, 175, 0.52);
		color: #dbe8ff;
	}

	:global(body.theme-dark) .tool-btn:hover,
	:global(body.theme-dark) .width-btn:hover,
	:global(body.theme-dark) .action-btn:hover,
	:global(:root[data-theme='dark']) .tool-btn:hover,
	:global(:root[data-theme='dark']) .width-btn:hover,
	:global(:root[data-theme='dark']) .action-btn:hover,
	.draw-shell[data-theme='dark'] .tool-btn:hover,
	.draw-shell[data-theme='dark'] .width-btn:hover,
	.draw-shell[data-theme='dark'] .action-btn:hover {
		border-color: rgba(158, 195, 247, 0.86);
		background: rgba(42, 68, 106, 0.94);
	}

	:global(body.theme-dark) .swatch.adaptive,
	:global(:root[data-theme='dark']) .swatch.adaptive,
	.draw-shell[data-theme='dark'] .swatch.adaptive {
		background: rgba(23, 37, 62, 0.88);
		color: #dce9ff;
	}

	:global(body.theme-dark) .draw-meta,
	:global(:root[data-theme='dark']) .draw-meta,
	.draw-shell[data-theme='dark'] .draw-meta {
		color: rgba(183, 204, 236, 0.9);
		border-bottom-color: rgba(112, 138, 179, 0.28);
		background: rgba(6, 12, 21, 0.7);
	}

	@media (max-width: 900px) {
		.toolbar-actions {
			margin-left: 0;
		}

		.draw-meta {
			font-size: 0.62rem;
			gap: 0.5rem;
		}
	}
</style>
