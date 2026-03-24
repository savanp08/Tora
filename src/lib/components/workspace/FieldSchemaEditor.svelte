<script lang="ts">
	import { createEventDispatcher } from 'svelte';
	import {
		createFieldSchema,
		deleteFieldSchema,
		fieldSchemaStore,
		updateFieldSchema,
		type FieldSchema,
		type FieldSchemaType
	} from '$lib/stores/fieldSchema';
	import { normalizeRoomIDValue } from '$lib/utils/chat/core';

	type FieldTypeOption = {
		value: FieldSchemaType;
		label: string;
	};

	const dispatch = createEventDispatcher<{ close: void }>();

	const FIELD_TYPE_OPTIONS: FieldTypeOption[] = [
		{ value: 'text', label: 'Text' },
		{ value: 'number', label: 'Number' },
		{ value: 'date', label: 'Date' },
		{ value: 'select', label: 'Select' },
		{ value: 'multi_select', label: 'Multi Select' },
		{ value: 'checkbox', label: 'Checkbox' },
		{ value: 'person', label: 'Person' },
		{ value: 'url', label: 'URL' }
	];

	export let roomId = '';

	let createOpen = false;
	let createName = '';
	let createType: FieldSchemaType = 'text';
	let createOptionsInput = '';
	let createBusy = false;
	let editBusy = false;
	let localError = '';
	let editingFieldId = '';
	let editName = '';
	let editType: FieldSchemaType = 'text';
	let editOptionsInput = '';
	let dragFieldId = '';

	$: normalizedRoomId = normalizeRoomIDValue(roomId);
	$: schemas = [...$fieldSchemaStore];

	function isOptionField(type: FieldSchemaType) {
		return type === 'select' || type === 'multi_select';
	}

	function parseOptionsInput(value: string) {
		const seen = new Set<string>();
		const options: string[] = [];
		for (const rawOption of value.split(',')) {
			const trimmed = rawOption.trim();
			if (!trimmed) {
				continue;
			}
			const dedupeKey = trimmed.toLowerCase();
			if (seen.has(dedupeKey)) {
				continue;
			}
			seen.add(dedupeKey);
			options.push(trimmed);
		}
		return options;
	}

	function optionsToInput(options: string[] | undefined) {
		return Array.isArray(options) && options.length > 0 ? options.join(', ') : '';
	}

	function resetCreateForm() {
		createName = '';
		createType = 'text';
		createOptionsInput = '';
		createOpen = false;
	}

	function startEditing(schema: FieldSchema) {
		editingFieldId = schema.fieldId;
		editName = schema.name;
		editType = schema.fieldType;
		editOptionsInput = optionsToInput(schema.options);
		localError = '';
	}

	function cancelEditing() {
		editingFieldId = '';
		editName = '';
		editType = 'text';
		editOptionsInput = '';
	}

	async function handleCreateField() {
		if (createBusy || !normalizedRoomId) {
			return;
		}
		const name = createName.trim();
		if (!name) {
			localError = 'Field name is required.';
			return;
		}
		createBusy = true;
		localError = '';
		try {
			await createFieldSchema(normalizedRoomId, {
				name,
				fieldType: createType,
				options: isOptionField(createType) ? parseOptionsInput(createOptionsInput) : []
			});
			resetCreateForm();
		} catch (error) {
			localError = error instanceof Error ? error.message : 'Failed to create field';
		} finally {
			createBusy = false;
		}
	}

	async function handleSaveEdit(schema: FieldSchema) {
		if (!normalizedRoomId || editBusy) {
			return;
		}
		const nextName = editName.trim();
		if (!nextName) {
			localError = 'Field name is required.';
			return;
		}
		editBusy = true;
		localError = '';
		try {
			await updateFieldSchema(normalizedRoomId, schema.fieldId, {
				name: nextName,
				fieldType: editType,
				options: isOptionField(editType) ? parseOptionsInput(editOptionsInput) : [],
				position: schema.position
			});
			cancelEditing();
		} catch (error) {
			localError = error instanceof Error ? error.message : 'Failed to update field';
		} finally {
			editBusy = false;
		}
	}

	async function handleDeleteField(schema: FieldSchema) {
		if (!normalizedRoomId || editBusy) {
			return;
		}
		const shouldDelete = window.confirm(
			`Delete "${schema.name}" field? Existing task values stay stored.`
		);
		if (!shouldDelete) {
			return;
		}
		editBusy = true;
		localError = '';
		try {
			await deleteFieldSchema(normalizedRoomId, schema.fieldId);
			if (editingFieldId === schema.fieldId) {
				cancelEditing();
			}
		} catch (error) {
			localError = error instanceof Error ? error.message : 'Failed to delete field';
		} finally {
			editBusy = false;
		}
	}

	async function reorderSchemas(draggedFieldId: string, targetFieldId: string) {
		if (
			!normalizedRoomId ||
			editBusy ||
			!draggedFieldId ||
			!targetFieldId ||
			draggedFieldId === targetFieldId
		) {
			return;
		}
		const current = [...schemas];
		const fromIndex = current.findIndex((schema) => schema.fieldId === draggedFieldId);
		const toIndex = current.findIndex((schema) => schema.fieldId === targetFieldId);
		if (fromIndex < 0 || toIndex < 0 || fromIndex === toIndex) {
			return;
		}

		const [moved] = current.splice(fromIndex, 1);
		current.splice(toIndex, 0, moved);

		const updates = current
			.map((schema, index) => ({ schema, nextPosition: index }))
			.filter(({ schema, nextPosition }) => schema.position !== nextPosition);
		if (updates.length === 0) {
			return;
		}

		editBusy = true;
		localError = '';
		try {
			for (const { schema, nextPosition } of updates) {
				await updateFieldSchema(normalizedRoomId, schema.fieldId, { position: nextPosition });
			}
		} catch (error) {
			localError = error instanceof Error ? error.message : 'Failed to reorder fields';
		} finally {
			editBusy = false;
		}
	}
</script>

<section class="field-schema-editor" aria-label="Field schema editor">
	<header class="field-schema-header">
		<div>
			<h3>Custom Fields</h3>
			<p>{schemas.length} field{schemas.length === 1 ? '' : 's'}</p>
		</div>
		<div class="field-schema-header-actions">
			<button type="button" class="ghost-btn" on:click={() => dispatch('close')}> Close </button>
			<button type="button" class="primary-btn" on:click={() => (createOpen = !createOpen)}>
				{createOpen ? 'Cancel' : 'Add Field'}
			</button>
		</div>
	</header>

	{#if localError}
		<p class="field-schema-error">{localError}</p>
	{/if}

	{#if createOpen}
		<form
			class="field-form"
			on:submit|preventDefault={() => void handleCreateField()}
			aria-label="Create field schema"
		>
			<label>
				<span>Name</span>
				<input type="text" bind:value={createName} maxlength="120" placeholder="Priority" />
			</label>
			<label>
				<span>Type</span>
				<select bind:value={createType}>
					{#each FIELD_TYPE_OPTIONS as option (option.value)}
						<option value={option.value}>{option.label}</option>
					{/each}
				</select>
			</label>
			{#if isOptionField(createType)}
				<label class="field-form-wide">
					<span>Options</span>
					<input type="text" bind:value={createOptionsInput} placeholder="High, Medium, Low" />
				</label>
			{/if}
			<div class="field-form-actions">
				<button type="submit" class="primary-btn" disabled={createBusy}>
					{createBusy ? 'Saving…' : 'Save Field'}
				</button>
			</div>
		</form>
	{/if}

	<div class="field-schema-list" role="list">
		{#if schemas.length === 0}
			<p class="field-schema-empty">
				No custom fields yet. Add one to start tracking richer task data.
			</p>
		{:else}
			{#each schemas as schema (schema.fieldId)}
				<article
					class="field-row"
					class:is-editing={editingFieldId === schema.fieldId}
					draggable="true"
					on:dragstart={() => (dragFieldId = schema.fieldId)}
					on:dragover|preventDefault
					on:drop|preventDefault={() => void reorderSchemas(dragFieldId, schema.fieldId)}
					role="listitem"
				>
					{#if editingFieldId === schema.fieldId}
						<form
							class="field-form"
							on:submit|preventDefault={() => void handleSaveEdit(schema)}
							aria-label={`Edit ${schema.name}`}
						>
							<label>
								<span>Name</span>
								<input type="text" bind:value={editName} maxlength="120" />
							</label>
							<label>
								<span>Type</span>
								<select bind:value={editType}>
									{#each FIELD_TYPE_OPTIONS as option (option.value)}
										<option value={option.value}>{option.label}</option>
									{/each}
								</select>
							</label>
							{#if isOptionField(editType)}
								<label class="field-form-wide">
									<span>Options</span>
									<input type="text" bind:value={editOptionsInput} />
								</label>
							{/if}
							<div class="field-form-actions">
								<button type="submit" class="primary-btn" disabled={editBusy}>
									{editBusy ? 'Saving…' : 'Save'}
								</button>
								<button
									type="button"
									class="ghost-btn"
									on:click={cancelEditing}
									disabled={editBusy}
								>
									Cancel
								</button>
							</div>
						</form>
					{:else}
						<div class="field-row-main">
							<span class="drag-handle" title="Drag to reorder">::</span>
							<div class="field-row-copy">
								<strong>{schema.name}</strong>
								<div class="field-row-meta">
									<span class="type-badge">{schema.fieldType}</span>
									{#if schema.options && schema.options.length > 0}
										<span class="options-text">{schema.options.join(', ')}</span>
									{/if}
								</div>
							</div>
						</div>
						<div class="field-row-actions">
							<button type="button" class="ghost-btn" on:click={() => startEditing(schema)}
								>Edit</button
							>
							<button
								type="button"
								class="danger-btn"
								on:click={() => void handleDeleteField(schema)}
								disabled={editBusy}
							>
								Delete
							</button>
						</div>
					{/if}
				</article>
			{/each}
		{/if}
	</div>
</section>

<style>
	.field-schema-editor {
		display: grid;
		grid-template-rows: auto auto auto minmax(0, 1fr);
		gap: 0.75rem;
		height: 100%;
		min-height: 0;
		padding: 0.8rem;
		background: var(--workspace-taskboard-bg, #17181a);
		color: var(--workspace-taskboard-text, #f2f3f4);
	}

	.field-schema-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
	}

	.field-schema-header h3 {
		margin: 0;
		font-size: 0.95rem;
	}

	.field-schema-header p {
		margin: 0.16rem 0 0;
		font-size: 0.78rem;
		opacity: 0.72;
	}

	.field-schema-header-actions {
		display: inline-flex;
		gap: 0.45rem;
	}

	.primary-btn,
	.ghost-btn,
	.danger-btn {
		border-radius: 10px;
		border: 1px solid rgba(255, 255, 255, 0.16);
		background: rgba(255, 255, 255, 0.04);
		color: inherit;
		font-size: 0.76rem;
		font-weight: 600;
		padding: 0.4rem 0.62rem;
		cursor: pointer;
	}

	.primary-btn {
		background: rgba(255, 255, 255, 0.14);
	}

	.danger-btn {
		border-color: rgba(248, 113, 113, 0.48);
		color: #fca5a5;
	}

	.primary-btn:disabled,
	.ghost-btn:disabled,
	.danger-btn:disabled {
		cursor: not-allowed;
		opacity: 0.56;
	}

	.field-schema-error {
		margin: 0;
		font-size: 0.78rem;
		color: #fca5a5;
	}

	.field-form {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.6rem;
		padding: 0.72rem;
		border-radius: 12px;
		border: 1px solid rgba(255, 255, 255, 0.14);
		background: rgba(0, 0, 0, 0.2);
	}

	.field-form-wide {
		grid-column: 1 / -1;
	}

	.field-form label {
		display: grid;
		gap: 0.34rem;
	}

	.field-form label span {
		font-size: 0.72rem;
		opacity: 0.74;
	}

	.field-form input,
	.field-form select {
		border-radius: 9px;
		border: 1px solid rgba(255, 255, 255, 0.2);
		background: rgba(0, 0, 0, 0.28);
		color: inherit;
		font-size: 0.8rem;
		padding: 0.43rem 0.52rem;
	}

	.field-form-actions {
		grid-column: 1 / -1;
		display: inline-flex;
		gap: 0.52rem;
	}

	.field-schema-list {
		overflow: auto;
		min-height: 0;
		display: grid;
		gap: 0.52rem;
		align-content: start;
	}

	.field-schema-empty {
		margin: 0;
		font-size: 0.8rem;
		opacity: 0.75;
		padding: 0.66rem;
		border-radius: 10px;
		border: 1px dashed rgba(255, 255, 255, 0.2);
	}

	.field-row {
		border-radius: 12px;
		border: 1px solid rgba(255, 255, 255, 0.14);
		background: rgba(0, 0, 0, 0.2);
		padding: 0.58rem;
		display: grid;
		gap: 0.5rem;
	}

	.field-row-main {
		display: flex;
		align-items: center;
		gap: 0.56rem;
	}

	.drag-handle {
		font-size: 0.9rem;
		opacity: 0.45;
		cursor: grab;
		user-select: none;
	}

	.field-row-copy {
		min-width: 0;
		display: grid;
		gap: 0.26rem;
	}

	.field-row-copy strong {
		font-size: 0.82rem;
		font-weight: 650;
	}

	.field-row-meta {
		display: inline-flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.4rem;
	}

	.type-badge {
		display: inline-flex;
		align-items: center;
		border-radius: 999px;
		padding: 0.16rem 0.48rem;
		font-size: 0.66rem;
		background: rgba(255, 255, 255, 0.12);
	}

	.options-text {
		font-size: 0.68rem;
		opacity: 0.72;
	}

	.field-row-actions {
		display: inline-flex;
		gap: 0.45rem;
		justify-content: flex-end;
	}

	@media (max-width: 760px) {
		.field-form {
			grid-template-columns: minmax(0, 1fr);
		}
	}
</style>
