<script lang="ts">
	const MIN_HOURS = 0.1;
	const MAX_HOURS = 15 * 24;
	const KEYBOARD_STEP_HOURS = 0.5;
	const DIAL_SIZE = 190;
	const CENTER = DIAL_SIZE / 2;
	const OUTER_RADIUS = 87;
	const MARKER_RADIUS = 80;
	const EVEN_LABEL_RADIUS = 67;
	const ODD_LABEL_RADIUS = 73;
	const PIN_RADIUS = 76;
	const HOURS_PER_DAY = 24;
	const DIAL_LABEL_COUNT = 16;
	const DEGREES_PER_LABEL = 360 / DIAL_LABEL_COUNT;
	const START_ANGLE = 30;
	const DAY_MARKERS = Array.from({ length: DIAL_LABEL_COUNT }, (_, index) => index);

	export let valueHours = 6;
	export let disabled = false;
	let activePointerId: number | null = null;
	let isDragging = false;
	let isClockExpanded = false;
	let isNumericInputFocused = false;
	let numericHoursInput = '';

	$: normalizedValue = normalizeHours(valueHours);
	$: pinAngle = angleFromHours(normalizedValue);
	$: pinPoint = pointForAngle(pinAngle, PIN_RADIUS);
	$: if (!isNumericInputFocused) {
		numericHoursInput = formatNumber(normalizedValue);
	}

	function normalizeHours(input: number) {
		const safe = Number.isFinite(input) ? input : 6;
		const clamped = Math.min(MAX_HOURS, Math.max(MIN_HOURS, safe));
		return Math.round(clamped * 10) / 10;
	}

	function normalizeAngle(angle: number) {
		return ((angle % 360) + 360) % 360;
	}

	function angleFromDayValue(dayValue: number) {
		return normalizeAngle(START_ANGLE + dayValue * DEGREES_PER_LABEL);
	}

	function angleFromHours(hours: number) {
		const dayValue = normalizeHours(hours) / HOURS_PER_DAY;
		return angleFromDayValue(dayValue);
	}

	function hoursFromAngle(angle: number) {
		const normalizedFromStart = normalizeAngle(angle - START_ANGLE);
		const dayValue = normalizedFromStart / DEGREES_PER_LABEL;
		return normalizeHours(dayValue * HOURS_PER_DAY);
	}

	function radiansForAngle(angle: number) {
		return ((angle - 90) * Math.PI) / 180;
	}

	function pointForAngle(angle: number, radius: number) {
		const radians = radiansForAngle(angle);
		return {
			x: CENTER + Math.cos(radians) * radius,
			y: CENTER + Math.sin(radians) * radius
		};
	}

	function angleFromPointer(clientX: number, clientY: number, target: HTMLElement) {
		const rect = target.getBoundingClientRect();
		const dx = clientX - (rect.left + rect.width / 2);
		const dy = clientY - (rect.top + rect.height / 2);
		const degrees = (Math.atan2(dy, dx) * 180) / Math.PI;
		return normalizeAngle(degrees + 90);
	}

	function formatNumber(value: number) {
		const rounded = Math.round(value * 10) / 10;
		return Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1);
	}

	function formatSelectedDuration(hours: number) {
		const safeHours = normalizeHours(hours);
		if (safeHours < HOURS_PER_DAY) {
			return `${formatNumber(safeHours)}hrs`;
		}
		const days = Math.floor(safeHours / HOURS_PER_DAY);
		const remainingHours = Math.round((safeHours - days * HOURS_PER_DAY) * 10) / 10;
		if (remainingHours <= 0) {
			return `${days}d`;
		}
		return `${days}d ${formatNumber(remainingHours)}hrs`;
	}

	function parseHoursInput(raw: string) {
		const trimmed = raw.trim();
		if (trimmed === '') {
			return null;
		}
		const parsed = Number(trimmed);
		return Number.isFinite(parsed) ? parsed : null;
	}

	function labelRadius(markerDay: number) {
		return markerDay % 2 === 0 ? EVEN_LABEL_RADIUS : ODD_LABEL_RADIUS;
	}

	function setByAngle(angle: number) {
		if (disabled) {
			return;
		}
		valueHours = hoursFromAngle(angle);
	}

	function onDialPointerDown(event: PointerEvent) {
		if (disabled) {
			return;
		}
		if (event.pointerType === 'mouse' && event.button !== 0) {
			return;
		}
		const target = event.currentTarget as HTMLElement | null;
		if (!target) {
			return;
		}
		activePointerId = event.pointerId;
		isDragging = true;
		isClockExpanded = true;
		target.setPointerCapture(event.pointerId);
		setByAngle(angleFromPointer(event.clientX, event.clientY, target));
		event.preventDefault();
	}

	function onDialPointerMove(event: PointerEvent) {
		if (disabled || activePointerId === null || event.pointerId !== activePointerId) {
			return;
		}
		const target = event.currentTarget as HTMLElement | null;
		if (!target) {
			return;
		}
		setByAngle(angleFromPointer(event.clientX, event.clientY, target));
	}

	function stopDialDrag(event: PointerEvent) {
		if (activePointerId === null || event.pointerId !== activePointerId) {
			return;
		}
		const target = event.currentTarget as HTMLElement | null;
		if (target?.hasPointerCapture(event.pointerId)) {
			target.releasePointerCapture(event.pointerId);
		}
		activePointerId = null;
		isDragging = false;
		isClockExpanded = false;
	}

	function onDialLostPointerCapture(event: PointerEvent) {
		if (event.pointerId !== activePointerId) {
			return;
		}
		activePointerId = null;
		isDragging = false;
		isClockExpanded = false;
	}

	function onDialKeyDown(event: KeyboardEvent) {
		if (disabled) {
			return;
		}
		if (event.key === 'ArrowRight' || event.key === 'ArrowUp') {
			event.preventDefault();
			valueHours = normalizeHours(normalizedValue + KEYBOARD_STEP_HOURS);
			return;
		}
		if (event.key === 'ArrowLeft' || event.key === 'ArrowDown') {
			event.preventDefault();
			valueHours = normalizeHours(normalizedValue - KEYBOARD_STEP_HOURS);
		}
	}

	function onNumericInput(event: Event) {
		const target = event.currentTarget as HTMLInputElement | null;
		if (!target) {
			return;
		}
		numericHoursInput = target.value;
		const parsed = parseHoursInput(target.value);
		if (parsed === null) {
			return;
		}
		valueHours = normalizeHours(parsed);
	}

	function onNumericFocus() {
		isNumericInputFocused = true;
	}

	function onNumericBlur() {
		isNumericInputFocused = false;
		const parsed = parseHoursInput(numericHoursInput);
		if (parsed !== null) {
			valueHours = normalizeHours(parsed);
		}
		numericHoursInput = formatNumber(normalizedValue);
	}
</script>

<section class="expiry-picker" aria-label="Room expiry picker">
	<div class="meta-row">
		<span class="meta-label">Room expiry</span>
		<strong class="meta-value">{formatSelectedDuration(normalizedValue)}</strong>
	</div>
	<div class="picker-row">
		<div
			class="clock-face {disabled ? 'is-disabled' : ''} {isDragging
				? 'is-dragging'
				: ''} {isClockExpanded ? 'is-expanded' : ''}"
			role="slider"
			aria-label="Room expiry in hours"
			aria-valuemin={MIN_HOURS}
			aria-valuemax={MAX_HOURS}
			aria-valuenow={normalizedValue}
			aria-valuetext={formatSelectedDuration(normalizedValue)}
			tabindex={disabled ? -1 : 0}
			on:pointerdown={onDialPointerDown}
			on:pointermove={onDialPointerMove}
			on:pointerup={stopDialDrag}
			on:pointercancel={stopDialDrag}
			on:lostpointercapture={onDialLostPointerCapture}
			on:keydown={onDialKeyDown}
		>
			<svg viewBox={`0 0 ${DIAL_SIZE} ${DIAL_SIZE}`} aria-hidden="true">
				<circle class="outer-ring" cx={CENTER} cy={CENTER} r={OUTER_RADIUS}></circle>
				{#each DAY_MARKERS as markerDay (markerDay)}
					{@const markerAngle = angleFromDayValue(markerDay)}
					{@const markerPoint = pointForAngle(markerAngle, MARKER_RADIUS)}
					{@const labelPoint = pointForAngle(markerAngle, labelRadius(markerDay))}
					<circle
						class="day-marker {markerDay % 2 === 0 ? 'is-even' : 'is-odd'}"
						cx={markerPoint.x}
						cy={markerPoint.y}
						r={markerDay % 2 === 0 ? 2.5 : 1.8}
					></circle>
					<text
						class="tick-label {markerDay % 2 === 0 ? 'is-even' : 'is-odd'}"
						x={labelPoint.x}
						y={labelPoint.y}
					>
						{markerDay}
					</text>
				{/each}
				<line class="pin-line" x1={CENTER} y1={CENTER} x2={pinPoint.x} y2={pinPoint.y}></line>
				<circle class="pin-head" cx={pinPoint.x} cy={pinPoint.y} r="5.2"></circle>
				<circle class="pin-center" cx={CENTER} cy={CENTER} r="5"></circle>
			</svg>
		</div>
		<div class="numeric-field">
			<label for="room-duration-hours-input">Hours (numeric)</label>
			<input
				id="room-duration-hours-input"
				type="text"
				inputmode="decimal"
				placeholder="e.g. 6 or 24.5"
				value={numericHoursInput}
				{disabled}
				on:input={onNumericInput}
				on:focus={onNumericFocus}
				on:blur={onNumericBlur}
			/>
			<small>Range: 0.1 to 360 hours.</small>
		</div>
	</div>
	<p class="help-text">Hold/click the clock to enlarge it, drag to set time, release to shrink.</p>
</section>

<style>
	.expiry-picker {
		display: grid;
		gap: 0.45rem;
		width: 100%;
	}

	.meta-row {
		width: 100%;
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.6rem;
	}

	.meta-label {
		font-size: 0.82rem;
		font-weight: 600;
		color: #475569;
	}

	.meta-value {
		font-size: 0.84rem;
		color: #0f766e;
		letter-spacing: 0.01em;
		font-variant-numeric: tabular-nums;
	}

	.picker-row {
		display: flex;
		align-items: center;
		gap: 0.65rem;
		width: 100%;
	}

	.clock-face {
		width: 96px;
		aspect-ratio: 1;
		flex: 0 0 auto;
		border-radius: 999px;
		border: 1px solid #d9e2ef;
		background: radial-gradient(circle at center, #f8fbff 0 44%, #f2f7fd 44% 72%, #edf3fb 72% 100%);
		box-shadow:
			inset 0 0 0 1px rgba(255, 255, 255, 0.7),
			0 6px 16px rgba(15, 23, 42, 0.08);
		cursor: pointer;
		outline: none;
		user-select: none;
		touch-action: none;
	}

	.clock-face.is-expanded {
		width: 188px;
	}

	.clock-face:focus-visible {
		box-shadow:
			0 0 0 3px rgba(20, 184, 166, 0.2),
			inset 0 0 0 1px rgba(255, 255, 255, 0.7),
			0 6px 16px rgba(15, 23, 42, 0.08);
	}

	.clock-face.is-disabled {
		cursor: not-allowed;
		opacity: 0.68;
	}

	.clock-face.is-dragging {
		cursor: grabbing;
	}

	.numeric-field {
		display: grid;
		gap: 0.34rem;
		flex: 1;
		min-width: 0;
		text-align: left;
	}

	.numeric-field label {
		font-size: 0.78rem;
		font-weight: 600;
		color: #475569;
	}

	.numeric-field input {
		padding: 7px 9px;
		border: 1px solid #cbd5e1;
		border-radius: 6px;
		font-size: 0.86rem;
		color: #0f172a;
		font-variant-numeric: tabular-nums;
	}

	.numeric-field input:focus-visible {
		outline: 2px solid rgba(20, 184, 166, 0.35);
		outline-offset: 1px;
		border-color: rgba(15, 118, 110, 0.6);
	}

	.numeric-field input:disabled {
		background: #f8fafc;
		color: #64748b;
	}

	.numeric-field small {
		font-size: 0.7rem;
		color: #64748b;
	}

	svg {
		width: 100%;
		height: 100%;
		display: block;
	}

	.outer-ring {
		fill: none;
		stroke: #c8d6e8;
		stroke-width: 1.25;
	}

	.day-marker {
		fill: #93a2bb;
		opacity: 0.72;
	}

	.day-marker.is-odd {
		opacity: 0.55;
	}

	.tick-label {
		fill: #64748b;
		text-anchor: middle;
		dominant-baseline: middle;
		font-variant-numeric: tabular-nums;
	}

	.tick-label.is-even {
		font-size: 9px;
		font-weight: 700;
	}

	.tick-label.is-odd {
		font-size: 6.2px;
		font-weight: 600;
		opacity: 0.76;
	}

	.pin-line {
		stroke: #0f766e;
		stroke-width: 2.3;
		stroke-linecap: round;
	}

	.pin-head {
		fill: #0f766e;
	}

	.pin-center {
		fill: #ffffff;
		stroke: #0f766e;
		stroke-width: 2.2;
	}

	.help-text {
		margin: 0;
		font-size: 0.7rem;
		color: #64748b;
		text-align: left;
	}

	@media (max-width: 760px) {
		.picker-row {
			align-items: flex-start;
		}

		.clock-face {
			width: 88px;
		}

		.clock-face.is-expanded {
			width: 158px;
		}
	}
</style>
