<script lang="ts">
	import { onDestroy, onMount } from 'svelte';

	export let seed = '';

	// ── DOM refs ────────────────────────────────────────────────────
	let hostEl: HTMLDivElement | null = null;
	let canvasEl: HTMLCanvasElement | null = null;
	let resizeObserver: ResizeObserver | null = null;
	let rafId = 0;
	let animRafId = 0;
	let startTime = 0;
	let cachedBitmap: ImageBitmap | null = null;
	let cachedBitmapKey = '';

	// ── PRNG / hashing ──────────────────────────────────────────────
	function fnv1a(s: string): number {
		let h = 2166136261;
		for (let i = 0; i < s.length; i++) {
			h ^= s.charCodeAt(i);
			h = Math.imul(h, 16777619);
		}
		return h >>> 0;
	}

	function makePRNG(seed32: number) {
		let s = seed32 >>> 0;
		return () => {
			s = (s + 0x6d2b79f5) | 0;
			let t = Math.imul(s ^ (s >>> 15), 1 | s);
			t ^= t + Math.imul(t ^ (t >>> 7), 61 | t);
			return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
		};
	}

	// ── Theme detection ─────────────────────────────────────────────
	function isDarkMode(): boolean {
		if (typeof document === 'undefined') return true;
		const root = document.documentElement;
		if (root.getAttribute('data-theme') === 'dark') return true;
		if (root.classList.contains('theme-dark')) return true;
		if (typeof window !== 'undefined' && window.matchMedia?.('(prefers-color-scheme: dark)').matches) return true;
		return false;
	}

	// ── Flow-field helpers ──────────────────────────────────────────
	// Returns an angle in radians for canvas position (x, y).
	// Uses layered sin/cos waves seeded from rng parameters.
	function fieldAngle(
		x: number, y: number, w: number, h: number,
		f1: number, f2: number, f3: number, f4: number,
		ph1: number, ph2: number, ph3: number,
		timeOffset: number
	): number {
		const nx = (x / w) * Math.PI * 2;
		const ny = (y / h) * Math.PI * 2;
		const a =
			Math.sin(nx * f1 + ph1 + timeOffset * 0.18) * Math.cos(ny * f2 + ph2) +
			Math.cos(nx * f3 - ny * f1 * 0.6 + ph3 + timeOffset * 0.11) * 0.6 +
			Math.sin((nx + ny) * f4 * 0.5 + ph1 * 1.3 + timeOffset * 0.07) * 0.35;
		return a * Math.PI;
	}

	// ── Core renderer ────────────────────────────────────────────────
	// Draws the base art into an offscreen canvas and returns it as ImageBitmap.
	async function buildBase(w: number, h: number, seedText: string): Promise<ImageBitmap | null> {
		if (typeof OffscreenCanvas === 'undefined') {
			// Fallback: use a regular canvas
			const tmp = document.createElement('canvas');
			tmp.width = w;
			tmp.height = h;
			const ctx = tmp.getContext('2d');
			if (!ctx) return null;
			drawArt(ctx, w, h, seedText, 0);
			return await createImageBitmap(tmp);
		}
		const osc = new OffscreenCanvas(w, h);
		const ctx = osc.getContext('2d') as OffscreenCanvasRenderingContext2D | null;
		if (!ctx) return null;
		drawArt(ctx as unknown as CanvasRenderingContext2D, w, h, seedText, 0);
		return await osc.transferToImageBitmap();
	}

	function drawArt(
		ctx: CanvasRenderingContext2D,
		w: number, h: number,
		seedText: string,
		timeOffset: number
	) {
		const rng = makePRNG(fnv1a(seedText));
		const dark = isDarkMode();

		// Background
		ctx.fillStyle = dark ? '#0c0e16' : '#f2f4f9';
		ctx.fillRect(0, 0, w, h);

		// ── Flow-field parameters (seeded) ──────────────────────────
		const f1 = 1.4 + rng() * 1.2;
		const f2 = 1.1 + rng() * 1.0;
		const f3 = 0.9 + rng() * 0.8;
		const f4 = 1.6 + rng() * 1.4;
		const ph1 = rng() * Math.PI * 2;
		const ph2 = rng() * Math.PI * 2;
		const ph3 = rng() * Math.PI * 2;

		// Stroke color — visible contrast in both modes
		const strokeR = dark ? 200 : 14;
		const strokeG = dark ? 210 : 16;
		const strokeB = dark ? 240 : 24;

		const diag = Math.sqrt(w * w + h * h);
		const stepSize = Math.max(2, diag * 0.0025);   // adaptive step

		// ── Flow lines ───────────────────────────────────────────────
		const lineCount = Math.round(180 + rng() * 120);   // 180-300 lines
		for (let li = 0; li < lineCount; li++) {
			const startX = rng() * w;
			const startY = rng() * h;
			const maxSteps = 80 + Math.floor(rng() * 180);  // 80-260 steps
			const alpha = 0.06 + rng() * 0.14;              // 0.06-0.20
			const lw = 0.55 + rng() * 0.85;                  // 0.55-1.4 px

			ctx.strokeStyle = `rgba(${strokeR},${strokeG},${strokeB},${alpha.toFixed(3)})`;
			ctx.lineWidth = lw;
			ctx.lineCap = 'round';
			ctx.beginPath();

			let x = startX;
			let y = startY;
			let moved = false;

			for (let step = 0; step < maxSteps; step++) {
				const angle = fieldAngle(x, y, w, h, f1, f2, f3, f4, ph1, ph2, ph3, timeOffset);
				const nx = x + Math.cos(angle) * stepSize;
				const ny = y + Math.sin(angle) * stepSize;

				// Stop if out of bounds
				if (nx < -w * 0.1 || nx > w * 1.1 || ny < -h * 0.1 || ny > h * 1.1) break;

				if (!moved) {
					ctx.moveTo(x, y);
					moved = true;
				}
				ctx.lineTo(nx, ny);
				x = nx;
				y = ny;
			}
			if (moved) ctx.stroke();
		}

		// ── Accent marks (scattered geometric) ───────────────────────
		const accentCount = 40 + Math.floor(rng() * 50);
		for (let ai = 0; ai < accentCount; ai++) {
			const ax = rng() * w;
			const ay = rng() * h;
			const alpha = 0.04 + rng() * 0.10;
			const pick = rng();
			ctx.strokeStyle = `rgba(${strokeR},${strokeG},${strokeB},${alpha.toFixed(3)})`;
			ctx.lineWidth = 0.7 + rng() * 0.8;

			if (pick < 0.4) {
				// Circle
				const r = 4 + rng() * (diag * 0.025);
				ctx.beginPath();
				ctx.arc(ax, ay, r, 0, Math.PI * 2);
				ctx.stroke();
			} else if (pick < 0.72) {
				// Diamond
				const s = 5 + rng() * (diag * 0.018);
				ctx.beginPath();
				ctx.moveTo(ax, ay - s);
				ctx.lineTo(ax + s, ay);
				ctx.lineTo(ax, ay + s);
				ctx.lineTo(ax - s, ay);
				ctx.closePath();
				ctx.stroke();
			} else {
				// Cross / plus
				const len = 4 + rng() * (diag * 0.016);
				const angle = rng() * Math.PI;
				ctx.beginPath();
				ctx.moveTo(ax - Math.cos(angle) * len, ay - Math.sin(angle) * len);
				ctx.lineTo(ax + Math.cos(angle) * len, ay + Math.sin(angle) * len);
				ctx.moveTo(ax - Math.cos(angle + Math.PI / 2) * len * 0.6, ay - Math.sin(angle + Math.PI / 2) * len * 0.6);
				ctx.lineTo(ax + Math.cos(angle + Math.PI / 2) * len * 0.6, ay + Math.sin(angle + Math.PI / 2) * len * 0.6);
				ctx.stroke();
			}
		}

		// ── Vignette ─────────────────────────────────────────────────
		const vig = ctx.createRadialGradient(w * 0.5, h * 0.5, 0, w * 0.5, h * 0.5, diag * 0.65);
		const vigColor = dark ? '12,14,22' : '235,237,245';
		vig.addColorStop(0,   `rgba(${vigColor},0)`);
		vig.addColorStop(0.6, `rgba(${vigColor},0)`);
		vig.addColorStop(1,   `rgba(${vigColor},0.55)`);
		ctx.fillStyle = vig;
		ctx.fillRect(0, 0, w, h);
	}

	// ── Compositing loop ─────────────────────────────────────────────
	// Renders the cached bitmap onto the visible canvas each frame,
	// shifting it slowly for a parallax drift without re-computing the art.
	function startLoop() {
		startTime = performance.now();
		animRafId = requestAnimationFrame(compositeLoop);
	}

	function compositeLoop(now: number) {
		if (!canvasEl || !hostEl) return;
		const elapsed = (now - startTime) / 1000; // seconds

		const ctx = canvasEl.getContext('2d');
		if (!ctx) return;

		const dpr = Math.min(2, window.devicePixelRatio || 1);
		const w = canvasEl.width / dpr;
		const h = canvasEl.height / dpr;

		ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
		ctx.clearRect(0, 0, w, h);

		if (cachedBitmap) {
			// Slow drift: shift by tiny amounts, loop seamlessly with modulo
			const PERIOD = 120; // seconds for one full drift cycle
			const t = (elapsed % PERIOD) / PERIOD;
			const dx = Math.sin(t * Math.PI * 2) * w * 0.018;
			const dy = Math.cos(t * Math.PI * 2 * 0.7) * h * 0.012;
			const scale = 1.04 + Math.sin(t * Math.PI * 2 * 1.3) * 0.01;

			ctx.save();
			ctx.translate(w * 0.5, h * 0.5);
			ctx.scale(scale, scale);
			ctx.translate(-w * 0.5 + dx, -h * 0.5 + dy);
			ctx.drawImage(cachedBitmap, 0, 0, w, h);
			ctx.restore();
		}

		animRafId = requestAnimationFrame(compositeLoop);
	}

	// ── Resize / redraw ───────────────────────────────────────────────
	function queueRebuild() {
		if (rafId) cancelAnimationFrame(rafId);
		rafId = requestAnimationFrame(() => {
			rafId = 0;
			void rebuild();
		});
	}

	async function rebuild() {
		if (!hostEl || !canvasEl || typeof window === 'undefined') return;

		const dpr = Math.min(2, window.devicePixelRatio || 1);
		const w = Math.max(1, Math.floor(hostEl.clientWidth));
		const h = Math.max(1, Math.floor(hostEl.clientHeight));

		if (canvasEl.width !== Math.floor(w * dpr) || canvasEl.height !== Math.floor(h * dpr)) {
			canvasEl.width = Math.floor(w * dpr);
			canvasEl.height = Math.floor(h * dpr);
		}

		const key = `${normalized}:${w}:${h}:${isDarkMode() ? 'd' : 'l'}`;
		if (key !== cachedBitmapKey) {
			cachedBitmapKey = key;
			// Draw at 1.04× to give headroom for drift without edge gaps
			const bw = Math.ceil(w * 1.08);
			const bh = Math.ceil(h * 1.08);
			const bm = await buildBase(bw, bh, normalized);
			if (key === cachedBitmapKey) {
				cachedBitmap?.close?.();
				cachedBitmap = bm;
			}
		}
	}

	// ── Reactivity ───────────────────────────────────────────────────
	$: normalized = seed.trim() || 'tora-default';
	$: {
		void normalized; // depend on seed
		if (typeof window !== 'undefined') queueRebuild();
	}

	// ── Lifecycle ────────────────────────────────────────────────────
	onMount(() => {
		void rebuild();
		startLoop();

		if (typeof ResizeObserver !== 'undefined' && hostEl) {
			resizeObserver = new ResizeObserver(queueRebuild);
			resizeObserver.observe(hostEl);
		} else {
			window.addEventListener('resize', queueRebuild, { passive: true });
		}

		// Re-draw when theme changes
		const mq = window.matchMedia?.('(prefers-color-scheme: dark)');
		const onThemeChange = () => {
			cachedBitmapKey = '';
			cachedBitmap?.close?.();
			cachedBitmap = null;
			queueRebuild();
		};
		mq?.addEventListener('change', onThemeChange);

		// Also watch data-theme attribute mutations
		const attrObserver = new MutationObserver(onThemeChange);
		attrObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'class'] });

		return () => {
			mq?.removeEventListener('change', onThemeChange);
			attrObserver.disconnect();
			resizeObserver?.disconnect();
			if (!resizeObserver) window.removeEventListener('resize', queueRebuild);
			if (rafId) cancelAnimationFrame(rafId);
			if (animRafId) cancelAnimationFrame(animRafId);
			cachedBitmap?.close?.();
		};
	});

	onDestroy(() => {
		if (rafId) cancelAnimationFrame(rafId);
		if (animRafId) cancelAnimationFrame(animRafId);
		cachedBitmap?.close?.();
	});
</script>

<div class="mrb-host" bind:this={hostEl} aria-hidden="true">
	<canvas bind:this={canvasEl} class="mrb-canvas"></canvas>
	<div class="mrb-overlay"></div>
</div>

<style>
	.mrb-host {
		position: absolute;
		inset: 0;
		overflow: hidden;
		pointer-events: none;
		z-index: 0;
	}

	.mrb-canvas {
		position: absolute;
		inset: 0;
		width: 100%;
		height: 100%;
		display: block;
	}

	/* Very light overlay — just enough to help readability of text above,
	   without killing the art. No blur that destroys the visual. */
	.mrb-overlay {
		position: absolute;
		inset: 0;
		/* subtle center-lit vignette that fades to slightly tinted edges */
		background: radial-gradient(
			ellipse 80% 80% at 50% 45%,
			transparent 30%,
			rgba(0, 0, 0, 0.08) 100%
		);
	}
</style>
