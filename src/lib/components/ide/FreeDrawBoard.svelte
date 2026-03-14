<script lang="ts">
	import { onDestroy, onMount } from 'svelte';

	const COLOR_SWATCHES = ['#111827', '#2563eb', '#0ea5e9', '#16a34a', '#d97706', '#dc2626', '#7c3aed'];

	let canvasEl: HTMLCanvasElement | null = null;
	let boardWrapEl: HTMLDivElement | null = null;
	let context: CanvasRenderingContext2D | null = null;
	let resizeObserver: ResizeObserver | null = null;
	let activePointerId = -1;
	let lastPoint = { x: 0, y: 0 };
	let brushColor = '#111827';
	let brushSize = 3;
	let eraserEnabled = false;

	function resizeBoard() {
		if (!canvasEl || !boardWrapEl) {
			return;
		}
		const rect = boardWrapEl.getBoundingClientRect();
		const nextWidth = Math.max(1, Math.floor(rect.width));
		const nextHeight = Math.max(1, Math.floor(rect.height));
		const dpr = Math.max(1, window.devicePixelRatio || 1);
		const prevWidth = canvasEl.width;
		const prevHeight = canvasEl.height;
		const snapshot = document.createElement('canvas');
		snapshot.width = prevWidth;
		snapshot.height = prevHeight;
		const snapshotContext = snapshot.getContext('2d');
		if (snapshotContext && prevWidth > 0 && prevHeight > 0) {
			snapshotContext.drawImage(canvasEl, 0, 0);
		}

		canvasEl.width = Math.max(1, Math.floor(nextWidth * dpr));
		canvasEl.height = Math.max(1, Math.floor(nextHeight * dpr));
		canvasEl.style.width = `${nextWidth}px`;
		canvasEl.style.height = `${nextHeight}px`;
		context = canvasEl.getContext('2d');
		if (!context) {
			return;
		}
		context.setTransform(dpr, 0, 0, dpr, 0, 0);
		context.lineCap = 'round';
		context.lineJoin = 'round';
		context.fillStyle = '#ffffff';
		context.fillRect(0, 0, nextWidth, nextHeight);
		if (prevWidth > 0 && prevHeight > 0) {
			context.drawImage(snapshot, 0, 0, prevWidth, prevHeight, 0, 0, nextWidth, nextHeight);
		}
	}

	function getRelativePoint(event: PointerEvent) {
		if (!canvasEl) {
			return { x: 0, y: 0 };
		}
		const rect = canvasEl.getBoundingClientRect();
		return {
			x: event.clientX - rect.left,
			y: event.clientY - rect.top
		};
	}

	function drawLine(from: { x: number; y: number }, to: { x: number; y: number }) {
		if (!context) {
			return;
		}
		context.strokeStyle = eraserEnabled ? '#ffffff' : brushColor;
		context.lineWidth = brushSize;
		context.beginPath();
		context.moveTo(from.x, from.y);
		context.lineTo(to.x, to.y);
		context.stroke();
	}

	function handlePointerDown(event: PointerEvent) {
		if (!canvasEl || activePointerId !== -1) {
			return;
		}
		activePointerId = event.pointerId;
		const point = getRelativePoint(event);
		lastPoint = point;
		drawLine(point, point);
		canvasEl.setPointerCapture(event.pointerId);
	}

	function handlePointerMove(event: PointerEvent) {
		if (event.pointerId !== activePointerId) {
			return;
		}
		const point = getRelativePoint(event);
		drawLine(lastPoint, point);
		lastPoint = point;
	}

	function handlePointerEnd(event: PointerEvent) {
		if (event.pointerId !== activePointerId || !canvasEl) {
			return;
		}
		canvasEl.releasePointerCapture(event.pointerId);
		activePointerId = -1;
	}

	function clearBoard() {
		if (!context || !canvasEl) {
			return;
		}
		const rect = canvasEl.getBoundingClientRect();
		context.fillStyle = '#ffffff';
		context.fillRect(0, 0, rect.width, rect.height);
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

	function selectColor(color: string) {
		brushColor = color;
		eraserEnabled = false;
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

<section class="draw-shell">
	<header class="draw-toolbar">
		<div class="swatches" aria-label="Brush colors">
			{#each COLOR_SWATCHES as color}
				<button
					type="button"
					class="swatch"
					class:is-active={!eraserEnabled && brushColor === color}
					style={`background:${color};`}
					on:click={() => selectColor(color)}
					aria-label={`Use ${color} brush`}
				></button>
			{/each}
		</div>
		<label class="brush-size">
			<span>Brush</span>
			<input type="range" min="1" max="18" step="1" bind:value={brushSize} />
			<strong>{brushSize}px</strong>
		</label>
		<div class="toolbar-actions">
			<button
				type="button"
				class="control-btn"
				class:is-active={eraserEnabled}
				on:click={() => (eraserEnabled = !eraserEnabled)}
			>
				Eraser
			</button>
			<button type="button" class="control-btn" on:click={clearBoard}>Clear</button>
			<button type="button" class="control-btn" on:click={downloadBoardAsImage}>Download PNG</button>
		</div>
	</header>

	<div class="draw-board-wrap" bind:this={boardWrapEl}>
		<canvas
			bind:this={canvasEl}
			class="draw-board"
			on:pointerdown={handlePointerDown}
			on:pointermove={handlePointerMove}
			on:pointerup={handlePointerEnd}
			on:pointercancel={handlePointerEnd}
			on:pointerleave={handlePointerEnd}
		></canvas>
	</div>
</section>

<style>
	.draw-shell {
		height: 100%;
		display: grid;
		grid-template-rows: auto minmax(0, 1fr);
		background: linear-gradient(180deg, #f8fafc 0%, #eef2ff 100%);
		border-radius: 0.95rem;
		overflow: hidden;
		border: 1px solid rgba(15, 23, 42, 0.12);
	}

	.draw-toolbar {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		padding: 0.7rem 0.8rem;
		background: rgba(255, 255, 255, 0.9);
		border-bottom: 1px solid rgba(15, 23, 42, 0.12);
		flex-wrap: wrap;
	}

	.swatches {
		display: inline-flex;
		align-items: center;
		gap: 0.36rem;
	}

	.swatch {
		width: 1.15rem;
		height: 1.15rem;
		border-radius: 999px;
		border: 2px solid transparent;
		padding: 0;
		cursor: pointer;
	}

	.swatch.is-active {
		border-color: #0f172a;
	}

	.brush-size {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		font-size: 0.78rem;
		color: #0f172a;
	}

	.brush-size input {
		width: 9rem;
	}

	.toolbar-actions {
		display: inline-flex;
		align-items: center;
		gap: 0.44rem;
		margin-left: auto;
	}

	.control-btn {
		border: 1px solid rgba(15, 23, 42, 0.2);
		background: #ffffff;
		color: #0f172a;
		border-radius: 0.5rem;
		padding: 0.4rem 0.65rem;
		font-size: 0.75rem;
		font-weight: 600;
		cursor: pointer;
	}

	.control-btn:hover {
		background: #e2e8f0;
	}

	.control-btn.is-active {
		background: #0f172a;
		color: #f8fafc;
		border-color: #0f172a;
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
		background: #ffffff;
		touch-action: none;
		cursor: crosshair;
	}
	@media (max-width: 900px) {
		.toolbar-actions {
			margin-left: 0;
		}
	}
</style>
