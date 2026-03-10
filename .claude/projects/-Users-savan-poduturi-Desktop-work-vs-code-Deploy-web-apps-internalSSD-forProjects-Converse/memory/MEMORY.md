# Converse Project – Claude Memory

## Stack
- SvelteKit + Svelte 5 (uses Svelte 4 syntax in workspace components, Svelte 5 runes only in TableBoard)
- TypeScript, TailwindCSS not used (raw CSS with CSS variables for theming)
- Dark/light theme via `data-theme='light'` / `.theme-light` on root
- Backend API at `VITE_API_BASE` (default `http://localhost:8080`)

## Key workspace files
| File | Purpose |
|---|---|
| `src/lib/types/timeline.ts` | Core data types: TimelineTask, Sprint, ProjectTimeline |
| `src/lib/stores/timeline.ts` | Timeline store, AI generation/edit functions, AI_TIMELINE_FORMAT_HINT |
| `src/lib/stores/boardActivity.ts` | Activity feed store (notifications) |
| `src/lib/stores/tasks.ts` | Simple task store for room tasks (non-timeline) |
| `src/lib/components/workspace/ProjectWorkspace.svelte` | Main shell: activity bar + feed sidebar + canvas |
| `src/lib/components/workspace/TimelineBoard.svelte` | Overview dashboard (KPIs, sprint details, backlog, budget) |
| `src/lib/components/workspace/ActivityFeedPanel.svelte` | Persistent left sidebar activity feed |
| `src/lib/components/workspace/ToraAIPanel.svelte` | AI chat panel for board edits |
| `src/lib/components/workspace/TaskBoard.svelte` | Kanban board (Tasks tab) |
| `src/lib/components/workspace/ProgressGanttTab.svelte` | Gantt chart (Progress tab) |
| `src/lib/components/workspace/TableBoard.svelte` | Table view — uses Svelte 5 $derived |
| `src/lib/components/workspace/ProjectOnboarding.svelte` | Initial board setup (template / AI generation) |

## Workspace layout
```
[icon rail 52px] | [activity feed 220px] | [main canvas flex:1]
```
Activity bar tabs: overview · tasks · progress · table · tora_ai

## Data model highlights
- `TimelineTask` has: priority (critical|high|medium|low), assignee, effort_score, type, status
- `Sprint` has: goal, budget_allocated
- `ProjectTimeline` has: budget_total, budget_spent, description
- Activity feed events: task_completed, task_added, task_modified, board_generated, board_edited, etc.

## AI integration
- All AI prompts are prefixed with `AI_TIMELINE_FORMAT_HINT` (exported from timeline.ts)
- Projects >24kB state are auto-compressed (descriptions stripped) before sending to AI
- Backend endpoints: POST `/api/rooms/:id/ai-timeline` (generate), POST `/api/rooms/:id/ai-edit` (edit)

## Theme pattern
CSS variables defined at `:global(:root)` for dark default, overridden at `:global(:root[data-theme='light'])` and `:global(.theme-light)`.
