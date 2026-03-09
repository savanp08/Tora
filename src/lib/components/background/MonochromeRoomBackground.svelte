<script lang="ts">
	import { onDestroy, onMount } from 'svelte';

	export let seed = '';

	let hostElement: HTMLDivElement | null = null;
	let canvasElement: HTMLCanvasElement | null = null;
	let resizeObserver: ResizeObserver | null = null;
	let pendingFrame = 0;
	let lastDrawnSeed = '';

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

	function drawPattern() {
		if (!hostElement || !canvasElement) {
			return;
		}
		const width = Math.max(1, Math.floor(hostElement.clientWidth));
		const height = Math.max(1, Math.floor(hostElement.clientHeight));
		if (width === 0 || height === 0) {
			return;
		}
		const dpr = Math.max(1, window.devicePixelRatio || 1);
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

		const rng = createPRNG(hashSeed(seed || 'default-room'));
		const blobCount = 4 + Math.floor(rng() * 3);
		for (let index = 0; index < blobCount; index += 1) {
			const centerX = rng() * width;
			const centerY = rng() * height;
			const radius = (Math.min(width, height) * 0.16 + rng() * Math.min(width, height) * 0.26) | 0;
			const gradient = context.createRadialGradient(centerX, centerY, 0, centerX, centerY, radius);
			gradient.addColorStop(0, 'rgba(26, 26, 36, 0.23)');
			gradient.addColorStop(1, 'rgba(26, 26, 36, 0)');
			context.fillStyle = gradient;
			context.beginPath();
			context.arc(centerX, centerY, radius, 0, Math.PI * 2);
			context.fill();
		}

		const tile = 52 + Math.floor(rng() * 56);
		const tileOffsetX = Math.floor(rng() * tile);
		const tileOffsetY = Math.floor(rng() * tile);
		for (let y = -tileOffsetY; y <= height + tile; y += tile) {
			for (let x = -tileOffsetX; x <= width + tile; x += tile) {
				const alpha = 0.06 + rng() * 0.13;
				const localX = x + rng() * tile;
				const localY = y + rng() * tile;
				const pick = rng();
				context.strokeStyle = `rgba(26, 26, 36, ${alpha.toFixed(3)})`;
				context.fillStyle = `rgba(26, 26, 36, ${(alpha * 0.86).toFixed(3)})`;
				context.lineWidth = 1;
				if (pick < 0.34) {
					context.beginPath();
					context.moveTo(x, y + tile);
					context.lineTo(x + tile, y);
					context.stroke();
					continue;
				}
				if (pick < 0.62) {
					const size = 2 + Math.floor(rng() * 6);
					context.beginPath();
					context.arc(localX, localY, size, 0, Math.PI * 2);
					context.fill();
					continue;
				}
				if (pick < 0.82) {
					const rectSize = 6 + Math.floor(rng() * 16);
					context.strokeRect(localX - rectSize / 2, localY - rectSize / 2, rectSize, rectSize);
					continue;
				}
				context.beginPath();
				context.moveTo(localX - 8, localY);
				context.lineTo(localX + 8, localY);
				context.moveTo(localX, localY - 8);
				context.lineTo(localX, localY + 8);
				context.stroke();
			}
		}

		const vignette = context.createRadialGradient(
			width * 0.5,
			height * 0.5,
			Math.min(width, height) * 0.18,
			width * 0.5,
			height * 0.5,
			Math.max(width, height) * 0.8
		);
		vignette.addColorStop(0, 'rgba(13, 13, 18, 0)');
		vignette.addColorStop(1, 'rgba(13, 13, 18, 0.42)');
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

	$: if (seed !== lastDrawnSeed) {
		lastDrawnSeed = seed;
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
		filter: saturate(0.72) contrast(1.03);
		animation: monochrome-room-drift 54s ease-in-out infinite alternate;
	}

	.pattern-overlay {
		position: absolute;
		inset: 0;
		background: rgba(13, 13, 18, 0.85);
		backdrop-filter: blur(2px);
		-webkit-backdrop-filter: blur(2px);
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
