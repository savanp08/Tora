<script lang="ts">
	import { browser } from '$app/environment';
	import { createEventDispatcher } from 'svelte';
	import { resolveApiBase } from '$lib/config/apiBase';
	import { currentUser } from '$lib/store';
	import { fieldSchemaStore } from '$lib/stores/fieldSchema';
	import { normalizeRoomIDValue } from '$lib/utils/chat/core';

	type IntakeFieldType = 'text' | 'textarea' | 'number' | 'email' | 'select' | 'checkbox';

	type IntakeFormField = {
		field_id: string;
		label: string;
		field_type: IntakeFieldType;
		required: boolean;
		options?: string[];
	};

	type IntakeFormRecord = {
		form_id: string;
		room_id: string;
		title: string;
		description?: string;
		fields: IntakeFormField[];
		target_status: string;
		target_sprint?: string;
		enabled: boolean;
		submission_count?: number;
		created_at: string;
	};

	type IntakeSubmissionRecord = {
		form_id: string;
		submission_id: string;
		room_id: string;
		task_id?: string;
		data?: Record<string, unknown>;
		submitter_email?: string;
		submitted_at: string;
	};

	type BuilderField = {
		id: string;
		field_id: string;
		label: string;
		field_type: IntakeFieldType;
		required: boolean;
		optionsInput: string;
	};

	export let roomId = '';
	export let canEdit = true;

	const dispatch = createEventDispatcher<{
		requestTaskEdit: { taskId: string };
	}>();

	const API_BASE_RAW = import.meta.env.VITE_API_BASE as string | undefined;
	const API_BASE = resolveApiBase(API_BASE_RAW);
	const FIELD_TYPE_OPTIONS: Array<{ value: IntakeFieldType; label: string }> = [
		{ value: 'text', label: 'Text' },
		{ value: 'textarea', label: 'Textarea' },
		{ value: 'number', label: 'Number' },
		{ value: 'email', label: 'Email' },
		{ value: 'select', label: 'Select' },
		{ value: 'checkbox', label: 'Checkbox' }
	];
	const TARGET_STATUS_OPTIONS = [
		{ value: 'todo', label: 'To Do' },
		{ value: 'in_progress', label: 'Working on it' },
		{ value: 'done', label: 'Done' }
	];

	let forms: IntakeFormRecord[] = [];
	let formsLoading = false;
	let formsError = '';
	let selectedFormId = '';
	let selectedFormSubmissions: IntakeSubmissionRecord[] = [];
	let submissionsLoading = false;
	let submissionsError = '';
	let builderOpen = false;
	let editingFormId = '';
	let builderTitle = '';
	let builderDescription = '';
	let builderTargetStatus = 'todo';
	let builderTargetSprint = '';
	let builderEnabled = true;
	let builderFields: BuilderField[] = [];
	let builderError = '';
	let builderSaving = false;
	let togglingFormIds = new Set<string>();
	let deletingFormIds = new Set<string>();
	let draggingFieldId = '';
	let lastLoadedRoomId = '';
	let loadedSubmissionsFormId = '';
	let copyFeedbackByFormId: Record<string, string> = {};
	let copyFeedbackTimers = new Map<string, ReturnType<typeof setTimeout>>();

	$: sessionUserId = ($currentUser?.id || '').trim();
	$: sessionUsername = ($currentUser?.username || '').trim();
	$: normalizedRoomId = normalizeRoomIDValue(roomId);
	$: roomFieldSchemas = [...$fieldSchemaStore];
	$: roomFieldSchemaOptions = roomFieldSchemas
		.map((schema) => ({
			id: schema.fieldId,
			label: schema.name,
			fieldType: (schema.fieldType || '').trim().toLowerCase()
		}))
		.sort((left, right) => left.label.localeCompare(right.label, undefined, { sensitivity: 'base' }));
	$: roomFieldSchemaLabelById = new Map(roomFieldSchemaOptions.map((option) => [option.id, option.label]));
	$: selectedForm = forms.find((form) => form.form_id === selectedFormId) ?? null;
	$: if (normalizedRoomId !== lastLoadedRoomId) {
		lastLoadedRoomId = normalizedRoomId;
		selectedFormId = '';
		selectedFormSubmissions = [];
		submissionsError = '';
		loadedSubmissionsFormId = '';
		builderOpen = false;
		editingFormId = '';
		resetBuilder();
		if (normalizedRoomId) {
			void loadForms();
		} else {
			forms = [];
			formsError = '';
		}
	}
	$: if (selectedForm && selectedForm.form_id !== loadedSubmissionsFormId && !submissionsLoading) {
		void loadSubmissions(selectedForm.form_id);
	}

	function withSessionHeaders(headers: Record<string, string> = {}) {
		if (sessionUserId) {
			return {
				...headers,
				'X-User-Id': sessionUserId,
				...(sessionUsername ? { 'X-User-Name': sessionUsername } : {})
			};
		}
		if (sessionUsername) {
			return {
				...headers,
				'X-User-Name': sessionUsername
			};
		}
		return headers;
	}

	async function parseError(response: Response) {
		const payload = (await response.json().catch(() => null)) as
			| {
					error?: string;
					message?: string;
			  }
			| null;
		return payload?.error?.trim() || payload?.message?.trim() || `HTTP ${response.status}`;
	}

	function safeFieldType(value: string): IntakeFieldType {
		const normalized = value.trim().toLowerCase();
		if (
			normalized === 'text' ||
			normalized === 'textarea' ||
			normalized === 'number' ||
			normalized === 'email' ||
			normalized === 'select' ||
			normalized === 'checkbox'
		) {
			return normalized;
		}
		return 'text';
	}

	function makeBuilderField(partial?: Partial<BuilderField>): BuilderField {
		const candidateSchema = roomFieldSchemaOptions.find((option) => option.id === partial?.field_id);
		return {
			id:
				partial?.id ||
				`bf_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`,
			field_id: partial?.field_id || roomFieldSchemaOptions[0]?.id || '',
			label:
				partial?.label?.trim() || candidateSchema?.label?.trim() || roomFieldSchemaOptions[0]?.label || '',
			field_type: safeFieldType(partial?.field_type || 'text'),
			required: Boolean(partial?.required),
			optionsInput: partial?.optionsInput || ''
		};
	}

	function resetBuilder() {
		builderTitle = '';
		builderDescription = '';
		builderTargetStatus = 'todo';
		builderTargetSprint = '';
		builderEnabled = true;
		builderFields = [makeBuilderField()];
		builderError = '';
	}

	function openCreateBuilder() {
		editingFormId = '';
		builderOpen = true;
		resetBuilder();
	}

	function openEditBuilder(form: IntakeFormRecord) {
		editingFormId = form.form_id;
		builderOpen = true;
		builderTitle = form.title;
		builderDescription = form.description || '';
		builderTargetStatus = form.target_status || 'todo';
		builderTargetSprint = form.target_sprint || '';
		builderEnabled = form.enabled;
		builderError = '';
		builderFields =
			form.fields.length > 0
				? form.fields.map((field) =>
						makeBuilderField({
							field_id: field.field_id,
							label: field.label,
							field_type: field.field_type,
							required: field.required,
							optionsInput: (field.options || []).join(', ')
						})
					)
				: [makeBuilderField()];
	}

	function normalizeOptionsInput(raw: string) {
		return [
			...new Set(
				raw
					.split(',')
					.map((option) => option.trim())
					.filter(Boolean)
			)
		];
	}

	function normalizeBuilderFieldsForRequest() {
		const payload: IntakeFormField[] = [];
		for (let index = 0; index < builderFields.length; index += 1) {
			const field = builderFields[index];
			const fieldId = field.field_id.trim();
			const label = field.label.trim();
			if (!fieldId) {
				return { fields: [] as IntakeFormField[], error: `Field ${index + 1}: select a room field` };
			}
			if (!label) {
				return { fields: [] as IntakeFormField[], error: `Field ${index + 1}: label is required` };
			}
			const fieldType = safeFieldType(field.field_type);
			const options = fieldType === 'select' ? normalizeOptionsInput(field.optionsInput) : [];
			if (fieldType === 'select' && options.length === 0) {
				return {
					fields: [] as IntakeFormField[],
					error: `Field ${index + 1}: add select options (comma separated)`
				};
			}
			payload.push({
				field_id: fieldId,
				label,
				field_type: fieldType,
				required: Boolean(field.required),
				options: options.length > 0 ? options : undefined
			});
		}
		if (payload.length === 0) {
			return { fields: [] as IntakeFormField[], error: 'Add at least one field' };
		}
		return { fields: payload, error: '' };
	}

	async function loadForms() {
		if (!normalizedRoomId) {
			forms = [];
			formsError = '';
			return;
		}
		formsLoading = true;
		formsError = '';
		try {
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomId)}/forms`,
				{
					method: 'GET',
					credentials: 'include',
					headers: withSessionHeaders()
				}
			);
			if (!response.ok) {
				throw new Error(await parseError(response));
			}
			const payload = (await response.json().catch(() => [])) as IntakeFormRecord[];
			forms = Array.isArray(payload) ? payload : [];
			if (forms.length > 0) {
				if (!forms.some((form) => form.form_id === selectedFormId)) {
					selectedFormId = forms[0].form_id;
					await loadSubmissions(selectedFormId);
				}
			} else {
				selectedFormId = '';
				selectedFormSubmissions = [];
			}
		} catch (error) {
			formsError = error instanceof Error ? error.message : 'Failed to load forms';
			forms = [];
		} finally {
			formsLoading = false;
		}
	}

	async function loadSubmissions(formId: string) {
		const normalizedFormId = formId.trim();
		if (!normalizedRoomId || !normalizedFormId) {
			selectedFormSubmissions = [];
			loadedSubmissionsFormId = '';
			return;
		}
		submissionsLoading = true;
		submissionsError = '';
		try {
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomId)}/forms/${encodeURIComponent(normalizedFormId)}/submissions`,
				{
					method: 'GET',
					credentials: 'include',
					headers: withSessionHeaders()
				}
			);
			if (!response.ok) {
				throw new Error(await parseError(response));
			}
			const payload = (await response.json().catch(() => [])) as IntakeSubmissionRecord[];
			selectedFormSubmissions = Array.isArray(payload) ? payload : [];
			loadedSubmissionsFormId = normalizedFormId;
		} catch (error) {
			submissionsError = error instanceof Error ? error.message : 'Failed to load submissions';
			selectedFormSubmissions = [];
			loadedSubmissionsFormId = '';
		} finally {
			submissionsLoading = false;
		}
	}

	async function saveBuilder() {
		if (!normalizedRoomId || !canEdit) {
			return;
		}
		const title = builderTitle.trim();
		if (!title) {
			builderError = 'Form title is required';
			return;
		}
		const { fields, error } = normalizeBuilderFieldsForRequest();
		if (error) {
			builderError = error;
			return;
		}

		builderSaving = true;
		builderError = '';
		try {
			const endpoint = editingFormId
				? `${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomId)}/forms/${encodeURIComponent(editingFormId)}`
				: `${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomId)}/forms`;
			const method = editingFormId ? 'PATCH' : 'POST';
			const body = {
				title,
				description: builderDescription.trim(),
				fields,
				target_status: builderTargetStatus,
				target_sprint: builderTargetSprint.trim(),
				enabled: builderEnabled
			};
			const response = await fetch(endpoint, {
				method,
				credentials: 'include',
				headers: withSessionHeaders({ 'Content-Type': 'application/json' }),
				body: JSON.stringify(body)
			});
			if (!response.ok) {
				throw new Error(await parseError(response));
			}
			const payload = (await response.json().catch(() => null)) as IntakeFormRecord | null;
			await loadForms();
			if (payload?.form_id) {
				selectedFormId = payload.form_id;
				await loadSubmissions(payload.form_id);
			}
			builderOpen = false;
			editingFormId = '';
		} catch (error) {
			builderError = error instanceof Error ? error.message : 'Failed to save form';
		} finally {
			builderSaving = false;
		}
	}

	async function toggleFormEnabled(form: IntakeFormRecord) {
		if (!normalizedRoomId || !canEdit) {
			return;
		}
		const formId = form.form_id.trim();
		if (!formId || togglingFormIds.has(formId)) {
			return;
		}
		const nextSet = new Set(togglingFormIds);
		nextSet.add(formId);
		togglingFormIds = nextSet;
		try {
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomId)}/forms/${encodeURIComponent(formId)}`,
				{
					method: 'PATCH',
					credentials: 'include',
					headers: withSessionHeaders({ 'Content-Type': 'application/json' }),
					body: JSON.stringify({ enabled: !form.enabled })
				}
			);
			if (!response.ok) {
				throw new Error(await parseError(response));
			}
			await loadForms();
		} catch (error) {
			formsError = error instanceof Error ? error.message : 'Failed to toggle form';
		} finally {
			const next = new Set(togglingFormIds);
			next.delete(formId);
			togglingFormIds = next;
		}
	}

	async function deleteForm(form: IntakeFormRecord) {
		if (!normalizedRoomId || !canEdit) {
			return;
		}
		const formId = form.form_id.trim();
		if (!formId || deletingFormIds.has(formId)) {
			return;
		}
		const shouldDelete = window.confirm(`Delete "${form.title}" and all submissions?`);
		if (!shouldDelete) {
			return;
		}
		const nextSet = new Set(deletingFormIds);
		nextSet.add(formId);
		deletingFormIds = nextSet;
		try {
			const response = await fetch(
				`${API_BASE}/api/rooms/${encodeURIComponent(normalizedRoomId)}/forms/${encodeURIComponent(formId)}`,
				{
					method: 'DELETE',
					credentials: 'include',
					headers: withSessionHeaders()
				}
			);
			if (!response.ok) {
				throw new Error(await parseError(response));
			}
			if (selectedFormId === formId) {
				selectedFormId = '';
				selectedFormSubmissions = [];
				loadedSubmissionsFormId = '';
			}
			await loadForms();
		} catch (error) {
			formsError = error instanceof Error ? error.message : 'Failed to delete form';
		} finally {
			const next = new Set(deletingFormIds);
			next.delete(formId);
			deletingFormIds = next;
		}
	}

	async function copyFormLink(formId: string) {
		const normalizedFormId = formId.trim();
		if (!normalizedFormId || !browser) {
			return;
		}
		const url = `${window.location.origin}/form/${encodeURIComponent(normalizedFormId)}`;
		const setFeedback = (message: string) => {
			copyFeedbackByFormId = { ...copyFeedbackByFormId, [normalizedFormId]: message };
			const existing = copyFeedbackTimers.get(normalizedFormId);
			if (existing) {
				clearTimeout(existing);
			}
			const timer = setTimeout(() => {
				const next = { ...copyFeedbackByFormId };
				delete next[normalizedFormId];
				copyFeedbackByFormId = next;
				copyFeedbackTimers.delete(normalizedFormId);
			}, 1800);
			copyFeedbackTimers.set(normalizedFormId, timer);
		};
		try {
			await navigator.clipboard.writeText(url);
			setFeedback('Copied');
		} catch {
			setFeedback('Copy failed');
		}
	}

	function addBuilderField() {
		builderFields = [...builderFields, makeBuilderField()];
	}

	function removeBuilderField(fieldId: string) {
		if (builderFields.length <= 1) {
			return;
		}
		builderFields = builderFields.filter((field) => field.id !== fieldId);
	}

	function updateBuilderField(fieldId: string, updater: (field: BuilderField) => BuilderField) {
		builderFields = builderFields.map((field) => (field.id === fieldId ? updater(field) : field));
	}

	function onBuilderFieldDragStart(event: DragEvent, fieldId: string) {
		draggingFieldId = fieldId;
		event.dataTransfer?.setData('text/plain', fieldId);
		event.dataTransfer?.setDragImage(event.currentTarget as Element, 10, 10);
	}

	function onBuilderFieldDrop(targetFieldId: string) {
		const sourceFieldId = draggingFieldId;
		draggingFieldId = '';
		if (!sourceFieldId || sourceFieldId === targetFieldId) {
			return;
		}
		const sourceIndex = builderFields.findIndex((field) => field.id === sourceFieldId);
		const targetIndex = builderFields.findIndex((field) => field.id === targetFieldId);
		if (sourceIndex < 0 || targetIndex < 0) {
			return;
		}
		const reordered = [...builderFields];
		const [moved] = reordered.splice(sourceIndex, 1);
		reordered.splice(targetIndex, 0, moved);
		builderFields = reordered;
	}

	function selectForm(formId: string) {
		const normalized = formId.trim();
		if (!normalized) {
			return;
		}
		selectedFormId = normalized;
		selectedFormSubmissions = [];
		loadedSubmissionsFormId = '';
		submissionsError = '';
		void loadSubmissions(normalized);
	}

	function prettyDate(raw: string) {
		const parsed = new Date(raw);
		if (Number.isNaN(parsed.getTime())) {
			return '--';
		}
		return parsed.toLocaleString();
	}

	function submissionPreview(submission: IntakeSubmissionRecord) {
		const data = submission.data || {};
		const entries = Object.entries(data);
		if (entries.length === 0) {
			return '--';
		}
		const preview = entries.slice(0, 3).map(([fieldId, value]) => {
			const label = roomFieldSchemaLabelById.get(fieldId) || fieldId;
			const text = typeof value === 'boolean' ? (value ? 'Yes' : 'No') : String(value ?? '');
			return `${label}: ${text}`;
		});
		return preview.join(' • ');
	}

	function openLinkedTask(taskId: string | undefined) {
		const normalizedTaskId = (taskId || '').trim();
		if (!normalizedTaskId) {
			return;
		}
		dispatch('requestTaskEdit', { taskId: normalizedTaskId });
	}
</script>

<section class="intake-forms-panel" aria-label="Intake forms">
	<header class="forms-header">
		<div>
			<h3>Intake Forms</h3>
			<p>Create public forms that submit directly into this room&apos;s task board.</p>
		</div>
		{#if canEdit}
			<button type="button" class="primary-btn" on:click={openCreateBuilder}>Create form</button>
		{/if}
	</header>

	{#if formsError}
		<div class="forms-error">{formsError}</div>
	{/if}

	<div class="forms-grid">
		<section class="forms-list" aria-label="Forms list">
			<header>
				<h4>Forms</h4>
				<span>{forms.length}</span>
			</header>
			{#if formsLoading}
				<p class="forms-empty">Loading forms...</p>
			{:else if forms.length === 0}
				<p class="forms-empty">No forms yet. Create one to start collecting intake submissions.</p>
			{:else}
				<div class="forms-list-rows">
					{#each forms as form (form.form_id)}
						<article class="form-row" class:is-selected={selectedFormId === form.form_id}>
							<button
								type="button"
								class="form-main"
								on:click={() => selectForm(form.form_id)}
								title={form.title}
							>
								<strong>{form.title}</strong>
								<small>{form.submission_count ?? 0} submissions</small>
							</button>
							<div class="form-actions">
								<button
									type="button"
									class="mini-btn"
									on:click={() => copyFormLink(form.form_id)}
									title="Copy public form link"
								>
									Copy link
								</button>
								{#if copyFeedbackByFormId[form.form_id]}
									<span class="copy-feedback">{copyFeedbackByFormId[form.form_id]}</span>
								{/if}
								{#if canEdit}
									<label class="toggle-wrap">
										<input
											type="checkbox"
											checked={form.enabled}
											disabled={togglingFormIds.has(form.form_id)}
											on:change={() => void toggleFormEnabled(form)}
										/>
										<span>{form.enabled ? 'Enabled' : 'Disabled'}</span>
									</label>
									<button type="button" class="mini-btn" on:click={() => openEditBuilder(form)}>
										Edit
									</button>
									<button
										type="button"
										class="mini-btn danger"
										disabled={deletingFormIds.has(form.form_id)}
										on:click={() => void deleteForm(form)}
									>
										Delete
									</button>
								{/if}
							</div>
						</article>
					{/each}
				</div>
			{/if}
		</section>

		<section class="forms-builder" aria-label="Form builder">
			<header>
				<h4>{editingFormId ? 'Edit form' : 'Form builder'}</h4>
				{#if builderOpen}
					<button
						type="button"
						class="mini-btn"
						on:click={() => {
							builderOpen = false;
							editingFormId = '';
							builderError = '';
						}}
					>
						Close
					</button>
				{/if}
			</header>

			{#if !builderOpen}
				<p class="forms-empty">
					Select &quot;Create form&quot; or edit an existing form to open the builder.
				</p>
			{:else}
				<div class="builder-fields">
					<label>
						<span>Title</span>
						<input type="text" bind:value={builderTitle} maxlength="180" />
					</label>
					<label>
						<span>Description</span>
						<textarea bind:value={builderDescription} maxlength="1000"></textarea>
					</label>
					<div class="builder-grid-2">
						<label>
							<span>Target status</span>
							<select bind:value={builderTargetStatus}>
								{#each TARGET_STATUS_OPTIONS as statusOption (statusOption.value)}
									<option value={statusOption.value}>{statusOption.label}</option>
								{/each}
							</select>
						</label>
						<label>
							<span>Target sprint</span>
							<input type="text" bind:value={builderTargetSprint} maxlength="160" />
						</label>
					</div>
					<label class="toggle-wrap large">
						<input type="checkbox" bind:checked={builderEnabled} />
						<span>{builderEnabled ? 'Form enabled' : 'Form disabled'}</span>
					</label>
				</div>

				<div class="builder-rows">
					<header>
						<h5>Fields</h5>
						<button type="button" class="mini-btn" on:click={addBuilderField}>Add field</button>
					</header>
					<div class="field-rows">
						{#each builderFields as field (field.id)}
							<!-- svelte-ignore a11y_no_static_element_interactions -->
							<div
								class="field-row"
								draggable="true"
								on:dragstart={(event) => onBuilderFieldDragStart(event, field.id)}
								on:dragover|preventDefault
								on:drop={() => onBuilderFieldDrop(field.id)}
								on:dragend={() => (draggingFieldId = '')}
							>
								<div class="drag-handle" aria-hidden="true">::</div>
								<label>
									<span>Room field</span>
									<select
										value={field.field_id}
										on:change={(event) => {
											const selectedFieldId = (event.currentTarget as HTMLSelectElement).value;
											updateBuilderField(field.id, (current) => {
												const schema = roomFieldSchemaOptions.find(
													(option) => option.id === selectedFieldId
												);
												return {
													...current,
													field_id: selectedFieldId,
													label: current.label.trim() || schema?.label || ''
												};
											});
										}}
									>
										<option value="">Select field schema</option>
										{#each roomFieldSchemaOptions as schemaOption (schemaOption.id)}
											<option value={schemaOption.id}>{schemaOption.label}</option>
										{/each}
									</select>
								</label>
								<label>
									<span>Label</span>
									<input
										type="text"
										value={field.label}
										maxlength="120"
										on:input={(event) =>
											updateBuilderField(field.id, (current) => ({
												...current,
												label: (event.currentTarget as HTMLInputElement).value
											}))}
									/>
								</label>
								<label>
									<span>Type</span>
									<select
										value={field.field_type}
										on:change={(event) =>
											updateBuilderField(field.id, (current) => ({
												...current,
												field_type: safeFieldType((event.currentTarget as HTMLSelectElement).value)
											}))}
									>
										{#each FIELD_TYPE_OPTIONS as fieldTypeOption (fieldTypeOption.value)}
											<option value={fieldTypeOption.value}>{fieldTypeOption.label}</option>
										{/each}
									</select>
								</label>
								{#if field.field_type === 'select'}
									<label class="options-field">
										<span>Options (comma separated)</span>
										<input
											type="text"
											value={field.optionsInput}
											on:input={(event) =>
												updateBuilderField(field.id, (current) => ({
													...current,
													optionsInput: (event.currentTarget as HTMLInputElement).value
												}))}
										/>
									</label>
								{/if}
								<label class="toggle-wrap">
									<input
										type="checkbox"
										checked={field.required}
										on:change={(event) =>
											updateBuilderField(field.id, (current) => ({
												...current,
												required: (event.currentTarget as HTMLInputElement).checked
											}))}
									/>
									<span>Required</span>
								</label>
								<button
									type="button"
									class="mini-btn danger"
									on:click={() => removeBuilderField(field.id)}
									disabled={builderFields.length <= 1}
								>
									Remove
								</button>
							</div>
						{/each}
					</div>
				</div>

				{#if builderError}
					<div class="forms-error">{builderError}</div>
				{/if}
				<div class="builder-actions">
					<button type="button" class="primary-btn" on:click={() => void saveBuilder()} disabled={builderSaving}>
						{builderSaving ? 'Saving...' : 'Save form'}
					</button>
				</div>
			{/if}
		</section>
	</div>

	<section class="submissions-panel" aria-label="Form submissions">
		<header>
			<h4>Submissions</h4>
			<span>{selectedFormSubmissions.length}</span>
		</header>
		{#if !selectedForm}
			<p class="forms-empty">Select a form to view submissions.</p>
		{:else if submissionsLoading}
			<p class="forms-empty">Loading submissions...</p>
		{:else if submissionsError}
			<div class="forms-error">{submissionsError}</div>
		{:else if selectedFormSubmissions.length === 0}
			<p class="forms-empty">No submissions for this form yet.</p>
		{:else}
			<div class="submissions-table-wrap">
				<table class="submissions-table" aria-label="Submissions table">
					<thead>
						<tr>
							<th>Submitted</th>
							<th>Email</th>
							<th>Key fields</th>
							<th>Task</th>
						</tr>
					</thead>
					<tbody>
						{#each selectedFormSubmissions as submission (submission.submission_id)}
							<tr
								class:clickable={Boolean(submission.task_id)}
								on:click={() => openLinkedTask(submission.task_id)}
							>
								<td>{prettyDate(submission.submitted_at)}</td>
								<td>{submission.submitter_email || '--'}</td>
								<td>{submissionPreview(submission)}</td>
								<td>
									{#if submission.task_id}
										<span class="task-badge">{submission.task_id.slice(0, 8)}...</span>
									{:else}
										--
									{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</section>
</section>

<style>
	.intake-forms-panel {
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-rows: auto auto minmax(0, 1fr) minmax(0, 0.9fr);
		gap: 0.72rem;
	}

	.forms-header {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 0.75rem;
		flex-wrap: wrap;
		padding: 0.7rem 0.8rem;
		border: 1px solid var(--ws-border);
		border-radius: 12px;
		background: color-mix(in srgb, var(--ws-surface) 90%, var(--ws-surface-soft));
	}

	.forms-header h3 {
		margin: 0;
		font-size: 0.9rem;
	}

	.forms-header p {
		margin: 0.22rem 0 0;
		font-size: 0.74rem;
		color: var(--ws-muted);
	}

	.primary-btn,
	.mini-btn {
		border: 1px solid var(--ws-border);
		background: color-mix(in srgb, var(--ws-surface) 86%, var(--ws-surface-soft));
		color: var(--ws-text);
		border-radius: 9px;
		cursor: pointer;
	}

	.primary-btn {
		height: 2rem;
		padding: 0 0.8rem;
		font-size: 0.75rem;
		font-weight: 700;
	}

	.mini-btn {
		height: 1.7rem;
		padding: 0 0.56rem;
		font-size: 0.68rem;
		font-weight: 700;
	}

	.mini-btn.danger {
		color: #dc2626;
		border-color: color-mix(in srgb, #dc2626 42%, var(--ws-border));
	}

	.forms-error {
		padding: 0.55rem 0.68rem;
		border: 1px solid color-mix(in srgb, #ef4444 40%, transparent);
		background: color-mix(in srgb, #ef4444 10%, transparent);
		color: color-mix(in srgb, #b91c1c 82%, var(--ws-text));
		border-radius: 10px;
		font-size: 0.73rem;
	}

	.forms-grid {
		min-height: 0;
		display: grid;
		grid-template-columns: minmax(260px, 0.85fr) minmax(360px, 1.15fr);
		gap: 0.72rem;
	}

	.forms-list,
	.forms-builder,
	.submissions-panel {
		min-height: 0;
		border: 1px solid var(--ws-border);
		border-radius: 12px;
		background: color-mix(in srgb, var(--ws-surface) 92%, var(--ws-surface-soft));
		padding: 0.65rem;
		display: grid;
		align-content: start;
		gap: 0.58rem;
	}

	.forms-list > header,
	.forms-builder > header,
	.submissions-panel > header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}

	h4 {
		margin: 0;
		font-size: 0.8rem;
	}

	h5 {
		margin: 0;
		font-size: 0.74rem;
	}

	.forms-list > header span,
	.submissions-panel > header span {
		font-size: 0.7rem;
		color: var(--ws-muted);
		font-weight: 700;
	}

	.forms-empty {
		margin: 0;
		font-size: 0.74rem;
		color: var(--ws-muted);
	}

	.forms-list-rows {
		min-height: 0;
		overflow: auto;
		display: grid;
		align-content: start;
		gap: 0.46rem;
		padding-right: 0.12rem;
	}

	.form-row {
		border: 1px solid var(--ws-border);
		border-radius: 10px;
		padding: 0.48rem;
		display: grid;
		gap: 0.46rem;
	}

	.form-row.is-selected {
		border-color: color-mix(in srgb, var(--ws-accent) 52%, var(--ws-border));
		background: color-mix(in srgb, var(--ws-accent-soft) 38%, transparent);
	}

	.form-main {
		width: 100%;
		border: none;
		background: transparent;
		text-align: left;
		padding: 0;
		cursor: pointer;
		display: grid;
		gap: 0.16rem;
	}

	.form-main strong {
		font-size: 0.75rem;
		color: var(--ws-text);
	}

	.form-main small {
		font-size: 0.66rem;
		color: var(--ws-muted);
	}

	.form-actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.35rem;
	}

	.copy-feedback {
		font-size: 0.66rem;
		font-weight: 700;
		color: var(--ws-muted);
	}

	.toggle-wrap {
		display: inline-flex;
		align-items: center;
		gap: 0.34rem;
		font-size: 0.68rem;
		color: var(--ws-muted);
	}

	.toggle-wrap.large {
		font-size: 0.72rem;
		font-weight: 600;
	}

	.builder-fields {
		display: grid;
		gap: 0.5rem;
	}

	.builder-fields label,
	.field-row label {
		display: grid;
		gap: 0.24rem;
	}

	.builder-fields span,
	.field-row span {
		font-size: 0.66rem;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: var(--ws-muted);
		font-weight: 700;
	}

	input,
	select,
	textarea {
		width: 100%;
		border: 1px solid var(--ws-border);
		border-radius: 8px;
		background: color-mix(in srgb, var(--ws-surface) 90%, var(--ws-surface-soft));
		color: var(--ws-text);
		font-size: 0.74rem;
		padding: 0.45rem 0.52rem;
	}

	textarea {
		resize: vertical;
		min-height: 4.2rem;
	}

	.builder-grid-2 {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.46rem;
	}

	.builder-rows {
		display: grid;
		gap: 0.48rem;
	}

	.builder-rows > header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}

	.field-rows {
		max-height: 330px;
		overflow: auto;
		display: grid;
		align-content: start;
		gap: 0.42rem;
		padding-right: 0.1rem;
	}

	.field-row {
		border: 1px solid var(--ws-border);
		border-radius: 10px;
		padding: 0.48rem;
		display: grid;
		gap: 0.38rem;
		background: color-mix(in srgb, var(--ws-surface) 88%, var(--ws-surface-soft));
	}

	.drag-handle {
		width: 1.6rem;
		height: 1.1rem;
		border-radius: 7px;
		border: 1px dashed var(--ws-border);
		color: var(--ws-muted);
		font-size: 0.72rem;
		display: inline-grid;
		place-items: center;
		cursor: grab;
	}

	.options-field {
		grid-column: 1 / -1;
	}

	.builder-actions {
		display: flex;
		justify-content: flex-end;
	}

	.submissions-panel {
		overflow: hidden;
	}

	.submissions-table-wrap {
		min-height: 0;
		overflow: auto;
	}

	.submissions-table {
		width: 100%;
		min-width: 680px;
		border-collapse: collapse;
	}

	.submissions-table th,
	.submissions-table td {
		padding: 0.42rem 0.5rem;
		border-bottom: 1px solid var(--ws-border);
		font-size: 0.72rem;
		text-align: left;
		vertical-align: top;
	}

	.submissions-table th {
		position: sticky;
		top: 0;
		background: color-mix(in srgb, var(--ws-surface) 96%, var(--ws-surface-soft));
		font-size: 0.66rem;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--ws-muted);
	}

	.submissions-table tr.clickable {
		cursor: pointer;
	}

	.submissions-table tr.clickable:hover {
		background: color-mix(in srgb, var(--ws-accent-soft) 36%, transparent);
	}

	.task-badge {
		display: inline-flex;
		align-items: center;
		height: 1.25rem;
		padding: 0 0.4rem;
		border-radius: 999px;
		border: 1px solid var(--ws-border);
		font-size: 0.64rem;
		font-weight: 700;
	}

	@media (max-width: 980px) {
		.forms-grid {
			grid-template-columns: minmax(0, 1fr);
		}

		.builder-grid-2 {
			grid-template-columns: minmax(0, 1fr);
		}
	}
</style>
