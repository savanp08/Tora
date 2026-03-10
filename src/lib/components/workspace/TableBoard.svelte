<script lang="ts">
	import { projectTimeline } from '$lib/stores/timeline';
	import { 
		createTable, 
		getCoreRowModel, 
		getSortedRowModel 
	} from '@tanstack/svelte-table';

	// 1. Svelte 5 Reactive Data Transformation
	// Using $derived to flatten sprints into rows automatically
	let data = $derived(
		$projectTimeline?.sprints.flatMap((sprint) =>
			sprint.tasks.map((task) => ({
				...task,
				sprintName: sprint.name,
				parentSprintId: sprint.id,
				startDate: sprint.start_date,
				endDate: sprint.end_date
			}))
		) || []
	);

	// 2. Column Definitions
	const columns = [
		{
			accessorKey: 'title',
			header: 'Task Name',
		},
		{
			accessorKey: 'sprintName',
			header: 'Sprint/Phase',
		},
		{
			accessorKey: 'status',
			header: 'Status',
			// Use a simple string return if flexRender is causing issues
			cell: (info: any) => info.getValue().replace('_', ' ').toUpperCase()
		},
		{
			accessorKey: 'type',
			header: 'Category',
		},
		{
			accessorKey: 'effort_score',
			header: 'Effort',
		}
	];

	// 3. Initialize TanStack Table with Svelte 5 Runes
	const table = createTable({
		get data() { return data; }, // Getter for Svelte 5 reactivity
		columns,
		getCoreRowModel: getCoreRowModel(),
		getSortedRowModel: getSortedRowModel(),
	});

    // Helper to handle rendering without the broken flexRender import
    function render(comp: any, props: any) {
        if (typeof comp === 'function') return comp(props);
        return comp;
    }
</script>

<div class="flex flex-col h-full w-full bg-[#0D0D12] text-white p-6 overflow-hidden">
	<div class="flex justify-between items-center mb-6">
		<h1 class="text-2xl font-bold bg-gradient-to-r from-white to-gray-500 bg-clip-text text-transparent">
			Project Data Grid
		</h1>
	</div>

	<div class="flex-1 overflow-auto rounded-xl border border-white/10 bg-white/[0.02] backdrop-blur-xl">
		<table class="w-full border-collapse text-sm">
			<thead class="sticky top-0 z-20 bg-[#121218] border-b border-white/10">
				{#each table.getHeaderGroups() as headerGroup}
					<tr>
						{#each headerGroup.headers as header}
							<th class="px-6 py-4 text-left font-semibold text-gray-400 uppercase tracking-wider text-[10px]">
								<div class="flex items-center gap-2">
                                    {render(header.column.columnDef.header, header.getContext())}
									{#if header.column.getIsSorted() === 'asc'} ↑ {/if}
									{#if header.column.getIsSorted() === 'desc'} ↓ {/if}
								</div>
							</th>
						{/each}
					</tr>
				{/each}
			</thead>

			<tbody class="divide-y divide-white/5">
				{#each table.getRowModel().rows as row}
					<tr class="group hover:bg-white/[0.04] transition-colors">
						{#each row.getVisibleCells() as cell}
							<td class="px-6 py-4 text-gray-300">
								{#if cell.column.id === 'status'}
									<span class="px-2 py-1 rounded-md text-[10px] font-bold border border-white/5 bg-blue-500/10 text-blue-400">
										{render(cell.column.columnDef.cell, cell.getContext())}
									</span>
								{:else}
									{render(cell.column.columnDef.cell, cell.getContext())}
								{/if}
							</td>
						{/each}
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
</div>