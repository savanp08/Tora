<script lang="ts">
	import { onDestroy, onMount } from 'svelte';

	export let seed = '';

	let hostElement: HTMLDivElement | null = null;
	let canvasElement: HTMLCanvasElement | null = null;
	let resizeObserver: ResizeObserver | null = null;
	let pendingFrame = 0;
	let lastDrawnSeed = '';
	let cachedTileSeed = '';
	let cachedPatternTile: PatternTile | null = null;

	type PatternTile = {
		canvas: HTMLCanvasElement;
		size: number;
		offsetX: number;
		offsetY: number;
	};

	function hashSeed(value: string) {
		let hash = 2166136261;
		for (let index = 0; index < value.length; index += 1) {
			hash ^= value.charCodeAt(index);
			hash = Math.imul(hash, 16777619);
		}
		return hash >>> 0;
	}

	function createPRNG(seedValue: number) {
		let state = seedValue >>> 0;
		return () => {
			state = (state + 0x6d2b79f5) | 0;
			let t = Math.imul(state ^ (state >>> 15), 1 | state);
			t ^= t + Math.imul(t ^ (t >>> 7), 61 | t);
			return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
		};
	}

	function drawGlowLayer(context: CanvasRenderingContext2D, tileSize: number, rng: () => number) {
		const glowCount = 3 + Math.floor(rng() * 3);
		for (let index = 0; index < glowCount; index += 1) {
			const centerX = rng() * tileSize;
			const centerY = rng() * tileSize;
			const radius = tileSize * (0.22 + rng() * 0.26);
			const alpha = 0.08 + rng() * 0.1;
			const gradient = context.createRadialGradient(centerX, centerY, 0, centerX, centerY, radius);
			gradient.addColorStop(0, `rgba(26, 26, 36, ${alpha.toFixed(3)})`);
			gradient.addColorStop(1, 'rgba(26, 26, 36, 0)');
			context.fillStyle = gradient;
			context.beginPath();
			context.arc(centerX, centerY, radius, 0, Math.PI * 2);
			context.fill();
		}
	}

	function drawContourLayer(context: CanvasRenderingContext2D, tileSize: number, rng: () => number) {
		const lineCount = 14 + Math.floor(rng() * 14);
		const stepX = 10;
		for (let lineIndex = 0; lineIndex < lineCount; lineIndex += 1) {
			const progress = lineCount > 1 ? lineIndex / (lineCount - 1) : 0;
			const baseY = progress * tileSize + (rng() - 0.5) * 24;
			const amplitude = 6 + rng() * 16;
			const secondaryAmplitude = amplitude * (0.12 + rng() * 0.18);
			const frequencyA = 0.8 + rng() * 1.6;
			const frequencyB = 1.4 + rng() * 1.4;
			const phase = rng() * Math.PI * 2;
			const alpha = 0.035 + rng() * 0.07;
			context.strokeStyle = `rgba(26, 26, 36, ${alpha.toFixed(3)})`;
			context.lineWidth = 0.7 + rng() * 0.95;
			context.beginPath();
			for (let x = -stepX; x <= tileSize + stepX; x += stepX) {
				const normalizedX = x / tileSize;
				const waveA = Math.sin(normalizedX * Math.PI * 2 * frequencyA + phase) * amplitude;
				const waveB = Math.cos(normalizedX * Math.PI * 2 * frequencyB + phase * 0.7) * secondaryAmplitude;
				const y = baseY + waveA + waveB;
				if (x <= 0) {
					context.moveTo(x, y);
				} else {
					context.lineTo(x, y);
				}
			}
			context.stroke();
		}
	}

	function drawLatticeLayer(context: CanvasRenderingContext2D, tileSize: number, rng: () => number) {
		const cell = 34 + Math.floor(rng() * 30);
		const jitter = cell * 0.22;
		for (let y = -cell; y <= tileSize + cell; y += cell) {
			for (let x = -cell; x <= tileSize + cell; x += cell) {
				if (rng() > 0.52) {
					continue;
				}
				const centerX = x + cell * 0.5 + (rng() - 0.5) * jitter;
				const centerY = y + cell * 0.5 + (rng() - 0.5) * jitter;
				const size = 6 + rng() * (cell * 0.42);
				const alpha = 0.03 + rng() * 0.07;
				context.strokeStyle = `rgba(26, 26, 36, ${alpha.toFixed(3)})`;
				context.lineWidth = 0.7 + rng() * 0.8;
				context.beginPath();
				context.moveTo(centerX, centerY - size);
				context.lineTo(centerX + size, centerY);
				context.lineTo(centerX, centerY + size);
				context.lineTo(centerX - size, centerY);
				context.closePath();
				context.stroke();
				if (rng() < 0.45) {
					context.fillStyle = `rgba(26, 26, 36, ${(alpha * 1.1).toFixed(3)})`;
					context.beginPath();
					context.arc(centerX, centerY, 1.1 + rng() * 1.8, 0, Math.PI * 2);
					context.fill();
				}
			}
		}
	}

	function drawOrbitLayer(context: CanvasRenderingContext2D, tileSize: number, rng: () => number) {
		const clusterCount = 2 + Math.floor(rng() * 3);
		for (let index = 0; index < clusterCount; index += 1) {
			const centerX = rng() * tileSize;
			const centerY = rng() * tileSize;
			const ringCount = 2 + Math.floor(rng() * 3);
			const baseRadius = tileSize * (0.08 + rng() * 0.06);
			for (let ringIndex = 0; ringIndex < ringCount; ringIndex += 1) {
				const radius = baseRadius + ringIndex * (tileSize * (0.04 + rng() * 0.03));
				const sweep = Math.PI * (0.9 + rng() * 1.05);
				const start = rng() * Math.PI * 2;
				const alpha = 0.03 + rng() * 0.06;
				context.strokeStyle = `rgba(26, 26, 36, ${alpha.toFixed(3)})`;
				context.lineWidth = 0.8 + rng() * 1;
				context.beginPath();
				context.arc(centerX, centerY, radius, start, start + sweep);
				context.stroke();
			}
		}
	}

	function drawGlyphLayer(context: CanvasRenderingContext2D, tileSize: number, rng: () => number) {
		const glyphCount = Math.floor(tileSize * 0.55);
		for (let index = 0; index < glyphCount; index += 1) {
			const x = rng() * tileSize;
			const y = rng() * tileSize;
			const alpha = 0.02 + rng() * 0.055;
			const pick = rng();
			context.strokeStyle = `rgba(26, 26, 36, ${alpha.toFixed(3)})`;
			context.fillStyle = `rgba(26, 26, 36, ${(alpha * 1.15).toFixed(3)})`;
			context.lineWidth = 0.6 + rng() * 0.8;
			if (pick < 0.45) {
				const size = 0.9 + rng() * 1.8;
				context.beginPath();
				context.arc(x, y, size, 0, Math.PI * 2);
				context.fill();
				continue;
			}
			if (pick < 0.78) {
				const length = 3 + rng() * 8;
				context.beginPath();
				context.moveTo(x - length * 0.5, y);
				context.lineTo(x + length * 0.5, y);
				context.stroke();
				continue;
			}
			const rectSize = 2 + rng() * 5;
			context.strokeRect(x - rectSize * 0.5, y - rectSize * 0.5, rectSize, rectSize);
		}
	}

	function buildPatternTile(seedText: string): PatternTile | null {
		if (typeof document === 'undefined') {
			return null;
		}
		const rng = createPRNG(hashSeed(seedText));
		const tileSize = 260 + Math.floor(rng() * 120);
		const tileCanvas = document.createElement('canvas');
		tileCanvas.width = tileSize;
		tileCanvas.height = tileSize;
		const context = tileCanvas.getContext('2d');
		if (!context) {
			return null;
		}

		context.fillStyle = '#0d0d12';
		context.fillRect(0, 0, tileSize, tileSize);
		drawGlowLayer(context, tileSize, rng);
		drawContourLayer(context, tileSize, rng);

		const motifMode = Math.floor(rng() * 3);
		if (motifMode === 0) {
			drawLatticeLayer(context, tileSize, rng);
		} else if (motifMode === 1) {
			drawOrbitLayer(context, tileSize, rng);
		} else {
			drawLatticeLayer(context, tileSize, rng);
			drawOrbitLayer(context, tileSize, rng);
		}
		drawGlyphLayer(context, tileSize, rng);

		const vignette = context.createRadialGradient(
			tileSize * 0.5,
			tileSize * 0.5,
			tileSize * 0.16,
			tileSize * 0.5,
			tileSize * 0.5,
			tileSize * 0.82
		);
		vignette.addColorStop(0, 'rgba(13, 13, 18, 0)');
		vignette.addColorStop(1, 'rgba(13, 13, 18, 0.24)');
		context.fillStyle = vignette;
		context.fillRect(0, 0, tileSize, tileSize);

		return {
			canvas: tileCanvas,
			size: tileSize,
			offsetX: Math.floor(rng() * tileSize),
			offsetY: Math.floor(rng() * tileSize)
		};
	}

	function getPatternTile(seedText: string) {
		if (cachedPatternTile && cachedTileSeed === seedText) {
			return cachedPatternTile;
		}
		cachedPatternTile = buildPatternTile(seedText);
		cachedTileSeed = seedText;
		return cachedPatternTile;
	}

	function drawPattern() {
		if (!hostElement || !canvasElement) {
			return;
		}
		const width = Math.max(1, Math.floor(hostElement.clientWidth));
		const height = Math.max(1, Math.floor(hostElement.clientHeight));
		if (width === 0 || height === 0) {
			return;
		}
		const dpr = Math.max(1, Math.min(2, window.devicePixelRatio || 1));
		const targetWidth = Math.floor(width * dpr);
		const targetHeight = Math.floor(height * dpr);
		if (canvasElement.width !== targetWidth || canvasElement.height !== targetHeight) {
			canvasElement.width = targetWidth;
			canvasElement.height = targetHeight;
		}

		const context = canvasElement.getContext('2d');
		if (!context) {
			return;
		}
		context.setTransform(1, 0, 0, 1, 0, 0);
		context.scale(dpr, dpr);
		context.clearRect(0, 0, width, height);

		context.fillStyle = '#0d0d12';
		context.fillRect(0, 0, width, height);

		const tile = getPatternTile(normalizedSeed);
		if (tile) {
			const pattern = context.createPattern(tile.canvas, 'repeat');
			if (pattern) {
				context.save();
				context.translate(-tile.offsetX, -tile.offsetY);
				context.fillStyle = pattern;
				context.fillRect(0, 0, width + tile.size, height + tile.size);
				context.restore();
			}
		}

		const shading = context.createLinearGradient(0, 0, width, height);
		shading.addColorStop(0, 'rgba(13, 13, 18, 0.14)');
		shading.addColorStop(0.5, 'rgba(13, 13, 18, 0.05)');
		shading.addColorStop(1, 'rgba(13, 13, 18, 0.16)');
		context.fillStyle = shading;
		context.fillRect(0, 0, width, height);

		const vignette = context.createRadialGradient(
			width * 0.5,
			height * 0.5,
			Math.min(width, height) * 0.18,
			width * 0.5,
			height * 0.5,
			Math.max(width, height) * 0.85
		);
		vignette.addColorStop(0, 'rgba(13, 13, 18, 0)');
		vignette.addColorStop(1, 'rgba(13, 13, 18, 0.34)');
		context.fillStyle = vignette;
		context.fillRect(0, 0, width, height);
	}

	function queueDraw() {
		if (pendingFrame) {
			cancelAnimationFrame(pendingFrame);
		}
		pendingFrame = requestAnimationFrame(() => {
			pendingFrame = 0;
			drawPattern();
		});
	}

	$: normalizedSeed = seed.trim() || 'default-room';
	$: if (normalizedSeed !== lastDrawnSeed) {
		lastDrawnSeed = normalizedSeed;
		cachedTileSeed = '';
		cachedPatternTile = null;
		if (typeof window !== 'undefined') {
			queueDraw();
		}
	}

	onMount(() => {
		queueDraw();
		if (typeof ResizeObserver !== 'undefined' && hostElement) {
			resizeObserver = new ResizeObserver(() => queueDraw());
			resizeObserver.observe(hostElement);
		} else {
			window.addEventListener('resize', queueDraw, { passive: true });
		}
		return () => {
			if (resizeObserver) {
				resizeObserver.disconnect();
				resizeObserver = null;
			} else {
				window.removeEventListener('resize', queueDraw);
			}
			if (pendingFrame) {
				cancelAnimationFrame(pendingFrame);
				pendingFrame = 0;
			}
		};
	});

	onDestroy(() => {
		if (pendingFrame) {
			cancelAnimationFrame(pendingFrame);
			pendingFrame = 0;
		}
	});
</script>

<div class="monochrome-room-background" bind:this={hostElement} aria-hidden="true">
	<canvas bind:this={canvasElement}></canvas>
	<div class="pattern-overlay"></div>
</div>

<style>
	.monochrome-room-background {
		position: absolute;
		inset: 0;
		overflow: hidden;
		pointer-events: none;
		z-index: 0;
	}

	.monochrome-room-background canvas {
		width: 100%;
		height: 100%;
		display: block;
		filter: saturate(0.78) contrast(1.03);
		animation: monochrome-room-drift 72s ease-in-out infinite alternate;
	}

	.pattern-overlay {
		position: absolute;
		inset: 0;
		background: rgba(13, 13, 18, 0.85);
		backdrop-filter: blur(8px);
		-webkit-backdrop-filter: blur(8px);
	}

	@keyframes monochrome-room-drift {
		0% {
			transform: translate3d(0, 0, 0) scale(1.02);
		}
		50% {
			transform: translate3d(-1.1%, 0.8%, 0) scale(1.035);
		}
		100% {
			transform: translate3d(1.1%, -0.8%, 0) scale(1.02);
		}
	}
</style>
